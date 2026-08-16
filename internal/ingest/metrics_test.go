package ingest

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gnacho/ghostbird/internal/config"
	"github.com/gnacho/ghostbird/internal/metrics"
	"github.com/gnacho/ghostbird/internal/store"
)

// TestCapCampo: un pathname hostil de 5000 bytes se trunca a 2048 antes de
// almacenarse (cap de campo del backlog).
func TestCapCampo(t *testing.T) {
	srv, st := newTestServer(t, nil)
	gigante := strings.Repeat("A", 5000)
	body := strings.Replace(defaultBody, `"/test-page"`, `"`+gigante+`"`, 1)
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
	rows, err := st.Query(`SELECT length(pathname) FROM events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("sin filas")
	}
	var n int
	if err := rows.Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2048 {
		t.Errorf("pathname almacenado mide %d, quiero 2048 (cap)", n)
	}
}

// TestMetricsEndpoint: los contadores se exponen en formato texto Prometheus
// tras tráfico real por el handler.
func TestMetricsEndpoint(t *testing.T) {
	m := metrics.New()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := NewServer(&config.Config{TrustProxy: true}, st, slog.New(slog.NewTextHandler(io.Discard, nil)), m)
	h := s.Handler()

	// Un page_hit válido + un bot (mismo 202 stealth).
	for _, ua := range []string{macChromeUA, "Googlebot/2.1"} {
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/page_hit?name=analytics_events", strings.NewReader(defaultBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-site-uuid", siteHdr)
		req.Header.Set("User-Agent", ua)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusAccepted {
			t.Fatalf("status %d con ua %s", w.Code, ua)
		}
	}

	rec := httptest.NewRecorder()
	mreq, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	m.Handler().ServeHTTP(rec, mreq)
	out := rec.Body.String()
	for _, want := range []string{
		"ghostbird_page_hits_total 1",
		"ghostbird_bots_total 1",
		"ghostbird_uptime_seconds",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics sin %q:\n%s", want, out)
		}
	}
}
