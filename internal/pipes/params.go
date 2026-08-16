// Package pipes implementa la Query API de Tinybird: GET /v0/pipes/{name}.json
// con la semántica EXACTA de los pipes de Ghost (docs/contract/ghost-side.md).
// Los tests de fidelidad corren los mismos casos YAML que Tinybird
// (testdata/) contra el mismo fixture de eventos.
package pipes

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // binario estático sin depender del tzdata del sistema
)

// Params son los query params comunes de los pipes, ya normalizados.
type Params struct {
	SiteUUID    string
	DateFrom    string // 'YYYY-MM-DD' o ""
	DateTo      string
	Timezone    string // default Etc/UTC
	CurrentTime string // override de "ahora" (tests)

	MemberStatus []string // CSV expandido (paid → +comped+gift)
	Location     string
	Pathname     string
	PostUUID     string
	GiftLink     string // '' = sin filtro; 'false'/'0' = gift_link=''; resto = gift_link!=''
	PostType     string

	Source      string
	Device      string
	UtmSource   string
	UtmMedium   string
	UtmCampaign string
	UtmTerm     string
	UtmContent  string

	Limit int
	Skip  int

	PostUUIDs []string // solo api_post_visitor_counts

	// Derivados:
	Loc       *time.Location
	Now       time.Time // now (o current_time) en UTC
	SingleDay bool      // dateFrom == dateTo
	SourceSet bool      // source PRESENTE en la query (aunque vacío: filtro "Direct" del Admin)
}

// ParseParams extrae y valida los params comunes del query string.
func ParseParams(q url.Values, nowFunc func() time.Time) (Params, error) {
	p := Params{
		SiteUUID:    q.Get("site_uuid"),
		DateFrom:    q.Get("date_from"),
		DateTo:      q.Get("date_to"),
		Timezone:    q.Get("timezone"),
		CurrentTime: q.Get("current_time"),
		Location:    q.Get("location"),
		Pathname:    q.Get("pathname"),
		PostUUID:    q.Get("post_uuid"),
		GiftLink:    q.Get("gift_link"),
		PostType:    q.Get("post_type"),
		Source:      q.Get("source"),
		Device:      q.Get("device"),
		UtmSource:   q.Get("utm_source"),
		UtmMedium:   q.Get("utm_medium"),
		UtmCampaign: q.Get("utm_campaign"),
		UtmTerm:     q.Get("utm_term"),
		UtmContent:  q.Get("utm_content"),
	}
	if p.SiteUUID == "" {
		return p, fmt.Errorf("site_uuid requerido")
	}
	if p.Timezone == "" {
		p.Timezone = "Etc/UTC"
	}
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil {
		return p, fmt.Errorf("timezone inválida: %q", p.Timezone)
	}
	p.Loc = loc

	p.Now = nowFunc()
	if p.CurrentTime != "" {
		ct, err := time.ParseInLocation("2006-01-02 15:04:05", p.CurrentTime, loc)
		if err != nil {
			return p, fmt.Errorf("current_time inválido: %q", p.CurrentTime)
		}
		p.Now = ct
	}

	if ms := q.Get("member_status"); ms != "" {
		p.MemberStatus = expandMemberStatus(strings.Split(ms, ","))
	}

	p.Limit = 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return p, fmt.Errorf("limit inválido: %q", v)
		}
		p.Limit = n
	}
	p.Skip = 0
	if v := q.Get("skip"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return p, fmt.Errorf("skip inválido: %q", v)
		}
		p.Skip = n
	}

	if v := q.Get("post_uuids"); v != "" {
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				p.PostUUIDs = append(p.PostUUIDs, s)
			}
		}
	}

	for _, d := range []struct{ name, val string }{{"date_from", p.DateFrom}, {"date_to", p.DateTo}} {
		if d.val != "" {
			if _, err := time.ParseInLocation("2006-01-02", d.val, loc); err != nil {
				return p, fmt.Errorf("%s inválido: %q (espero YYYY-MM-DD)", d.name, d.val)
			}
		}
	}
	p.SingleDay = p.DateFrom != "" && p.DateFrom == p.DateTo
	// Ghost manda source= VACÍO para "Direct" (sources-card.tsx
	// onSourceClick(isDirectTraffic ? '' : source)) y el pipe real filtra
	// source=''. Distinguimos presente-y-vacío de ausente.
	p.SourceSet = false
	if vs, ok := q["source"]; ok && len(vs) > 0 {
		p.Source = vs[0]
		p.SourceSet = true
	}

	// Cap de rango (endurecimiento): series de años serían millones de
	// buckets en RAM. Ghost pide como mucho 30 días.
	if p.DateFrom != "" && p.DateTo != "" {
		from := dayEpoch(p.DateFrom, loc)
		to := dayEpoch(p.DateTo, loc)
		if to >= from && (to-from)/86400 > 366 {
			return p, fmt.Errorf("rango de fechas demasiado grande (máx 366 días)")
		}
	}
	return p, nil
}

// expandMemberStatus replica la expansión de filtered_sessions: 'paid' añade
// 'comped' y 'gift'.
func expandMemberStatus(in []string) []string {
	var out []string
	hasPaid := false
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
		if s == "paid" {
			hasPaid = true
		}
	}
	if hasPaid {
		out = append(out, "comped", "gift")
	}
	return out
}

// dayEpoch devuelve el epoch UTC de la medianoche (en loc) de la fecha.
func dayEpoch(date string, loc *time.Location) int64 {
	t, _ := time.ParseInLocation("2006-01-02", date, loc)
	return t.Unix()
}

