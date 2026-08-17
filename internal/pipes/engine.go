package pipes

import (
	"fmt"
	"strings"
	"time"

	"github.com/gnacho/ghostbird/internal/store"
)

// Engine ejecuta los pipes contra el store.
type Engine struct {
	st   *store.Store
	nowF func() time.Time
}

// NewEngine construye el motor de queries.
func NewEngine(st *store.Store, nowFunc func() time.Time) *Engine {
	if nowFunc == nil {
		nowFunc = time.Now
	}
	return &Engine{st: st, nowF: nowFunc}
}

// row es una fila de respuesta.
type row = map[string]any

// Run ejecuta el pipe por nombre y devuelve las filas.
func (e *Engine) Run(pipe string, p Params) ([]row, error) {
	switch pipe {
	case "api_kpis":
		return e.kpis(p)
	case "api_top_pages":
		return e.topPages(p)
	case "api_post_visitor_counts":
		return e.postVisitorCounts(p)
	case "api_top_sources":
		return e.firstHitGroupBy(p, "source", "", true)
	case "api_top_devices":
		return e.firstHitGroupBy(p, "device", "", true)
	case "api_top_utm_sources":
		return e.firstHitGroupBy(p, "utm_source", "", false)
	case "api_top_utm_mediums":
		return e.firstHitGroupBy(p, "utm_medium", "", false)
	case "api_top_utm_campaigns":
		return e.firstHitGroupBy(p, "utm_campaign", "", false)
	case "api_top_utm_contents":
		return e.firstHitGroupBy(p, "utm_content", "", false)
	case "api_top_utm_terms":
		return e.firstHitGroupBy(p, "utm_term", "", false)
	case "api_top_locations":
		return e.topLocations(p)
	case "api_active_visitors":
		return e.activeVisitors(p)
	case "api_gift_link_visits":
		return e.giftLinkVisits(p)
	default:
		return nil, fmt.Errorf("pipe desconocido: %s", pipe)
	}
}

// topPages: api_top_pages — hits de las sesiones que clasifican, agrupados
// por (post_uuid, pathname), visits = sesiones únicas.
func (e *Engine) topPages(p Params) ([]row, error) {
	cte, cteArgs := p.filteredSessionsSQL()
	var b strings.Builder
	b.WriteString("WITH " + cte + " SELECT CASE WHEN h.post_uuid = 'undefined' THEN '' ELSE h.post_uuid END AS post_uuid, h.pathname, COUNT(DISTINCT h.session_id) AS visits FROM events h INNER JOIN fs ON fs.session_id = h.session_id WHERE h.site_uuid = ?")
	args := append([]any{}, cteArgs...)
	args = append(args, p.SiteUUID)
	p.hitFilters(&b, &args, "h")
	if p.PostType != "" {
		if p.PostType == "post" {
			b.WriteString(" AND h.post_type = 'post'")
		} else {
			b.WriteString(" AND (h.post_type != 'post' OR h.post_type IS NULL)")
		}
	}
	b.WriteString(" GROUP BY 1, 2 ORDER BY visits DESC, post_uuid ASC, pathname ASC LIMIT ? OFFSET ?")
	args = append(args, p.Limit, p.Skip)

	rows, err := e.st.Query(b.String(), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []row
	for rows.Next() {
		var postUUID, pathname string
		var visits int64
		if err := rows.Scan(&postUUID, &pathname, &visits); err != nil {
			return nil, err
		}
		out = append(out, row{"post_uuid": postUUID, "pathname": pathname, "visits": visits})
	}
	return out, rows.Err()
}

// postVisitorCounts: all-time, post_uuids CSV, sin limit.
func (e *Engine) postVisitorCounts(p Params) ([]row, error) {
	if len(p.PostUUIDs) == 0 {
		return nil, fmt.Errorf("post_uuids requerido")
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(p.PostUUIDs)), ",")
	args := make([]any, 0, len(p.PostUUIDs)+1)
	args = append(args, p.SiteUUID)
	for _, u := range p.PostUUIDs {
		args = append(args, u)
	}
	q := "SELECT post_uuid, COUNT(DISTINCT session_id) AS visits FROM events WHERE site_uuid = ? AND post_uuid IN (" + ph + ") GROUP BY post_uuid ORDER BY visits DESC, post_uuid ASC"
	rows, err := e.st.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []row
	for rows.Next() {
		var postUUID string
		var visits int64
		if err := rows.Scan(&postUUID, &visits); err != nil {
			return nil, err
		}
		out = append(out, row{"post_uuid": postUUID, "visits": visits})
	}
	return out, rows.Err()
}

