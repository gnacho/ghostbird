package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// SessionID calcula la firma de sesión EXACTA de TrafficAnalytics
// (UserSignatureService.ts:127-132):
//
//	sha256_hex(salt:site_uuid:ip:user_agent)
//
// Misma IP+UA el mismo día = misma sesión; la sal rota diariamente por sitio.
func SessionID(salt, siteUUID, ip, userAgent string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%s", salt, siteUUID, ip, userAgent)))
	return hex.EncodeToString(h[:])
}

// SaltDay devuelve la clave de día UTC de la sal (YYYY-MM-DD).
func SaltDay(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}
