// Package ingest implementa los endpoints HTTP de GhostBird: el collector
// POST /api/v1/page_hit (lo que llama ghost-stats.js), la Events API
// POST /v0/events (compatibilidad con TrafficAnalytics como collector) y
// GET /healthz.
package ingest

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gnacho/ghostbird/internal/config"
	"github.com/gnacho/ghostbird/internal/store"
)

// Server es el servicio HTTP de ingesta.
type Server struct {
	cfg  *config.Config
	st   *store.Store
	log  *slog.Logger
	nowF func() time.Time // inyectable para tests
}

// NewServer construye el servidor de ingesta.
func NewServer(cfg *config.Config, st *store.Store, log *slog.Logger) *Server {
	return &Server{cfg: cfg, st: st, log: log, nowF: time.Now}
}

// Handler devuelve el http.Handler con CORS y todas las rutas.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /api/v1/page_hit", s.handlePageHit)
	mux.HandleFunc("POST /v0/events", s.handleEvents)
	return cors(mux)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	type health struct {
		Status string `json:"status"`
		Events int64  `json:"events"`
		Now    string `json:"now"`
	}
	h := health{Status: "ok", Now: s.nowF().UTC().Format(time.RFC3339)}
	status := http.StatusOK
	if s.st.Ping() != nil {
		h.Status = "degraded"
		status = http.StatusServiceUnavailable
	} else if n, err := s.st.CountEvents(); err == nil {
		h.Events = n
	}
	writeJSON(w, status, h)
}

// CORS replica la política CORS de TrafficAnalytics (src/plugins/cors.ts):
// origin *, métodos GET/POST/PUT/DELETE/OPTIONS y las mismas cabeceras
// permitadas (x-site-uuid incluida). El navegador del visitante Y el del
// Admin llaman cross-origin a este servicio. Exportada: main la aplica al
// árbol completo (pipes incluidos).
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Origin, X-Requested-With, Content-Type, Accept, Authorization, x-site-uuid")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// cors alias interno.
var cors = CORS

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": http.StatusText(status), "message": msg})
}

// clientIP resuelve la IP del cliente. Con trust-proxy activo (default, como
// el AS) se toma la PRIMERA entrada de X-Forwarded-For.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.SplitN(xff, ",", 2)[0]
			return strings.TrimSpace(first)
		}
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return strings.TrimSpace(host)
}
