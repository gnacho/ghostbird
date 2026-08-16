package ingest

import (
	"embed"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

// referrersData contiene el mapa de referers conocidos de
// @tryghost/referrer-parser 0.1.21 (MIT, datos derivados de
// snowplow-referer-parser). Ver data/referrers.LICENSE.
//
//go:embed data/referrers.json
var referrersData embed.FS

type referrerEntry struct {
	Medium string `json:"medium"`
	Source string `json:"source"`
}

var (
	referrersByKey    map[string]referrerEntry
	referrersBySource map[string]referrerEntry // clave: source en minúsculas
	referrerKeysDesc  []string                 // claves ordenadas por longitud desc
)

func init() {
	b, err := referrersData.ReadFile("data/referrers.json")
	if err != nil {
		panic("ghostbird: no se pudo leer data/referrers.json: " + err.Error())
	}
	var m map[string]referrerEntry
	if err := json.Unmarshal(b, &m); err != nil {
		panic("ghostbird: referrers.json inválido: " + err.Error())
	}
	referrersByKey = m
	referrersBySource = make(map[string]referrerEntry, len(m))
	for _, v := range m {
		referrersBySource[strings.ToLower(v.Source)] = v
	}
	referrerKeysDesc = make([]string, 0, len(m))
	for k := range m {
		referrerKeysDesc = append(referrerKeysDesc, k)
	}
	// getDataFromUrl ordena por longitud desc y hace startsWith: réplica exacta.
	sort.Slice(referrerKeysDesc, func(i, j int) bool {
		if len(referrerKeysDesc[i]) != len(referrerKeysDesc[j]) {
			return len(referrerKeysDesc[i]) > len(referrerKeysDesc[j])
		}
		return referrerKeysDesc[i] < referrerKeysDesc[j]
	})
}

// ReferrerResult es el output del parser (equivalente a lo que el AS guarda
// como referrerUrl/referrerSource/referrerMedium). URL es el HOSTNAME.
type ReferrerResult struct {
	Source string
	Medium string
	URL    string
}

// ParseReferrer es el port fiel de ReferrerParser.parse() del paquete de
// Ghost. adminURL/siteURL van vacíos en el contexto del AS (se instancia sin
// opciones), así que la autoreferencia nunca se trata como interna; se
// mantienen como parámetro para futuras configuraciones.
func ParseReferrer(referrerURLStr string, referrerSource, referrerMedium string, siteURLStr string) ReferrerResult {
	referrerURL := urlFromStr(referrerURLStr)

	// 1. Ghost Explore.
	if referrerSource == "ghost-explore" || (referrerURL != nil && referrerURL.Hostname() == "ghost.org" && strings.HasPrefix(referrerURL.Path, "/explore")) {
		return ReferrerResult{Source: "Ghost Explore", Medium: "Ghost Network", URL: hostname(referrerURL)}
	}
	// 2. ghost.org.
	if referrerURL != nil && referrerURL.Hostname() == "ghost.org" {
		return ReferrerResult{Source: "Ghost.org", Medium: "Ghost Network", URL: hostname(referrerURL)}
	}
	// 3. Newsletters de Ghost: "<nombre>-newsletter" → Email.
	if strings.HasSuffix(referrerSource, "-newsletter") {
		return ReferrerResult{Source: strings.ReplaceAll(referrerSource, "-", " "), Medium: "Email", URL: hostname(referrerURL)}
	}
	// 4. Fuente explícita del cliente: canónica del mapa si existe.
	if referrerSource != "" {
		known := referrersBySource[strings.ToLower(referrerSource)]
		src := referrerSource
		med := ""
		if known.Source != "" {
			src = known.Source
			med = known.Medium
		}
		if med == "" {
			med = referrerMedium
		}
		if med == "" {
			if d := dataFromURL(referrerURL); d != nil {
				med = d.Medium
			}
		}
		return ReferrerResult{Source: src, Medium: med, URL: hostname(referrerURL)}
	}
	// 5. Lookup por hostname+pathname (match de clave más larga).
	if !isSiteDomain(referrerURL, siteURLStr) {
		if d := dataFromURL(referrerURL); d != nil {
			return ReferrerResult{Source: d.Source, Medium: d.Medium, URL: hostname(referrerURL)}
		}
		return ReferrerResult{Source: hostname(referrerURL), Medium: "", URL: hostname(referrerURL)}
	}
	// 6. Autoreferencia (solo posible con siteURL configurado).
	return ReferrerResult{}
}

func dataFromURL(u *url.URL) *referrerEntry {
	if u == nil {
		return nil
	}
	hostPath := strings.ToLower(u.Hostname()) + u.Path
	for _, k := range referrerKeysDesc {
		if strings.HasPrefix(hostPath, k) {
			e := referrersByKey[k]
			return &e
		}
	}
	return nil
}

func urlFromStr(s string) *url.URL {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return nil
	}
	return u
}

func hostname(u *url.URL) string {
	if u == nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func isSiteDomain(u *url.URL, siteURLStr string) bool {
	if u == nil {
		return false
	}
	site := urlFromStr(siteURLStr)
	if site == nil {
		return false // como el AS: sin siteUrl configurado, nada es interno
	}
	sh := strings.TrimPrefix(site.Hostname(), "www.")
	uh := strings.TrimPrefix(u.Hostname(), "www.")
	return sh == uh && strings.HasPrefix(u.Path, site.Path)
}
