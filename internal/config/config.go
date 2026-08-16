// Package config carga la configuración de GhostBird: flags con fallback
// a variables de entorno GHOSTBIRD_*.
package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// Config es la configuración del servicio.
type Config struct {
	Addr        string // dirección de escucha HTTP
	DBPath      string // ruta del fichero SQLite
	IngestToken string // token de ingesta para /v0/events ("" = sin auth)
	AdminToken  string // secreto HS256 de los JWT que Ghost auto-firma ("" = sin auth de pipes)
	StatsToken  string // token estático alternativo para pipes (stats.token/local.token de Ghost)
	TrustProxy  bool   // usar la primera IP de X-Forwarded-For como IP cliente
	LogLevel    string
}

// Load parsea flags y variables de entorno. Los flags ganan si se pasan
// explícitamente; si no, se usa GHOSTBIRD_<NOMBRE>.
func Load() (*Config, error) {
	c := &Config{}

	flag.StringVar(&c.Addr, "addr", envOr("ADDR", ":8080"), "dirección HTTP de escucha")
	flag.StringVar(&c.DBPath, "db", envOr("DB", "data/ghostbird.db"), "ruta del fichero SQLite")
	flag.StringVar(&c.IngestToken, "ingest-token", envOr("INGEST_TOKEN", ""), "token de ingesta para /v0/events (vacío = sin autenticación)")
	flag.StringVar(&c.AdminToken, "admin-token", envOr("ADMIN_TOKEN", ""), "secreto HS256 de los JWT de pipes (tinybird.adminToken de Ghost; vacío = sin auth)")
	flag.StringVar(&c.StatsToken, "stats-token", envOr("STATS_TOKEN", ""), "token estático para pipes (stats.token/local.token de Ghost)")
	flag.BoolVar(&c.TrustProxy, "trust-proxy", envOrBool("TRUST_PROXY", true), "confiar en X-Forwarded-For (IP cliente = primera entrada)")
	flag.StringVar(&c.LogLevel, "log-level", envOr("LOG_LEVEL", "info"), "nivel de log (debug|info|warn|error)")
	flag.Parse()

	c.Addr = strings.TrimSpace(c.Addr)
	c.DBPath = strings.TrimSpace(c.DBPath)
	if c.Addr == "" {
		return nil, fmt.Errorf("addr no puede estar vacío")
	}
	if c.DBPath == "" {
		return nil, fmt.Errorf("db no puede estar vacío")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return nil, fmt.Errorf("log-level inválido: %q", c.LogLevel)
	}
	return c, nil
}

func envOr(name, def string) string {
	if v, ok := os.LookupEnv("GHOSTBIRD_" + name); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func envOrBool(name string, def bool) bool {
	v, ok := os.LookupEnv("GHOSTBIRD_" + name)
	if !ok {
		return def
	}
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}
