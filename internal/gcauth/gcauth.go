// Package gcauth valida tokens de la API de GoatCounter contra su base de
// datos (solo lectura), delegando en GoatCounter la identidad del ecosistema:
// un token de GoatCounter con permiso de lectura de estadísticas puede
// consultar los pipes de GhostBird, con el alcance de sitios del token.
package gcauth

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// APIPermStats es el bit "Read statistics" de GoatCounter
// (api_token.go: APIPermNothing=1, Count=2, Export=4, SiteRead=8,
// SiteCreate=16, SiteUpdate=32, Stats=64). Es el que exigimos para leer pipes.
const APIPermStats int64 = 64

// TokenInfo es el resultado de validar un token de GoatCounter.
type TokenInfo struct {
	Name        string  // nombre del token (logs)
	Email       string  // email del usuario dueño (logs)
	Permissions int64   // bitmask de permisos del token
	Sites       []int64 // IDs de sitio GoatCounter; [-1] = todos
}

// AllSites replica SiteIDs.All() de GoatCounter: exactamente [-1].
func (t TokenInfo) AllSites() bool {
	return len(t.Sites) == 1 && t.Sites[0] == -1
}

// HasStats dice si el token puede leer estadísticas.
func (t TokenInfo) HasStats() bool {
	return t.Permissions&APIPermStats != 0
}

// Authenticator valida tokens contra la BD de GoatCounter en modo solo
// lectura, con caché en memoria (los tokens casi nunca cambian y GoatCounter
// rate-limitea su propia API; evitamos martillear su BD).
type Authenticator struct {
	db  *sql.DB
	mu  sync.RWMutex
	ttl time.Duration
	// negTTL para tokens desconocidos: más corto, para que un token recién
	// creado en GoatCounter se acepte pronto.
	negTTL time.Duration
	cache  map[string]entry
}

type entry struct {
	info   TokenInfo
	found  bool
	expiry time.Time
}

const (
	defaultTTL    = 5 * time.Minute
	defaultNegTTL = 30 * time.Second
	maxCache      = 256 // pocos tokens existen; ante desborde se vacía
)

// Open abre la BD de GoatCounter en modo estrictamente de lectura
// (mode=ro + PRAGMA query_only: aunque algo fallara, no puede escribir).
func Open(path string) (*Authenticator, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(true)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("abrir goatcounter db: %w", err)
	}
	// Un pequeño pool de lectores: el acceso a disco es barato y así no
	// serializamos validaciones concurrentes.
	db.SetMaxOpenConns(2)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping goatcounter db: %w", err)
	}
	return &Authenticator{
		db: db, ttl: defaultTTL, negTTL: defaultNegTTL, cache: map[string]entry{},
	}, nil
}

// Close cierra la conexión de lectura.
func (a *Authenticator) Close() error { return a.db.Close() }

// Validate devuelve la información del token o found=false si no existe.
// El resultado se cachea (TTL positivo/negativo distinto). La comparación no
// es constante-en-tiempo en el sentido cripto: la BD busca por índice único
// (coste ~constante por longitud), y el token viaja como clave de lookup,
// igual que hace el propio GoatCounter.
func (a *Authenticator) Validate(token string) (TokenInfo, bool) {
	now := time.Now()
	a.mu.RLock()
	if e, ok := a.cache[token]; ok && now.Before(e.expiry) {
		a.mu.RUnlock()
		return e.info, e.found
	}
	a.mu.RUnlock()

	var info TokenInfo
	var sitesJSON string
	// CAST: GoatCounter puede almacenar permissions con afinidad TEXT
	// (verificado en producción: "126" como string).
	err := a.db.QueryRow(`
		SELECT t.name, CAST(t.permissions AS INTEGER), t.sites, COALESCE(u.email, '')
		FROM api_tokens t LEFT JOIN users u ON u.user_id = t.user_id
		WHERE t.token = ? LIMIT 1`, token).
		Scan(&info.Name, &info.Permissions, &sitesJSON, &info.Email)
	found := err == nil
	if err != nil && err != sql.ErrNoRows {
		// Error de BD (bloqueo, fichero desaparecido): tratar como no
		// encontrado; fail-closed sin tumbar el request con 500.
		found = false
	}
	if found {
		var ids []int64
		if err := json.Unmarshal([]byte(sitesJSON), &ids); err != nil || len(ids) == 0 {
			// sites vacío o corrupto: sin alcance conocible → no concede.
			found = false
		} else {
			info.Sites = ids
		}
	}

	a.mu.Lock()
	if len(a.cache) >= maxCache {
		a.cache = map[string]entry{}
	}
	ttl := a.negTTL
	if found {
		ttl = a.ttl
	}
	a.cache[token] = entry{info: info, found: found, expiry: now.Add(ttl)}
	a.mu.Unlock()
	return info, found
}
