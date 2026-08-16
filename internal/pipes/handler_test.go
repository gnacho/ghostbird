package pipes

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gnacho/ghostbird/internal/config"
	"github.com/gnacho/ghostbird/internal/store"
)

func newPipesServer(t *testing.T, cfg *config.Config) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if cfg == nil {
		cfg = &config.Config{}
	}
	h := NewHandler(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, st
}

func TestJWTAuth(t *testing.T) {
	cfg := &config.Config{AdminToken: "secreto-compartido"}
	srv, st := newPipesServer(t, cfg)

	// Sembrar un evento con session fija.
	now := time.Now()
	mkEvent := func(ts int64, session, pathname string) store.Event {
		return store.Event{Ts: ts, SiteUUID: "11111111-1111-1111-1111-111111111111",
			SessionID: session, EventID: session + pathname, Action: "page_hit",
			Pathname: pathname, PostType: "null", MemberStatus: "undefined",
			Device: "unknown", OS: "Unknown", Browser: "Unknown", Raw: "{}"}
	}
	if _, err := st.InsertEvents(now, []store.Event{mkEvent(now.Unix()-60, "s1", "/x/")}); err != nil {
		t.Fatal(err)
	}

	scopes := []jwtScope{{Type: "PIPES:READ", Resource: "api_kpis", FixedParams: map[string]string{"site_uuid": "11111111-1111-1111-1111-111111111111"}}}
	tok, err := SignJWT("secreto-compartido", jwtClaims{Exp: now.Add(time.Hour).Unix(), Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}

	get := func(url, bearer string) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+url, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	q := "/v0/pipes/api_kpis.json?site_uuid=11111111-1111-1111-1111-111111111111"
	if code := get(q, tok); code != 200 {
		t.Errorf("jwt válido: %d", code)
	}
	if code := get(q, ""); code != 401 {
		t.Errorf("sin token con auth activa: %d", code)
	}
	if code := get(q, "token-malo"); code != 401 {
		t.Errorf("token basura: %d", code)
	}

	// Site_uuid distinto al fixed_params del token → 401 (aislamiento).
	otro := "/v0/pipes/api_kpis.json?site_uuid=22222222-2222-2222-2222-222222222222"
	if code := get(otro, tok); code != 401 {
		t.Errorf("fixed_params mismatch: %d", code)
	}

	// Scope de otro pipe → 401.
	otroPipe := "/v0/pipes/api_top_pages.json?site_uuid=11111111-1111-1111-1111-111111111111"
	if code := get(otroPipe, tok); code != 401 {
		t.Errorf("scope de otro pipe: %d", code)
	}

	// Expirado → 401.
	expTok, _ := SignJWT("secreto-compartido", jwtClaims{Exp: now.Add(-time.Hour).Unix(), Scopes: scopes})
	if code := get(q, expTok); code != 401 {
		t.Errorf("expirado: %d", code)
	}

	// Secreto distinto → 401.
	malTok, _ := SignJWT("otro-secreto", jwtClaims{Exp: now.Add(time.Hour).Unix(), Scopes: scopes})
	if code := get(q, malTok); code != 401 {
		t.Errorf("secreto incorrecto: %d", code)
	}

	// Alias _v2 responde igual que v1 con el mismo scope (alias canónico).
	if code := get(strings.Replace(q, "api_kpis", "api_kpis_v2", 1), tok); code != 200 {
		t.Errorf("alias _v2: %d", code)
	}

	// Como llama el Admin real (@tinybirdco/charts): POST + token en query +
	// SIN site_uuid (lo inyecta fixed_params del JWT).
	reqP, _ := http.NewRequest(http.MethodPost, srv.URL+"/v0/pipes/api_kpis.json?date_from=2026-08-16&timezone=Europe%2FMadrid&token="+tok, nil)
	resP, err := srv.Client().Do(reqP)
	if err != nil {
		t.Fatal(err)
	}
	defer resP.Body.Close()
	if resP.StatusCode != 200 {
		t.Errorf("POST con token en query sin site_uuid: %d", resP.StatusCode)
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	json.NewDecoder(resP.Body).Decode(&resp)
	if len(resp.Data) == 0 {
		t.Error("POST sin site_uuid debe devolver datos (fixed_params)")
	}
}

func TestStaticTokenYModoAbierto(t *testing.T) {
	// Token estático (stats.local.token de Ghost).
	cfg := &config.Config{StatsToken: "mi-token-estatico"}
	srv, _ := newPipesServer(t, cfg)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v0/pipes/api_kpis.json?site_uuid=x", nil)
	req.Header.Set("Authorization", "Bearer mi-token-estatico")
	res, _ := srv.Client().Do(req)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("token estático: %d", res.StatusCode)
	}
	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/v0/pipes/api_kpis.json?site_uuid=x&token=mi-token-estatico", nil)
	res2, _ := srv.Client().Do(req2)
	res2.Body.Close()
	if res2.StatusCode != 200 {
		t.Errorf("token por query: %d", res2.StatusCode)
	}

	// Sin auth configurada → modo local abierto.
	srv2, _ := newPipesServer(t, nil)
	req3, _ := http.NewRequest(http.MethodGet, srv2.URL+"/v0/pipes/api_active_visitors.json?site_uuid=x", nil)
	res3, _ := srv2.Client().Do(req3)
	res3.Body.Close()
	if res3.StatusCode != 200 {
		t.Errorf("modo abierto: %d", res3.StatusCode)
	}
}

func TestPipeDesconocido404(t *testing.T) {
	srv, _ := newPipesServer(t, nil)
	res, err := srv.Client().Get(srv.URL + "/v0/pipes/no_existe.json?site_uuid=x")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("404 esperado: %d", res.StatusCode)
	}
	// Sin .json también enruta (defensivo).
	res2, _ := srv.Client().Get(srv.URL + "/v0/pipes/api_kpis?site_uuid=x")
	res2.Body.Close()
	if res2.StatusCode != 200 {
		t.Errorf("sin .json: %d", res2.StatusCode)
	}
}

