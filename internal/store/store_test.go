package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func ev(id string) Event {
	return Event{
		Ts: 1700000000, SiteUUID: "s1", SessionID: "sess1", EventID: id,
		Action: "page_hit", Pathname: "/", PostType: "null", MemberStatus: "undefined",
		Device: "unknown", OS: "Unknown", Browser: "Unknown", Raw: "{}",
	}
}

func TestDedupPorEventID(t *testing.T) {
	s := openTest(t)
	now := time.Now()
	if n, err := s.InsertEvents(now, []Event{ev("e1")}); err != nil || n != 1 {
		t.Fatalf("primera inserción: n=%d err=%v", n, err)
	}
	// Mismo event_id: se ignora (at-least-once).
	if n, err := s.InsertEvents(now, []Event{ev("e1")}); err != nil || n != 0 {
		t.Fatalf("duplicado: n=%d err=%v", n, err)
	}
	// Otro site con mismo event_id SÍ se inserta.
	e2 := ev("e1")
	e2.SiteUUID = "s2"
	if n, err := s.InsertEvents(now, []Event{e2}); err != nil || n != 1 {
		t.Fatalf("otro site: n=%d err=%v", n, err)
	}
	if c, _ := s.CountEvents(); c != 2 {
		t.Errorf("count = %d, quiero 2", c)
	}
}

func TestSalGetOrCreate(t *testing.T) {
	s := openTest(t)
	a, err := s.GetOrCreateSalt("2026-08-16", "site-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.GetOrCreateSalt("2026-08-16", "site-a")
	if err != nil {
		t.Fatal(err)
	}
	if a != b || len(a) != 64 {
		t.Errorf("sal inestable o tamaño incorrecto: %q", a)
	}
	c, _ := s.GetOrCreateSalt("2026-08-16", "site-b")
	if c == a {
		t.Error("sales por sitio deben diferir")
	}
	d, _ := s.GetOrCreateSalt("2026-08-17", "site-a")
	if d == a {
		t.Error("sales por día deben diferir")
	}
	if n, _ := s.DeleteOldSalts("2026-08-16"); n != 0 {
		t.Errorf("cutoff no inclusivo borra el día actual: %d", n)
	}
	if n, _ := s.DeleteOldSalts("2026-08-17"); n != 2 { // las dos del día 16 (site-a y site-b)
		t.Errorf("debe borrar las 2 del día 16: %d", n)
	}
}

func TestMigracionesIdempotentes(t *testing.T) {
	s := openTest(t)
	if err := s.migrate(); err != nil {
		t.Errorf("re-migrar debe ser no-op: %v", err)
	}
}

func TestRetencionYBackup(t *testing.T) {
	s := openTest(t)
	now := time.Now()
	old := ev("old")
	old.Ts = now.AddDate(0, 0, -30).Unix()
	reciente := ev("nuevo")
	reciente.Ts = now.Unix()
	if _, err := s.InsertEvents(now, []Event{old, reciente}); err != nil {
		t.Fatal(err)
	}
	n, err := s.DeleteEventsBefore(now.AddDate(0, 0, -7).Unix())
	if err != nil || n != 1 {
		t.Fatalf("retención: n=%d err=%v", n, err)
	}
	if c, _ := s.CountEvents(); c != 1 {
		t.Errorf("count tras retención = %d", c)
	}

	dir := t.TempDir()
	bak := filepath.Join(dir, "backup.db")
	if err := s.Backup(bak); err != nil {
		t.Fatalf("backup: %v", err)
	}
	// Reemplazo: un segundo backup del mismo destino lo REFRESCA (reinicio
	// el mismo día = backup fresco, no un fallo).
	if err := s.Backup(bak); err != nil {
		t.Fatalf("backup sobre existente debe reemplazar: %v", err)
	}
	// El backup contiene el evento restante.
	b, err := Open(bak)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if c, _ := b.CountEvents(); c != 1 {
		t.Errorf("backup events = %d", c)
	}
}
