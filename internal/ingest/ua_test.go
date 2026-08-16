package ingest

import (
	"regexp"
	"testing"
)

func TestEsBot(t *testing.T) {
	bots := []string{
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"curl/8.5.0",
		"Wget/1.21",
		"Mozilla/5.0 (compatible; AhrefsBot/7.0)",
		"python-requests/2.31 (urllib)",
	}
	for _, ua := range bots {
		if !EsBot(ua) {
			t.Errorf("debe ser bot: %s", ua)
		}
	}
	noBots := []string{
		macChromeUA,
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
		"Mozilla/5.0 (X11; Linux x86_64; rv:125.0) Gecko/20100101 Firefox/125.0",
	}
	for _, ua := range noBots {
		if EsBot(ua) {
			t.Errorf("NO debe ser bot: %s", ua)
		}
	}
}

func TestDerivarOS(t *testing.T) {
	// Orden LITERAL de mv_hits (windows→mac→linux→android→ios): por eso un
	// UA de iPhone ("like mac os x") da macos y uno Android ("linux;")
	// da linux. Es el comportamiento real del SQL de Ghost; os/browser no
	// los consume ningún pipe (el dashboard usa device), se portan por
	// fidelidad.
	casos := []struct{ ua, os string }{
		{"mozilla/5.0 (windows nt 10.0; win64; x64) applewebkit/537.36 (khtml, like gecko) chrome/124.0 safari/537.36", "windows"},
		{"mozilla/5.0 (macintosh; intel mac os x 10_15_7) applewebkit/605.1.15 (khtml, like gecko) version/17.0 safari/605.1.15", "macos"},
		{"mozilla/5.0 (x11; linux x86_64; rv:125.0) gecko/20100101 firefox/125.0", "linux"},
		{"mozilla/5.0 (linux; android 14; pixel 8) applewebkit/537.36 (khtml, like gecko) chrome/124.0 mobile safari/537.36", "linux"},
		{"mozilla/5.0 (iphone; cpu iphone os 17_0 like mac os x) applewebkit/605.1.15 (khtml, like gecko) version/17.0 mobile/15e148 safari/604.1", "macos"},
		{"strange-agent/1.0", "Unknown"},
	}
	for _, c := range casos {
		if got := DerivarOS(c.ua); got != c.os {
			t.Errorf("os(%q) = %q, quiero %q", c.ua, got, c.os)
		}
	}
}

func TestDerivarBrowser(t *testing.T) {
	casos := []struct{ ua, b string }{
		{"mozilla/5.0 (x11; linux x86_64; rv:125.0) gecko/20100101 firefox/125.0", "firefox"},
		{"mozilla/5.0 (windows nt 10.0) applewebkit/537.36 (khtml, like gecko) chrome/124.0 safari/537.36", "chrome"},
		{"mozilla/5.0 (iphone; cpu iphone os 17_0 like mac os x) applewebkit/605.1.15 (khtml, like gecko) version/17.0 mobile/15e148 safari/604.1", "safari"},
		{"mozilla/5.0 (windows nt 6.1; trident/7.0; rv:11.0) like gecko", "ie"},
		{"mozilla/5.0 (macintosh) applewebkit/537.36 (khtml, like gecko) crios/124.0 mobile/15e148 safari/604.1", "chrome"}, // crios
		{"strange-agent/1.0", "Unknown"},
	}
	for _, c := range casos {
		if got := DerivarBrowser(c.ua); got != c.b {
			t.Errorf("browser(%q) = %q, quiero %q", c.ua, got, c.b)
		}
	}
}

func TestDerivarDevice(t *testing.T) {
	casos := []struct{ ua, d string }{
		{"mozilla/5.0 (iphone; cpu iphone os 17_0 like mac os x)", "mobile-ios"},
		{"mozilla/5.0 (linux; android 14; pixel 8)", "mobile-android"},
		{"mozilla/5.0 (windows nt 10.0; win64; x64)", "desktop"},
		{"mozilla/5.0 (macintosh; intel mac os x 10_15_7)", "desktop"},
		{"mozilla/5.0 (x11; cros x86_64 14541.0.0)", "desktop"},
		{"mozilla/5.0 (compatible; googlebot/2.1)", "bot"},
		{"strange-agent/1.0", "unknown"},
	}
	for _, c := range casos {
		if got := DerivarDevice(c.ua); got != c.d {
			t.Errorf("device(%q) = %q, quiero %q", c.ua, got, c.d)
		}
	}
}

func TestSessionID(t *testing.T) {
	a := SessionID("salt", "site", "1.2.3.4", "UA")
	b := SessionID("salt", "site", "1.2.3.4", "UA")
	if a != b {
		t.Error("misma entrada debe dar misma firma")
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(a) {
		t.Errorf("formato de firma: %q", a)
	}
	if SessionID("salt", "site", "1.2.3.4", "UA") == SessionID("otra-sal", "site", "1.2.3.4", "UA") {
		t.Error("sal distinta debe dar firma distinta")
	}
	if SessionID("salt", "site", "1.2.3.4", "UA") == SessionID("salt", "site2", "1.2.3.4", "UA") {
		t.Error("site distinto debe dar firma distinta")
	}
}
