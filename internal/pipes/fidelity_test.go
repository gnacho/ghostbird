package pipes

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gnacho/ghostbird/internal/ingest"
	"github.com/gnacho/ghostbird/internal/store"
)

// seedFixture importa el NDJSON de Tinybird replicando la lógica de mv_hits
// (referrer = payload.referrerSource || meta.referrerSource; source =
// normalización mv_hits; os/browser del UA; device del payload). Los
// event_id se sintetizan por línea (el fixture no los trae y el dedup es
// UNIQUE(site_uuid, event_id)).
func seedFixture(t *testing.T, st *store.Store, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	type payload struct {
		EventID        *string          `json:"event_id"`
		SiteUUID       string           `json:"site_uuid"`
		MemberUUID     *string          `json:"member_uuid"`
		MemberStatus   *string          `json:"member_status"`
		PostUUID       *string          `json:"post_uuid"`
		PostType       *string          `json:"post_type"`
		GiftLink       *string          `json:"gift_link"`
		Locale         *string          `json:"locale"`
		Location       *string          `json:"location"`
		Referrer       *string          `json:"referrer"`
		Pathname       *string          `json:"pathname"`
		Href           *string          `json:"href"`
		UserAgent      string           `json:"user-agent"`
		Device         *string          `json:"device"`
		UtmSource      *string          `json:"utm_source"`
		UtmMedium      *string          `json:"utm_medium"`
		UtmCampaign    *string          `json:"utm_campaign"`
		UtmTerm        *string          `json:"utm_term"`
		UtmContent     *string          `json:"utm_content"`
		ParsedReferrer *json.RawMessage `json:"parsedReferrer"`
		Meta           *struct {
			ReceivedTimestamp *string `json:"received_timestamp"`
			ReferrerSource    *string `json:"referrerSource"`
		} `json:"meta"`
	}
	type event struct {
		Timestamp string  `json:"timestamp"`
		SessionID string  `json:"session_id"`
		Action    string  `json:"action"`
		Payload   payload `json:"payload"`
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	i := 0
	now := time.Now()
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		i++
		var ev event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("línea %d: %v", i, err)
		}
		ts, err := time.Parse("2006-01-02 15:04:05", ev.Timestamp)
		if err != nil {
			t.Fatalf("línea %d timestamp %q: %v", i, ev.Timestamp, err)
		}
		p := ev.Payload
		str := func(s *string) string {
			if s == nil {
				return ""
			}
			return *s
		}
		referrer := str(p.Referrer)
		// mv_hits: payload.referrerSource si no vacío, si no meta.referrerSource.
		// El fixture no trae payload.referrerSource: usa referrer/meta.
		metaRef := ""
		if p.Meta != nil {
			metaRef = str(p.Meta.ReferrerSource)
		}
		if referrer == "" {
			referrer = metaRef
		}
		os_, browser, _ := ingest.UAInfo(p.UserAgent)
		device := str(p.Device)
		ev2 := store.Event{
			Ts:         ts.UTC().Unix(),
			ReceivedMs: 0,
			SiteUUID:   p.SiteUUID,
			SessionID:  ev.SessionID,
			EventID:    fmt.Sprintf("fixture-%03d", i),
			Action:     "page_hit",
			Pathname:   str(p.Pathname),
			Href:       str(p.Href),
			PostUUID:   normUndefined(str(p.PostUUID)),
			PostType:   str(p.PostType),
			MemberUUID: normUndefined(str(p.MemberUUID)),
			MemberStatus: func() string {
				if v := str(p.MemberStatus); v != "" {
					return v
				}
				return "undefined"
			}(),
			GiftLink:       str(p.GiftLink),
			Location:       str(p.Location),
			Locale:         str(p.Locale),
			OS:             os_,
			Browser:        browser,
			Device:         device,
			UserAgent:      p.UserAgent,
			ReferrerURL:    referrer,
			ReferrerSource: referrer,
			Source:         ingest.NormalizarSource(referrer),
			UtmSource:      str(p.UtmSource),
			UtmMedium:      str(p.UtmMedium),
			UtmCampaign:    str(p.UtmCampaign),
			UtmTerm:        str(p.UtmTerm),
			UtmContent:     str(p.UtmContent),
			Raw:            line,
		}
		if _, err := st.InsertEvents(now, []store.Event{ev2}); err != nil {
			t.Fatalf("línea %d insert: %v", i, err)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
}

func normUndefined(s string) string {
	if s == "undefined" {
		return ""
	}
	return s
}

// yamlCase es un caso del archivo de tests de Tinybird.
type yamlCase struct {
	Name       string
	Parameters string
	Expected   []string // líneas NDJSON esperadas
}

// parseYAMLCases parsea el formato de los tests de Tinybird (suficientemente
// simple para un parser a mano: name/parameters/expected_result).
func parseYAMLCases(t *testing.T, path string) []yamlCase {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cases []yamlCase
	var cur *yamlCase
	inExpected := false
	for _, line := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(line, "- name:"):
			cases = append(cases, yamlCase{Name: strings.TrimSpace(strings.TrimPrefix(line, "- name:"))})
			cur = &cases[len(cases)-1]
			inExpected = false
		case cur != nil && strings.HasPrefix(line, "  parameters:"):
			cur.Parameters = strings.TrimSpace(strings.TrimPrefix(line, "  parameters:"))
			inExpected = false
		case cur != nil && strings.HasPrefix(line, "  expected_result:"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "  expected_result:"))
			v = strings.TrimSuffix(strings.TrimSuffix(v, "|"), "-")
			v = strings.TrimSpace(v)
			if v == "''" {
				cur.Expected = nil
				inExpected = true // '' = explícitamente vacío: bloque sin líneas
			} else if v != "" {
				cur.Expected = []string{v}
				inExpected = false
			} else {
				// "|" (o "|-"): las líneas esperadas vienen a continuación.
				inExpected = true
			}
		case inExpected && strings.HasPrefix(line, "    ") && strings.TrimSpace(line) != "":
			cur.Expected = append(cur.Expected, strings.TrimSpace(line))
		}
	}
	return cases
}

