package ingest

import (
	"bytes"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/gnacho/ghostbird/internal/store"
)

// eventsBodyLimit: batches del AS de 50 eventos (~50-100 KB); margen holgado.
const eventsBodyLimit = 4 << 20

// handleEvents implementa POST /v0/events?name=analytics_events[&wait=true]:
// la Events API de Tinybird que consume TrafficAnalytics. Acepta un objeto
// JSON o NDJSON multilínea; auth por Bearer o ?token=; dedup por event_id.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		jsonError(w, http.StatusUnauthorized, "token de ingesta inválido o ausente")
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		jsonError(w, http.StatusBadRequest, "parámetro name requerido")
		return
	}

	body, err := readBodyLimited(w, r, eventsBodyLimit)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "body inválido o demasiado grande")
		return
	}

	now := s.nowF()
	var evs []store.Event
	var errores []string
	for _, line := range splitLines(body) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		ev, err := s.eventoDesdeProcesado(line, now)
		if err != nil {
			if isBot(err) {
				continue // descarte silencioso, como el worker del AS
			}
			errores = append(errores, err.Error())
			continue
		}
		evs = append(evs, ev)
	}
	if evs == nil && errores != nil {
		// Ninguna línea válida: el emisor reintenta el batch entero si es
		// 4xx/5xx; un 400 le dice que no lo reintente (contrato remote-write).
		jsonError(w, http.StatusBadRequest, "ningún evento válido: "+strings.Join(errores, "; "))
		return
	}
	if _, err := s.st.InsertEvents(now, evs); err != nil {
		s.log.Error("insertar batch /v0/events", "error", err, "eventos", len(evs))
		jsonError(w, http.StatusInternalServerError, "no se pudo almacenar el batch")
		return
	}
	// wait=true: respondemos tras persistir (nuestros inserts son síncronos).
	writeJSON(w, http.StatusAccepted, map[string]any{"success": true, "accepted": len(evs)})
}

// eventoDesdeProcesado convierte una línea NDJSON (o un JSON único) en
// store.Event, replicando el enriquecimiento de mv_hits: os/browser del UA,
// source normalizado y device con fallback.
func (s *Server) eventoDesdeProcesado(line []byte, now time.Time) (store.Event, error) {
	e, sessionID, err := ProcesadoAEntrante(line)
	if err != nil {
		return store.Event{}, err
	}
	if e.DeviceIn == "bot" {
		// El worker del AS descarta device=bot de forma defensiva; idem.
		return store.Event{}, errBot
	}
	uaLower := strings.ToLower(e.UserAgent)
	os := DerivarOS(uaLower)
	browser := DerivarBrowser(uaLower)
	device := e.DeviceIn
	if device == "" {
		device = DerivarDevice(uaLower)
	}

	// Referrer: el evento procesado del AS ya trae referrerSource a nivel
	// payload; si no (evento crudo), se parsea server-side.
	refSource := str(e.ReferrerSrcSrv)
	refURL := str(e.ReferrerURLSrv)
	refMedium := str(e.ReferrerMedSrv)
	if refSource == "" && refURL == "" && e.ReferrerURLIn != "" {
		res := ParseReferrer(e.ReferrerURLIn, e.ReferrerSrcIn, e.ReferrerMedIn, "")
		refURL, refSource, refMedium = res.URL, res.Source, res.Medium
	}

	eventID := e.EventID
	if eventID == "" {
		eventID = uuid.NewString()
	}
	if sessionID == "" {
		sessionID = "0" // mv_hits: coalesce(session_id, '0')
	}

	var ts int64 = now.Unix()
	if !e.RootTs.IsZero() {
		ts = e.RootTs.Unix()
	}

	return store.Event{
		Ts:             ts,
		SiteUUID:       e.SiteUUID,
		SessionID:      sessionID,
		EventID:        eventID,
		Action:         "page_hit",
		Pathname:       e.Pathname,
		Href:           e.Href,
		PostUUID:       e.PostUUID,
		PostType:       e.PostType,
		MemberUUID:     e.MemberUUID,
		MemberStatus:   e.MemberStatus,
		GiftLink:       e.GiftLink,
		Location:       e.Location,
		Locale:         e.Locale,
		OS:             os,
		Browser:        browser,
		Device:         device,
		UserAgent:      e.UserAgent,
		ReferrerURL:    refURL,
		ReferrerSource: refSource,
		ReferrerMedium: refMedium,
		Source:         NormalizarSource(refSource),
		UtmSource:      e.UtmSource,
		UtmMedium:      e.UtmMedium,
		UtmCampaign:    e.UtmCampaign,
		UtmTerm:        e.UtmTerm,
		UtmContent:     e.UtmContent,
		Raw:            string(line),
	}, nil
}

// errBot marca eventos de bot descartados silenciosamente.
var errBot = &botError{}

type botError struct{}

func (*botError) Error() string { return "evento de bot descartado" }

func isBot(err error) bool {
	_, ok := err.(*botError)
	return ok
}

// authorized valida el token de ingesta: Bearer header (preferente) o
// ?token= (lo usa el AS en modo proxy sin token en env). Sin token
// configurado se acepta todo (igual de abierto que el collector del AS).
// Comparación constant-time (P3 auditoría).
func (s *Server) authorized(r *http.Request) bool {
	if s.cfg.IngestToken == "" {
		return true
	}
	if h := r.Header.Get("Authorization"); h != "" {
		parts := strings.SplitN(h, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") &&
			subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.cfg.IngestToken)) == 1 {
			return true
		}
	}
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(s.cfg.IngestToken)) == 1 {
		return true
	}
	return false
}

func readBodyLimited(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	return readAll(http.MaxBytesReader(w, r.Body, limit))
}

func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func splitLines(b []byte) [][]byte {
	return bytes.Split(b, []byte("\n"))
}
