package ingest

import "testing"

func TestParseReferrerBuscador(t *testing.T) {
	r := ParseReferrer("https://www.google.com/search?q=ghost", "", "", "")
	if r.Source != "Google" || r.Medium != "search" || r.URL != "www.google.com" {
		t.Errorf("google: %+v", r)
	}
}

func TestParseReferrerSocial(t *testing.T) {
	for url, fuente := range map[string]string{
		"https://x.com/alguien/status/1":         "Twitter",
		"https://t.co/abc":                       "Twitter",
		"https://www.facebook.com/l.php?u=x":     "Facebook",
		"https://www.reddit.com/r/ghost":         "Reddit",
		"https://news.ycombinator.com/item?id=1": "Hacker News",
	} {
		r := ParseReferrer(url, "", "", "")
		if r.Source != fuente {
			t.Errorf("%s → %q, quiero %q", url, r.Source, fuente)
		}
	}
}

func TestParseReferrerGhostNetwork(t *testing.T) {
	r := ParseReferrer("https://ghost.org/pricing", "", "", "")
	if r.Source != "Ghost.org" || r.Medium != "Ghost Network" {
		t.Errorf("ghost.org: %+v", r)
	}
	r = ParseReferrer("https://ghost.org/explore/ejemplo", "", "", "")
	if r.Source != "Ghost Explore" {
		t.Errorf("explore por URL: %+v", r)
	}
	r = ParseReferrer("", "ghost-explore", "", "")
	if r.Source != "Ghost Explore" {
		t.Errorf("explore por source: %+v", r)
	}
}

func TestParseReferrerNewsletter(t *testing.T) {
	r := ParseReferrer("https://example.com/x", "mi-marca-newsletter", "", "")
	if r.Source != "mi marca newsletter" || r.Medium != "Email" {
		t.Errorf("newsletter: %+v", r)
	}
}

func TestParseReferrerFuenteCliente(t *testing.T) {
	// El cliente manda source conocido: se canoniza con el mapa.
	r := ParseReferrer("https://out.reddit.com/x", "reddit", "", "")
	if r.Source != "Reddit" || r.Medium != "social" {
		t.Errorf("canonización: %+v", r)
	}
}

func TestParseReferrerDesconocido(t *testing.T) {
	r := ParseReferrer("https://blog.ejemplo.es/post?utm_source=x", "", "", "")
	if r.Source != "blog.ejemplo.es" || r.URL != "blog.ejemplo.es" || r.Medium != "" {
		t.Errorf("desconocido: %+v", r)
	}
}

func TestParseReferrerAutoreferenciaSinSiteURL(t *testing.T) {
	// Como el AS (sin siteUrl): el propio dominio NO se trata como interno.
	r := ParseReferrer("https://example.com/alguna-pagina", "", "", "")
	if r.Source != "example.com" {
		t.Errorf("sin siteUrl nada es interno: %+v", r)
	}
	// Con siteURL configurado sí.
	r = ParseReferrer("https://example.com/alguna-pagina", "", "", "https://example.com")
	if r.Source != "" || r.URL != "" {
		t.Errorf("autoreferencia debe ser vacía: %+v", r)
	}
}

func TestParseReferrerInvalido(t *testing.T) {
	r := ParseReferrer("no-es-url", "", "", "")
	if r.Source != "" || r.URL != "" {
		t.Errorf("url inválida → vacío: %+v", r)
	}
}

func TestNormalizarSource(t *testing.T) {
	casos := []struct{ in, out string }{
		{"", ""}, // directo
		{"www.facebook.com", "Facebook"},
		{"Facebook", "Facebook"},
		{"l.facebook.com", "Facebook"},
		{"bsky", "Bluesky"},
		{"go.bsky.app", "Bluesky"},
		{"x.com", "Twitter"},
		{"com.reddit.frontpage", "Reddit"},
		{"search.brave.com", "Brave Search"},
		{"com.google.android.gm", "Gmail"},
		{"ghost.org", "Ghost"},
		{"en.wikipedia.org", "Wikipedia"},
		{"blog.ejemplo.es", "blog.ejemplo.es"},   // fallback dominio
		{"www.otro-sitio.net", "otro-sitio.net"}, // domainWithoutWWW
		{"Ghost Explore", "Ghost Explore"},       // sin punto → tal cual
		{"mi marca newsletter", "mi marca newsletter"},
	}
	for _, c := range casos {
		if got := NormalizarSource(c.in); got != c.out {
			t.Errorf("NormalizarSource(%q) = %q, quiero %q", c.in, got, c.out)
		}
	}
}
