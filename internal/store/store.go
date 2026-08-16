// Package store persiste los eventos de analytics y las sales de firma en
// SQLite (único escritor, WAL). El schema es la frontera del sistema: se
// versiona con PRAGMA user_version y solo cambia por migraciones.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store envuelve la conexión SQLite.
type Store struct {
	db *sql.DB
}

// migrations es la lista ordenada de migraciones; el índice+1 es la versión.
// NUNCA reordenar ni modificar entradas ya publicadas.
var migrations = []string{
	// v1: eventos aplanados + sales de firma.
	`
	CREATE TABLE IF NOT EXISTS events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts INTEGER NOT NULL,
		received_ms INTEGER,
		site_uuid TEXT NOT NULL,
		session_id TEXT NOT NULL,
		event_id TEXT NOT NULL,
		action TEXT NOT NULL DEFAULT 'page_hit',
		pathname TEXT NOT NULL DEFAULT '',
		href TEXT NOT NULL DEFAULT '',
		post_uuid TEXT NOT NULL DEFAULT '',
		post_type TEXT NOT NULL DEFAULT 'null',
		member_uuid TEXT NOT NULL DEFAULT '',
		member_status TEXT NOT NULL DEFAULT 'undefined',
		gift_link TEXT NOT NULL DEFAULT '',
		location TEXT NOT NULL DEFAULT '',
		locale TEXT NOT NULL DEFAULT '',
		os TEXT NOT NULL DEFAULT 'Unknown',
		browser TEXT NOT NULL DEFAULT 'Unknown',
		device TEXT NOT NULL DEFAULT 'unknown',
		user_agent TEXT NOT NULL DEFAULT '',
		referrer_url TEXT NOT NULL DEFAULT '',
		referrer_source TEXT NOT NULL DEFAULT '',
		referrer_medium TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT '',
		utm_source TEXT NOT NULL DEFAULT '',
		utm_medium TEXT NOT NULL DEFAULT '',
		utm_campaign TEXT NOT NULL DEFAULT '',
		utm_term TEXT NOT NULL DEFAULT '',
		utm_content TEXT NOT NULL DEFAULT '',
		raw TEXT NOT NULL,
		inserted_at INTEGER NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_events_dedup ON events(site_uuid, event_id);
	CREATE INDEX IF NOT EXISTS idx_events_site_ts ON events(site_uuid, ts);
	CREATE INDEX IF NOT EXISTS idx_events_site_session ON events(site_uuid, session_id);

	CREATE TABLE IF NOT EXISTS salts (
		day TEXT NOT NULL,
		site_uuid TEXT NOT NULL,
		salt TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (day, site_uuid)
	);
	`,
}

// Open abre (o crea) la base de datos, aplica las pragmas de producción y
// ejecuta las migraciones pendientes.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("crear directorio de datos: %w", err)
		}
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=txlock(IMMEDIATE)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("abrir sqlite: %w", err)
	}
	// Único escritor: una sola conexión de escritura evita SQLITE_BUSY entre
	// goroutines del propio proceso.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("leer user_version: %w", err)
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("migración v%d: %w", i+1, err)
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migración v%d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migración v%d (version): %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migración v%d (commit): %w", i+1, err)
		}
	}
	return nil
}

// Close cierra la conexión y hace checkpoint del WAL.
func (s *Store) Close() error {
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return s.db.Close()
}

// Ping comprueba que la base de datos responde.
func (s *Store) Ping() error {
	var one int
	return s.db.QueryRow(`SELECT 1`).Scan(&one)
}

// Query ejecuta una consulta de solo lectura (pipes de la fase 2, tests).
func (s *Store) Query(query string, args ...any) (*sql.Rows, error) {
	return s.db.Query(query, args...)
}

// QueryRow ejecuta una consulta de una fila (pipes).
func (s *Store) QueryRow(query string, args ...any) *sql.Row {
	return s.db.QueryRow(query, args...)
}

// Event es un page_hit listo para persistir. Los campos de texto usan ""
// como valor vacío (nunca null): las queries de la fase 2 dependen de eso.
type Event struct {
	Ts             int64 // unix seconds UTC (hora del servidor de ingesta)
	ReceivedMs     int64 // ms epoch de x-ghost-analytics-start; 0 si falta
	SiteUUID       string
	SessionID      string
	EventID        string
	Action         string
	Pathname       string
	Href           string
	PostUUID       string
	PostType       string
	MemberUUID     string
	MemberStatus   string
	GiftLink       string
	Location       string
	Locale         string
	OS             string
	Browser        string
	Device         string
	UserAgent      string
	ReferrerURL    string
	ReferrerSource string
	ReferrerMedium string
	Source         string // referrer_source normalizado (mapa mv_hits)
	UtmSource      string
	UtmMedium      string
	UtmCampaign    string
	UtmTerm        string
	UtmContent     string
	Raw            string // JSON canónico del evento procesado
}