// fixedNow: "ahora" fijo en fecha real (el fixture está en 2100, futuro).
// Los casos del YAML usan fechas explícitas o current_time; solo los de
// fechas por defecto dependen de now, y esperan "hoy" real.
var fixedNow = time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

// TestFidelidadTinybird corre TODOS los casos YAML de los tests de Tinybird
// contra el mismo fixture: misma entrada → misma salida.
func TestFidelidadTinybird(t *testing.T) {
	files, err := filepath.Glob("testdata/api_*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("sin YAMLs: %v", err)
	}
	for _, f := range files {
		pipe := strings.TrimSuffix(filepath.Base(f), ".yaml")
		for _, c := range parseYAMLCases(t, f) {
			c := c
			t.Run(pipe+"/"+c.Name, func(t *testing.T) {
				st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				seedFixture(t, st, "testdata/analytics_events.ndjson")

				q, err := urlParseQuery(c.Parameters)
				if err != nil {
					t.Fatalf("params %q: %v", c.Parameters, err)
				}
				p, err := ParseParams(q, func() time.Time { return fixedNow })
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				eng := NewEngine(st, func() time.Time { return fixedNow })
				got, err := eng.Run(pipe, p)
				if err != nil {
					t.Fatalf("run: %v", err)
				}

				if len(got) != len(c.Expected) {
					t.Fatalf("filas: got %d, want %d\n got: %s\nwant: %s",
						len(got), len(c.Expected), rowsStr(got), strings.Join(c.Expected, "\n"))
				}
				if err := compareRows(got, c.Expected); err != nil {
					t.Errorf("%v\n got: %s\nwant: %s", err, rowsStr(got), strings.Join(c.Expected, "\n"))
				}
			})
		}
	}
}

// compareRows valida fila a fila con tolerancia al ORDEN de empates: entre
// filas con el mismo valor de `visits`, ClickHouse no garantiza orden (el
// YAML grabó una corrida concreta); se comparan como multisete.
func compareRows(got []row, expected []string) error {
	type pair struct {
		got map[string]any
		exp map[string]any
	}
	var groups [][]pair
	var curVisits *float64
	var cur []pair

	flush := func() {
		if len(cur) > 0 {
			groups = append(groups, cur)
			cur = nil
		}
		curVisits = nil
	}
	for i, expLine := range expected {
		var exp, g map[string]any
		if err := json.Unmarshal([]byte(expLine), &exp); err != nil {
			return fmt.Errorf("expected JSON %q: %v", expLine, err)
		}
		gj, _ := json.Marshal(got[i])
		if err := json.Unmarshal(gj, &g); err != nil {
			return fmt.Errorf("got JSON: %v", err)
		}
		ev, _ := toFloat(exp["visits"])
		gv, _ := toFloat(g["visits"])
		if ev != gv {
			return fmt.Errorf("fila %d: visits got %v want %v", i, g["visits"], exp["visits"])
		}
		if curVisits == nil || *curVisits != ev {
			flush()
			v := ev
			curVisits = &v
		}
		cur = append(cur, pair{got: g, exp: exp})
	}
	flush()

	for gi, grp := range groups {
		// Multisete: cada expected debe casar con UN got no usado.
		used := make([]bool, len(grp))
		for _, p := range grp {
			found := false
			for j, g := range grp {
				if used[j] {
					continue
				}
				if jsonEqual(p.exp, g.got) {
					used[j] = true
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("grupo de empate %d: fila esperada sin correspondencia", gi)
			}
		}
	}
	return nil
}

func rowsStr(rows []row) string {
	var b strings.Builder
	for _, r := range rows {
		j, _ := json.Marshal(r)
		b.Write(j)
		b.WriteByte('\n')
	}
	return b.String()
}

// jsonEqual compara con tolerancia numérica int/float (el YAML trae 0 y Go
// puede dar 0.0).
func jsonEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return false
		}
		na, aok := toFloat(va)
		nb, bok := toFloat(vb)
		if aok && bok {
			if na != nb {
				return false
			}
			continue
		}
		if fmt.Sprint(va) != fmt.Sprint(vb) {
			return false
		}
	}
	return true
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func urlParseQuery(s string) (url.Values, error) {
	return url.ParseQuery(strings.ReplaceAll(s, "+", "%20"))
}
