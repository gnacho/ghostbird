# GhostBird

Drop-in replacement **self-hosted de Tinybird** para las estadísticas nativas de Ghost 6.x.
Filosofía GoatCounter: **un binario Go + un fichero SQLite + cero configuración obligatoria**.
Ghost sigue creyendo que habla con Tinybird; solo cambias la URL de conexión.

```
┌──────────────┐  1. page_hit   ┌────────────────────────────┐        ┌──────────────────┐
│ Visitante    │───────────────►│  GhostBird (Go, 1 binario) │───────►│ SQLite (default) │
│ (ghost-stats │  CORS + 202    │  · collector /api/v1/page │        │ PostgreSQL (opt) │
│  .min.js)    │                │  · ingesta /v0/events      │        └──────────────────┘
└──────────────┘                │  · pipes /v0/pipes/*.json │                 ▲
┌──────────────┐  2. pipes JWT  │  · JWT HS256 scoped        │        ┌──────────────────┐
│ Ghost server │───────────────►│  · tablas agregadas + job  │        │ Tablas agregadas │
└──────────────┘                └────────────────────────────┘        │ (sesiones, daily)│
┌──────────────┐  3. pipes JWT        ▲                               └──────────────────┘
│ Admin (React │──────────────────────┘  (el navegador del Admin consulta DIRECTO,
│  navegador)  │                          con el JWT que le sirve Ghost)
└──────────────┘
```

## Stack

| Capa | Elección | Por qué |
|---|---|---|
| Lenguaje | Go | Binario estático único (~10-15 MB), sin runtime. Precedente: GoatCounter. |
| DB default | SQLite (WAL) | Cero config, un fichero. Reglas del stack: single-writer + pragmas de producción. |
| DB opcional | PostgreSQL | Sitios con >100K pageviews/mes; se comparte instancia con Ghost. |
| Agregaciones | Tablas agregadas + job periódico | Sesiones (primer hit) y KPIs pre-computados; en lugar de ClickHouse. |
| Auth queries | JWT HS256 (scopes + fixed_params.site_uuid) + tokens estáticos | Compatible con el token que Ghost auto-firma. |
| Deploy | Binario + systemd; sin Docker necesario | Igual que el resto de apps self-hosted del autor. |

## Contrato (Fase 0 — COMPLETADA)

Documentación completa del contrato Ghost↔Tinybird, verificada contra código real
(Ghost 6.58.0-rc.0 + TrafficAnalytics 1.0.351):

- **[docs/api-contract.md](docs/api-contract.md)** — resumen ejecutivo con todas las decisiones.
- **[docs/contract/ghost-side.md](docs/contract/ghost-side.md)** — lado Ghost: config, pipes
  (SQL literal de cada uno), JWT, formato de respuestas, fixtures, SSRF guard.
- **[docs/contract/trafficanalytics-side.md](docs/contract/trafficanalytics-side.md)** — lado
  collector: payload exacto, bots, session_id, batching, tokens, fixtures literales.

Hallazgos que cambiaron el plan inicial: el Admin consulta el backend **desde el navegador**
(CORS obligatorio), no existe `user_signature` (es `session_id` calculada por el collector),
`location` viene del navegador (sin geo server-side), los pipes `_v2` están aparcados, el JWT
lo auto-firma Ghost con un secreto compartido, y la entrega es at-least-once (dedup por
`event_id`).

## Fases

| Fase | Estado | Contenido |
|---|---|---|
| 0. Reconocimiento | ✅ Completada | Contrato documentado (docs/). |
| 1. MVP ingesta | ✅ Completada | Single binary: collector `POST /api/v1/page_hit` (bots, UA, session_id) + `POST /v0/events` (compat TrafficAnalytics) + SQLite + CORS + `/healthz`. |
| 2. Pipes lectura | ✅ Completada | 13 pipes v1 + JWT HS256 scoped + `filtered_sessions` en SQLite. **Tests de fidelidad: la suite YAML oficial de Tinybird pasa entera contra el mismo fixture.** |
| 3. Integración Ghost | ✅ Completada | Verificado end-to-end con Ghost 6.57.1 real: tracker en el HTML, page_hit del navegador real almacenado, JWT firmado por Ghost, dashboard Admin pintando "Unique visitors" y "online" desde GhostBird. |
| 4. Robustez | 🔶 Esencial hecha | Job nocturno: purge de sales, retención configurable (`-retention-days`), backup diario verificado con VACUUM INTO + rotación 14 días (`-backup-dir`), `PRAGMA optimize`+checkpoint. Pendiente: rate limiting, métricas Prometheus, cobertura >70%. |
| 5. Comunidad | ⬜ | README EN/ES, instalador one-liner, CI, licencia (a decidir), anuncio. |

## Deploy con Ghost real (verificado con Ghost 6.57.1)

GhostBird necesita ser alcanzable por (a) los navegadores de los visitantes
(tracker) y (b) el navegador del Admin (pipes) → sírvelo bajo el mismo dominio
que tu Ghost con nginx:

```nginx
location /ghb/ {
    proxy_pass http://127.0.0.1:18181/;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;  # imprescindible: session_id firma la IP
    proxy_set_header X-Forwarded-Proto $http_x_forwarded_proto;
}
```

En `config.production.json` (o el env de tu Ghost):

```json
{
  "tinybird": {
    "workspaceId": "ghostbird-local",
    "adminToken": "<EL MISMO secreto que pasaste a GhostBird con -admin-token>",
    "tracker": { "endpoint": "https://TU-SITIO/ghb/api/v1/page_hit", "datasource": "analytics_events" },
    "stats": {
      "endpoint": "http://127.0.0.1:18181",
      "endpointBrowser": "https://TU-SITIO/ghb"
    }
  }
}
```

