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
| 1. MVP ingesta | ⬜ | Single binary: collector `POST /api/v1/page_hit` (bots, UA, session_id) + `POST /v0/events` (compat TrafficAnalytics) + SQLite + CORS + `/healthz`. |
| 2. Pipes lectura | ⬜ | 13 pipes v1 + JWT HS256 scoped + `filtered_sessions` en SQLite + agregados y job de refresco. |
| 3. Integración Ghost | ⬜ | Prueba end-to-end contra Ghost 6.x real (tracker + dashboard Admin). Ojo: SSRF guard de producción con IPs privadas. |
| 4. Robustez | ⬜ | Retención configurable, rate limiting, backups SQLite, métricas, tests >70%. |
| 5. Comunidad | ⬜ | README EN/ES, instalador one-liner, CI, licencia (a decidir), anuncio. |

## Decisiones registradas

- **Single binary con collector integrado** (opción B): el usuario de GhostBird despliega un
  solo servicio; TrafficAnalytics (Node) deja de ser necesario. La API `/v0/events` se
  mantiene por compatibilidad para quien prefiera el collector oficial.
- **Dedup por `event_id`** (UNIQUE + INSERT OR IGNORE) por semántica at-least-once.
- **SQLite single-writer** con las pragmas del stack (WAL, busy_timeout, BEGIN IMMEDIATE).
- Sin Docker en el flujo de desarrollo del autor (pruebas en LXC si hace falta).
