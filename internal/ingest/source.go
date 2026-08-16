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

// domainWithoutWWW replica domainWithoutWWW de ClickHouse para el caso que
// nos ocupa (entradas que son hostnames o nombres de marca): quita el
// prefijo www. de un valor con punto; un valor sin punto no es dominio → "".
func domainWithoutWWW(s string) string {
	if !strings.Contains(s, ".") {
		return ""
	}
	return strings.TrimPrefix(s, "www.")
}