Notas verificadas:
- `stats.endpoint` (server-side) puede ser `127.0.0.1` si Ghost y GhostBird
  comparten host. OJO: el guard SSRF de Ghost bloquea IPs privadas en
  `env: production`; con `env: development` funciona directo (así está
  probado). En producción real usa un hostname que resuelva, o el mismo
  dominio público (`/ghb/`).
- Reinicia Ghost tras cambiar la config (cachea el tracker en memoria).
- El setting BD `web_analytics` (default on) es el interruptor del dashboard.

## Ejecución (Fase 1)

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ghostbird ./cmd/ghostbird
./ghostbird -addr :8080 -db data/ghostbird.db
```

Flags (equiv. env `GHOSTBIRD_ADDR`, `GHOSTBIRD_DB`, `GHOSTBIRD_INGEST_TOKEN`,
`GHOSTBIRD_ADMIN_TOKEN`, `GHOSTBIRD_STATS_TOKEN`, `GHOSTBIRD_TRUST_PROXY`,
`GHOSTBIRD_RETENTION_DAYS`, `GHOSTBIRD_BACKUP_DIR`, `GHOSTBIRD_LOG_LEVEL`):
`-admin-token` activa la auth JWT de los pipes ( ponlo SIEMPRE si el servicio
es alcanzable desde fuera: sin él, `/v0/pipes/` queda abierto);
`-ingest-token` activa la auth de `/v0/events`; `-trust-proxy` (default true)
toma la primera IP de X-Forwarded-For. Un env malformado aborta el arranque.

### systemd (producción)

Usuario dedicado + secreto en EnvironmentFile (NUNCA en ExecStart: cmdline es
legible en /proc por cualquier usuario local) + sandbox:

```ini
[Unit]
Description=GhostBird (Tinybird drop-in for Ghost analytics)
After=network.target

[Service]
User=ghostbird
EnvironmentFile=/etc/ghostbird/ghostbird.env   # GHOSTBIRD_ADMIN_TOKEN=...
ExecStart=/opt/ghostbird/ghostbird -addr 127.0.0.1:18181 -db /opt/ghostbird/data/ghostbird.db -trust-proxy
Restart=always
RestartSec=10
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=/opt/ghostbird/data
ProtectHome=yes
PrivateTmp=yes
MemoryMax=256M

[Install]
WantedBy=multi-user.target
```

Endpoints:

| Ruta | Qué hace |
|---|---|
| `POST /api/v1/page_hit?name=analytics_events` | Collector: valida, filtra bots (202 stealth), enriquece (UA→os/browser/device, referrer parseado con port de @tryghost/referrer-parser, source normalizado con el mapa mv_hits, `session_id=sha256(salt:site:ip:ua)` con sal diaria por sitio) y almacena en SQLite. Responde `202 {"message":"Page hit event received"}`. |
| `POST /v0/events?name=analytics_events[&wait=true]` | Events API (compat TrafficAnalytics como collector): NDJSON u objeto único, Bearer o `?token=`; dedup por `(site_uuid, event_id)` (at-least-once); `device=bot` se descarta. Responde `202 {"success":true}`. |
| `GET /healthz` | `{"status":"ok","events":N}` (503 si la BD no responde). |

CORS en todo el servicio: `origin *`, métodos GET/POST/PUT/DELETE/OPTIONS,
headers `Origin, X-Requested-With, Content-Type, Accept, Authorization,
x-site-uuid` (réplica de TrafficAnalytics; el navegador del visitante Y el del
Admin llaman cross-origin).

Eventos en tabla `events` aplanada (una fila = un page_hit, campos
normalizados a `""`, `"undefined"`→`""` en UUIDs, `source` pre-normalizado,
`raw` JSON canónico) + tabla `salts` (sal diaria por sitio, purge a 7 días).
Migraciones con `PRAGMA user_version`. Single-writer (WAL, busy_timeout,
txlock IMMEDIATE, MaxOpenConns 1).

Tests: `go test -race ./...` (parsing tolerante con fixtures del e2e del AS,
bots, derivación UA, firma, referrer port, normalización source, dedup,
endpoints e2e con SQLite temporal).

## Decisiones registradas

- **Single binary con collector integrado** (opción B): el usuario de GhostBird despliega un
  solo servicio; TrafficAnalytics (Node) deja de ser necesario. La API `/v0/events` se
  mantiene por compatibilidad para quien prefiera el collector oficial.
- **Dedup por `event_id`** (UNIQUE + INSERT OR IGNORE) por semántica at-least-once.
- **SQLite single-writer** con las pragmas del stack (WAL, busy_timeout, BEGIN IMMEDIATE).
- Sin Docker en el flujo de desarrollo del autor (pruebas en LXC si hace falta).
- **Referrer parsing**: port fiel de `@tryghost/referrer-parser` 0.1.21 (MIT) con su mapa de
  2.381 referers embebido (`internal/ingest/data/referrers.json` + `referrers.LICENSE`),
  más la normalización de `source` del mapa de `mv_hits.pipe`. La autoreferencia nunca se
  trata como interna (el AS instancia el parser sin siteUrl).
- **os/browser se recomputan del user-agent** con las regexes literales de `mv_hits`
  (incluye su quirk: iPhone→macos, Android→linux porque "mac"/"linux" van antes en el CASE);
  `device` sí usa detección factual móvil-primero (es lo que el dashboard consume).
