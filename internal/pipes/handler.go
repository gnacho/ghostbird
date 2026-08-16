package pipes

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gnacho/ghostbird/internal/config"
	"github.com/gnacho/ghostbird/internal/store"
)

// Handler sirve GET /v0/pipes/{name}.json con auth JWT HS256 (o token
// estático), respondiendo {"meta":[...],"data":[...],"rows":N} como la
// Query API de Tinybird. Ghost SOLO consume .data.
type Handler struct {
	cfg  *config.Config
	eng  *Engine
	log  *slog.Logger
	nowF func() time.Time
}

// NewHandler construye el handler de pipes.
func NewHandler(cfg *config.Config, st *store.Store, log *slog.Logger) *Handler {
	nowF := time.Now
	return &Handler{cfg: cfg, eng: NewEngine(st, nowF), log: log, nowF: nowF}
}

// ServeHTTP enruta /v0/pipes/{name}.json.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "método no permitido"})
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/v0/pipes/")
	name = strings.TrimSuffix(name, ".json")
	pipe := canonicalPipe(name)
	if pipe == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "pipe desconocido: " + name})
		return
	}

	if !h.authorize(r, pipe) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized", "message": "token inválido o sin scope para " + pipe})
		return
	}

	p, err := ParseParams(r.URL.Query(), h.nowF)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Bad Request", "message": err.Error()})
		return
	}

	data, err := h.eng.Run(pipe, p)
	if err != nil {
		h.log.Error("pipe error", "pipe", pipe, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Bad Request", "message": err.Error()})
		return
	}
	if data == nil {
		data = []row{}
	}
	resp := map[string]any{
		"meta": pipeMeta(pipe),
		"data": data,
		"rows": len(data),
	}
	writeJSON(w, http.StatusOK, resp)
}

// authorize: si hay AdminToken configurado → JWT HS256 válido con scope del
// pipe y fixed_params.site_uuid == query.site_uuid; alternativa token
// estático (stats.token / stats.local.token de Ghost). Sin nada configurado
// → acceso libre (modo local abierto, como tinybird-local en dev).
func (h *Handler) authorize(r *http.Request, pipe string) bool {
	bearer := bearerToken(r)
	q := r.URL.Query()

	if h.cfg.AdminToken != "" && bearer != "" {
		claims, err := VerifyJWT(bearer, h.cfg.AdminToken)
		if err == nil {
			if err := claims.AuthorizePipe(pipe, q.Get("site_uuid"), h.nowF()); err != nil {
				h.log.Warn("jwt sin permiso", "pipe", pipe, "error", err)
				return false
			}
			return true
		}
		// No era JWT válido: cae al token estático si coincide.
	}
	if h.cfg.StatsToken != "" {
		return bearer == h.cfg.StatsToken || q.Get("token") == h.cfg.StatsToken
	}
	if h.cfg.AdminToken != "" {
		return false // AdminToken configurado exige JWT (o stats-token)
	}
	return true // sin auth configurada: modo local abierto
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	parts := strings.SplitN(h, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// canonicalPipe normaliza el nombre: alias _v2/_v3/_router → pipe v1 (los
// v2/v3 están aparcados en Ghost y solo se activan con stats.version).
func canonicalPipe(name string) string {
	for _, suffix := range []string{"_v2", "_v3", "_router"} {
		if strings.HasSuffix(name, suffix) {
			name = strings.TrimSuffix(name, suffix)
			break
		}
	}
	switch name {
	case "api_kpis", "api_top_pages", "api_post_visitor_counts", "api_top_sources",
		"api_top_locations", "api_top_devices", "api_top_utm_sources", "api_top_utm_mediums",
		"api_top_utm_campaigns", "api_top_utm_contents", "api_top_utm_terms",
		"api_active_visitors", "api_gift_link_visits":
		return name
	default:
		return ""
	}
}

// pipeMeta emite meta mínima (nombre/tipo por columna) como Tinybird.
func pipeMeta(pipe string) []map[string]string {
	str := func(cols ...string) []map[string]string {
		out := make([]map[string]string, 0, len(cols))
		for _, c := range cols {
			out = append(out, map[string]string{"name": c, "type": "String"})
		}
		return out
	}
	switch pipe {
	case "api_kpis":
		return []map[string]string{
			{"name": "date", "type": "DateTime"},
			{"name": "visits", "type": "UInt64"},
			{"name": "pageviews", "type": "UInt64"},
			{"name": "bounce_rate", "type": "Float64"},
			{"name": "avg_session_sec", "type": "Float64"},
		}
	case "api_top_pages":
		return append(str("post_uuid", "pathname"), map[string]string{"name": "visits", "type": "UInt64"})
	case "api_post_visitor_counts":
		return append(str("post_uuid"), map[string]string{"name": "visits", "type": "UInt64"})
	case "api_active_visitors":
		return []map[string]string{{"name": "active_visitors", "type": "UInt64"}}
	case "api_gift_link_visits":
		return []map[string]string{
			{"name": "gift_link", "type": "String"},
			{"name": "visits", "type": "UInt64"},
			{"name": "views", "type": "UInt64"},
			{"name": "last_seen", "type": "DateTime"},
		}
	case "api_top_locations":
		return []map[string]string{
			{"name": "location", "type": "String"},
			{"name": "visits", "type": "UInt64"},
		}
	case "api_top_devices":
		return []map[string]string{
			{"name": "device", "type": "String"},
			{"name": "visits", "type": "UInt64"},
		}
	case "api_top_sources":
		return []map[string]string{
			{"name": "source", "type": "String"},
			{"name": "visits", "type": "UInt64"},
		}
	default: // utm_*
		col := strings.TrimPrefix(strings.TrimPrefix(pipe, "api_top_utm_"), "")
		return []map[string]string{
			{"name": col, "type": "String"},
			{"name": "visits", "type": "UInt64"},
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
