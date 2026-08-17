// GhostBird: drop-in replacement self-hosted de Tinybird para las
// estadísticas nativas de Ghost 6.x. Un binario, una SQLite, cero config.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gnacho/ghostbird/internal/config"
	"github.com/gnacho/ghostbird/internal/gcauth"
	"github.com/gnacho/ghostbird/internal/ingest"
	"github.com/gnacho/ghostbird/internal/metrics"
	"github.com/gnacho/ghostbird/internal/pipes"
	"github.com/gnacho/ghostbird/internal/store"
)

// version se inyecta con -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("ghostbird terminó con error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	var gc *gcauth.Authenticator
	if cfg.GoatCounterDB != "" {
		gc, err = gcauth.Open(cfg.GoatCounterDB)
		if err != nil {
			return fmt.Errorf("goatcounter: %w", err)
		}
		defer gc.Close()
		log.Info("auth GoatCounter activa", "db", cfg.GoatCounterDB)
	}

	m := metrics.New()
	srv := ingest.NewServer(cfg, st, log, m)
	pipesHandler := pipes.NewHandler(cfg, st, log, m, gc)
	m.SetExtra(func() []string {
		lines := metrics.DBSizeLines(cfg.DBPath)
		if n, err := st.CountEvents(); err == nil {
			lines = append(lines,
				"# HELP ghostbird_events_stored_total Eventos en BD\n# TYPE ghostbird_events_stored_total gauge",
				fmt.Sprintf("ghostbird_events_stored_total %d", n))
		}
		return lines
	})

	root := http.NewServeMux()
	root.Handle("/v0/pipes/", pipesHandler)
	root.Handle("GET /metrics", m.Handler())
	root.Handle("/", srv.Handler())

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           ingest.CORS(root),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Job nocturno unificado (patrón sqlite-ops): purge de sales, retención,
	// backup verificado con rotación, optimize + heartbeat. Con WaitGroup
	// para drenarlo (acotado) en shutdown.
	var jobWG sync.WaitGroup
	jobWG.Add(1)
	go func() {
		defer jobWG.Done()
		nightlyJob(ctx, cfg, st, log)
	}()

	errCh := make(chan error, 1)
	go func() {
		log.Info("ghostbird escuchando", "addr", cfg.Addr, "db", cfg.DBPath, "version", version, "trust_proxy", cfg.TrustProxy, "ingest_auth", cfg.IngestToken != "")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("apagando (señal recibida)…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	// Drenar el job nocturno (backup incluido) con tope: si no acaba, no
	// bloqueamos el apagado (el backup usa tmp+rename, un corte deja solo
	// un .tmp limpiable).
	jobDone := make(chan struct{})
	go func() { jobWG.Wait(); close(jobDone) }()
	select {
	case <-jobDone:
	case <-time.After(15 * time.Second):
		log.Warn("job nocturno no terminó en la ventana de shutdown; continuando")
	}
	log.Info("ghostbird apagado")
	return nil
}

// rotateBackups borra backups ghostbird-YYYYMMDD.db con fecha > keepDays.
// La fecha sale del NOMBRE (una restauración/copias no refrescan mtime).
func rotateBackups(dir string, keepDays int, log *slog.Logger) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -keepDays).Format("20060102")
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "ghostbird-") || !strings.HasSuffix(name, ".db") {
			continue
		}
		date := strings.TrimSuffix(strings.TrimPrefix(name, "ghostbird-"), ".db")
		if len(date) != 8 || date >= cutoff {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			log.Info("backup rotado", "file", name)
		}
	}
	// Limpieza de .tmp huérfanos (backup interrumpido).
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(path, ".tmp") {
			_ = os.Remove(path)
		}
		return nil
	})
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}

// nightlyJob corre al arranque y luego cada 24 h: purge de sales (>7 días),
// retención de eventos (si configurada), backup diario verificado (si
// configurado; conserva 14 días de backups) y optimize/checkpoint.
func nightlyJob(ctx context.Context, cfg *config.Config, st *store.Store, log *slog.Logger) {
	run := func() {
		// Write-probe: un write confirmado aunque no haya tráfico.
		if err := st.TouchHeartbeat(time.Now()); err != nil {
			log.Error("heartbeat write falla (¿disco lleno / BD rota?)", "error", err)
		}
		// Sales: la firma rota a diario; >7 días no sirven para nada.
		if n, err := st.DeleteOldSalts(time.Now().UTC().AddDate(0, 0, -7).Format("2006-01-02")); err != nil {
			log.Warn("limpiar sales", "error", err)
		} else if n > 0 {
			log.Info("sales expiradas borradas", "count", n)
		}
		// Retención de eventos crudos.
		if cfg.RetentionDays > 0 {
			cutoff := time.Now().UTC().AddDate(0, 0, -cfg.RetentionDays).Unix()
			if n, err := st.DeleteEventsBefore(cutoff); err != nil {
				log.Warn("retención de eventos", "error", err)
			} else if n > 0 {
				log.Info("eventos purgados por retención", "count", n, "days", cfg.RetentionDays)
			}
		}
		// Backup diario verificado (VACUUM INTO, tmp+rename).
		if cfg.BackupDir != "" {
			dest := filepath.Join(cfg.BackupDir, "ghostbird-"+time.Now().UTC().Format("20060102")+".db")
			if err := st.Backup(dest); err != nil {
				log.Warn("backup diario", "error", err)
			} else {
				log.Info("backup diario OK", "path", dest)
			}
			// Rotación INCONDICIONAL (aunque el backup falle: si no, el
			// disco apretado realimenta el fallo), por fecha del nombre.
			rotateBackups(cfg.BackupDir, 14, log)
		}
		st.Optimize()
		// Summary diario para vigilar crecimiento sin métricas externas.
		if n, err := st.CountEvents(); err == nil {
			var dbSize int64
			if fi, err := os.Stat(cfg.DBPath); err == nil {
				dbSize = fi.Size()
			}
			log.Info("summary diario", "events", n, "db_bytes", dbSize)
		}
	}
	run()
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
