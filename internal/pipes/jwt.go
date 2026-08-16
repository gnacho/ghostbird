package pipes

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// jwtClaims es el payload del JWT que Ghost auto-firma
// (tinybird-service.js:139-156): HS256 con secreto = tinybird.adminToken.
type jwtClaims struct {
	WorkspaceID string     `json:"workspace_id"`
	Name        string     `json:"name"`
	Exp         int64      `json:"exp"`
	Scopes      []jwtScope `json:"scopes"`
}

// jwtScope: type PIPES:READ, resource = nombre del pipe, fixed_params fuerza
// el site_uuid (aislamiento multi-tenant).
type jwtScope struct {
	Type        string            `json:"type"`
	Resource    string            `json:"resource"`
	FixedParams map[string]string `json:"fixed_params"`
}

// VerifyJWT valida el token HS256 contra el secreto compartido y devuelve
// los claims. No depende de librerías externas (header.payload.signature).
func VerifyJWT(token, secret string) (jwtClaims, error) {
	var claims jwtClaims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims, fmt.Errorf("formato JWT inválido")
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims, fmt.Errorf("header JWT: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return claims, fmt.Errorf("header JWT: %w", err)
	}
	if header.Alg != "HS256" {
		return claims, fmt.Errorf("alg no soportado: %s", header.Alg)
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, fmt.Errorf("payload JWT: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return claims, fmt.Errorf("firma JWT: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return claims, fmt.Errorf("firma JWT inválida")
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return claims, fmt.Errorf("payload JWT: %w", err)
	}
	return claims, nil
}

// AuthorizePipe comprueba exp + scope del pipe + fixed_params.site_uuid ==
// site_uuid de la query. Es la validación que Tinybird aplica y la que
// Ghost espera (403 en discrepancia).
func (c jwtClaims) AuthorizePipe(pipe, siteUUID string, now time.Time) error {
	if c.Exp > 0 && now.Unix() >= c.Exp {
		return fmt.Errorf("token expirado")
	}
	for _, s := range c.Scopes {
		if s.Type != "PIPES:READ" || s.Resource != pipe {
			continue
		}
		if fixed, ok := s.FixedParams["site_uuid"]; ok && fixed != siteUUID {
			return fmt.Errorf("fixed_params.site_uuid no coincide con la query")
		}
		return nil
	}
	return fmt.Errorf("sin scope PIPES:READ para %s", pipe)
}

// SignJWT firma un HS256 como lo hace Ghost (útil para tests y herramientas).
func SignJWT(secret string, claims jwtClaims) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	body := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
