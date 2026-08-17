// Package metrics implementa un contador de métricas en formato texto de
// Prometheus, sin dependencias. El endpoint /metrics permite scraping
// futuro sin desplegar hoy ningún servidor de métricas.
package metrics

import (
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// PipeStats son los contadores de un pipe.
type PipeStats struct {
	Reqs  atomic.Int64
	Slow  atomic.Int64
	DurMs atomic.Int64 // suma de duraciones (ms): media = DurMs/Reqs
}

// Metrics es el set de contadores del proceso. Todos los métodos son
// nil-safe (un *Metrics nil es un no-op) para simplificar los tests.
type Metrics struct {
	start        time.Time
	PageHits     atomic.Int64
	Bots         atomic.Int64
	V0Events     atomic.Int64
	Accepted     atomic.Int64 // eventos nuevos almacenados
	Dupes        atomic.Int64 // duplicados descartados (dedup)
	IngestErrors atomic.Int64

	mu    sync.Mutex
	pipes map[string]*PipeStats

	extra func() []string // líneas adicionales (gauges de BD etc.)
}

// New crea el set de métricas.
func New() *Metrics {
	return &Metrics{start: time.Now(), pipes: map[string]*PipeStats{}}
}

// Pipe devuelve (creándolos lazy) los stats de un pipe.
func (m *Metrics) Pipe(name string) *PipeStats {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.pipes[name]
	if !ok {
		st = &PipeStats{}
		m.pipes[name] = st
	}
	return st
}

// SetExtra registra un proveedor de líneas extra (se llama en cada scrape).
func (m *Metrics) SetExtra(f func() []string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.extra = f
	m.mu.Unlock()
}

// Handler sirve GET /metrics.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = fmt.Fprint(w, m.Render())
	})
}

// Render produce el texto Prometheus.
func (m *Metrics) Render() string {
	if m == nil {
		return ""
	}
	var b string
	b += "# HELP ghostbird_uptime_seconds Segundos desde el arranque\n# TYPE ghostbird_uptime_seconds gauge\n"
	b += fmt.Sprintf("ghostbird_uptime_seconds %d\n", int64(time.Since(m.start).Seconds()))

	b += "# HELP ghostbird_page_hits_total Page hits aceptados por el collector\n# TYPE ghostbird_page_hits_total counter\n"
	b += fmt.Sprintf("ghostbird_page_hits_total %d\n", m.PageHits.Load())
	b += "# HELP ghostbird_bots_total Hits filtrados como bot\n# TYPE ghostbird_bots_total counter\n"
	b += fmt.Sprintf("ghostbird_bots_total %d\n", m.Bots.Load())
	b += "# HELP ghostbird_v0_events_requests_total Requests a /v0/events\n# TYPE ghostbird_v0_events_requests_total counter\n"
	b += fmt.Sprintf("ghostbird_v0_events_requests_total %d\n", m.V0Events.Load())
	b += "# HELP ghostbird_events_accepted_total Eventos nuevos almacenados\n# TYPE ghostbird_events_accepted_total counter\n"
	b += fmt.Sprintf("ghostbird_events_accepted_total %d\n", m.Accepted.Load())
	b += "# HELP ghostbird_events_dupes_total Duplicados descartados por dedup\n# TYPE ghostbird_events_dupes_total counter\n"
	b += fmt.Sprintf("ghostbird_events_dupes_total %d\n", m.Dupes.Load())
	b += "# HELP ghostbird_ingest_errors_total Errores de almacenamiento en ingesta\n# TYPE ghostbird_ingest_errors_total counter\n"
	b += fmt.Sprintf("ghostbird_ingest_errors_total %d\n", m.IngestErrors.Load())

	b += "# HELP ghostbird_pipes_requests_total Queries por pipe\n# TYPE ghostbird_pipes_requests_total counter\n"
	m.mu.Lock()
	for name, st := range m.pipes {
		label := pipeLabel(name)
		b += fmt.Sprintf("ghostbird_pipes_requests_total{pipe=%q} %d\n", label, st.Reqs.Load())
	}
	b += "# HELP ghostbird_pipes_duration_ms_sum Milisegundos totales por pipe\n# TYPE ghostbird_pipes_duration_ms_sum counter\n"
	for name, st := range m.pipes {
		label := pipeLabel(name)
		b += fmt.Sprintf("ghostbird_pipes_duration_ms_sum{pipe=%q} %d\n", label, st.DurMs.Load())
	}
	b += "# HELP ghostbird_pipes_slow_total Pipes por encima de 500 ms\n# TYPE ghostbird_pipes_slow_total counter\n"
	for name, st := range m.pipes {
		label := pipeLabel(name)
		b += fmt.Sprintf("ghostbird_pipes_slow_total{pipe=%q} %d\n", label, st.Slow.Load())
	}
	extra := m.extra
	m.mu.Unlock()
	if extra != nil {
		for _, line := range extra() {
			b += line + "\n"
		}
	}
	return b
}

// pipeLabel higieniza el nombre del pipe para label de Prometheus.
func pipeLabel(name string) string {
	out := make([]byte, 0, len(name))
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			out = append(out, byte(c))
		}
	}
	return string(out)
}

// DBSizeLines son las líneas gauge del tamaño de BD/WAL (helper para extra).
func DBSizeLines(dbPath string) []string {
	var lines []string
	if fi, err := os.Stat(dbPath); err == nil {
		lines = append(lines, "# HELP ghostbird_db_size_bytes Tamaño del fichero SQLite\n# TYPE ghostbird_db_size_bytes gauge",
			fmt.Sprintf("ghostbird_db_size_bytes %d", fi.Size()))
	}
	if fi, err := os.Stat(dbPath + "-wal"); err == nil {
		lines = append(lines, "# HELP ghostbird_db_wal_bytes Tamaño del WAL\n# TYPE ghostbird_db_wal_bytes gauge",
			fmt.Sprintf("ghostbird_db_wal_bytes %d", fi.Size()))
	}
	return lines
}
