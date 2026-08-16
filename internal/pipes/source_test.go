package pipes

import (
	"net/url"
	"testing"
	"time"

	"github.com/gnacho/ghostbird/internal/store"
)

// TestSourceVacioFiltraDirecto reproduce el P1 del bug-hunt: Ghost manda
// source= (vacío) al pinchar "Direct" en la tarjeta de Sources
// (sources-card.tsx: onSourceClick(row.isDirectTraffic ? ” : row.source)).
// El pipe real filtra source=”; nosotros colapsábamos "presente y vacío"
// con "ausente" y devolvíamos TODO sin filtrar.
func TestSourceVacioFiltraDirecto(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	mk := func(session, path, source string) store.Event {
		return store.Event{
			Ts: now.Unix() - 3600, SiteUUID: "site-d", SessionID: session,
			EventID: session + path, Action: "page_hit", Pathname: path,
			PostType: "null", MemberStatus: "undefined", Source: source,
			Device: "desktop", OS: "windows", Browser: "chrome", UserAgent: "ua", Raw: "{}",
		}
	}
	// Dos sesiones: una directa (source='') y otra desde google.
	evs := []store.Event{
		mk("dir", "/direct/", ""),
		mk("org", "/organic/", "google"),
	}
	if _, err := st.InsertEvents(now, evs); err != nil {
		t.Fatal(err)
	}

	eng := NewEngine(st, func() time.Time { return now })
	q := url.Values{}
	q.Set("site_uuid", "site-d")
	q.Set("date_from", "2026-08-16")
	q.Set("date_to", "2026-08-16")
	q.Set("source", "") // PRESENTE y vacío = Direct
	p, err := ParseParams(q, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	rows, err := eng.Run("api_top_pages", p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["pathname"] != "/direct/" {
		t.Fatalf("source='' debe devolver SOLO la sesión directa; got %v", rows)
	}
}
