package ingest

import (
	"regexp"
	"strings"
)

// botRe es la regex de detección de bots de TrafficAnalytics
// (src/utils/bot-detection.ts:1), replicada literalmente. OJO: "bot" y
// "curl" son substrings: matchea cualquier UA que los contenga.
var botRe = regexp.MustCompile(`(?i)wget|ahrefsbot|curl|bot|crawler|spider|urllib|bitdiscovery|\+https:\/\/|googlebot`)

// EsBot dice si el user-agent parece un bot/crawler.
func EsBot(ua string) bool {
	return botRe.MatchString(ua)
}

// Regexpes del port de mv_hits.pipe (líneas 112-141 de Ghost): os y browser
// se derivan del user-agent EN MINÚSCULAS con este orden exacto.
var (
	reWindows = regexp.MustCompile(`windows`)
	reMac     = regexp.MustCompile(`mac`)
	reLinux   = regexp.MustCompile(`linux`)
	reAndroid = regexp.MustCompile(`android`)
	reIOS     = regexp.MustCompile(`iphone|ipad|ipod`)

	reFirefox = regexp.MustCompile(`firefox`)
	reChrome  = regexp.MustCompile(`chrome|crios`)
	reOpera   = regexp.MustCompile(`opera`)
	reIE      = regexp.MustCompile(`msie|trident`)
	reSafari  = regexp.MustCompile(`iphone|ipad|safari`)

	reCros = regexp.MustCompile(`cros`) // Chrome OS: "CrOS" (sin "linux" en el UA)
)

// DerivarOS replica el CASE de os de mv_hits (orden: windows, mac, linux,
// android, iphone|ipad|ipod; else 'Unknown' con mayúscula inicial).
func DerivarOS(uaLower string) string {
	switch {
	case reWindows.MatchString(uaLower):
		return "windows"
	case reMac.MatchString(uaLower):
		return "macos"
	case reLinux.MatchString(uaLower):
		return "linux"
	case reAndroid.MatchString(uaLower):
		return "android"
	case reIOS.MatchString(uaLower):
		return "ios"
	default:
		return "Unknown"
	}
}

// DerivarBrowser replica el CASE de browser de mv_hits (orden: firefox,
// chrome|crios, opera, msie|trident, iphone|ipad|safari; else 'Unknown').
func DerivarBrowser(uaLower string) string {
	switch {
	case reFirefox.MatchString(uaLower):
		return "firefox"
	case reChrome.MatchString(uaLower):
		return "chrome"
	case reOpera.MatchString(uaLower):
		return "opera"
	case reIE.MatchString(uaLower):
		return "ie"
	case reSafari.MatchString(uaLower):
		return "safari"
	default:
		return "Unknown"
	}
}

// DerivarDevice replica la semántica del AS (device derivado del OS con
// ua-parser): bot | mobile-ios | mobile-android | desktop | unknown. OJO: no
// se puede reusar DerivarOS tal cual porque el orden literal de mv_hits pone
// "mac" antes que "iphone|ipad|ipod" y "linux" antes que "android" (todo UA
// de iPhone lleva "like mac os x" y todo Android lleva "linux"): aquí los
// patrones móviles ganan, como hace ua-parser.
func DerivarDevice(uaLower string) string {
	if EsBot(uaLower) {
		return "bot"
	}
	switch {
	case reAndroid.MatchString(uaLower):
		return "mobile-android"
	case reIOS.MatchString(uaLower):
		return "mobile-ios"
	case reWindows.MatchString(uaLower), reMac.MatchString(uaLower), reLinux.MatchString(uaLower), reCros.MatchString(uaLower):
		return "desktop"
	default:
		return "unknown"
	}
}

// UAInfo junta las tres derivaciones para un user-agent.
func UAInfo(ua string) (os, browser, device string) {
	l := strings.ToLower(ua)
	return DerivarOS(l), DerivarBrowser(l), DerivarDevice(l)
}