// nullInt convierte 0 en NULL para columnas opcionales.
func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// InsertEvents inserta eventos con dedup por (site_uuid, event_id) en una
// única transacción. Devuelve el número de filas nuevas (los duplicados se
// ignoran: la entrega es at-least-once).
func (s *Store) InsertEvents(now time.Time, evs []Event) (int64, error) {
	if len(evs) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO events (
			ts, received_ms, site_uuid, session_id, event_id, action,
			pathname, href, post_uuid, post_type, member_uuid, member_status,
			gift_link, location, locale, os, browser, device, user_agent,
			referrer_url, referrer_source, referrer_medium, source,
			utm_source, utm_medium, utm_campaign, utm_term, utm_content,
			raw, inserted_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var inserted int64
	insertedAt := now.Unix()
	for _, e := range evs {
		res, err := stmt.Exec(
			e.Ts, nullInt(e.ReceivedMs), e.SiteUUID, e.SessionID, e.EventID, e.Action,
			e.Pathname, e.Href, e.PostUUID, e.PostType, e.MemberUUID, e.MemberStatus,
			e.GiftLink, e.Location, e.Locale, e.OS, e.Browser, e.Device, e.UserAgent,
			e.ReferrerURL, e.ReferrerSource, e.ReferrerMedium, e.Source,
			e.UtmSource, e.UtmMedium, e.UtmCampaign, e.UtmTerm, e.UtmContent,
			e.Raw, insertedAt,
		)
		if err != nil {
			return 0, err
		}
		if n, err := res.RowsAffected(); err == nil {
			inserted += n
		}
	}
	return inserted, tx.Commit()
}

// GetOrCreateSalt devuelve la sal de firma del par (día UTC, site_uuid),
// creándola si no existe. La sal rota cada día y por sitio.
func (s *Store) GetOrCreateSalt(day, siteUUID string) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		var salt string
		err := s.db.QueryRow(`SELECT salt FROM salts WHERE day = ? AND site_uuid = ?`, day, siteUUID).Scan(&salt)
		if err == nil {
			return salt, nil
		}
		if err != sql.ErrNoRows {
			return "", err
		}
		salt, err = randomSalt()
		if err != nil {
			return "", err
		}
		now := time.Now().UTC().Unix()
		_, err = s.db.Exec(`INSERT OR IGNORE INTO salts (day, site_uuid, salt, created_at) VALUES (?,?,?,?)`, day, siteUUID, salt, now)
		if err != nil {
			return "", err
		}
		// Si otro proceso insertó en paralelo, el SELECT del siguiente intento
		// devolverá su sal (INSERT OR IGNORE la conserva).
	}
	return "", fmt.Errorf("no se pudo crear la sal para %s/%s", day, siteUUID)
}

// DeleteOldSalts borra sales de días anteriores a cutoff. Devuelve filas borradas.
func (s *Store) DeleteOldSalts(cutoffDay string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM salts WHERE day < ?`, cutoffDay)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountEvents devuelve el número total de eventos almacenados (para health/stats).
func (s *Store) CountEvents() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT count(*) FROM events`).Scan(&n)
	return n, err
}

// DeleteEventsBefore purga eventos anteriores al epoch dado (retención).
// Devuelve filas borradas. No toca las sales (rota solas con su purge).
func (s *Store) DeleteEventsBefore(cutoff int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM events WHERE ts < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Backup crea una copia consistente de la BD con VACUUM INTO (skill
// sqlite-ops: respeta WAL, no necesita bloquear escritores) y verifica su
// integridad antes de devolver la ruta.
func (s *Store) Backup(destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(destPath); err == nil {
		return fmt.Errorf("el backup ya existe: %s", destPath)
	}
	if _, err := s.db.Exec(`VACUUM INTO ?`, destPath); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}
	// Verificación de integridad del fichero resultante.
	d, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", destPath))
	if err != nil {
		return err
	}
	defer d.Close()
	var ok string
	if err := d.QueryRow(`PRAGMA quick_check`).Scan(&ok); err != nil || ok != "ok" {
		return fmt.Errorf("backup corrupto (quick_check=%q err=%v)", ok, err)
	}
	return nil
}

// Optimize corre la rutina de mantenimiento nocturno (PRAGMA optimize +
// checkpoint). Barato y recomendado por SQLite tras borrados grandes.
func (s *Store) Optimize() {
	_, _ = s.db.Exec(`PRAGMA optimize`)
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(PASSIVE)`)
}