// today devuelve el día (en loc) del "ahora" efectivo.
func (p *Params) today() time.Time {
	return time.Date(p.Now.In(p.Loc).Year(), p.Now.In(p.Loc).Month(), p.Now.In(p.Loc).Day(), 0, 0, 0, 0, p.Loc)
}

// hitWindow devuelve [from, to) para el stage hit-level de filtered_sessions:
// defaults = últimos 7 días .. mañana (en loc), igual que el pipe.
func (p *Params) hitWindow() (from, to int64) {
	if p.DateFrom != "" {
		from = dayEpoch(p.DateFrom, p.Loc)
	} else {
		from = p.today().AddDate(0, 0, -7).Unix()
	}
	if p.DateTo != "" {
		to = dayEpoch(p.DateTo, p.Loc) + 86400
	} else {
		to = p.today().AddDate(0, 0, 1).Unix()
	}
	return from, to
}

// sessionWindow: mismo default que hitWindow pero sobre first_pageview.
func (p *Params) sessionWindow() (from, to int64) {
	return p.hitWindow()
}

// giftBool: true = gift_link != ”, false = gift_link = ”.
func giftBool(v string) bool {
	return !(v == "false" || v == "0")
}

// hitFilters construye el WHERE hit-level compartido (sesiones con AL MENOS
// un hit que cumpla). args se rellena en orden.
func (p *Params) hitFilters(pred *strings.Builder, args *[]any, col string) {
	if len(p.MemberStatus) > 0 {
		pred.WriteString(" AND " + col + ".member_status IN (")
		for i, v := range p.MemberStatus {
			if i > 0 {
				pred.WriteString(",")
			}
			pred.WriteString("?")
			*args = append(*args, v)
		}
		pred.WriteString(")")
	}
	if p.Location != "" {
		pred.WriteString(" AND " + col + ".location = ?")
		*args = append(*args, p.Location)
	}
	if p.Pathname != "" {
		pred.WriteString(" AND " + col + ".pathname = ?")
		*args = append(*args, p.Pathname)
	}
	if p.PostUUID != "" {
		pred.WriteString(" AND " + col + ".post_uuid = ?")
		*args = append(*args, p.PostUUID)
	}
	if p.GiftLink != "" {
		if giftBool(p.GiftLink) {
			pred.WriteString(" AND " + col + ".gift_link != ''")
		} else {
			pred.WriteString(" AND " + col + ".gift_link = ''")
		}
	}
}

// hasSessionFilters dice si hay filtros de atributos de sesión (source,
// device, utm_*): solo entonces se une contra los atributos del primer hit.
// OJO: source PRESENTE y vacío ("Direct") ES un filtro de sesión — el pipe
// real aplica {% if defined(source) %} y source = ” matchea tráfico directo.
func (p *Params) hasSessionFilters() bool {
	return p.SourceSet || p.Device != "" || p.UtmSource != "" ||
		p.UtmMedium != "" || p.UtmCampaign != "" || p.UtmTerm != "" || p.UtmContent != ""
}

// filteredSessionsSQL construye el CTE `fs` (equivalente a filtered_sessions):
// sesiones que pasan el stage hit-level y, si hay filtros de sesión, cuyos
// atributos del PRIMER hit (y first_pageview) cumplen. Devuelve el fragmento
// "fs AS (SELECT session_id FROM ...)" y sus args.
func (p *Params) filteredSessionsSQL() (string, []any) {
	var b strings.Builder
	var args []any

	// Stage 1: sesiones con ≥1 hit que cumpla los filtros hit-level.
	b.WriteString("sfha AS (SELECT DISTINCT session_id FROM events WHERE site_uuid = ? AND ts >= ? AND ts < ?")
	from, to := p.hitWindow()
	args = append(args, p.SiteUUID, from, to)
	p.hitFilters(&b, &args, "events")
	if !p.hasSessionFilters() {
		return b.String() + "), fs AS (SELECT session_id FROM sfha)", args
	}

	// Stage 2: atributos del primer hit de cada sesión.
	sfrom, sto := p.sessionWindow()
	b.WriteString("), firsthit AS (SELECT session_id, ts AS first_ts, source, device, utm_source, utm_medium, utm_campaign, utm_term, utm_content FROM (SELECT session_id, ts, source, device, utm_source, utm_medium, utm_campaign, utm_term, utm_content, ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY ts, id) AS rn FROM events WHERE site_uuid = ?) WHERE rn = 1)")
	args = append(args, p.SiteUUID)
	b.WriteString(", fs AS (SELECT fh.session_id FROM firsthit fh INNER JOIN sfha ON sfha.session_id = fh.session_id WHERE fh.first_ts >= ? AND fh.first_ts < ?")
	args = append(args, sfrom, sto)
	if p.Device != "" {
		b.WriteString(" AND fh.device = ?")
		args = append(args, p.Device)
	}
	if p.SourceSet {
		b.WriteString(" AND fh.source = ?")
		args = append(args, p.Source) // '' incluido: filtro Direct
	}
	for _, u := range []struct{ col, val string }{
		{"utm_source", p.UtmSource}, {"utm_medium", p.UtmMedium}, {"utm_campaign", p.UtmCampaign},
		{"utm_term", p.UtmTerm}, {"utm_content", p.UtmContent},
	} {
		if u.val != "" {
			b.WriteString(" AND fh." + u.col + " = ?")
			args = append(args, u.val)
		}
	}
	b.WriteString(")")
	return b.String(), args
}
