package gcauth

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// newGCDB crea una BD GoatCounter mínima con los tokens de fixture.
func newGCDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gc.sqlite3")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE users (user_id INTEGER PRIMARY KEY, email TEXT)`,
		`CREATE TABLE api_tokens (api_token_id INTEGER PRIMARY KEY, site_id INTEGER, user_id INTEGER, name TEXT, token TEXT, permissions INTEGER, sites TEXT)`,
		`INSERT INTO users VALUES (1, 'nacho@example.com')`,
		// dashboard: todos los permisos (126) y todos los sitios [-1].
		`INSERT INTO api_tokens VALUES (1, 1, 1, 'dashboard', 'tok-all-sites', 126, '[-1]')`,
		// scope: solo stats (64), sitio 10.
		`INSERT INTO api_tokens VALUES (2, 1, 1, 'scope', 'tok-site-10', 64, '[10]')`,
		// sin stats: count+export+site_read (2+4+8=14).
		`INSERT INTO api_tokens VALUES (3, 1, 1, 'writer', 'tok-no-stats', 14, '[-1]')`,
		// sites corrupto.
		`INSERT INTO api_tokens VALUES (4, 1, 1, 'roto', 'tok-bad-json', 126, 'no-json')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return path
}

func TestValidate(t *testing.T) {
	a, err := Open(newGCDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if info, ok := a.Validate("tok-all-sites"); !ok || !info.AllSites() || !info.HasStats() {
		t.Errorf("all-sites: %+v ok=%v", info, ok)
	} else if info.Email != "nacho@example.com" || info.Name != "dashboard" {
		t.Errorf("info: %+v", info)
	}

	if info, ok := a.Validate("tok-site-10"); !ok || info.AllSites() || !info.HasStats() {
		t.Errorf("site-10: %+v ok=%v", info, ok)
	} else if len(info.Sites) != 1 || info.Sites[0] != 10 {
		t.Errorf("sites: %v", info.Sites)
	}

	if _, ok := a.Validate("tok-no-stats"); !ok {
		t.Error("token existe (la comprobación de permisos es del handler)")
	}
	if _, ok := a.Validate("no-existe"); ok {
		t.Error("token desconocido no debe encontrarse")
	}
	if _, ok := a.Validate("tok-bad-json"); ok {
		t.Error("sites corrupto no debe validar")
	}
}

func TestCacheYFailClosed(t *testing.T) {
	path := newGCDB(t)
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// Caché positivo: borrar el token de la BD y seguir validando (TTL).
	if _, ok := a.Validate("tok-all-sites"); !ok {
		t.Fatal("primera validación")
	}
	db, _ := sql.Open("sqlite", "file:"+path)
	db.Exec(`DELETE FROM api_tokens`)
	db.Close()
	if _, ok := a.Validate("tok-all-sites"); !ok {
		t.Error("caché positivo: debe validar dentro del TTL")
	}

	// Expirar y comprobar que re-consulta (fail hacia no encontrado).
	a.mu.Lock()
	for k := range a.cache {
		e := a.cache[k]
		e.expiry = time.Now().Add(-time.Second)
		a.cache[k] = e
	}
	a.mu.Unlock()
	if _, ok := a.Validate("tok-all-sites"); ok {
		t.Error("tras TTL y borrado en BD: no debe validar")
	}
}
