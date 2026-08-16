package ingest

import (
	"encoding/json"
	"fmt"
	"time"
)

// PageHitRequest es el body que envía ghost-stats.js al collector. El parseo
// es tolerante por contrato: los strings pueden llegar como null, "" o
// ausentes; member_uuid/post_uuid como string literal "undefined"; post_type
// como string literal "null". Ver docs/contract/trafficanalytics-side.md §2.5.
type PageHitRequest struct {
	Timestamp string  `json:"timestamp"` // ignorado: se usa la hora del servidor
	Action    string  `json:"action"`
	Version   string  `json:"version"`
	Payload   Payload `json:"payload"`
}

// Payload es el payload del page_hit (campos puntero para distinguir
// ausente/null/valor).
type Payload struct {
	EventID        *string         `json:"event_id"`
	UserAgent      *string         `json:"user-agent"`
	Locale         *string         `json:"locale"`
	Location       *string         `json:"location"`
	Referrer       *string         `json:"referrer"`
	Pathname       *string         `json:"pathname"`
	Href           *string         `json:"href"`
	SiteUUID       *string         `json:"site_uuid"`
	PostUUID       *string         `json:"post_uuid"`
	PostType       *string         `json:"post_type"`
	GiftLink       *string         `json:"gift_link"`
	MemberUUID     *string         `json:"member_uuid"`
	MemberStatus   *string         `json:"member_status"`
	UtmSource      *string         `json:"utm_source"`
	UtmMedium      *string         `json:"utm_medium"`
	UtmCampaign    *string         `json:"utm_campaign"`
	UtmTerm        *string         `json:"utm_term"`
	UtmContent     *string         `json:"utm_content"`
	ParsedReferrer *ParsedReferrer `json:"parsedReferrer"`
	OS             *string         `json:"os"`
	Browser        *string         `json:"browser"`
	Device         *string         `json:"device"`
	ReferrerURL    *string         `json:"referrerUrl"`
	ReferrerSource *string         `json:"referrerSource"`
	ReferrerMedium *string         `json:"referrerMedium"`
	Meta           *PayloadMeta    `json:"meta"`
}

// PayloadMeta es el bloque meta (received_timestamp del cliente; el collector
// de producción añade referrerSource crudo cuando no hay parsedReferrer).
type PayloadMeta struct {
	ReceivedTimestamp *string `json:"received_timestamp"`
	ReferrerSource    *string `json:"referrerSource"`
}

// ParsedReferrer es el referrer parseado por el navegador.
type ParsedReferrer struct {
	URL    *string `json:"url"`
	Source *string `json:"source"`
	Medium *string `json:"medium"`
}

// str devuelve el valor o "" si es nil. "undefined" se normaliza a "" para
// los campos UUID (member_uuid, post_uuid): así los consume la fase 2.
func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func strUndefined(p *string) string {
	if p == nil || *p == "undefined" {
		return ""
	}
	return *p
}

// EventoEntrante es el evento ya normalizado, listo para enriquecer.
type EventoEntrante struct {
	EventID        string
	UserAgent      string
	Locale         string
	Location       string
	Pathname       string
	Href           string
	SiteUUID       string
	PostUUID       string
	PostType       string
	GiftLink       string
	MemberUUID     string
	MemberStatus   string
	UtmSource      string
	UtmMedium      string
	UtmCampaign    string
	UtmTerm        string
	UtmContent     string
	ReferrerURLIn  string // parsedReferrer.url del cliente
	ReferrerSrcIn  string // parsedReferrer.source del cliente
	ReferrerMedIn  string // parsedReferrer.medium del cliente
	DeviceIn       string // device calculado por un collector upstream (AS)
	OSIn           string
	BrowserIn      string
	ReferrerURLSrv *string // referrerUrl calculado server-side (AS)
	ReferrerSrcSrv *string
	ReferrerMedSrv *string
	ReceivedTsStr  *string   // meta.received_timestamp ISO (de x-ghost-analytics-start)
	RootTs         time.Time // timestamp raíz del evento (ingesta del AS/collector)
}

