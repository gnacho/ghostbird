package ingest

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gnacho/ghostbird/internal/config"
	"github.com/gnacho/ghostbird/internal/store"
)

// newTestServer monta el handler con una BD temporal.
func newTestServer(t *testing.T, cfg *config.Config) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if cfg == nil {
		cfg = &config.Config{Addr: ":0", DBPath: "test.db", TrustProxy: true}
	}
	s := NewServer(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, st
}

func pageHitRequest(srv *httptest.Server) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/page_hit?name=analytics_events", strings.NewReader(defaultBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-site-uuid", siteHdr)
	req.Header.Set("User-Agent", macChromeUA)
	req.Header.Set("X-Forwarded-For", "203.0.113.42, 192.168.1.1")
	return req, nil
}

type eventRow struct {
	ts                             int64
	session, os, browser, device   string
	source, postUUID, memberStatus string
	referrerSource                 string
}

func queryEvents(t *testing.T, st *store.Store) []eventRow {
	t.Helper()
	rows, err := st.Query(`SELECT ts, session_id, os, browser, device, source, post_uuid, member_status, referrer_source FROM events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []eventRow
	for rows.Next() {
		var r eventRow
		if err := rows.Scan(&r.ts, &r.session, &r.os, &r.browser, &r.device, &r.source, &r.postUUID, &r.memberStatus, &r.referrerSource); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPageHitEndToEnd(t *testing.T) {
	srv, st := newTestServer(t, nil)
	req, _ := pageHitRequest(srv)
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, quiero 202", res.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(res.Body).Decode(&body)
	if body["message"] != "Page hit event received" {
		t.Errorf("body = %v", body)
	}

	rows := queryEvents(t, st)
	if len(rows) != 1 {
		t.Fatalf("events = %d, quiero 1", len(rows))
	}
	r := rows[0]
	if r.ts == 0 {
		t.Error("ts debe ser la hora del servidor")
	}
	if len(r.session) != 64 {
		t.Errorf("session_id debe ser sha256 hex 64: %q", r.session)
	}
	if r.os != "macos" || r.browser != "chrome" || r.device != "desktop" {
		t.Errorf("ua derivado: os=%s browser=%s device=%s", r.os, r.browser, r.device)
	}
	if r.source != "" {
		t.Errorf("sin referrer, source debe ser '': %q", r.source)
	}
	if r.postUUID != "" || r.memberStatus != "free" {
		t.Errorf("post_uuid=%q member_status=%q", r.postUUID, r.memberStatus)
	}
}

func TestPageHitBotNoAlmacena(t *testing.T) {
	srv, st := newTestServer(t, nil)
	req, _ := pageHitRequest(srv)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("bot debe recibir 202 (stealth): %d", res.StatusCode)
	}
	if c, _ := st.CountEvents(); c != 0 {
		t.Errorf("bot no debe almacenarse: %d filas", c)
	}
}

func TestPageHitValidaciones(t *testing.T) {
	srv, _ := newTestServer(t, nil)

	nueva := func(mut func(*http.Request)) int {
		req, _ := pageHitRequest(srv)
		mut(req)
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := nueva(func(r *http.Request) { r.Header.Set("x-site-uuid", "no-guid") }); code != 400 {
		t.Errorf("x-site-uuid inválido: %d", code)
	}
	if code := nueva(func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }); code != 400 {
		t.Errorf("content-type: %d", code)
	}
	if code := nueva(func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader("no json")); r.ContentLength = 7 }); code != 400 {
		t.Errorf("json inválido: %d", code)
	}

	// Cabeceras ausentes: contra el handler directo (el transporte del client
	// rellenaría User-Agent por defecto y falsearía el resultado).
	handler := func(req *http.Request) int {
		s := NewServer(&config.Config{TrustProxy: true}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		return w.Code
	}
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/page_hit", strings.NewReader(defaultBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", macChromeUA)
	if code := handler(req); code != 400 { // falta x-site-uuid
		t.Errorf("sin x-site-uuid: %d", code)
	}
	req2, _ := http.NewRequest(http.MethodPost, "/api/v1/page_hit", strings.NewReader(defaultBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("x-site-uuid", siteHdr)
	if code := handler(req2); code != 400 { // falta User-Agent
		t.Errorf("sin UA: %d", code)
	}
}

func TestPageHitReferrerYFuente(t *testing.T) {
	srv, st := newTestServer(t, nil)
	body := strings.Replace(defaultBody,
		`"parsedReferrer": {"source": null, "medium": null, "url": null}`,
		`"parsedReferrer": {"source": "", "medium": "", "url": "https://www.google.com/search?q=ghost"}`, 1)
	req, _ := pageHitRequest(srv)
	req.Body = io.NopCloser(strings.NewReader(body))
	req.ContentLength = int64(len(body))
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 202 {
		t.Fatalf("status %d", res.StatusCode)
	}
	rows := queryEvents(t, st)
	if len(rows) != 1 {
		t.Fatalf("events %d", len(rows))
	}
	r := rows[0]
	if r.referrerSource != "Google" || r.source != "Google" {
		t.Errorf("referrer_source=%q source=%q, quiero Google", r.referrerSource, r.source)
	}
}

func TestCORSPreflight(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/api/v1/page_hit", nil)
	req.Header.Set("Origin", "https://blog.ejemplo.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type,x-site-uuid")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight: %d", res.StatusCode)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("ACAO debe ser *")
	}
	if !strings.Contains(res.Header.Get("Access-Control-Allow-Headers"), "x-site-uuid") {
		t.Error("x-site-uuid debe estar permitido")
	}
	// CORS también en respuestas normales.
	req2, _ := pageHitRequest(srv)
	res2, err := srv.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("ACAO falta en POST")
	}
}

func TestHealthz(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	res, err := srv.Client().Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("healthz: %d", res.StatusCode)
	}
	var h struct {
		Status string `json:"status"`
		Events int64  `json:"events"`
	}
	json.NewDecoder(res.Body).Decode(&h)
	if h.Status != "ok" || h.Events != 0 {
		t.Errorf("healthz: %+v", h)
	}
}

// eventoProcesado construye una línea NDJSON como la que manda el AS a /v0/events.
func eventoProcesado(eventID, session, ts string) string {
	return `{"timestamp":"` + ts + `","action":"page_hit","version":"1",` +
		`"site_uuid":"` + siteHdr + `","session_id":"` + session + `",` +
		`"payload":{"event_id":"` + eventID + `","site_uuid":"` + siteHdr + `",` +
		`"member_uuid":"undefined","member_status":"free","post_uuid":"undefined","post_type":"null",` +
		`"locale":"en-US","location":"US","pathname":"/test-page","href":"https://example.com/test-page",` +
		`"os":"macos","browser":"chrome","device":"desktop",` +
		`"referrerUrl":"www.google.com","referrerSource":"Google","referrerMedium":"search",` +
		`"utm_source":null,"utm_medium":null,"utm_campaign":null,"utm_term":null,"utm_content":null,` +
		`"user-agent":"` + macChromeUA + `","meta":{"received_timestamp":"2026-08-16T16:06:06.090Z"}}}`
}

func TestV0EventsBatchNDJSON(t *testing.T) {
	srv, st := newTestServer(t, nil)
	sessA := strings.Repeat("a", 64)
	sessB := strings.Repeat("b", 64)
	ndjson := eventoProcesado("ev-1", sessA, "2026-08-16T16:06:06.095Z") + "\n" +
		eventoProcesado("ev-2", sessB, "2026-08-16T16:06:07.095Z")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v0/events?name=analytics_events", strings.NewReader(ndjson))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body struct {
		Success  bool `json:"success"`
		Accepted int  `json:"accepted"`
	}
	json.NewDecoder(res.Body).Decode(&body)
	if !body.Success || body.Accepted != 2 {
		t.Errorf("body %+v", body)
	}
	if c, _ := st.CountEvents(); c != 2 {
		t.Errorf("events %d", c)
	}
	rows := queryEvents(t, st)
	if rows[0].referrerSource != "Google" || rows[0].source != "Google" {
		t.Errorf("referrer del procesado: %+v", rows[0])
	}
	if rows[0].session != sessA {
		t.Errorf("session del AS debe conservarse: %q", rows[0].session)
	}
	wantTs, _ := time.Parse(time.RFC3339Nano, "2026-08-16T16:06:06.095Z")
	if rows[0].ts != wantTs.Unix() {
		t.Errorf("ts raíz del evento: %d, quiero %d", rows[0].ts, wantTs.Unix())
	}

	// Reenvío del mismo batch (at-least-once): dedup.
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/v0/events?name=analytics_events", strings.NewReader(ndjson))
	req2.Header.Set("Authorization", "Bearer test-token")
	res2, _ := srv.Client().Do(req2)
	res2.Body.Close()
	if c, _ := st.CountEvents(); c != 2 {
		t.Errorf("dedup falla: %d", c)
	}
}

func TestV0EventsObjetoUnicoYQueryToken(t *testing.T) {
	cfg := &config.Config{Addr: ":0", DBPath: "t.db", TrustProxy: true, IngestToken: "secreto"}
	srv, st := newTestServer(t, cfg)
	sessC := strings.Repeat("c", 64)
	uno := eventoProcesado("ev-x", sessC, "2026-08-16T16:06:06.095Z")

	// Sin token → 401.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v0/events?name=analytics_events", strings.NewReader(uno))
	res, _ := srv.Client().Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("sin token: %d", res.StatusCode)
	}

	// Token por query param (modo proxy del AS sin env token).
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/v0/events?name=analytics_events&token=secreto", strings.NewReader(uno))
	res2, _ := srv.Client().Do(req2)
	res2.Body.Close()
	if res2.StatusCode != http.StatusAccepted {
		t.Errorf("query token: %d", res2.StatusCode)
	}
	if c, _ := st.CountEvents(); c != 1 {
		t.Errorf("events %d", c)
	}
}

func TestV0EventsBotDescartado(t *testing.T) {
	srv, st := newTestServer(t, nil)
	ev := eventoProcesado("ev-bot", strings.Repeat("d", 64), "2026-08-16T16:06:06.095Z")
	ev = strings.Replace(ev, `"device":"desktop"`, `"device":"bot"`, 1)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v0/events?name=analytics_events", strings.NewReader(ev))
	req.Header.Set("Authorization", "Bearer t")
	res, _ := srv.Client().Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d", res.StatusCode)
	}
	if c, _ := st.CountEvents(); c != 0 {
		t.Errorf("bot debe descartarse: %d", c)
	}
}

func TestV0EventsNameRequerido(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v0/events", strings.NewReader(eventoProcesado("ev-n", strings.Repeat("e", 64), "2026-08-16T16:06:06.095Z")))
	res, _ := srv.Client().Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("sin name: %d", res.StatusCode)
	}
}

func TestClientIPXFF(t *testing.T) {
	srv, st := newTestServer(t, nil)
	req, _ := pageHitRequest(srv) // XFF: 203.0.113.42, 192.168.1.1
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	// Misma IP+UA → misma session_id.
	req2, _ := pageHitRequest(srv)
	res2, _ := srv.Client().Do(req2)
	res2.Body.Close()
	rows := queryEvents(t, st)
	if len(rows) != 2 {
		t.Fatalf("events %d", len(rows))
	}
	if rows[0].session != rows[1].session {
		t.Error("misma IP+UA mismo día debe dar la misma sesión")
	}

	// IP distinta → sesión distinta.
	req3, _ := pageHitRequest(srv)
	req3.Header.Set("X-Forwarded-For", "198.51.100.7")
	res3, _ := srv.Client().Do(req3)
	res3.Body.Close()
	rows = queryEvents(t, st)
	if len(rows) != 3 || rows[2].session == rows[0].session {
		t.Errorf("IP distinta debe dar sesión distinta (%d filas)", len(rows))
	}
}

func TestPageHitTimestampServidor(t *testing.T) {
	srv, st := newTestServer(t, nil)
	body := strings.Replace(defaultBody, "2025-04-14T22:16:06.095Z", "2001-01-01T00:00:00.000Z", 1)
	req, _ := pageHitRequest(srv)
	req.Body = io.NopCloser(strings.NewReader(body))
	req.ContentLength = int64(len(body))
	res, _ := srv.Client().Do(req)
	res.Body.Close()
	rows := queryEvents(t, st)
	if len(rows) != 1 {
		t.Fatal("events")
	}
	want := time.Now().UTC().Unix()
	if rows[0].ts < want-60 || rows[0].ts > want+60 {
		t.Errorf("ts=%d debe ser ~ahora (%d): el timestamp del cliente se descarta", rows[0].ts, want)
	}
}
