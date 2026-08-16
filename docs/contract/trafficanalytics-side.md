# TrafficAnalytics (TryGhost) — Lado emisor: contrato completo con Tinybird

> **Propósito**: documento autosuficiente para implementar un drop-in replacement de Tinybird en Go
> que reciba los eventos que este servicio envía. Todo lo citado está verificado contra el código
> clonado en `/tmp/opencode/trafficanalytics-src` (versión `1.0.351`, `package.json:3`). Formato de
> cita: `ruta:línea`. "Hecho" = leído en el código; "Hipótesis" = deducción sin contrastar, con su prueba.

---

## 1. Arquitectura general

### 1.1 Qué es este servicio

TrafficAnalytics es el **proxy de analítica web de Ghost** (MIT, repo público `TryGhost/TrafficAnalytics`).
Se sitúa entre el navegador del visitante y Tinybird:

```
Navegador (ghost-stats.js) ──POST──> Analytics Service ──POST──> Tinybird /v0/events (ClickHouse)
                                         (valida, filtra bots,
                                          enriquece, batcha)
```

- Hecho: `README.md:3-14` ("A web analytics proxy for Ghost that processes and enriches traffic data
  before forwarding it to Tinybird's analytics API") y diagrama de secuencia `README.md:16-41`
  (Tinybird responde `202 Accepted` y el AS lo propaga al navegador).
- Papel frente a **Ghost**: Ghost sirve una página con el script `ghost-stats.js`; ese script hace
  `POST /api/v1/page_hit` contra el AS (`README.md:8-10`). En local, el `caddy` de Ghost enruta
  `/.ghost/analytics/**` hacia el AS (`README.md:75`). El AS **no consulta** la API de Tinybird: solo ingiere.
- Papel frente a **Tinybird**: es un **cliente de ingesta** puro (`POST /v0/events`). No usa endpoints
  de query/API de Tinybird (hecho: grep de `tinybird` en `src/` → solo `client.ts`, `worker-plugin.ts`,
  `BatchWorker.ts`; ninguno hace GET de queries).

### 1.2 ¿Self-hostable?

Sí (hecho): repo público MIT (`package.json:6`), desarrollo docker-first (`README.md:62-69`). Matices:

- **Modo proxy (síncrono)**: 100 % self-hostable sin GCP — solo Fastify + `PROXY_TARGET`.
- **Modo batch (default en dev y prod)**: depende de **Google Cloud Pub/Sub** (transporte) y
  **Firestore** (salt store de firmas) → para self-host completo habría que usar modo proxy o los
  emuladores (hecho: `docs/architecture.md:5-15`, `compose.yml:20-27`).

### 1.3 Stack y framework

- **Fastify 5.11.3** (`package.json:84`), Node `^22.18.0` (`package.json:43`), TypeScript 5.9.3, ESM.
- **Zod 4.4.3 → ajv 8.20.0**: los schemas se escriben en Zod pero se compilan UNA vez a JSON Schema
  (draft-7) y se validan con ajv (hecho: `src/schemas/validation.ts:7-28`). Dos instancias ajv:
  - `requestAjv` (ruta HTTP): `coerceTypes:'array'`, `useDefaults:true`, `removeAdditional:true`
    (`src/schemas/validation.ts:22-28`). **Ojo**: con coerción ajv reescribe `null` → `""` en strings
    nullable del body (comentario literal del repo: "it rewrites a null utm_source to an empty string
    to satisfy the string branch of a union", `src/schemas/validation.ts:30-32`). Por eso al upstream
    pueden llegar `""` donde el schema dice nullable — el lado Go debe tolerar ambos.
  - `dataAjv` (worker, mensajes Pub/Sub): sin coerción (`src/schemas/validation.ts:33-39`).
- **Workers**: no son "web workers" ni colas propias; es **una segunda app Fastify** (rol worker)
  consumiendo Pub/Sub. La MISMA imagen corre en dos roles según `WORKER_MODE` (hecho: `server.ts:5-12`):
  - **Ingest app** (`WORKER_MODE` unset → `src/app.ts`): servidor HTTP que recibe `POST /api/v1/page_hit`.
  - **Worker app** (`WORKER_MODE=true` → `src/worker-app.ts`): consumidor Pub/Sub que enriquece y envía
    a Tinybird; solo expone health (`/`, `/health` → `{"status":"worker-healthy"}`, `src/worker-app.ts:28-34`).
- Dependencias clave (`package.json:62-90`): `@google-cloud/pubsub 6.0.1`, `@google-cloud/firestore 9.0.0`,
  `@fastify/reply-from 12.6.4` (proxy síncrono), `@fastify/cors 11.3.0`, `ua-parser-js 1.0.41`,
  `@tryghost/referrer-parser 0.1.21`, `pino 10.3.1`, OpenTelemetry.

### 1.4 Dos estrategias de manejo del hit (mismo endpoint)

Elegidas en runtime por el handler (hecho: `src/handlers/page-hit-handlers.ts:95-99`):

| Modo | Disparador | Flujo | Perfil compose | Puertos |
|---|---|---|---|---|
| **Batch** (default dev/prod) | `PUBSUB_TOPIC_PAGE_HITS_RAW` set | valida → filtra bots → publica raw en Pub/Sub → `202` inmediato; el worker enriquece → batcha → POST NDJSON a Tinybird | `batch` (`compose.yml:17,54`) | `analytics-service` 3000, `worker` 3001 (`compose.yml:13,50`) |
| **Proxy** (síncrono) | topic unset | valida → filtra bots → enriquece inline → `reply.from(PROXY_TARGET)` con body enriquecido; devuelve la respuesta del upstream al navegador | `proxy` (`compose.yml:97`) | `analytics-service-proxy` 4000→3000 (`compose.yml:93`) |

Evidencia de modo: `docs/architecture.md:12-15`, `AGENTS.md:33-35`. Servicios de soporte en compose:
`firestore` (emulador :8080, `compose.yml:121-137`), `pubsub` (emulador :8085, `compose.yml:139-157`),
`fake-tinybird` (**WireMock 3.13.2**, :8089, `compose.yml:158-169`), `jaeger` (:16686/:4317/:4318,
`compose.yml:196-204`).

---

## 2. Endpoint de entrada (lo que recibe del navegador)

### 2.1 Ruta exacta

- **`POST /api/v1/page_hit`** (hecho: `src/app.ts:44` registra rutas v1 con prefijo `/api/v1`;
  `src/routes/v1/index.ts:4` añade prefijo `/page_hit`; `src/routes/v1/page_hit.ts:7` registra `POST /`).
- `bodyLimit` = **20 KB** (`src/handlers/page-hit-handlers.ts:7,120`).
- En producción real, la URL pública es `https://<ghost-site>/.ghost/analytics/api/v1/page_hit`
  (caddy de Ghost hace proxy; `README.md:75,80`).
- Otros endpoints: `GET /` → `'Hello Ghost Traffic Analytics'` (`src/app.ts:45-47`, healthcheck de
  compose `compose.yml:32`), `GET /info` → `{build: BUILD_LABEL}` (`src/app.ts:49-51`),
  `POST /local-proxy*` → stub de desarrollo (`src/plugins/proxy.ts:6-8`).

### 2.2 Autenticación de entrada

- **NO hay API key obligatoria.** El `token` de la query NO se valida en el AS: solo se propaga
  (o se sustituye) hacia Tinybird en modo proxy (hecho: `src/handlers/page-hit-handlers.ts:46-63`).
- **HMAC opcional**: si `HMAC_SECRET` está seteado, un `preValidation` GLOBAL valida la firma que
  llega en la query (`src/plugins/hmac-validation.ts:5-16`):
  - Params: `hmac` (firma) y `t` (unix seconds) en la query. Se extraen y se reconstruye la URL
    "limpia" sin `hmac` ni `t` (`src/services/hmac-validation/hmac.ts:27-55`).
  - Firma esperada: **HMAC-SHA1** del `validationUrl` (path + query **sin** `hmac` pero **con** `t`),
    digest **base64url + '='** final (`hmac.ts:40-42,61-66`). Comparación timing-safe (`hmac.ts:71-85`).
  - Ventana temporal del `t`: entre `now − 5 min` y `now + 5 s` (`hmac.ts:98-100`).
  - Fallo → `401 {"error":"Unauthorized","message":...}` (`src/plugins/hmac-validation.ts:49-52`).
  - Exentos: GET/HEAD/OPTIONS/TRACE y `/local-proxy*` (`hmac-validation.ts:19`).
  - `HMAC_VALIDATION_LOG_ONLY=true` → solo loguea y deja pasar (`hmac-validation.ts:13,42-46`).
  - Si `HMAC_SECRET` no está seteado, el plugin se desactiva con warning (`hmac-validation.ts:7-10`).

### 2.3 Query params esperados (schema `PageHitRequestQueryParamsSchema`)

`src/schemas/v1/page-hit-request.ts:40-43`:

| Param | Tipo | Obligatorio | Notas |
|---|---|---|---|
| `token` | string no vacío | NO | Token de Tinybird del sitio; en modo proxy se reenvía si no hay `TINYBIRD_TRACKER_TOKEN` |
| `name` | enum `analytics_events` \| `analytics_events_test` | **SÍ** | Nombre del datasource Tinybird |
| (otros) | string | — | `catchall(StringSchema)`: se toleran params extra |

### 2.4 Headers esperados (`PageHitRequestHeadersSchema`, `page-hit-request.ts:48-54`)

| Header | Tipo | Obligatorio |
|---|---|---|
| `x-site-uuid` | GUID-shaped (UUID sin validar versión/variante RFC; `z.guid()`, `page-hit-request.ts:10`) | **SÍ** |
| `content-type` | literal `application/json` | **SÍ** |
| `user-agent` | string no vacío | **SÍ** |
| `x-ghost-analytics-start` | string (ms epoch) | NO → se convierte en `meta.received_timestamp` |
| `referer` | string | NO (se declara pero NO se usa en el pipeline: no aparece en `page-hit-transformations.ts`) |

### 2.5 Body JSON EXACTO (`PageHitRequestBodySchema`, `page-hit-request.ts:90-96` + payload `67-87`)

```jsonc
{
  "timestamp": "2025-04-14T22:16:06.095Z",   // ISO8601 con ms OBLIGATORIOS (z.iso.datetime({precision: 3}), línea 13)
  "action": "page_hit",                       // literal
  "version": "1",                             // literal
  "session_id": "9017be4c-...",               // string, OPCIONAL — se IGNORA: el AS genera la suya (§3.5)
  "payload": {
    "event_id": "<cualquier cosa>",           // opcional; cualquier no-string-no-vacío → UUID nuevo (líneas 23-37)
    "user-agent": "Mozilla/5.0 ...",          // NO vacío, obligatorio
    "locale": "en-US",                        // NO vacío, obligatorio
    "location": "US",                         // string NO vacío o null. VIENE DEL CLIENTE (NO hay geo server-side, §3.6)
    "referrer": null,                         // string o null, opcional
    "parsedReferrer": {                       // opcional {source, medium, url} cada uno string|null
      "source": null, "medium": null, "url": null
    },
    "pathname": "/test-page",                 // NO vacío, obligatorio
    "href": "https://example.com/test-page",  // string (puede ser vacío), obligatorio
    "site_uuid": "940b73e9-4952-4752-b23d-9486f999c47e", // GUID-shaped, obligatorio
    "post_uuid": "undefined",                 // GUID-shaped | literal "undefined"
    "post_type": "null",                      // enum "null" | "post" | "page"  (¡"null" es STRING!)
    "gift_link": null,                        // string o null, opcional
    "member_uuid": "undefined",               // GUID-shaped | literal "undefined"
    "member_status": "free",                  // string no vacío | literal "undefined"
    "utm_source": null, "utm_medium": null, "utm_campaign": null,
    "utm_term": null, "utm_content": null     // string|null, opcionales
  }
}
```

- OJO 1: `post_uuid`/`member_uuid`/`member_status` pueden llegar con el **string literal `"undefined"`**
  (así los manda ghost-stats.js). El AS los reenvía tal cual a Tinybird.
- OJO 2: por la coerción ajv del request path, un `null` en un campo string nullable del **body** puede
  llegar al upstream reescrito como `""` (hecho: comentario `src/schemas/validation.ts:30-32` y test e2e
  que espera `parsedReferrer {source:'', medium:'', url:''}` tras enviar nulls,
  `test/e2e/web_analytics.test.ts:27-31` vs `133-138`).
- Defaults aplicados por `preHandler` si faltan (`PageHitRequestPayloadDefaults`,
  `page-hit-request.ts:135-158`; se aplican con spread `:124-127`): `referrer:null`,
  `parsedReferrer:{source:null,medium:null,url:null}`, `post_uuid:'undefined'`, `post_type:'null'`,
  `member_uuid:'undefined'`, `member_status:'undefined'`, utm_* `null`, `gift_link:null`, etc.

### 2.6 Respuestas del endpoint de entrada

| Caso | Status | Body / Headers | Evidencia |
|---|---|---|---|
| Hit aceptado (batch publicado o proxyeado) | `202` | `{"message":"Page hit event received"}` | `src/utils/page-hit-response.ts:1-3`; usado en `page-hit-handlers.ts:13` y bot plugin |
| Bot filtrado | `202` | mismo body; header `x-ghost-bot-detected: true` SOLO si `ENABLE_BOT_DETECTION_HEADER=true` | `src/plugins/bot-detection.ts:23-27` |
| Validación schema (ajv/Fastify) | `400` | error estándar Fastify | (errorHandler envuelto, `src/app.ts:29`) |
| HMAC inválido (si activo) | `401` | `{"error":"Unauthorized","message":...}` | `src/plugins/hmac-validation.ts:49-52` |
| Fallo publicando en Pub/Sub | `500` | `{"error":"Failed to process page hit event"}` | `page-hit-handlers.ts:30` |
| Fallo del upstream en modo proxy | `502` | `{"error":"Proxy error"}` | `page-hit-handlers.ts:83` |
| Modo proxy OK | el status/body del upstream Tinybird (p.ej. `202 {"success":true}`) | — | `test/e2e/web_analytics.test.ts:152-155` |

---

## 3. Procesamiento por hit (antes de Tinybird)

Pipeline exacto con orden de hooks (hecho, `src/app.ts:26-44` + `src/routes/v1/page_hit.ts:6-8`):

```
onRequest:      timestamp plugin → request.serverReceivedAt = new Date()     (src/plugins/timestamp.ts:12-14)
preValidation:  hmac-validation (global, opcional)                           (src/plugins/hmac-validation.ts:16)
validación:     Fastify/ajv contra PageHitRequestSchema (body+query+headers)
preHandler:     bot-detection (bots → 202 corto, no se publican)             (src/plugins/bot-detection.ts:7-28)
preHandler:     populateAndTransformPageHitRequest (defaults + event_id)     (page-hit-request.ts:123-133)
handler:        batch (publicar raw) | proxy (enriquecer + reply.from)       (page-hit-handlers.ts:88-99)
```

### 3.1 Construcción del evento RAW (`pageHitRawPayloadFromRequest`)

`src/transformations/page-hit-transformations.ts:3-50`:

- `timestamp` = **`serverReceivedAt`** (hora del servidor). El `timestamp` del body del cliente **se
  descarta** (hecho: línea 17; verificado por e2e: espera que NO sea el del cliente,
  `test/e2e/web_analytics.test.ts:117-118`).
- `site_uuid` (nivel raíz del raw) = header `x-site-uuid` (línea 20) — NO el del body.
- `payload.event_id` = `resolveEventId(body.payload.event_id)`: si es string no vacío se respeta
  TAL CUAL (no tiene que ser UUID válido); si no → `crypto.randomUUID()` nuevo
  (`page-hit-request.ts:31-37`, re-aplicado en `:24`).
- `payload.meta.received_timestamp` = `new Date(parseInt(x-ghost-analytics-start)).toISOString()`
  o `null` si falta/no parsea (líneas 4-14, 42).
- `meta.ip` = `request.ip` — con `trustProxy` activo (default `true`, `src/app.ts:18`) resuelve la
  **primera IP de `X-Forwarded-For`** (verificado: `test/integration/app.test.ts:566-633`).
- `meta['user-agent']` = header `user-agent` (línea 47).
- El RAW completo (`PageHitRawSchema`, `src/schemas/v1/page-hit-raw.ts:54-61`) es lo que se serializa
  a JSON y se publica en Pub/Sub (`src/services/events/publisher.ts:30-35`).

### 3.2 Filtrado de bots

- Lista NO: **una única regex hardcoded**, case-insensitive (`src/utils/bot-detection.ts:1`):

  ```js
  /wget|ahrefsbot|curl|bot|crawler|spider|urllib|bitdiscovery|\+https:\/\/|googlebot/i
  ```

- Se aplica sobre el header `user-agent`. Ojo: `bot` y `curl` son substrings — matchea cualquier UA
  que contenga "bot" ("Bot", "BotDetect"...).
- Ingest: `preHandler` devuelve `202` con el body estándar y NO publica nada
  (`src/plugins/bot-detection.ts:7-28`).
- Worker: **re-chequeo defensivo** — si tras el enriquecimiento `payload.device === 'bot'`, hace
  `ack` y descarta (`src/services/batch-worker/BatchWorker.ts:75-85`).

### 3.3 Parsing de user-agent

- Librería: **`ua-parser-js` 1.0.41** (API de clase: `new uap(ua)` → `.getOS()`, `.getBrowser()`)
  — `src/schemas/v1/page-hit-processed.ts:4,73-75`, `package.json:88`.
- Normalizaciones (`page-hit-processed.ts:63-111`):
  - `browser`: nombre en **minúsculas**; se quita prefijo `"mobile "` ("Mobile Safari" → `"safari"`); vacío → `"unknown"`.
  - `os`: minúsculas; `"mac os"` → `"macos"`; vacío → `"unknown"`.
  - `device`: derivado, NO de ua-parser: `"bot"` (si `isBot`), `"mobile-ios"` (os=ios),
    `"mobile-android"` (os=android), `"desktop"` (os ∈ {macos, windows, linux, chrome os, chromium os,
    ubuntu}), resto `"unknown"` (líneas 87-97).
  - Cualquier excepción → `{os:'unknown', browser:'unknown', device:'unknown'}` (líneas 104-110).
  - UA vacío → todo `"unknown"` (líneas 65-71).

### 3.4 Referrer parsing

- Librería: **`@tryghost/referrer-parser` 0.1.21** (`package.json:80`), instancia única
  `new ReferrerParser()` (`page-hit-processed.ts:9`).
- Solo si `parsedReferrer.url` es truthy: `referrerParser.parse(url, source?, medium?)` →
  `{referrerUrl, referrerSource, referrerMedium}` con `|| null` (líneas 113-132). Si no hay url o
  falla → devuelve `{}` → los tres campos **ausentes** del evento (no null).
- El `parsedReferrer` ORIGINAL del cliente se conserva dentro del payload "for auditing purposes"
  (línea 171) — llega a Tinybird tal cual (con nulls o con `""` si ajv los coercicionó).

### 3.5 `session_id` / user_signature (¡es el mismo campo!)

- El evento enviado a Tinybird **NO tiene campo `user_signature`**: la firma va en `session_id`
  (raíz del evento) — `src/schemas/v1/page-hit-processed.ts:22,147-158`.
- La `session_id` que envía el navegador en el body **se ignora por completo** (no se copia en el raw
  ni en el processed; hecho: `page-hit-transformations.ts` no la referencia; la processed se genera en
  `page-hit-processed.ts:147-151`).
- Fórmula EXACTA (`src/services/user-signature/UserSignatureService.ts:127-132`):

  ```
  signature = sha256_hex( `${salt}:${site_uuid}:${ip}:${user_agent}` )
  ```

  - Concatenación con `:` de: salt (64 hex), site_uuid, IP (cliente, XFF-aware), user-agent completo.
  - Digest: SHA-256, hex minúsculas, 64 chars (verificado: `test/integration/app.test.ts:563`
    espera `/^[a-f0-9]{64}$/`).
- **Salt**: `crypto.randomBytes(32).toString('hex')` (líneas 87-89), clave
  `salt:<YYYY-MM-DD>:<site_uuid>` con fecha UTC (`new Date().toISOString().split('T')[0]`, líneas 77-80),
  creación atómica vía `getOrCreate` del salt store (líneas 103-107; interfaz `ISaltStore.getOrCreate`
  `src/services/salt-store/ISaltStore.ts:48-55`). **Rotación diaria POR SITIO, pasiva** (nuevo día →
  nueva clave → nuevo salt).
- Salt stores (`SALT_STORE_TYPE`, factoría `src/services/salt-store/SaltStoreFactory.ts:16-43`):
  `memory` (default del código), `file` (`SALT_STORE_FILE_PATH`, default `./data/salts.json`),
  `firestore` (requiere `GOOGLE_CLOUD_PROJECT` + `FIRESTORE_DATABASE_ID`; es el default de compose
  dev/test y el de producción — `compose.yml:23`, `docs/architecture.md:68-74`).
- Cleanup de salts expirados: scheduler diario con delay aleatorio 0-60 min
  (`UserSignatureService.ts:29-59`); configurable `ENABLE_SALT_CLEANUP_SCHEDULER` (default on) y
  `FIRESTORE_CLEANUP_BATCH_SIZE` (default 500).
- **Para el reemplazo Go esto es irrelevante** (la firma ya llega calculada), pero documenta la
  semántica: mismo usuario (misma IP+UA) mismo día = mismo `session_id`; cambia al día siguiente.

### 3.6 Geolocalización

- **NO hay geolocalización server-side** (hecho: grep `maxmind|geoip|GeoLite|geolocation` en `src/` →
  0 resultados). El campo `location` (p.ej. `"US"`) lo envía el navegador en el payload y el AS lo
  reenvía sin tocar (`page-hit-transformations.ts:31`). Hipótesis (no verificada en este repo): lo
  calcula `ghost-stats.js` en el navegador (timezone/locale); confirmarlo requiriría leer el repo de Ghost.

### 3.7 Deduplicación / reintentos en el AS

- **No hay ninguno** (hecho: grep `retry|retries|backoff|dedup|idempoten` en `src/` → 0 resultados).
- El `event_id` se limita a viajar en el payload (trazabilidad). La semántica de entrega es
  **at-least-once**: ver §4.4.

---

## 4. Envío a Tinybird — contrato EXACTO

### 4.1 URL, método, headers (`src/services/tinybird/client.ts`)

- Instanciación en el worker (`src/plugins/worker-plugin.ts:24-29`):

  ```ts
  new TinybirdClient({
      apiUrl: process.env.PROXY_TARGET,          // p.ej. http://fake-tinybird:8080/v0/events
      apiToken: process.env.TINYBIRD_TRACKER_TOKEN,
      datasource: 'analytics_events',            // SIEMPRE fijo en batch mode
      wait: process.env.TINYBIRD_WAIT === 'true'
  })
  ```

- El constructor **normaliza** la base: quita el sufijo `/v0/events` de `apiUrl` si lo lleva
  (`client.ts:25`, regex `/\/v0\/events$/`). O sea: `PROXY_TARGET` puede apuntar con o sin el path.
- **Endpoint final** (`client.ts:32-38`):

  ```
  POST {base}/v0/events?name=analytics_events[&wait=true]
  ```

  - `name` = datasource, **URL-encoded** con `encodeURIComponent` (test con espacios/símbolos:
    `test/unit/services/tinybird/client.test.ts:31-39`).
  - En **batch mode** el datasource es SIEMPRE `analytics_events` (`worker-plugin.ts:27`); el `name`
    de la query del navegador (`analytics_events_test` incluido) se ignora.
  - En **proxy mode** el `name` de la query del navegador SÍ viaja al upstream (el handler reescribe
    la query conservándolo: `page-hit-handlers.ts:43-54`).
- **Headers** (idénticos en evento simple y batch, `client.ts:43-46,66-69`):

  ```
  Authorization: Bearer {TINYBIRD_TRACKER_TOKEN}
  Content-Type: application/json
  ```

- **Body**:
  - Batch: **NDJSON** — `events.map(e => JSON.stringify(e)).join('\n')` (`client.ts:62`). Una línea
    JSON por evento, separadas por `\n`, SIN newline final (join, no append).
  - Evento simple (`postEvent`, no usado en el flujo actual): un único objeto JSON (`client.ts:47`).
- **Manejo de respuesta**: solo se comprueba `response.ok` (cualquier 2XX vale). En error lanza
  `Error('Tinybird API error: <status> <statusText> - <body>')` (`client.ts:50-53`) o
  `'Tinybird batch API error: ...'` (`:73-76`). El body de respuesta NO se parsea ni se usa.
  No hay timeout explícito ni retry HTTP (fetch/undici por defecto).

### 4.2 El evento procesado, campo a campo (`PageHitProcessed`)

Schema: `src/schemas/v1/page-hit-processed.ts:17-56`; construcción: `:153-185`. Esto es LO QUE LLEGA
a `/v0/events` (una línea NDJSON = un objeto así):

```jsonc
{
  "timestamp": "2026-08-16T16:06:06.095Z",     // ISO8601 con ms (serverReceivedAt del INGEST, no del cliente)
  "action": "page_hit",                          // literal
  "version": "1",                                // literal
  "site_uuid": "940b73e9-4952-4752-b23d-9486f999c47e",  // GUID-shaped (del header x-site-uuid)
  "session_id": "a1b2c3...64 hex",               // SHA-256 hex de 64 (§3.5) — ES la "user_signature"
  "payload": {
    "event_id": "0196...-...",                   // UUID (o string del cliente si venía no vacío)
    "site_uuid": "940b73e9-...",                 // DUPLICADO del raíz
    "member_uuid": "undefined",                  // GUID-shaped | "undefined" (string)
    "member_status": "free",                     // string no vacío | "undefined"
    "post_uuid": "undefined",                    // GUID-shaped | "undefined"
    "post_type": "null",                         // "null" | "post" | "page"  (string)
    "gift_link": null,                           // string | null (OPCIONAL: puede no existir la key)
    "locale": "en-US",                           // string no vacío
    "location": "US",                            // string no vacío | null
    "pathname": "/test-page",                    // string no vacío
    "href": "https://example.com/test-page",     // string (puede ser "")
    "os": "macos",                               // §3.3
    "browser": "chrome",                         // §3.3 (minúsculas, sin "mobile ")
    "device": "desktop",                         // bot|mobile-ios|mobile-android|desktop|unknown
    "parsedReferrer": {                          // OPCIONAL: copia del cliente (auditoría); valores string|null (o "" por coerción ajv)
      "url": null, "source": null, "medium": null
    },
    "referrerUrl": null,                         // OPCIONAL: solo presente si parsedReferrer.url era truthy (§3.4)
    "referrerSource": null,                      // OPCIONAL, ídem
    "referrerMedium": null,                      // OPCIONAL, ídem
    "utm_source": null, "utm_medium": null, "utm_campaign": null,
    "utm_term": null, "utm_content": null,       // OPCIONALES, string|null
    "user-agent": "Mozilla/5.0 (Macintosh; ...", // OJO: key CON guión y entre comillas en JSON
    "meta": {
      "received_timestamp": "2026-08-16T16:06:06.090Z"  // ISO con ms | null (de x-ghost-analytics-start)
    }
  }
}
```

Aclaraciones críticas para el lado Go:

1. **No existen** los campos `user_signature` ni `ip` en el evento — la firma es `session_id` y la IP
   nunca sale del AS.
2. `referrerUrl/referrerSource/referrerMedium` y `parsedReferrer` son **opcionales** (pueden ausentar
   la key según el cliente y el resultado del parser).
3. Strings "nullable" pueden llegar como `null`, como `""` (coerción ajv §2.5) o ausentes. Ser tolerante.
4. `member_uuid`/`post_uuid`/`member_status` pueden ser el string `"undefined"`; `post_type` puede ser
   el string `"null"`. NO tratarlos como UUID/enum estrictos al ingerir.
5. `event_id`: en schema es GUID (`page-hit-processed.ts:24`) pero el código deja pasar strings del
   cliente no-UUID (`page-hit-request.ts:28-37` "IDs that are not valid UUIDs still round-trip").

### 4.3 Batching (`src/services/batch-worker/BatchWorker.ts`)

- **Tamaño**: `BATCH_SIZE` default **50** (línea 36). Flush al llegar al tamaño cuando se añade un
  mensaje (líneas 102-104).
- **Intervalo**: `BATCH_FLUSH_INTERVAL_MS` default **1000 ms**, timer que se reprograma (líneas 37,
  177-203).
- **Flush** = un único `postEventBatch` con todo el batch en NDJSON (líneas 138-148).
- **Ack/nack de Pub/Sub**:
  - Flush OK → `ack` de TODOS los mensajes del batch (líneas 150-153).
  - Flush falla → `nack` de TODOS + throw (líneas 160-174) → Pub/Sub **redelivra** → el evento se
    re-procesa y re-envía.
  - Mensaje que no parsea/valida contra el schema raw → `ack` y descarte silencioso (líneas 121-131,
    "If we failed to parse it, we won't succeed next time").
  - Error enriqueciendo → `nack` (líneas 105-113).
- **Shutdown**: `stop()` cancela timer y hace flush final de lo pendiente (líneas 50-64).
- **Duplicados / idempotencia**: NO hay. Semántica resultante **at-least-once**: si Tinybird acepta el
  batch pero la respuesta se pierde, o si hay redelivery tras nack, el mismo `event_id` puede llegar
  DOS O MÁS veces. El dedup (si se quiere) es responsabilidad del lado Tinybird/reemplazo — p.ej.
  keyed table / dedup por (`event_id`, `site_uuid`). Hipótesis de diseño: por eso `event_id` existe.

### 4.4 Modo proxy (síncrono) — diferencias

`src/handlers/page-hit-handlers.ts:34-86`:

- El body enriquecido (`PageHitProcessed`) se pone en `request.body` y se proxyea con
  `reply.from(PROXY_TARGET)` (`@fastify/reply-from`).
- Query reescrita (`:43-54`): conserva los params del request original MENOS `token` (se elimina si
  `TINYBIRD_TRACKER_TOKEN` está en env); añade `wait=true` si `TINYBIRD_WAIT=true`.
- Headers reescritos (`:55-64`): si `TINYBIRD_TRACKER_TOKEN` está en env → se añade/inyecta
  `authorization: Bearer <token>`.
- Si NO hay token en env: el `?token=` del navegador viaja tal cual y NO se envía Authorization
  (verificado: `test/integration/app.test.ts:686-707`).
- Error del upstream → `502 {"error":"Proxy error"}` (`:65-84`). Respuesta OK → el status/body del
  upstream se devuelve al navegador tal cual (e2e espera `{"success":true}` del stub,
  `test/e2e/web_analytics.test.ts:152-155`).

---

## 5. Config: variables de entorno

Fuentes: `AGENTS.md:87-125`, `.env.example`, código citado. Defaults entre paréntesis.

### Core / run mode
| Var | Default | Uso | Evidencia |
|---|---|---|---|
| `PORT` | `3000` | puerto HTTP | `server.ts:3` |
| `LISTEN_HOST` | `0.0.0.0` | bind | `server.ts:4` |
| `WORKER_MODE` | unset | `'true'` → arranca worker app en vez de ingest | `server.ts:5` |
| `PROXY_TARGET` | `http://localhost:3000/local-proxy` | URL base de Tinybird (con o sin `/v0/events`); en proxy mode es el destino del `reply.from` | `page-hit-handlers.ts:41`; normalización `client.ts:25`; `.env.example:5-7` |
| `TINYBIRD_TRACKER_TOKEN` | — (compose dev: `test-token`) | Bearer de ingesta | `compose.yml:27`; `worker-plugin.ts:26` |
| `TINYBIRD_WAIT` | `false` | `true` → añade `&wait=true` (Tinybird responde tras persistir) | `worker-plugin.ts:28`; `client.ts:34-36` |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `gcp`\|`json` (auto) | logging pino | `AGENTS.md:96-97` |
| `BUILD_LABEL` | `''` | expuesto en `GET /info` | `src/app.ts:50` |

### Pub/Sub (batch mode)
| Var | Default | Uso |
|---|---|---|
| `PUBSUB_TOPIC_PAGE_HITS_RAW` | — | **Set = modo batch; unset = modo proxy** (`page-hit-handlers.ts:95`) |
| `PUBSUB_SUBSCRIPTION_PAGE_HITS_RAW` | — | subscripción del worker (`worker-plugin.ts:31`) |
| `PUBSUB_EMULATOR_HOST` | — | emulador local (compose: `pubsub:8085`, `compose.yml:24`) |
| `GOOGLE_CLOUD_PROJECT` | — | proyecto Pub/Sub + Firestore (`publisher.ts:16`, `subscriber.ts:23`) |
| `BATCH_SIZE` | `50` | flush por tamaño (`BatchWorker.ts:36`) |
| `BATCH_FLUSH_INTERVAL_MS` | `1000` | flush por timer (`BatchWorker.ts:37`) |

### Salt store
| Var | Default | Uso |
|---|---|---|
| `SALT_STORE_TYPE` | `memory` (código) / `firestore` (compose) | `memory`\|`file`\|`firestore` (`SaltStoreFactory.ts:17`) |
| `SALT_STORE_FILE_PATH` | `./data/salts.json` | store `file` (`SaltStoreFactory.ts:37`) |
| `FIRESTORE_DATABASE_ID` | — | store `firestore` (obligatorio, `SaltStoreFactory.ts:30-32`) |
| `FIRESTORE_EMULATOR_HOST` | — | emulador (compose: `firestore:8080`) |
| `FIRESTORE_SALT_COLLECTION` | `salts` (uso en test, `compose.yml:221`) | colección de salts |
| `ENABLE_SALT_CLEANUP_SCHEDULER` | `true` | cleanup diario de salts (`UserSignatureService.ts:31`) |
| `FIRESTORE_CLEANUP_BATCH_SIZE` | `500` (máx 500) | docs por loop de cleanup (`AGENTS.md:113`) |

### Seguridad / red
| Var | Default | Uso |
|---|---|---|
| `TRUST_PROXY` | `true` | resolver IP de `X-Forwarded-For` (`src/app.ts:18`) |
| `HMAC_SECRET` | — (unset = HMAC desactivado) | §2.2 (`hmac-validation.ts:7`) |
| `HMAC_VALIDATION_LOG_ONLY` | `false` | fallos solo log (`hmac-validation.ts:13`) |
| `ENABLE_BOT_DETECTION_HEADER` | `false` | `x-ghost-bot-detected: true` en 202 de bots (`bot-detection.ts:23`) |

### Observabilidad
`OTEL_TRACE_EXPORTER` (`jaeger`|`gcp`), `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`
(default `http://jaeger:4318/v1/traces`), `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`, `K_SERVICE` (Cloud Run)
— `AGENTS.md:121-125`.

### ¿Endpoints de ingesta y API separados?

**No.** Este servicio solo usa la **ingesta** (`POST /v0/events`). No hay segunda URL de API en todo
el código (hecho: la única construcción de URL es `client.ts:32-38`). Las queries sobre los datos las
hace Ghost contra Tinybird directamente, fuera de este repo. El drop-in Go solo necesita implementar
la ingesta para que ESTE servicio funcione.

### Modo local / mock en compose

- En ESTE repo, el "Tinybird" local es **WireMock** (`fake-tinybird`, imagen
  `wiremock/wiremock:3.13.2`, `compose.yml:158-169`); el stub por defecto responde `202
  {"success":true}` (`test/e2e/web_analytics.test.ts:78-82`). NO es un ClickHouse/Materialize real.
- La imagen **`tinybird-local`** real (Tinybird local sobre ClickHouse) vive en el repo **TryGhost/Ghost**
  (perfil `analytics` junto a un `tb-cli` que despliega el schema y escribe `.env.tinybird` con los
  tokens en un volumen `shared-config`) — `README.md:76-78`.
- Puente entre ambos: `scripts/entrypoint.sh:29-40` — si existe `/mnt/shared-config/.env.tinybird`,
  lo carga (`TINYBIRD_TRACKER_TOKEN`) y fuerza `PROXY_TARGET="http://tinybird-local:7181"`.
  `compose.ghost.yml:6-9` monta ese volumen externo. Puerto de tinybird-local: **7181**
  (también `.env.example:6`: `http://host.docker.internal:7181/v0/events`).

---

## 6. Token de ingesta y su validación

- **Token usado**: `TINYBIRD_TRACKER_TOKEN`. Formato típico Tinybird: `p.` + JWT (`p.eyJxxxxxxxx`,
  `.env.example:2`) — pero **para el emisor es opaco**: ningún código valida su estructura (solo
  `TinybirdClient` exige que sea no-vacío en el constructor, `client.ts:21-23`).
- **Cómo se envía**:
  - Batch mode: SIEMPRE `Authorization: Bearer <token>` (`client.ts:44,67`).
  - Proxy mode: si hay env token → header `Authorization: Bearer` + `token` ELIMINADO de la query
    (verificado: `test/integration/app.test.ts:662-684`: `expect(targetRequest.url).not.toContain('token=')`
    y `authorization === 'Bearer tinybird-secret-token'`). Si NO hay env token → el `?token=` del
    navegador pasa al upstream y NO se manda Authorization (`app.test.ts:686-707`).
- **Qué debe implementar el reemplazo Go** (esto es Tinybird, no este repo, pero es el contrato a cubrir):
  1. Aceptar `POST /v0/events` con `Authorization: Bearer <token>` (formato header EXACTO:
     `Authorization: Bearer X`, case-insensitive el nombre de cabecera).
  2. Aceptar TAMBIÉN el token como query param `?token=` (llega en proxy mode sin env token; Tinybird
     real lo soporta). Recomendación: aceptar ambos; si llegan ambos, preferir el header.
  3. Validar el token contra el/los configurados (p.ej. token de ingesta del datasource
     `analytics_events`). Rechazo → `401` (Tinybird real responde 401 con JSON de error; el emisor
     solo necesita un no-2xx para lanzar el error con status+body en el mensaje).
  4. No hay rotación/refresh ni validación criptográfica que el emisor espere: el token es un string
     opaco comparado server-side.
- `wait=true` (query): Tinybird responde solo tras ingerir. El emisor no depende del body de respuesta,
  solo del status 2XX (`client.ts:50,73`). Cuerpo de respuesta libre (WireMock devuelve texto plano
  `OK` en el stub default de `setupTinybirdStub`, `test/utils/wiremock.ts:136-139`; el e2e usa
  `202 {"success":true}`).

---

## 7. Tests como documentación (payloads y requests literales)

### 7.1 `test/unit/services/tinybird/client.test.ts` — requests EXACTOS al cliente

Config del test (`:9-13`):

```ts
{ apiUrl: 'https://api.tinybird.co', apiToken: 'test-token', datasource: 'test_datasource' }
```

Endpoint esperado (`:28`): `https://api.tinybird.co/v0/events?name=test_datasource`
— con encoding (`:37`): `...?name=test%20datasource%20with%20spaces%20%26%20symbols`
— con wait (`:47`): `...?name=test_datasource&wait=true`.

`postEvent` (`:70-80`) — llamada EXACTA a fetch:

```ts
fetch('https://api.tinybird.co/v0/events?name=test_datasource', {
    method: 'POST',
    headers: { Authorization: 'Bearer test-token', 'Content-Type': 'application/json' },
    body: JSON.stringify(mockEvent)
})
```

`postEventBatch` (`:271-281`) — body EXACTO NDJSON:

```ts
body: mockEvents.map(event => JSON.stringify(event)).join('\n')
// 3 eventos de fixture (:255-259): {timestamp:'2024-01-15T12:00:00Z', type:'page_view', user_id:'user1'}...
```

- Batch de 100 → body con exactamente 100 líneas (`:356-359`).
- Batch vacío → NO llama a fetch (`:284-287`).
- Mensaje de error batch (`:326-328`): `'Tinybird batch API error: 400 Bad Request - Batch validation failed'`.

### 7.2 `test/e2e/web_analytics.test.ts` — flujo completo browser → AS → Tinybird

Fixture de entrada (`:6-40`) — LO QUE MANDA EL NAVEGADOR:

```ts
DEFAULT_QUERY_PARAMS = { token: 'test-token', name: 'analytics_events' };
DEFAULT_HEADERS = {
    'Content-Type': 'application/json',
    'User-Agent': 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36',
    'x-site-uuid': '940b73e9-4952-4752-b23d-9486f999c47e'
};
DEFAULT_BODY = {
    timestamp: '2025-04-14T22:16:06.095Z',
    action: 'page_hit', version: '1',
    session_id: '9017be4c-3065-484b-b117-9719ad1e3977',
    payload: {
        'user-agent': 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36',
        locale: 'en-US', location: 'US', referrer: null,
        parsedReferrer: { source: null, medium: null, url: null },
        pathname: '/test-page', href: 'https://example.com/test-page',
        site_uuid: '940b73e9-4952-4752-b23d-9486f999c47e',
        post_uuid: 'undefined', post_type: 'null',
        member_uuid: 'undefined', member_status: 'free'
    }
};
```

Stub de Tinybird (`:78-82`): `status 202`, body `{"success":true}`, `Content-Type: application/json`.

Lo que el test EXIGE del request que llega a "Tinybird" vía WireMock (batch `:107-146` y proxy
`:158-197`, idénticos): URL con `name=analytics_events` (`:107-109`); body con:

```ts
{
    action: 'page_hit', version: '1',
    session_id: expect.any(String),                       // firma generada
    timestamp: /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/,  // ISO ms del SERVIDOR, ≠ cliente
    payload: {
        site_uuid: '940b73e9-4952-4752-b23d-9486f999c47e',
        pathname: '/test-page', href: 'https://example.com/test-page',
        browser: expect.any(String), os: expect.any(String), device: expect.any(String),
        parsedReferrer: { source: '', medium: '', url: '' },   // ¡nulls del cliente llegan como ''!
        utm_source: null, utm_medium: null, utm_campaign: null, utm_term: null, utm_content: null
    }
}
```

Respuesta al navegador: batch → `202 {"message":"Page hit event received"}` (`:101-104`);
proxy → `202 {"success":true}` (el body del upstream, `:152-155`).

### 7.3 `test/utils/wiremock.ts` — cómo se simula Tinybird

- `setupTinybirdStub()` (`:131-154`): stub catch-all `POST urlPattern: '.*'` → default `200 'OK'`
  `Content-Type: text/plain` (el e2e lo sobrescribe a `202 {"success":true}`).
- `verifyTinybirdRequest({token, name, bodyContains})` (`:157-195`): filtra los requests recibidos por
  query params `token`/`name` y substring del body — confirma que el emisor manda `name` en la QUERY.
- `parseRequestBody` (`:198-213`): `JSON.parse` del body crudo.
- `waitForRequest` (`:216-242`): polling `__admin/requests` cada 100 ms hasta 10 s — así se tolera el
  batch delay del worker.

### 7.4 `test/integration/app.test.ts` — extras útiles

- Fixture `eventPayload` (`:18-36`): igual que DEFAULT_BODY con `pathname:'/'`,
  `href:'https://www.chrisraiffe.com/'` — mismo shape.
- `session_id` enviada al upstream: `/^[a-f0-9]{64}$/` (`:563`).
- IP: `X-Forwarded-For: '203.0.113.42, 192.168.1.1'` → la firma se calcula con `203.0.113.42`
  (primera IP) (`:566-587`); funciona con 1, 2 o 3 hops (`:589-633`); sin XFF usa la IP de conexión (`:635-658`).
- Token handling en proxy mode: §6 arriba (`:661-708`).

---

## 8. Checklist de implementación del lado ingesta (Go)

1. **Endpoint**: `POST /v0/events` (y aceptar path base configurable). Query: `name` (datasource;
   URL-encoded; tratar cualquier string), `token` (opcional), `wait` (opcional, `true`).
2. **Auth**: `Authorization: Bearer <t>` y/o `?token=`; 401 en token inválido; el emisor no valida formato.
3. **Body**: `Content-Type: application/json` con UN objeto JSON **o** NDJSON multilínea (hasta 50
   líneas por request en config default; recomendar límite ≥ 4 MB y parseo por líneas con streaming).
4. **Tolerancia de tipos** (crítico): strings que pueden ser `null`, `""` o ausentes; `member_uuid`/
   `post_uuid` = string `"undefined"`; `post_type` = string `"null"`; `event_id` normalmente UUID pero
   puede ser string arbitrario; key `user-agent` con guión; `parsedReferrer` con claves `url/source/medium`.
5. **Respuesta**: 2XX (`200`/`202`/`204`); body libre (JSON `{"success":true}` es lo que usa el mock);
   respetar `wait=true` = responder tras persistir. El emisor SOLO mira `response.ok`.
6. **Errores**: cualquier no-2XX → el emisor lanza error y hace nack/redelivery ⇒ llegarán reintentos
   de batches completos. Diseñar para **at-least-once**: dedup downstream por `event_id` si se quiere
   exactitud (ReplacingMergeTree/kdedup o tabla auxiliar).
7. **No se necesita**: endpoints de query, CORS del emisor (el AS ya gestiona el de entrada),
   cookies/sesiones, chunked especial. Solo un endpoint de ingesta HTTP.

---

## 9. Fuentes (ficheros leídos íntegros)

`README.md`, `AGENTS.md`, `docs/architecture.md`, `compose.yml`, `compose.ghost.yml`,
`compose.override.yml`, `.env.example`, `package.json`, `server.ts`, `scripts/entrypoint.sh`,
`src/app.ts`, `src/worker-app.ts`, `src/routes/v1/index.ts`, `src/routes/v1/page_hit.ts`,
`src/handlers/page-hit-handlers.ts`, `src/plugins/{bot-detection,hmac-validation,timestamp,proxy,worker-plugin}.ts`,
`src/services/hmac-validation/hmac.ts`, `src/services/tinybird/client.ts`,
`src/services/batch-worker/BatchWorker.ts`, `src/services/events/{publisher,publisherUtils,subscriber}.ts`,
`src/services/user-signature/UserSignatureService.ts`, `src/services/salt-store/{ISaltStore,SaltStoreFactory}.ts`,
`src/schemas/validation.ts`, `src/schemas/index.ts`, `src/schemas/v1/{page-hit-request,page-hit-raw,page-hit-processed}.ts`,
`src/transformations/page-hit-transformations.ts`, `src/utils/{bot-detection,page-hit-response,query-params}.ts`,
`test/unit/services/tinybird/client.test.ts`, `test/e2e/web_analytics.test.ts`,
`test/utils/wiremock.ts`, `test/integration/app.test.ts` (líneas 1-180 y 560-710).
Greps de ausencia (0 resultados en `src/`): `maxmind|geoip|GeoLite|geolocation`,
`retry|retries|backoff|dedup|idempoten`.