// Normalizar valida y normaliza el request. siteUUIDHeader es el header
// x-site-uuid (fuente de verdad del site_uuid raíz, como en el AS).
func (r *PageHitRequest) Normalizar(siteUUIDHeader, headerUA string) (EventoEntrante, error) {
	e := EventoEntrante{}

	if r.Action != "page_hit" {
		return e, fmt.Errorf("action inválido: %q", r.Action)
	}

	site := str(r.Payload.SiteUUID)
	if !esGUID(site) {
		return e, fmt.Errorf("payload.site_uuid no es un GUID válido")
	}
	if siteUUIDHeader != "" && !esGUID(siteUUIDHeader) {
		return e, fmt.Errorf("header x-site-uuid no es un GUID válido")
	}
	e.SiteUUID = site
	if siteUUIDHeader != "" {
		e.SiteUUID = siteUUIDHeader
	}

	pathname := capStr(str(r.Payload.Pathname))
	if pathname == "" {
		return e, fmt.Errorf("payload.pathname vacío")
	}
	e.Pathname = pathname

	ua := capStr(str(r.Payload.UserAgent))
	if ua == "" {
		ua = headerUA // tolerante: el header es obligatorio de todas formas
	}
	e.UserAgent = ua
	if e.UserAgent == "" {
		return e, fmt.Errorf("user-agent vacío")
	}

	e.Locale = capStr(str(r.Payload.Locale))
	e.Location = capStr(str(r.Payload.Location))
	e.Href = capStr(str(r.Payload.Href))
	e.EventID = capStr(str(r.Payload.EventID))
	e.PostUUID = strUndefined(r.Payload.PostUUID)
	e.PostType = def(str(r.Payload.PostType), "null")
	e.MemberUUID = strUndefined(r.Payload.MemberUUID)
	e.MemberStatus = capStr(def(str(r.Payload.MemberStatus), "undefined"))
	e.GiftLink = capStr(str(r.Payload.GiftLink))
	e.UtmSource = capStr(str(r.Payload.UtmSource))
	e.UtmMedium = capStr(str(r.Payload.UtmMedium))
	e.UtmCampaign = capStr(str(r.Payload.UtmCampaign))
	e.UtmTerm = capStr(str(r.Payload.UtmTerm))
	e.UtmContent = capStr(str(r.Payload.UtmContent))

	if r.Payload.ParsedReferrer != nil {
		e.ReferrerURLIn = capStr(str(r.Payload.ParsedReferrer.URL))
		e.ReferrerSrcIn = capStr(str(r.Payload.ParsedReferrer.Source))
		e.ReferrerMedIn = capStr(str(r.Payload.ParsedReferrer.Medium))
	}
	e.DeviceIn = capStr(str(r.Payload.Device))
	e.OSIn = capStr(str(r.Payload.OS))
	e.BrowserIn = capStr(str(r.Payload.Browser))
	e.ReferrerURLSrv = capPtr(r.Payload.ReferrerURL)
	e.ReferrerSrcSrv = capPtr(r.Payload.ReferrerSource)
	e.ReferrerMedSrv = capPtr(r.Payload.ReferrerMedium)
	if r.Payload.Meta != nil {
		e.ReceivedTsStr = r.Payload.Meta.ReceivedTimestamp
	}
	return e, nil
}

func def(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// fieldCap acota campos de texto en ingesta: un pathname/UA/href real no
// pasa de unos cientos de bytes; sin cap, un evento hostil de /v0/events
// podía almacenar ~4 MB en un solo campo (y viajar en los pipes).
const fieldCap = 2048

// capStr trunca por BYTES (el corte puede partir un rune multibyte: da igual
// para un valor hostil; los valores legítimos nunca llegan al cap).
func capStr(s string) string {
	if len(s) > fieldCap {
		return s[:fieldCap]
	}
	return s
}

func capPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := capStr(*p)
	return &v
}

// esGUID valida forma 8-4-4-4-12 hex (sin validar versión/variante RFC,
// igual que z.guid() del AS).
func esGUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// ProcesadoAEntrante convierte un evento PROCESADO (formato que el AS manda
// a /v0/events: raíz con site_uuid/session_id/timestamp + payload
// enriquecido) en EventoEntrante + sessionID + timestamp raíz, para
// reutilizar el mismo camino de store.
func ProcesadoAEntrante(raw []byte) (EventoEntrante, string, error) {
	var doc struct {
		Timestamp string  `json:"timestamp"`
		Action    string  `json:"action"`
		SiteUUID  string  `json:"site_uuid"`
		SessionID string  `json:"session_id"`
		Payload   Payload `json:"payload"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return EventoEntrante{}, "", fmt.Errorf("json inválido: %w", err)
	}
	if doc.Action != "page_hit" {
		return EventoEntrante{}, "", fmt.Errorf("action inválido: %q", doc.Action)
	}
	if !esGUID(doc.SiteUUID) {
		return EventoEntrante{}, "", fmt.Errorf("site_uuid raíz no es un GUID válido")
	}
	req := PageHitRequest{Action: doc.Action, Payload: doc.Payload}
	e, err := req.Normalizar(doc.SiteUUID, "")
	if err != nil {
		return EventoEntrante{}, "", err
	}
	if len(doc.SessionID) > 128 {
		doc.SessionID = doc.SessionID[:128]
	}
	// El timestamp raíz (hora de ingesta del AS) es el canónico del evento.
	// Acepto ISO con Z/ms y el formato naive 'YYYY-MM-DD HH:MM:SS' (UTC),
	// que es como el fixture de Tinybird lo trae.
	if ts, err := time.Parse(time.RFC3339Nano, doc.Timestamp); err == nil {
		e.ReceivedTsStr = nil
		e.RootTs = ts
	} else if ts, err := time.Parse("2006-01-02 15:04:05", doc.Timestamp); err == nil {
		e.ReceivedTsStr = nil
		e.RootTs = ts.UTC()
	}
	// Fallback de referrer como mv_hits: payload.referrerSource vacío →
	// meta.referrerSource (el collector de producción lo pone ahí).
	if e.ReferrerSrcSrv == nil || str(e.ReferrerSrcSrv) == "" {
		if doc.Payload.Meta != nil && doc.Payload.Meta.ReferrerSource != nil {
			e.ReferrerSrcSrv = doc.Payload.Meta.ReferrerSource
		}
	}
	return e, doc.SessionID, nil
}
