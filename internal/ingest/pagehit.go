package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/gnacho/ghostbird/internal/store"
)

// pageHitBodyLimit replica el bodyLimit del AS (20 KB).
const pageHitBodyLimit = 20 << 10

// handlePageHit implementa POST /api/v1/page_hit: valida, filtra bots,
// enriquece (UA, referrer, session_id) y almacena. Réplica del pipeline de
// TrafficAnalytics (docs/contract/trafficanalytics-side.md §3).
func (s *Server) handlePageHit(w http.ResponseWriter, r *http.Request) {
	// Cabeceras obligatorias (schema de request del AS).
	siteUUIDHeader := r.Header.Get("x-site-uuid")
	headerUA := r.Header.Get("User-Agent")
	if siteUUIDHeader == "" {
		jsonError(w, http.StatusBadRequest, "header x-site-uuid requerido")
		return
	}
	if headerUA == "" {
		jsonError(w, http.StatusBadRequest, "header user-agent requerido")
		return
	}
	ct := r.Header.Get("Content-Type")
	if !containsJSON(ct) {
		jsonError(w, http.StatusBadRequest, "content-type debe ser application/json")
		return
	}

	// Bots: mismo 202 que un hit normal, sin almacenar (stealth, como el AS).
	if EsBot(headerUA) {
		if s.m != nil {
			s.m.Bots.Add(1)
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"message": "Page hit event received"})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, pageHitBodyLimit))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "body inválido o demasiado grande")
		return
	}
	var req PageHitRequest
	if err := json.Unmarshal(body, &req); err != nil {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("json inválido: %v", err))
		return
	}

	e, err := req.Normalizar(siteUUIDHeader, headerUA)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	ev, err := s.enriquecer(r, e)
	if err != nil {
		s.log.Error("enriquecer page_hit", "error", err)
		jsonError(w, http.StatusInternalServerError, "no se pudo procesar el evento")
		return
	}
	if _, err := s.st.InsertEvents(s.nowF(), []store.Event{ev}); err != nil {
		s.log.Error("insertar page_hit", "error", err)
		if s.m != nil {
			s.m.IngestErrors.Add(1)
		}
		jsonError(w, http.StatusInternalServerError, "no se pudo almacenar el evento")
		return
	}
	if s.m != nil {
		s.m.PageHits.Add(1)
		s.m.Accepted.Add(1)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "Page hit event received"})
}

// enriquecer calcula todo lo que añade el collector: os/browser/device,
// referrer server-side, source normalizado, session_id, event_id y el JSON
// canónico del evento procesado.
func (s *Server) enriquecer(r *http.Request, e EventoEntrante) (store.Event, error) {
	now := s.nowF().UTC()

	uaLower := strings.ToLower(e.UserAgent)
	os := DerivarOS(uaLower)
	browser := DerivarBrowser(uaLower)
	device := e.DeviceIn
	if device == "" {
		device = DerivarDevice(uaLower)
	}

	// Referrer server-side: solo si parsedReferrer.url es truthy (como el AS).
	var refURL, refSource, refMedium string
	if e.ReferrerURLIn != "" {
		res := ParseReferrer(e.ReferrerURLIn, e.ReferrerSrcIn, e.ReferrerMedIn, "")
		refURL, refSource, refMedium = res.URL, res.Source, res.Medium
	}

	// session_id = firma (sal diaria por sitio + IP + UA).
	day := SaltDay(now)
	salt, err := s.st.GetOrCreateSalt(day, e.SiteUUID)
	if err != nil {
		return store.Event{}, fmt.Errorf("sal de firma: %w", err)
	}
	ip := s.clientIP(r)
	sessionID := SessionID(salt, e.SiteUUID, ip, e.UserAgent)

	eventID := e.EventID
	if eventID == "" {
		eventID = uuid.NewString()
	}

	var receivedMs int64
	if v := e.ReceivedTsStr; v != nil {
		if t, err := time.Parse(time.RFC3339Nano, *v); err == nil {
			receivedMs = t.UnixMilli()
		}
	} else if hdr := r.Header.Get("x-ghost-analytics-start"); hdr != "" {
		if ms, err := strconv.ParseInt(hdr, 10, 64); err == nil {
			receivedMs = ms
		}
	}

	// JSON canónico del evento procesado (misma forma que el AS manda a
	// /v0/events) para trazabilidad y reprocesado.
	raw := construirRaw(now, e, eventID, sessionID, os, browser, device, refURL, refSource, refMedium)

	return store.Event{
		Ts:             now.Unix(),
		ReceivedMs:     receivedMs,
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
		Raw:            raw,
	}, nil
}

// construirRaw serializa el evento procesado con el mismo shape que el AS.
func construirRaw(now time.Time, e EventoEntrante, eventID, sessionID, os, browser, device, refURL, refSource, refMedium string) string {
	payload := map[string]any{
		"event_id":      eventID,
		"site_uuid":     e.SiteUUID,
		"member_uuid":   e.MemberUUID,
		"member_status": e.MemberStatus,
		"post_uuid":     e.PostUUID,
		"post_type":     e.PostType,
		"locale":        e.Locale,
		"location":      e.Location,
		"pathname":      e.Pathname,
		"href":          e.Href,
		"os":            os,
		"browser":       browser,
		"device":        device,
		"user-agent":    e.UserAgent,
		"meta": map[string]any{
			"received_timestamp": now.Format("2006-01-02T15:04:05.000Z"),
		},
	}
	if e.GiftLink != "" {
		payload["gift_link"] = e.GiftLink
	}
	if e.ReferrerURLIn != "" {
		payload["parsedReferrer"] = map[string]any{
			"url":    e.ReferrerURLIn,
			"source": e.ReferrerSrcIn,
			"medium": e.ReferrerMedIn,
		}
		payload["referrerUrl"] = refURL
		payload["referrerSource"] = refSource
		payload["referrerMedium"] = refMedium
	}
	for k, v := range map[string]string{
		"utm_source": e.UtmSource, "utm_medium": e.UtmMedium, "utm_campaign": e.UtmCampaign,
		"utm_term": e.UtmTerm, "utm_content": e.UtmContent,
	} {
		if v != "" {
			payload[k] = v
		}
	}
	doc := map[string]any{
		"timestamp":  now.Format("2006-01-02T15:04:05.000Z"),
		"action":     "page_hit",
		"version":    "1",
		"site_uuid":  e.SiteUUID,
		"session_id": sessionID,
		"payload":    payload,
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

func containsJSON(ct string) bool {
	media := ct
	if i := strings.IndexByte(media, ';'); i >= 0 {
		media = media[:i]
	}
	return strings.EqualFold(strings.TrimSpace(media), "application/json")
}