// firstHitGroupBy: pipes de atributos de sesión (source/device/utm_*):
// count() de sesiones agrupadas por el atributo del primer hit (tabla
// sessions agregada: O(sesiones), no O(eventos)).
// includeEmpty=true incluye ” (tráfico directo); los utm_* lo excluyen.
func (e *Engine) firstHitGroupBy(p Params, col, _ string, includeEmpty bool) ([]row, error) {
	cte, cteArgs := p.filteredSessionsSQL()
	q := "WITH " + cte + " SELECT sd." + col + " AS v, COUNT(*) AS visits FROM sessions sd INNER JOIN fs ON fs.session_id = sd.session_id WHERE sd.site_uuid = ?"
	args := append([]any{}, cteArgs...)
	args = append(args, p.SiteUUID)
	if !includeEmpty {
		q += " AND sd." + col + " != ''"
	}
	q += " GROUP BY sd." + col + " ORDER BY visits DESC, v ASC LIMIT ? OFFSET ?"
	args = append(args, p.Limit, p.Skip)

	rows, err := e.st.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []row
	for rows.Next() {
		var v string
		var visits int64
		if err := rows.Scan(&v, &visits); err != nil {
			return nil, err
		}
		out = append(out, row{col: v, "visits": visits})
	}
	return out, rows.Err()
}

