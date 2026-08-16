package store

import (
	"path/filepath"
	"testing"
	"time"
)

func sessEv(ts int64, site, session, eventID, pathname, source string) Event {
	return Event{
		Ts: ts, SiteUUID: site, SessionID: session, EventID: eventID,
		Action: "page_hit", Pathname: pathname, Source: source,
		PostType: "null", MemberStatus: "undefined",
		Device: "desktop", OS: "windows", Browser: "chrome", Raw: "{}",
	}
}

func readSessions(t *testing.T, s *Store) map[string][4]any {
	t.Helper()
	rows, err := s.Query(`SELECT session_id, first_ts, last_ts, pageviews, source FROM sessions`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][4]any{}
	for rows.Next() {
		var sid string
		var f, l, pv int64
		var src string
		if err := rows.Scan(&sid, &f, &l, &pv, &src); err != nil {
			t.Fatal(err)
		}
		out[sid] = [4]any{f, l, pv, src}
	}
	return out
}

func TestSessionsUpsertIncremental(t *testing.T) {
	s := openTest(t)
	now := time.Now()

	// 3 hits de la misma sesión (uno out-of-order con otro source).
	evs := []Event{
		sessEv(1000, "s1", "sessA", "e1", "/a/", "google"),
		sessEv(3000, "s1", "sessA", "e2", "/b/", "google"),
		sessEv(500, "s1", "sessA", "e3", "/c/", "bing"), // ANTERIOR al first: su source gana
		sessEv(2000, "s1", "sessB", "e4", "/", ""),
	}
	if n, err := s.InsertEvents(now, evs); err != nil || n != 4 {
		t.Fatalf("insert: n=%d err=%v", n, err)
	}
	got := readSessions(t, s)
	a := got["sessA"]
	if a[0] != int64(500) || a[1] != int64(3000) || a[2] != int64(3) {
		t.Errorf("sessA: first=%v last=%v pv=%v, quiero 500/3000/3", a[0], a[1], a[2])
	}
	if a[3] != "bing" {
		t.Errorf("source del primer hit (out-of-order) debe ser bing: %v", a[3])
	}
	if b := got["sessB"]; b[2] != int64(1) {
		t.Errorf("sessB pv=%v", b[2])
	}

	// Reenvío (dedup at-least-once): NO infla pageviews.
	if n, _ := s.InsertEvents(now, evs); n != 0 {
		t.Fatalf("dedup: n=%d", n)
	}
	got = readSessions(t, s)
	if got["sessA"][2] != int64(3) {
		t.Errorf("dedup infló pageviews: %v", got["sessA"][2])
	}
}

func TestSessionsBackfillYConsistencia(t *testing.T) {
	// BD creada ANTES de la migración v3: se abre una BD nueva, se inserta,
	// se cierra y se reabre — la migración v3 ya habrá corrido en el primer
	// Open (user_version=3), así que para probar el backfill forzamos la
	// situación: BD con migraciones solo v1 (simulada bajando user_version
	// tras borrar sessions y reabriendo).
	dir := t.TempDir()
	path := filepath.Join(dir, "bf.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	evs := []Event{
		sessEv(100, "s1", "x", "e1", "/a/", "google"),
		sessEv(200, "s1", "x", "e2", "/b/", "google"),
	}
	if _, err := s.InsertEvents(now, evs); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Simular BD pre-v3: bajar versión y borrar sessions.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.db.Exec(`DROP TABLE sessions; PRAGMA user_version = 2;`); err != nil {
		t.Fatal(err)
	}
	s2.Close()

	// Reabrir: la migración v3 debe reconstruir sessions desde events.
	s3, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	got := readSessions(t, s3)
	if got["x"][2] != int64(2) || got["x"][0] != int64(100) || got["x"][1] != int64(200) {
		t.Errorf("backfall sessions: %+v", got["x"])
	}

	// Y la ingesta sigue integrando sobre el backfill.
	if _, err := s3.InsertEvents(now, []Event{sessEv(300, "s1", "x", "e3", "/c/", "google")}); err != nil {
		t.Fatal(err)
	}
	if got := readSessions(t, s3); got["x"][2] != int64(3) {
		t.Errorf("upsert tras backfill: pv=%v", got["x"][2])
	}
}
