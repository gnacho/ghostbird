package pipes

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gnacho/ghostbird/internal/config"
	"github.com/gnacho/ghostbird/internal/gcauth"
	"github.com/gnacho/ghostbird/internal/store"
)

// createGCFixture crea una BD GoatCounter mínima (mismo schema que usa
// gcauth.Validate) con: tok-all-sites (126, [-1]), tok-site-10 (64, [10]),
// tok-no-stats (14, [-1]).
func createGCFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gc.sqlite3")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range []string{
		`CREATE TABLE users (user_id INTEGER PRIMARY KEY, email TEXT)`,
		`CREATE TABLE api_tokens (api_token_id INTEGER PRIMARY KEY, site_id INTEGER, user_id INTEGER, name TEXT, token TEXT, permissions INTEGER, sites TEXT)`,
		`INSERT INTO users VALUES (1, 'nacho@example.com')`,
		`INSERT INTO api_tokens VALUES (1, 1, 1, 'dashboard', 'tok-all-sites', 126, '[-1]')`,
		`INSERT INTO api_tokens VALUES (2, 1, 1, 'scope', 'tok-site-10', 64, '[10]')`,
		`INSERT INTO api_tokens VALUES (3, 1, 1, 'writer', 'tok-no-stats', 14, '[-1]')`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return path
}

func newGCAuth(t *testing.T) *gcauth.Authenticator {
	t.Helper()
	a, err := gcauth.Open(createGCFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func TestGoatCounterAuth(t *testing.T) {
	gc := newGCAuth(t)
	cfg := &config.Config{GoatCounterDB: "set"}
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Mapping: sitio GC 10 → site "site-oep" (migración v4 ya creada).
	if _, err := st.Exec(`INSERT INTO gc_site_map (gc_site_id, site_uuid) VALUES (10, 'site-oep')`); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, gc)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	get := func(path string) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	if c := get("/v0/pipes/api_kpis.json?site_uuid=site-oep&date_from=2026-08-17&token=tok-all-sites"); c != 200 {
		t.Errorf("all-sites + stats: %d", c)
	}
	if c := get("/v0/pipes/api_kpis.json?site_uuid=site-oep&date_from=2026-08-17&token=tok-site-10"); c != 200 {
		t.Errorf("scoped + mapeado: %d", c)
	}
	if c := get("/v0/pipes/api_kpis.json?site_uuid=otro&date_from=2026-08-17&token=tok-site-10"); c != 401 {
		t.Errorf("scoped + no mapeado: %d", c)
	}
	if c := get("/v0/pipes/api_kpis.json?date_from=2026-08-17&token=tok-site-10"); c != 200 {
		t.Errorf("scoped sin query site (mapeo único, inyección): %d", c)
	}
	if c := get("/v0/pipes/api_kpis.json?site_uuid=site-oep&date_from=2026-08-17&token=tok-no-stats"); c != 401 {
		t.Errorf("sin permiso stats: %d", c)
	}
	if c := get("/v0/pipes/api_kpis.json?site_uuid=site-oep&token=patata"); c != 401 {
		t.Errorf("token desconocido: %d", c)
	}
	if c := get("/v0/pipes/api_kpis.json?site_uuid=site-oep"); c != 401 {
		t.Errorf("sin token con GC activo (fail-closed): %d", c)
	}
}

func TestGCAuthConJWTPresente(t *testing.T) {
	// Con AdminToken + GC configurados, el JWT de Ghost sigue funcionando
	// (el orden prueba JWT primero) y el modo abierto queda cerrado.
	gc := newGCAuth(t)
	cfg := &config.Config{AdminToken: "secreto", GoatCounterDB: "set"}
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := NewHandler(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, gc)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	scopes := []jwtScope{{Type: "PIPES:READ", Resource: "api_kpis", FixedParams: map[string]string{"site_uuid": "site-x"}}}
	tok, err := SignJWT("secreto", jwtClaims{Exp: time.Now().Add(time.Hour).Unix(), Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v0/pipes/api_kpis.json?site_uuid=site-x&date_from=2026-08-17&token="+tok, nil)
	res, _ := srv.Client().Do(req)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("JWT con GC activo: %d", res.StatusCode)
	}
}