func TestActiveVisitorsVentana(t *testing.T) {
	_, st := newPipesServer(t, nil)
	eng := NewEngine(st, func() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) })
	now := eng.nowF().Unix()

	mk := func(ts int64, session, gift string) store.Event {
		return store.Event{Ts: ts, SiteUUID: "site-av", SessionID: session,
			EventID: session + gift + time.Unix(ts, 0).Format("15:04:05"), Action: "page_hit",
			Pathname: "/", PostType: "null", MemberStatus: "undefined", GiftLink: gift,
			Device: "unknown", OS: "Unknown", Browser: "Unknown", Raw: "{}"}
	}
	// s1 dentro de 5 min (con gift), s2 dentro (sin gift), s3 fuera (>5min).
	evs := []store.Event{
		mk(now-120, "s1", "gift_x"),
		mk(now-299, "s2", ""),
		mk(now-301, "s3", ""),
	}
	if _, err := st.InsertEvents(eng.nowF(), evs); err != nil {
		t.Fatal(err)
	}

	run := func(query string) []row {
		q, _ := parseQ(query)
		p, err := ParseParams(q, eng.nowF)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := eng.Run("api_active_visitors", p)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	if got := run("site_uuid=site-av")[0]["active_visitors"]; got != int64(2) {
		t.Errorf("total: %v", got)
	}
	if got := run("site_uuid=site-av&gift_link=1")[0]["active_visitors"]; got != int64(1) {
		t.Errorf("solo gift: %v", got)
	}
	if got := run("site_uuid=site-av&gift_link=false")[0]["active_visitors"]; got != int64(1) {
		t.Errorf("excluye gift: %v", got)
	}
}

func parseQ(s string) (url.Values, error) {
	return url.ParseQuery(strings.ReplaceAll(s, "+", "%20"))
}
