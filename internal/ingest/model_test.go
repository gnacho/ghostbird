package ingest

import (
	"encoding/json"
	"testing"
)

// defaultBody replica el fixture del e2e de TrafficAnalytics
// (test/e2e/web_analytics.test.ts:6-40).
const defaultBody = `{
	"timestamp": "2025-04-14T22:16:06.095Z",
	"action": "page_hit",
	"version": "1",
	"session_id": "9017be4c-3065-484b-b117-9719ad1e3977",
	"payload": {
		"user-agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36",
		"locale": "en-US",
		"location": "US",
		"referrer": null,
		"parsedReferrer": {"source": null, "medium": null, "url": null},
		"pathname": "/test-page",
		"href": "https://example.com/test-page",
		"site_uuid": "940b73e9-4952-4752-b23d-9486f999c47e",
		"post_uuid": "undefined",
		"post_type": "null",
		"member_uuid": "undefined",
		"member_status": "free"
	}
}`

const siteHdr = "940b73e9-4952-4752-b23d-9486f999c47e"
const macChromeUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36"

func TestNormalizarFixtureE2E(t *testing.T) {
	var req PageHitRequest
	if err := json.Unmarshal([]byte(defaultBody), &req); err != nil {
		t.Fatal(err)
	}
	e, err := req.Normalizar(siteHdr, macChromeUA)
	if err != nil {
		t.Fatal(err)
	}
	if e.SiteUUID != siteHdr {
		t.Errorf("site_uuid = %q, quiero el del header", e.SiteUUID)
	}
	if e.Pathname != "/test-page" {
		t.Errorf("pathname = %q", e.Pathname)
	}
	if e.PostUUID != "" {
		t.Errorf("post_uuid 'undefined' debe normalizar a '': %q", e.PostUUID)
	}
	if e.MemberUUID != "" {
		t.Errorf("member_uuid 'undefined' debe normalizar a '': %q", e.MemberUUID)
	}
	if e.MemberStatus != "free" {
		t.Errorf("member_status = %q", e.MemberStatus)
	}
	if e.PostType != "null" {
		t.Errorf("post_type = %q (literal 'null' se conserva)", e.PostType)
	}
	if e.UserAgent != macChromeUA {
		t.Errorf("user-agent = %q", e.UserAgent)
	}
	// session_id del cliente se IGNORA: no forma parte de EventoEntrante.
}

func TestNormalizarTolerante(t *testing.T) {
	// utm null / ausentes / strings vacíos; parsedReferrer con "" (coerción ajv).
	body := `{
		"action": "page_hit",
		"payload": {
			"user-agent": "` + macChromeUA + `",
			"pathname": "/x/",
			"site_uuid": "940b73e9-4952-4752-b23d-9486f999c47e",
			"utm_source": null,
			"utm_medium": "",
			"parsedReferrer": {"source": "", "medium": "", "url": ""},
			"post_type": "post"
		}
	}`
	var req PageHitRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	e, err := req.Normalizar("", macChromeUA)
	if err != nil {
		t.Fatal(err)
	}
	if e.UtmSource != "" || e.UtmMedium != "" {
		t.Errorf("utm null/'' deben ser '': %q %q", e.UtmSource, e.UtmMedium)
	}
	if e.PostType != "post" {
		t.Errorf("post_type = %q", e.PostType)
	}
	if e.MemberStatus != "undefined" {
		t.Errorf("member_status default = %q", e.MemberStatus)
	}
	if e.Locale != "" || e.Location != "" || e.Href != "" {
		t.Errorf("campos ausentes deben ser '': %q %q %q", e.Locale, e.Location, e.Href)
	}
}

func TestNormalizarErrores(t *testing.T) {
	casos := []struct {
		name string
		body string
		hdr  string
	}{
		{"action inválido", `{"action":"scroll","payload":{"pathname":"/","site_uuid":"940b73e9-4952-4752-b23d-9486f999c47e","user-agent":"x"}}`, ""},
		{"site_uuid no guid", `{"action":"page_hit","payload":{"pathname":"/","site_uuid":"no-guid","user-agent":"x"}}`, ""},
		{"pathname vacío", `{"action":"page_hit","payload":{"site_uuid":"940b73e9-4952-4752-b23d-9486f999c47e","user-agent":"x"}}`, ""},
		{"sin ua ni header", `{"action":"page_hit","payload":{"pathname":"/","site_uuid":"940b73e9-4952-4752-b23d-9486f999c47e"}}`, ""},
		{"header no guid", `{"action":"page_hit","payload":{"pathname":"/","site_uuid":"940b73e9-4952-4752-b23d-9486f999c47e","user-agent":"x"}}`, "no-guid"},
	}
	for _, c := range casos {
		t.Run(c.name, func(t *testing.T) {
			var req PageHitRequest
			if err := json.Unmarshal([]byte(c.body), &req); err != nil {
				t.Fatal(err)
			}
			if _, err := req.Normalizar(c.hdr, ""); err == nil {
				t.Errorf("esperaba error para %s", c.name)
			}
		})
	}
}

func TestEsGUID(t *testing.T) {
	if !esGUID("940b73e9-4952-4752-b23d-9486f999c47e") {
		t.Error("guid válido rechazado")
	}
	if esGUID("940b73e949524752b23d9486f999c47e") {
		t.Error("sin guiones debe fallar")
	}
	if esGUID("940b73e9-4952-4752-b23d-9486f999c47X") {
		t.Error("no-hex debe fallar")
	}
}