// topLocations: hits de sesiones que clasifican, uniqExact(session_id) por
// location (hit-level).
func (e *Engine) topLocations(p Params) ([]row, error) {
	cte, cteArgs := p.filteredSessionsSQL()
	var b strings.Builder
	b.WriteString("WITH " + cte + " SELECT h.location AS location, COUNT(DISTINCT h.session_id) AS visits FROM events h INNER JOIN fs ON fs.session_id = h.session_id WHERE h.site_uuid = ?")
	args := append([]any{}, cteArgs...)
	args = append(args, p.SiteUUID)
	p.hitFilters(&b, &args, "h")
	b.WriteString(" GROUP BY 1 ORDER BY visits DESC, location ASC LIMIT ? OFFSET ?")
	args = append(args, p.Limit, p.Skip)

	rows, err := e.st.Query(b.String(), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []row
	for rows.Next() {
		var v string
		var visits int64
		if err := rows.Scan(&v, &visits); err != nil {
			return nil, err
		}
		out = append(out, row{"location": v, "visits": visits})
	}
	return out, rows.Err()
}

// activeVisitors: sesiones únicas con hits en los últimos 5 min. Ventana
// sobre ts (UTC); post_uuid exacto y gift_link booleano.
func (e *Engine) activeVisitors(p Params) ([]row, error) {
	now := p.Now.UTC().Unix()
	q := "SELECT COUNT(DISTINCT session_id) FROM events WHERE site_uuid = ? AND ts >= ?"
	args := []any{p.SiteUUID, now - 300}
	if p.PostUUID != "" {
		q += " AND post_uuid = ?"
		args = append(args, p.PostUUID)
	}
	if p.GiftLink != "" {
		if giftBool(p.GiftLink) {
			q += " AND gift_link != ''"
		} else {
			q += " AND gift_link = ''"
		}
	}
	var n int64
	if err := e.st.QueryRow(q, args...).Scan(&n); err != nil {
		return nil, err
	}
	return []row{{"active_visitors": n}}, nil
}

// giftLinkVisits: por token de gift link; gift_link = match EXACTO (no
// booleano); fechas directas sobre ts; default all-time; sin limit.
func (e *Engine) giftLinkVisits(p Params) ([]row, error) {
	var b strings.Builder
	b.WriteString("SELECT gift_link, COUNT(DISTINCT session_id) AS visits, COUNT(*) AS views, MAX(ts) AS last_seen FROM events WHERE site_uuid = ? AND gift_link != ''")
	args := []any{p.SiteUUID}
	if p.DateFrom != "" {
		b.WriteString(" AND ts >= ?")
		args = append(args, dayEpoch(p.DateFrom, p.Loc))
	}
	if p.DateTo != "" {
		b.WriteString(" AND ts < ?")
		args = append(args, dayEpoch(p.DateTo, p.Loc)+86400)
	}
	if p.PostUUID != "" {
		b.WriteString(" AND post_uuid = ?")
		args = append(args, p.PostUUID)
	}
	if p.GiftLink != "" {
		b.WriteString(" AND gift_link = ?")
		args = append(args, p.GiftLink)
	}
	b.WriteString(" GROUP BY gift_link ORDER BY visits DESC, gift_link ASC")

	rows, err := e.st.Query(b.String(), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []row
	for rows.Next() {
		var gl string
		var visits, views, lastSeen int64
		if err := rows.Scan(&gl, &visits, &views, &lastSeen); err != nil {
			return nil, err
		}
		out = append(out, row{
			"gift_link": gl,
			"visits":    visits,
			"views":     views,
			"last_seen": time.Unix(lastSeen, 0).UTC().Format("2006-01-02 15:04:05"),
		})
	}
	return out, rows.Err()
}

// kpis replica api_kpis: serie completa con ceros (diaria u horaria si
// date_from==date_to), sesiones bucket por first_pageview en la tz del site,
// métricas all-time por sesión; pageviews por hits si hay filtro de
// pathname/post_uuid/gift_link.
func (e *Engine) kpis(p Params) ([]row, error) {
	// Serie temporal (claves y orden).
	keys, err := p.kpiSeries()
	if err != nil {
		return nil, err
	}
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}
	hourly := p.SingleDay

	type sessAgg struct {
		pv              int64
		firstTs, lastTs int64
	}
	sessBuckets := make(map[string][]sessAgg) // bucket → sesiones

	cte, cteArgs := p.filteredSessionsSQL()
	q := "WITH " + cte + " SELECT sm.session_id, sm.first_ts, sm.last_ts, sm.pageviews FROM sessions sm INNER JOIN fs ON fs.session_id = sm.session_id WHERE sm.site_uuid = ?"
	args := append([]any{}, cteArgs...)
	args = append(args, p.SiteUUID)
	rows, err := e.st.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var sid string
		var a sessAgg
		if err := rows.Scan(&sid, &a.firstTs, &a.lastTs, &a.pv); err != nil {
			return nil, err
		}
		k := bucketKey(a.firstTs, p.Loc, hourly)
		if keySet[k] {
			sessBuckets[k] = append(sessBuckets[k], a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Pageviews: por hits (si hay filtro pathname/post_uuid/gift_link) o por
	// sesión (suma de pv all-time de las sesiones del bucket).
	pvByBucket := make(map[string]int64)
	if p.Pathname != "" || p.PostUUID != "" || p.GiftLink != "" {
		var b strings.Builder
		b.WriteString("WITH " + cte + " SELECT h.ts FROM events h INNER JOIN fs ON fs.session_id = h.session_id WHERE h.site_uuid = ?")
		hargs := append([]any{}, cteArgs...)
		hargs = append(hargs, p.SiteUUID)
		p.hitFilters(&b, &hargs, "h")
		hrows, err := e.st.Query(b.String(), hargs...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = hrows.Close() }()
		for hrows.Next() {
			var ts int64
			if err := hrows.Scan(&ts); err != nil {
				return nil, err
			}
			if k := bucketKey(ts, p.Loc, hourly); keySet[k] {
				pvByBucket[k]++
			}
		}
		if err := hrows.Err(); err != nil {
			return nil, err
		}
	}

	out := make([]row, 0, len(keys))
	for _, k := range keys {
		sessions := sessBuckets[k]
		var visits, pv int64
		var bounceSum, durSum float64
		for _, s := range sessions {
			visits++
			pv += s.pv
			if s.pv == 1 {
				bounceSum++
			}
			durSum += float64(s.lastTs - s.firstTs)
		}
		if p.Pathname != "" || p.PostUUID != "" || p.GiftLink != "" {
			pv = pvByBucket[k]
		}
		var bounceRate, avgSec float64
		if visits > 0 {
			bounceRate = truncate2(bounceSum / float64(visits))
			avgSec = truncate2(durSum / float64(visits))
		}
		out = append(out, row{
			"date":            k,
			"visits":          visits,
			"pageviews":       pv,
			"bounce_rate":     bounceRate,
			"avg_session_sec": avgSec,
		})
	}
	return out, nil
}

// bucketKey convierte un epoch a clave de bucket: día 'YYYY-MM-DD' o hora
// 'YYYY-MM-DD HH:00:00' en loc.
func bucketKey(ts int64, loc *time.Location, hourly bool) string {
	t := time.Unix(ts, 0).In(loc)
	if hourly {
		return t.Format("2006-01-02") + " " + fmt.Sprintf("%02d:00:00", t.Hour())
	}
	return t.Format("2006-01-02")
}

// kpiSeries genera la serie completa (diaria u horaria) según los defaults
// del pipe: start = date_from o hoy-7; end = date_to o hoy; horaria solo si
// date_from == date_to (y hasta la hora en curso si el día es hoy).
func (p *Params) kpiSeries() ([]string, error) {
	start := p.today().AddDate(0, 0, -7)
	if p.DateFrom != "" {
		t, err := time.ParseInLocation("2006-01-02", p.DateFrom, p.Loc)
		if err != nil {
			return nil, err
		}
		start = t
	}
	end := p.today()
	if p.DateTo != "" {
		t, err := time.ParseInLocation("2006-01-02", p.DateTo, p.Loc)
		if err != nil {
			return nil, err
		}
		end = t
	}

	var keys []string
	if p.SingleDay {
		lastHour := 23
		if sameDay(end, p.today(), p.Loc) {
			h := p.Now.In(p.Loc).Hour()
			lastHour = h
		}
		for h := 0; h <= lastHour; h++ {
			keys = append(keys, fmt.Sprintf("%s %02d:00:00", start.Format("2006-01-02"), h))
		}
		return keys, nil
	}
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		keys = append(keys, d.Format("2006-01-02"))
	}
	return keys, nil
}

func sameDay(a, b time.Time, loc *time.Location) bool {
	a, b = a.In(loc), b.In(loc)
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

// truncate2 replica truncate(x, 2) de ClickHouse (trunca, NO redondea).
func truncate2(v float64) float64 {
	if v < 0 {
		return -truncate2(-v)
	}
	return float64(int64(v*100)) / 100
}
