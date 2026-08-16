package ingest

import "strings"

// sourceMap es el mapa de consolidación de fuentes de mv_hits.pipe (Ghost,
// líneas 63-105): referrerSource → nombre canónico de cara al dashboard.
var sourceMap = map[string]string{
	// Social Media Consolidation
	"Facebook": "Facebook", "www.facebook.com": "Facebook", "l.facebook.com": "Facebook",
	"lm.facebook.com": "Facebook", "m.facebook.com": "Facebook", "facebook": "Facebook",
	"Twitter": "Twitter", "x.com": "Twitter", "com.twitter.android": "Twitter",
	"go.bsky.app": "Bluesky", "bsky": "Bluesky", "bsky.app": "Bluesky",
	"Instagram": "Instagram", "www.instagram.com": "Instagram",
	"LinkedIn": "LinkedIn", "LINKEDIN_COMPANY": "LinkedIn",
	"l.threads.com": "Threads",
	// Reddit Ecosystem
	"www.reddit.com": "Reddit", "out.reddit.com": "Reddit", "old.reddit.com": "Reddit", "com.reddit.frontpage": "Reddit",
	// Search Engines
	"search.brave.com": "Brave Search",
	"www.ecosia.org":   "Ecosia",
	// Email Services
	"Gmail": "Gmail", "com.google.android.gm": "Gmail", "mail.google.com": "Gmail",
	"Outlook.com": "Outlook",
	"Yahoo!":      "Yahoo!", "www.yahoo.com": "Yahoo!", "Yahoo! Mail": "Yahoo!", "r.search.yahoo.com": "Yahoo!",
	"AOL Mail": "AOL Mail",
	// Content Platforms
	"flipboard": "Flipboard", "flipboard.com": "Flipboard", "flipboard.app": "Flipboard",
	"substack": "Substack", "substack.com": "Substack",
	"Ghost.org": "Ghost", "ghost.org": "Ghost",
	"buffer":   "Buffer",
	"Taboola":  "Taboola",
	"AppNexus": "AppNexus",
	// Wikipedia
	"en.wikipedia.org": "Wikipedia", "en.m.wikipedia.org": "Wikipedia",
	// Mastodon Network
	"mastodon.social": "Mastodon", "mastodon.online": "Mastodon", "org.joinmastodon.android": "Mastodon",
	"phanpy.social": "Mastodon", "dev.phanpy.social": "Mastodon",
	// News Aggregators
	"www.memeorandum.com": "Memeorandum", "memeorandum.com": "Memeorandum",
	"ground.news":       "Ground News",
	"apple.news":        "Apple News",
	"www.smartnews.com": "SmartNews",
}

// NormalizarSource replica el CASE completo de `source` de mv_hits: mapa de
// consolidación → domainWithoutWWW(referrer) → referrer tal cual.
func NormalizarSource(referrer string) string {
	if referrer == "" {
		return ""
	}
	if v, ok := sourceMap[referrer]; ok {
		return v
	}
	if d := domainWithoutWWW(referrer); d != "" {
		return d
	}
	return referrer
}

// domainWithoutWWW replica domainWithoutWWW de ClickHouse para nuestros
// casos: acepta URLs completas ("https://www.bing.com/x"), host+path
// ("bing.com/images/search") y hostnames solos; devuelve el dominio sin
// prefijo www. Un valor sin punto (nombres canónicos como "Bing") no es
// dominio → "" (el CASE de mv_hits cae al ELSE: el valor tal cual).
func domainWithoutWWW(s string) string {
	if s == "" {
		return ""
	}
	// Quitar esquema si es URL.
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	} else if strings.HasPrefix(s, "//") {
		s = s[2:]
	}
	// Quedarse con el host: hasta /, ? o #.
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	// Puerto fuera.
	if i := strings.LastIndex(s, ":"); i >= 0 && !strings.Contains(s[i:], "]") {
		s = s[:i]
	}
	s = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(s), "www."), "."))
	if !strings.Contains(s, ".") {
		return ""
	}
	for _, c := range s {
		valid := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_'
		if !valid {
			return ""
		}
	}
	return s
}
