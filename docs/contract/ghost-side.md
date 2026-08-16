# Contrato Ghost ↔ Tinybird — Documentación del lado Ghost (Fase 0)

> **Propósito**: documento autosuficiente para implementar un *drop-in replacement* de Tinybird (y su cadena de ingesta) en Go, sin volver a leer el código de Ghost.
>
> **Fuente analizada**: monorepo `TryGhost/Ghost` clonado en `/tmp/opencode/ghost-src`, versión **Ghost 6.58.0-rc.0** (`ghost/core/package.json`), commit `95117e1` (2026-08-16). Todas las rutas son relativas a `/tmp/opencode/ghost-src/` salvo indicación. Los números de línea corresponden a ese checkout.

---

## 0. Arquitectura general: quién habla con quién

El "Tinybird" de Ghost 6.x no es un único servicio: son **cuatro flujos** con tres actores externos a Ghost:

```
┌──────────────┐  1. INGESTA (navegador del visitante)
│ Visitante    │  POST {tracker.endpoint}?name=analytics_events[&token=...]
│ (ghost-stats │──────►  Traffic Analytics Service (collector, imagen privada
│  .min.js)    │         ghost/traffic-analytics)  ──►  Tinybird Events API
└──────────────┘         "https://e.ghost.org/tb/web_analytics" (Pro)
                                POST /v0/events  (datasource `analytics_events`)

┌──────────────┐  2. CONSULTA SERVER-SIDE (Node, Ghost core)
│ Ghost server │  GET {stats.endpoint}/v0/pipes/{pipe}.json   Authorization: Bearer <JWT>
│ (stats svc)  │──────►  Tinybird (api.tinybird.co | tinybird-local :7181)
└──────────────┘         Pipes: api_top_pages, api_post_visitor_counts

┌──────────────┐  3. TOKEN (navegador del Admin)
│ Admin (React │  GET /ghost/api/admin/tinybird/token  →  {tinybird:{token,exp}}
│  apps/admin) │──────►  Ghost (solo sirve el JWT, NO es proxy)
└──────┬───────┘
       │  4. CONSULTA BROWSER-SIDE (directa del navegador del Admin a Tinybird)
       └──────►  GET {endpointBrowser|local.endpoint|endpoint}/v0/pipes/{pipe}.json
                 Pipes: api_kpis, api_top_sources, api_top_locations, api_top_devices,
                 api_top_utm_{sources,mediums,campaigns,contents,terms},
                 api_active_visitors, api_gift_link_visits
```

**Evidencia clave**:
- El collector es un servicio separado, no parte de Ghost OSS: `compose.dev.analytics.yaml:8` (imagen `ghost/traffic-analytics:1.0.336`), `:25` (`PROXY_TARGET=http://tinybird-local:7181/v0/events`).
- El path `/.ghost/analytics/api/v1/page_hit` **no existe en el código de Ghost OSS** (solo aparece en `ghost/core/core/frontend/public/robots.txt:8` como `Disallow`, y en `e2e/helpers/environment/service-managers/ghost-manager.ts:275`, donde lo sirve un **contenedor gateway** dedicado del entorno e2e). En Ghost(Pro) el tracker de producción es `https://e.ghost.org/tb/web_analytics` (ver snapshots en `ghost/core/test/unit/frontend/helpers/__snapshots__/ghost-head.test.js.snap:1105`).
- El Admin React consulta Tinybird **directamente desde el navegador**: `apps/admin/src/analytics/views/stats/web/web.tsx:126-144` + `apps/admin-x-framework/src/utils/stats-config.ts:4-21`.
- El servidor Ghost (Node) solo llama a **dos pipes** directamente: `grep tinybirdClient.fetch` → `content-stats-service.js:142`, `posts-stats-service.js:1157,1210,1519`.

---

## 1. Configuración

### 1.1 Bloque `tinybird` del fichero de configuración (NO está en BD)

Todo el contrato vive bajo la clave `tinybird` del config de Ghost (fichero `config.production.json` del usuario, o variables de entorno `tinybird__stats__endpoint`, etc.). **No hay defaults en el core**: `grep -rn tinybird ghost/core/core/shared/config/` no devuelve nada; sin bloque `tinybird` no hay analytics (ver §1.4).

Definición canónica de la estructura (typedef): `ghost/core/core/server/services/tinybird/tinybird-service.js:4-17`. Ejemplo oficial: `ghost/core/core/server/data/tinybird/README.md:60-91`.

| Clave | Tipo | Uso |
|---|---|---|
| `tinybird.workspaceId` | string | ID del workspace. Junto con `adminToken` activa el modo JWT (`tinybird-service.js:90`). |
| `tinybird.adminToken` | string | **Secreto HMAC con el que Ghost FIRMA los JWT** (`tinybird-service.js:158`). Es el "admin token" del workspace Tinybird (en dev lo obtiene `scripts/configure-ghost.sh:25-26` de `GET /v0/tokens`). |
| `tinybird.tracker.endpoint` | string | URL a la que el **navegador del visitante** hace POST de pageviews. **Obligatoria para activar analytics** (`settings-helpers.js:325`). Ej.: `https://e.ghost.org/tb/web_analytics`, `http://localhost:3000/api/v1/page_hit`, `http://localhost:2368/.ghost/analytics/api/v1/page_hit` (dev con gateway). |
| `tinybird.tracker.token` | string | Token opcional del collector; solo se imprime en el HTML si `env !== 'production'` (`ghost_head.js:285`). |
| `tinybird.tracker.datasource` | string | Default `analytics_events` (`ghost-stats.js:13`). |
| `tinybird.tracker.local.{enabled,endpoint,token,datasource}` | — | Alternativa local del tracker (`ghost_head.js:269-274`). |
| `tinybird.stats.id` | string | Override de `site_uuid` para TODAS las queries server-side y browser ("temporal hasta tener modo mock local", `utils/tinybird.js:27-30`; también `public-config/config.js:74`). |
| `tinybird.stats.endpoint` | string | Base URL de las queries **server-side** (default en ejemplos: `https://api.tinybird.co`; dev: `http://tinybird-local:7181`). |
| `tinybird.stats.endpointBrowser` | string | Base URL de las queries **desde el navegador del Admin** (p.ej. `http://localhost:7181` en dev porque el contenedor Tinybird no es alcanzable como `tinybird-local` desde el host; `compose.dev.analytics.yaml:78`). Se expone al Admin vía config pública (`public-config/config.js:8-13`). |
| `tinybird.stats.version` | string | Sufijo de versión: `v2` → `api_kpis_v2` (`utils/tinybird.js:36-42`, `stats-config.ts:17-18`). **No se setea por defecto** (los `_v2` están "not in use", §2.5). |
| `tinybird.stats.token` | string | Token estático legado (deprecated). Se usa como Bearer si no hay JWT ni local (`tinybird-service.js:124-128`). |
| `tinybird.stats.datasource` | string | Informativo; se expone al Admin. |
| `tinybird.stats.local.{enabled,token,endpoint,datasource}` | — | Modo local: si `enabled`, el server-side usa `local.endpoint` (`utils/tinybird.js:31-32`) y el token `local.token` como Bearer si no hay JWT (`tinybird-service.js:118-122`). El Admin usa `local.endpoint` si `local.enabled` (`stats-config.ts:9-10`). |

Config de referencia para el entorno Docker local (`compose.dev.analytics.yaml:75-80`):
```yaml
tinybird__stats__endpoint: http://tinybird-local:7181
tinybird__stats__endpointBrowser: http://localhost:7181
tinybird__tracker__endpoint: http://localhost:2368/.ghost/analytics/api/v1/page_hit
tinybird__tracker__datasource: analytics_events
# workspaceId y adminToken llegan de /mnt/shared-config/.env.tinybird (docker/ghost-dev/entrypoint.sh:6-16)
```

### 1.2 Settings de base de datos

| Setting | Tipo | Evidencia |
|---|---|---|
| `site_uuid` | string (UUID) | Se genera al instalar. Identifica el sitio en TODAS las queries (`settingsCache.get('site_uuid')`). Validación anti-drift: `settings-service.js:203-215`. |
| `web_analytics` | boolean, grupo `analytics`, **default `true``** | Migración `ghost/core/core/server/data/migrations/versions/5.127/2025-06-19-13-41-54-add-web-analytics-setting.js`. Es el interruptor de UI (Settings → Labs/Analytics). |
| `web_analytics_enabled` | boolean **calculado** | `settings-service.js:180` = `web_analytics === true` && `isWebAnalyticsConfigured()`. |
| `web_analytics_configured` | boolean **calculado** | `settings-service.js:181` = `_isValidTinybirdConfig()` && `!limitService.isDisabled('limitAnalytics')` (límite Ghost(Pro)). |

`_isValidTinybirdConfig()` (`ghost/core/core/server/services/settings-helpers/settings-helpers.js:321-340`):
1. `tinybird.tracker.endpoint` debe existir.
2. Y (`workspaceId` && `adminToken`) **o** `stats.local.enabled`.

### 1.3 ¿Se puede cambiar por Admin API?

- **`web_analytics`**: SÍ — es un setting editable vía `PUT /ghost/api/admin/settings/` (aparece en la allowlist del input serializer: `ghost/core/core/server/api/endpoints/utils/serializers/input/settings.js:67`).
- **Config de Tinybird (endpoint, tokens, workspaceId)**: NO. Solo fichero de configuración / variables de entorno. No existe ningún endpoint admin para modificarla. El único endpoint relacionado, `GET /ghost/api/admin/tinybird/token`, es **de solo lectura** y no es un proxy (ver §6).

### 1.4 Activación / desactivación y sus efectos

Analytics activo ⇔ `settingsHelpers.isWebAnalyticsEnabled()` (`settings-helpers.js:279-292`). Cuando está activo:

1. **Inyección del tracker** en cada página del sitio: `ghost_head.js:457-464` (no en previews, `ghost_head.js:259-262`).
2. **Creación del cliente Tinybird server-side**: `stats-service.js:249-259` — `StatsService.create()` instancia `TinybirdServiceWrapper.init()` + cliente solo si `settingsCache.get('web_analytics_enabled')` (el campo calculado). OJO: se evalúa **en el arranque** (creación del servicio), no por-request.
3. **Admin (React)**: kill-switch global `useWebAnalyticsEnabled()`; si está off, los hooks no lanzan queries (`apps/admin-x-framework/src/hooks/use-tinybird-query.ts:17-34`).

Cuando está desactivado o sin config: sin script en el HTML, `tinybirdClient = null` → `getTopContent`/`getTopPostsViews`/`getPostStats`/`getPostsVisitorCounts` devuelven vacíos, y `GET /tinybird/token` responde `{tinybird: null}`.

---

## 2. Pipes consumidos

### 2.1 Mapa exacto de llamadas

**Llamadas server-side (Ghost Node → Tinybird)** — todas vía `services/stats/utils/tinybird.js`:

| Pipe | Call site | Endpoint Admin que lo dispara | Params que envía Ghost |
|---|---|---|---|
| `api_top_pages` | `content-stats-service.js:142` (`fetchRawTopContentData`) | `GET /ghost/api/admin/stats/top-content` | `site_uuid`, `date_from`, `date_to`, `timezone`, `member_status`, `post_type`, `post_uuid`, `pathname`, `device`, `location`, `source` (admite `''` = Directo), `gift_link` ('true'/'false'), `utm_source`, `utm_medium`, `utm_campaign`, `utm_content`, `utm_term` (`content-stats-service.js:85-143`; opciones del endpoint: `api/endpoints/stats.js:98-115`) |
| `api_top_pages` | `posts-stats-service.js:1157-1160` (`getPostStats`) | `GET /ghost/api/admin/stats/posts/:id/stats` | `site_uuid`, `post_uuid`, `date_from` (= fecha de publicación del post, sin `date_to`) |
| `api_top_pages` | `posts-stats-service.js:1201-1210` (`getTopPostsViews`) | `GET /ghost/api/admin/stats/top-posts-views` | `site_uuid`, `date_from`, `date_to`, `timezone` (default 'UTC'), `post_type=post`, `limit` (default 5) |
| `api_post_visitor_counts` | `posts-stats-service.js:1519-1521` (`getPostsVisitorCounts`) | `POST /ghost/api/admin/stats/posts-visitor-counts` body `{postUuids:[...]}` (`api/endpoints/stats.js:422-452`) | `site_uuid`, `post_uuids=uuid1,uuid2,...` (array → CSV, `utils/tinybird.js:72-76`) |

**Llamadas browser-side (navegador del Admin → Tinybird, con el JWT de §3)**:

| Pipe | Call site (apps/) | Params típicos |
|---|---|---|
| `api_kpis` | `admin/src/analytics/views/stats/overview/overview.tsx:100`, `.../stats/web/web.tsx:126`, `admin/src/posts/analytics/web/web.tsx:154` | `site_uuid`, `date_from`, `date_to`, `timezone`, `member_status`, filtros |
| `api_top_sources` | `web.tsx:133` (stats y posts) | ídem |
| `api_top_locations` | `web.tsx:140`, `posts/analytics/web/web.tsx:161` | ídem |
| `api_top_devices` | `admin/src/shared/analytics/stats-filter.tsx:101` | ídem (para poblar el dropdown de filtros) |
| `api_top_utm_sources` / `_mediums` / `_campaigns` / `_contents` / `_terms` | `stats-filter.tsx:59-79` | ídem (dropdowns UTM) |
| `api_active_visitors` | `admin-x-framework/src/hooks/use-active-visitors.ts` | `site_uuid`, opcional `post_uuid`, `_refresh` (poll cada 60 s) |
| `api_gift_link_visits` | `admin/src/posts/analytics/hooks/use-gift-link-usage.ts:50-55` | `site_uuid`, `post_uuid`, `gift_link` (**token exacto** del link) |

`member_status` desde el Admin: unión con comas de un subconjunto de `undefined,free,paid` según la audiencia seleccionada (`apps/admin/src/shared/analytics/audience.ts:17-30`, valores en `constants.ts:83-87`). El pipe expande `paid` → añade `comped` y `gift` (§2.3).

`api_monitoring_ingestion` / `api_monitoring_ingestion_aggregated`: NO los consume ni Ghost ni el Admin; son para monitorización interna de Ghost.org (nótese que usan `start_date`/`end_date`, no `date_from`/`date_to`).

### 2.2 Patrón de petición HTTP

```
GET {base}/v0/pipes/{nombre}[_{version}].json?{query-params}
Authorization: Bearer {token}
```

- Server-side (`utils/tinybird.js:25-94`):
  - `base` = `stats.local.endpoint` si `local.enabled`, si no `stats.endpoint` (`:31-32`).
  - `version`: `options.version` si está definida, si no `stats.version`; con versión → `/v0/pipes/api_kpis_v2.json` (`:36-42`).
  - `site_uuid` SIEMPRE (`:47-49`), desde `stats.id` o el setting `site_uuid`.
  - Conversión camelCase→snake_case de cualquier otra opción (`:68-79`); arrays → string separado por comas.
  - `Authorization: Bearer` + `timeout: {request: 10000}` ms (`:85-92`). Sin prefijo URL adicional.
- Browser-side (`apps/admin-x-framework/src/utils/stats-config.ts:4-21`):
  - `base` = `local.endpoint` (si `local.enabled`) || `endpointBrowser` || `endpoint`.
  - Mismo patrón `.../v0/pipes/{name}[_{version}].json?{params}`; el Bearer lo inyecta `useQuery` de `@tinybirdco/charts` (`use-tinybird-query.ts:30-34`).

### 2.3 Contrato pipe por pipe (ficheros `.pipe` en `ghost/core/core/server/data/tinybird/endpoints/`)

Notación de params: los nombres son los del query string. "Array" = acepta valores separados por comas (sintaxis `{{ Array(...) }}` de Tinybird). Los defaults entre paréntesis son los declarados en el propio `.pipe`.

#### `api_top_pages` (`endpoints/api_top_pages.pipe`, 40 líneas) — el más importante
Params: `site_uuid` (req), `member_status` (Array, opcional), `location`, `pathname`, `post_uuid`, `gift_link`, `post_type`, `skip` (0), `limit` (50). **No filtra por fecha directamente**: `date_from`/`date_to`/`timezone` (y `source`, `device`, `utm_*`) se propagan por cascada de parámetros de Tinybird al pipe interno `filtered_sessions` (§2.4).
SELECT final (`:8-38`):
```sql
select
    case when post_uuid = 'undefined' then '' else post_uuid end as post_uuid,
    pathname,
    uniqExact(session_id) as visits
from _mv_hits h
inner join filtered_sessions fs on fs.session_id = h.session_id
where site_uuid = ... [+ filtros hit-level]
group by post_uuid, pathname
order by visits desc
limit {skip},{limit}
```
**Salida**: `{post_uuid: string ('' si no aplica), pathname: string, visits: uint}`. `visits` = nº de **sesiones únicas** que vieron esa página dentro de las sesiones que cumplen los filtros (no pageviews).
Semántica de filtros: `member_status` en `undefined|free|paid` (+`comped`,`gift` si incluye `paid`); `post_type='post'` → solo posts, cualquier otro valor → "no posts" (`post_type != 'post' or null`); `gift_link`: `'false'`/`'0'` → `gift_link = ''`, cualquier otro valor → `gift_link != ''`.
Fixture (`tests/api_top_pages.yaml:2-9`):
```json
{"post_uuid":"6b8635fb-292f-4422-9fe4-d76cfab2ba31","pathname":"/blog/hello-world/","visits":9}
{"post_uuid":"06b1b0c9-fb53-4a15-a060-3db3fde7b1fc","pathname":"/about/","visits":8}
{"post_uuid":"","pathname":"/","visits":7}
```

#### `api_post_visitor_counts` (`endpoints/api_post_visitor_counts.pipe`)
Params: `site_uuid` (req), `post_uuids` (Array, **req**). Sin fechas (all-time), **sin limit**.
Salida: `{post_uuid: string, visits: uint}` (uniqExact de session_id), orden `visits desc`.
Fixture (`tests/api_post_visitor_counts.yaml:11-14`):
```json
{"post_uuid":"6b8635fb-292f-4422-9fe4-d76cfab2ba31","visits":9}
{"post_uuid":"06b1b0c9-fb53-4a15-a060-3db3fde7b1fc","visits":8}
```
Ghost lo consume como mapa `post_uuid → visits` (`posts-stats-service.js:1523-1531`).

#### `api_kpis` (`endpoints/api_kpis.pipe`, 172 líneas, 6 nodos)
Params: `site_uuid` (req), `date_from`, `date_to`, `timezone` (default `'Etc/UTC'`), `current_time` (solo tests), `member_status`, `location`, `pathname`, `post_uuid`, `gift_link`.
SELECT final (`finished_data`, `:159-171`):
```sql
select a.date as date,
    coalesce(b.visits, 0) as visits,
    [si pathname/post_uuid/gift_link definidos → c.pageviews; si no → b.pageviews] as pageviews,
    coalesce(b.bounce_rate, 0) as bounce_rate,
    coalesce(b.avg_session_sec, 0) as avg_session_sec
from timeseries a left join data b ... [left join pathname_pageviews c ...]
```
**Salida**: `{date, visits, pageviews, bounce_rate, avg_session_sec}`.
- `visits` = `uniq(session_id)` por bucket; `pageviews` = suma; `bounce_rate` = `truncate(avg(is_bounce),2)` ∈ [0,1] (fracción, no %); `avg_session_sec` = `truncate(avg(duration),2)`.
- Granularidad: **diaria** (`date` = `'YYYY-MM-DD'`) o **horaria** (`'YYYY-MM-DD HH:MM:SS'`) cuando `date_from == date_to`.
- La serie SIEMPRE viene completa (buckets sin datos → ceros, por el left join contra `timeseries`).
- Defaults si faltan fechas: últimos 7 días hasta hoy (`:22,36`).
- Si `date_from==date_to` y ese día es "hoy", solo hasta la hora en curso (`current_time`, `:38-53`).
- Si se filtra por `pathname`/`post_uuid`/`gift_link`, las `pageviews` se computan de los **hits** que cumplen el filtro (`pathname_pageviews`, `:118-156`), mientras visits/bounce/duración siguen siendo de sesión completa.
Fixture (`tests/api_kpis.yaml:2-12`):
```json
{"date":"2100-01-01","visits":3,"pageviews":5,"bounce_rate":0.33,"avg_session_sec":580.33}
{"date":"2100-01-02","visits":1,"pageviews":3,"bounce_rate":0,"avg_session_sec":1027}
...
{"date":"2100-01-06","visits":2,"pageviews":2,"bounce_rate":1,"avg_session_sec":0}
```
Single-day horario (`tests/api_kpis.yaml:98-105`):
```json
{"date":"2100-01-01 00:00:00","visits":1,"pageviews":1,"bounce_rate":1,"avg_session_sec":0}
{"date":"2100-01-01 01:00:00","visits":1,"pageviews":2,"bounce_rate":0,"avg_session_sec":1111}
```

#### `api_active_visitors` (`endpoints/api_active_visitors.pipe`)
Params: `site_uuid` (req), `post_uuid`, `gift_link`. Consulta `_mv_hits` con ventana fija `timestamp >= now() - 5 min` (NO usa filtered_sessions ni fechas).
Salida (una fila): `{active_visitors: uint}` = `uniqExact(session_id)`. Fixture: `{"active_visitors":17}`.

#### `api_top_sources` (`endpoints/api_top_sources.pipe`)
Params: los de `filtered_sessions` (`site_uuid` req, fechas, `source`, `device`, `utm_*`, etc.), `skip`(0), `limit`(50).
Salida: `{source: string, visits: uint}` donde `visits` = `count()` de **sesiones** (no hits) agrupadas por el `source` del primer hit. `source=''` = tráfico directo. Sin excluir vacíos. Fixture: `{"source":"","visits":7}`, `{"source":"bing.com","visits":2}`, …

#### `api_top_locations` (`endpoints/api_top_locations.pipe`)
Params: `site_uuid` (req) + hit-level: `member_status`, `location`, `pathname`, `post_uuid`, `gift_link` + los de `filtered_sessions` + `skip`/`limit`.
Salida: `{location: string (código país ISO), visits: uint}` = `uniqExact(session_id)`.

#### `api_top_devices` (`endpoints/api_top_devices.pipe`)
Params: como top_sources. Salida: `{device: string, visits: uint}` = `count()` sesiones por `device` del primer hit (`'desktop'|'mobile-*'|'bot'|'unknown'`).

#### `api_top_utm_sources` / `_mediums` / `_campaigns` / `_contents` / `_terms` (`endpoints/api_top_utm_*.pipe`)
Params: como top_sources. Salida: `{utm_source|utm_medium|utm_campaign|utm_content|utm_term: string, visits: uint}` = `count()` sesiones cuyo **primer hit** tenga ese UTM no vacío (excluyen `''`).

#### `api_gift_link_visits` (`endpoints/api_gift_link_visits.pipe`)
Params: `site_uuid` (req), `date_from`, `date_to` (aquí SÍ filtran directo sobre `timestamp`, rango `[from, to+1d)`), `post_uuid`, `gift_link` (**exact-match contra el token**, distinto del uso booleano en top_pages).
Salida (`:18-36`): `{gift_link: string, visits: uint (sesiones únicas), views: uint (hits), last_seen: datetime}` ordenado `visits desc`, sin limit. Solo filas con `gift_link != ''`.
Fixture (`tests/api_gift_link_visits.yaml:3-6`):
```json
{"gift_link":"gift_aaa","visits":2,"views":3,"last_seen":"2100-01-06 01:28:38"}
```

#### `api_monitoring_ingestion` / `api_monitoring_ingestion_aggregated` (`endpoints/api_monitoring_ingestion*.pipe`)
Params: `start_date`, `end_date` (default últimos 7 días), `site_uuid` (opcional). Salida: `{date, site_uuid?, total_events, avg_latency_ms, p50_latency_ms, p95_latency_ms, min_latency_ms, max_latency_ms}`. No consumidos por Ghost OSS.

### 2.4 `filtered_sessions` — el corazón del filtrado (`ghost/core/core/server/data/tinybird/pipes/filtered_sessions.pipe`)

Todos los pipes "top_*" y kpis se unen a `filtered_sessions` (o `filtered_sessions_v2`), que clasifica sesiones en 2 etapas:

1. **Hit-level** (`sessions_filtered_by_hit_attributes`, `:3-36`): sesiones con **al menos un** hit que cumpla: `site_uuid` + rango de fechas sobre `timestamp` (`>= date_from`, `< date_to + 1 día`; **default: últimos 7 días** si no hay `date_from`, hasta mañana si no hay `date_to`) + `member_status` (con expansión `paid`→`comped`+`gift`, `:25-32`) + `location` + `pathname` + `post_uuid` + `gift_link` (booleano).
2. **Session-level** (`sessions_filtered_by_session_attributes`, `:38-77`): SOLO si llega algún `source`/`device`/`utm_*`: filtra por los atributos del **primer hit** de la sesión (tabla `mv_session_data`, `argMin(field, timestamp)`) y por rango sobre `first_pageview`.

**Comportamiento crítico** (`endpoints/README.md:50-83`): los endpoints devuelven **TODOS los hits** de las sesiones que clasifican. Filtrar por `utm_medium=social` responde "¿qué páginas visitaron las sesiones que llegaron con utm_medium=social?" — la suma de `visits` entre páginas puede superar el nº de sesiones. Además, al no haber filtro de fecha en el SELECT exterior de `api_top_pages`, los hits de una sesión que cruza la medianoche del límite se cuentan completos (documentado como leve sobre-conteo en `api_top_pages_v3.pipe:19-22`).

### 2.5 Versiones `_v2`/`_v3` — estado real

- **Todos los `_v2` están "NOT currently in use"** y marcados para eliminación: cabecera literal en `endpoints/api_top_pages_v2.pipe:1-2` ("*This _v2 Tinybird pipe is NOT currently in use... will be removed in a future release*"). Son idénticos a los v1 pero usando `filtered_sessions_v2`/`_mv_session_data_v2` (AggregatingMergeTree).
- **`api_top_pages_v3` + `api_top_pages_router` + `mv_daily_pages`**: experimento aparcado (`api_top_pages_v3.pipe:1-4`). El router delegaba a v3 cuando no había filtros de sesión.
- **El código actual usa los pipes SIN sufijo (v1)**. La única vía para llamar a un `_v2` es setear `tinybird:stats:version` (nadie lo hace por defecto) o pasar `{version:'v3'}` por código (nadie lo hace).
- La lista de scopes del JWT (§3) incluye los 13 pipes v1 + 12 `_v2` (25 total) "por si acaso", pero **no** incluye `_v3` ni `_router`.
- **Conclusión para el reemplazo Go**: implementar los pipes sin sufijo es suficiente; opcionalmente responder también en `*_v2`/`*_v3` como alias idénticos para tolerar configs experimentales.

---

## 3. JWT

### 3.1 Generación (lado Ghost)

Fichero: `ghost/core/core/server/services/tinybird/tinybird-service.js`.

- **Biblioteca**: `jsonwebtoken` (`require('jsonwebtoken')`, `:1`). Firma `jwt.sign(payload, this.tinybirdConfig.adminToken, {noTimestamp: true})` (`:158`) → **algoritmo HS256** (default de jsonwebtoken), **secreto = el string `tinybird.adminToken` tal cual** (el mismo valor del config). No es un token emitido por Tinybird: es un JWT auto-firmado por Ghost con un secreto compartido con Tinybird (el admin token del workspace; en local lo crea la CLI: `scripts/configure-ghost.sh:25-26`).
- **Payload exacto** (`_generateToken`, `:139-156`; typedef `:39-45`):
```json
{
  "workspace_id": "<tinybird.workspaceId>",
  "name": "tinybird-jwt-<siteUuid>",
  "exp": <now + 180*60 (segundos unix)>,
  "scopes": [
    {"type": "PIPES:READ", "resource": "api_kpis",      "fixed_params": {"site_uuid": "<siteUuid>"}},
    {"type": "PIPES:READ", "resource": "api_active_visitors", "fixed_params": {"site_uuid": "<siteUuid>"}},
    ... 25 entradas: 13 pipes v1 + 12 _v2 (lista TINYBIRD_PIPES, :47-74) ...
  ]
}
```
  - `noTimestamp: true` → **no hay claim `iat`**.
  - `exp` default: **180 minutos** (`getToken`, `:103`); el nombre default `tinybird-jwt-${siteUuid}`.
  - `siteUuid` efectivo = `tinybird.stats.id || setting site_uuid` (`:86`).
- **Caché/refresh**: el token se cachea en memoria (`_serverToken`); se regenera si falta o si `_isJWTExpired` (verifica firma con `adminToken` + margen de **300 s** antes de `exp`, `:173-185`). Tests de referencia: `test/unit/server/services/tinybird/tinybird-service.test.js:118-175`.
- **Cadena de selección de token** (`getToken`, `:103-131`), en orden:
  1. JWT auto-firmado si `workspaceId && adminToken`;
  2. si no, `stats.local.token` si `stats.local.enabled`;
  3. si no, `stats.token` (estático legado);
  4. si no, `null` (y `/tinybird/token` devolverá `{tinybird:null}`).

### 3.2 Flujo completo del JWT hasta el navegador del Admin

1. Admin React: `getTinybirdToken()` → `GET /ghost/api/admin/tinybird/token/` con refresco automático cada **120 min** y `staleTime` 110 min (`apps/admin-x-framework/src/api/tinybird.ts`).
2. Ghost responde `{tinybird: {token, exp}}` (§6).
3. El navegador llama a Tinybird con `Authorization: Bearer <jwt>` vía el hook `useQuery` de `@tinybirdco/charts` (`use-tinybird-query.ts:30-34`).
4. El **servidor** Ghost usa el mismo mecanismo para sus 2 pipes (Bearer del mismo `getToken()`, `utils/tinybird.js:33-34,87`).

### 3.3 Qué debe validar el reemplazo Go (requisito del contrato)

- `Authorization: Bearer <jwt>`; **HS256** con el mismo secreto compartido (`adminToken`).
- Claims a comprobar: `exp` (reject si expirado), `scopes[]`: alguna entrada con `type == "PIPES:READ"`, `resource == <pipe solicitado>`, y `fixed_params.site_uuid` **==** valor del query param `site_uuid` de la petición. `fixed_params` es el mecanismo de aislamiento multi-tenant de Tinybird: el servidor DEBE forzar la query al `site_uuid` del token (ignorar/validar discrepancias → 403).
- `workspace_id` y `name` son informativos para la validación (Tinybird real los usa para identificar el workspace emisor).
- Alternativa sin JWT: aceptar tokens estáticos (Bearer = `stats.token` o `stats.local.token` o los tokens declarados en los `.pipe`, §3.4).
- Error de auth esperado: cualquier 401/403 hace que el server-side loguee el error y devuelva `null` (no reintenta), y el Admin muestre estado de error.

### 3.4 Tokens estáticos declarados en los `.pipe` (sistema de TOKEN de Tinybird)

Los `.pipe`/`.datasource` declaran tokens con nombre (mecanismo `TOKEN "<name>" <SCOPE>` de Tinybird, que genera un token estático por recurso):
- `endpoints/api_*.pipe` (todos los de analytics): `TOKEN "stats_page" READ` y `TOKEN "axis" READ`.
- `endpoints/api_monitoring_ingestion*.pipe`: `TOKEN "monitoring" READ`.
- `datasources/analytics_events.datasource:1-2`: `TOKEN "tracker" APPEND` y `TOKEN "analytics-service" APPEND` (ingesta).
`scripts/configure-ghost.sh:35-42` muestra cómo recuperarlos de `GET /v0/tokens` del workspace local.

---

## 4. Formato de respuesta HTTP que Ghost espera

### 4.1 Estructura del body

Formato estándar de la Query API de Tinybird (`/v0/pipes/....json`): JSON con clave `data` = array de objetos-fila (una key por columna del SELECT):

```json
{
  "meta": [...],            // opcional; Ghost server lo IGNORA
  "data": [ {"col1": v, "col2": v}, ... ],
  "rows": N, "statistics": {...}   // opcional; ignorado por Ghost server
}
```

- `parseResponse` (`utils/tinybird.js:102-132`): acepta `response.body` como string JSON, objeto, o respuesta directa; si `!responseData.data` → `null`; JSON inválido → `null` + log de error. **Solo se consume `.data`**.
- El Admin React sí usa `data` y `meta` del hook de Tinybird (`use-tinybird-query.ts:30`), pero `meta` no es imprescindible.
- Tipos: los numéricos llegan como números JSON (el Admin hace `Number(row.visits)` defensivamente, `use-gift-link-usage.ts:72-73`); fechas como strings `'YYYY-MM-DD'` / `'YYYY-MM-DD HH:MM:SS'` / `'YYYY-MM-DD HH:MM:SS'` (last_seen).
- **Caché de respuestas por parte de Ghost: NINGUNA contra Tinybird.** Lo único que se cachea (opcionalmente) es la respuesta de los endpoints `/ghost/api/admin/stats/*` vía adapter `cache:stats` si `hostSettings:statsCache:enabled` (`services/stats/service.js:27-29`; usado en `api/endpoints/stats.js:120` p.ej.).

### 4.2 Status codes y errores

- Éxito: 200 con `{data:[...]}`. Un rango vacío es `{data:[]}` (válido; `parseResponse` devuelve `[]`).
- Cualquier 4xx/5xx: `got` lanza excepción → `fetch` la captura, loguea `Error in Tinybird API request to <url>` y devuelve **`null`** (`utils/tinybird.js:140-150`). Los servicios que llaman tratan `null` como "sin datos" (`{data: []}` hacia el Admin). No hay manejo diferenciado de 401/403/429.
- El Admin (browser) marca `error` en el hook y las tarjetas muestran estado de error/vacío.

### 4.3 Timeout y reintentos

- **Timeout: 10 000 ms** por petición (`utils/tinybird.js:89-92`; el cliente `request-external` también defaultea 10 s, `ghost/core/core/server/lib/request-external.js:296-298`).
- **Reintentos**: ninguno propio. `got` se instancia con retry por defecto (limit 2, solo métodos idempotentes) en producción; en tests se fuerza `retry.limit=0` (`request-external.js:221-230, 304`). El `disableRetries` de tests además baja el timeout a 5 s, pero solo en `NODE_ENV=test`.
- User-Agent saliente: `Ghost(https://github.com/TryGhost/Ghost)` (`request-external.js:293-295`). Keep-alive HTTP activado.
- SSRF guard: en producción, `request-external` bloquea URLs que resuelvan a IPs privadas (`request-external.js:183-205`) — relevante si el reemplazo Go corre en red privada y Ghost apunta a él **en producción**: la IP privada será rechazada salvo que sea el host del propio `url` de Ghost o `env=development`. Para un drop-in self-hosted esto importa: usar `env: development` o hostname que resuelva público, o el propio dominio del sitio.

### 4.4 Fixtures literales de respuestas esperadas (extraídos de tests)

- Cliente unitario (`test/unit/server/services/stats/utils/tinybird.test.js:123-199`):
```json
{"data":[{"pathname":"/test-1/","visits":100},{"pathname":"/test-2/","visits":50}]}
```
- URL canónica server-side (`content.test.js:54`):
```
https://api.tinybird.co/v0/pipes/api_top_pages.json?site_uuid=site-id&date_from=2023-01-01&date_to=2023-01-31&timezone=UTC&member_status=all
```
- Respuestas por pipe: ver fixtures YAML citados en §2.3 (`ghost/core/core/server/data/tinybird/tests/*.yaml`) y el NDJSON de eventos de entrada (`fixtures/analytics_events.ndjson`).

---

## 5. Lado navegador (ingesta de pageviews)

### 5.1 Inyección del script tracker

`ghost/core/core/frontend/helpers/ghost_head.js`:
- Condición: `settingsHelpers.isWebAnalyticsEnabled()` (`:457-464`); no se inyecta en preview (`:259-262`).
- HTML generado (`:285`; snapshot real en `test/unit/frontend/helpers/__snapshots__/ghost-head.test.js.snap:1105`):
```html
<script defer src="/public/ghost-stats.min.js?v=<asset-hash>"
  data-stringify-payload="false"
  data-datasource="analytics_events"
  data-storage="localStorage"
  data-host="https://e.ghost.org/tb/web_analytics"
  data-token="tinybird_token"            <!-- SOLO si env != 'production' -->
  tb_site_uuid="77f09c60-..."
  tb_post_uuid="<uuid|undefined>"
  tb_post_type="post|page|null"
  tb_member_uuid="<uuid|undefined>"
  tb_member_status="free|paid|comped|gift|undefined"
  tb_gift_link=""></script>
```
- Los atributos `tb_*` se convierten en atributos globales del payload (`site_uuid`, `post_uuid`, `post_type`, `member_uuid`, `member_status`, `gift_link`; `ghost_head.js:276-283` → `ghost-stats.js:51-55`).
- El JS se sirve desde el propio Ghost: `GET /public/ghost-stats.min.js` (`ghost/core/core/frontend/web/routers/serve-public-file.js:128`; origen `core/frontend/src/ghost-stats/ghost-stats.js`, minificado por `ghost/core/scripts/minify-assets.mjs:55-56`).

### 5.2 El POST del page_hit (`core/frontend/src/ghost-stats/ghost-stats.js`)

- **URL**: `{data-host}?name={datasource}[&token={data-token}]` (`:82-85`). Es el formato de la Events API de Tinybird (`POST .../v0/events?name=...&token=...`) delante del cual se pone el collector.
- **Headers**: `Content-Type: application/json` y `x-site-uuid: <site_uuid>` si existe (`:103-108`).
- **Body** (`:92-97`):
```json
{
  "timestamp": "2026-08-16T12:00:00.000Z",
  "action": "page_hit",
  "version": "1",
  "payload": {
    "event_id": "<UUIDv4 generado en el cliente (:86)>",
    "user-agent": "<navigator.userAgent>",
    "locale": "es-ES",
    "location": "ES",
    "pathname": "/blog/hello-world/",
    "href": "https://site.com/blog/hello-world/?utm_source=...",
    "utm_source": "...", "utm_medium": "...", "utm_campaign": "...", "utm_term": "...", "utm_content": "...",
    "parsedReferrer": {"url": "...", "source": "...", "medium": "..."},
    "site_uuid": "...", "post_uuid": "...", "post_type": "post",
    "member_uuid": "...", "member_status": "free", "gift_link": ""
  }
}
```
  - `payload` se envía como **objeto** (no string) porque `data-stringify-payload="false"` (`:48`, `:89`; `core/frontend/src/utils/privacy.ts:53-65`).
  - `processPayload` aplica `maskSensitiveData` (regex sobre `email|token|password|...` → `"********"`, `privacy.ts:10-48`) y merge de los atributos `tb_*`.
  - `location` = país deducido del timezone del navegador vía `countries-and-timezones` (`:135-151`); referrer parseado de `document.referrer` + query params (`parseReferrerData`, con workaround `referrerData.url = getReferrer(href) || referrerData.url`, `:164-170`).
- **Comportamiento**: debounce de 300 ms (`:173`); timeout fetch 5 s con `AbortController` (`:100-101`); fallo silencioso (solo log en localhost, `:124-131`); se salta en entornos de test (`__nightmare`/webdriver/Cypress, salvo flag `__GHOST_SYNTHETIC_MONITORING__`, `browser-service.js:85-98`), en iframes (preview admin) y mientras `visibilityState=hidden` (prerender; espera al evento `visibilitychange`, `:199-215`).

### 5.3 Camino hasta Tinybird: el Analytics Service (collector)

- El navegador **NO llama a Ghost**: hace POST directo al `data-host` (el collector). En Ghost(Pro): `https://e.ghost.org/tb/web_analytics`. En dev/e2e: `http://.../.ghost/analytics/api/v1/page_hit`, ruta servida por un **gateway externo** (contenedor e2e o infraestructura Pro) que enruta al Traffic Analytics Service — **no existe handler en Ghost OSS** (únicamente `Disallow: /.ghost/analytics/api/` en `robots.txt:8`).
- El Traffic Analytics Service (imagen privada `ghost/traffic-analytics:1.0.336`, `compose.dev.analytics.yaml:8-31`) recibe el page_hit, **añade `session_id`** (el navegador no lo envía; el fixture `fixtures/analytics_events.ndjson` lo lleva a nivel raíz), añade `meta.received_timestamp`/`referrerSource` (comparar payload §5.2 con el fixture) y hace **append al datasource `analytics_events` de Tinybird** vía Events API (`PROXY_TARGET=http://tinybird-local:7181/v0/events`).
- **Implicación para el reemplazo Go**: hay que implementar (a) el collector `POST {host}?name=analytics_events[&token=]` con generación de `session_id` y normalización, y (b) la Events API `POST /v0/events` — o bien apuntar `tinybird:tracker:endpoint` directamente al servicio Go y fusionar ambos.

### 5.4 Esquema del datasource `analytics_events` (`datasources/analytics_events.datasource`)

```sql
`timestamp`    DateTime          `json:$.timestamp`
`session_id`   String            `json:$.session_id`
`action`       LowCardinality(String) `json:$.action`      -- 'page_hit'
`version`      LowCardinality(String) `json:$.version`     -- '1'
`payload`      String            `json:$.payload`          -- JSON serializado
`site_uuid`    LowCardinality(String) `json:$.payload.site_uuid`
`inserted_at`  DateTime64(3) DEFAULT now64()
ENGINE MergeTree, partición toYYYYMM(timestamp), sorting key (site_uuid, timestamp)
TOKEN "tracker" APPEND; TOKEN "analytics-service" APPEND; con FORWARD_QUERY
```
Estructura del `payload` ya transformado (`ARCHITECTURE.md:212-227` + fixture real):
```json
{"site_uuid":"...","member_uuid":"...|undefined","member_status":"free|paid|comped|gift|undefined",
 "post_uuid":"...|undefined","post_type":"post|page|''","user-agent":"...","locale":"...",
 "location":"ES","referrer":"https://...","pathname":"/...","href":"https://...",
 "utm_source":"...","utm_medium":"...","utm_campaign":"...","utm_term":null,"utm_content":null,
 "gift_link":"gift_aaa|''","device":"desktop|bot|...",
 "meta":{"referrerSource":"https://...","received_timestamp":"2100-01-01 00:06:15.050Z"}}
```

### 5.5 Vistas materializadas que alimentan los pipes

- `pipes/mv_hits.pipe` → datasource `_mv_hits` (`datasources/_mv_hits.datasource`): filtra `action='page_hit'`; extrae el payload; calcula `received_at` (de `payload.meta.received_timestamp`, epoch 0 si falta), `ingestion_latency_ms` (`date_diff('ms', received_at, inserted_at)`, `-1` si inválido), **normaliza `source`** (mapa extenso: Facebook/Twitter/Bluesky/Reddit/Gmail/…/`domainWithoutWWW(referrer)`, `:63-109`), `device` (''→'unknown'), `os` y `browser` por regex del user-agent (`:112-141`).
- `pipes/mv_session_data.pipe` (queryable) y `datasources/_mv_session_data_v2.datasource` (AggregatingMergeTree): por `session_id`: `pageviews`, `first_pageview`, `last_pageview`, `duration`, `is_bounce` (`pageviews=1`), y `argMin(source/device/utm_*, timestamp)` (atributos del primer hit).
- `pipes/mv_daily_pages.pipe` → `_mv_daily_pages` (solo para el experimento v3).

---

## 6. Endpoint Admin API: `GET /ghost/api/admin/tinybird/token`

- Ruta: `ghost/core/core/server/web/api/endpoints/admin/routes.js:296` → `router.get('/tinybird/token', mw.authAdminApi, http(api.tinybird.token))`.
- Controlador: `ghost/core/core/server/api/endpoints/tinybird.js` (31 líneas): **no es un proxy ni valida nada contra Tinybird**; llama `TinybirdServiceWrapper.init()` y devuelve el token actual (`getToken()`).
- Permisos: `permissions: {docName: 'members', method: 'browse'}` → en la práctica **Owner y Admin** (200); **Editor → 403**; anónimo → 403 (`test/e2e-api/admin/tinybird.test.js:14-17,123-151`).
- Serializador: `ghost/core/core/server/api/endpoints/utils/serializers/output/tinybird.js` → envuelve en `{tinybird: ...}`.
- Respuestas posibles (e2e test completo en `test/e2e-api/admin/tinybird.test.js`):
  - JWT: `{"tinybird": {"token": "<jwt>", "exp": "2026-08-16T15:00:00.000Z"}}` — `exp` en **ISO 8601** igual a `payload.exp * 1000` (verificado en test `:47-57`).
  - Token estático: `{"tinybird": {"token": "<stats.token|local.token>"}}` (sin `exp`).
  - Sin config: `{"tinybird": null}` (HTTP 200).
- `cacheInvalidate: false`. El Admin lo cachea 110-120 min (`apps/admin-x-framework/src/api/tinybird.ts`).

---

## 7. Versiones e historia

- **Versión analizada**: Ghost **6.58.0-rc.0** (`ghost/core/package.json`). El clone está squashed (un solo commit), así que no hay historial git fino.
- **Setting `web_analytics`**: introducido por la migración `5.127/2025-06-19-13-41-54-add-web-analytics-setting.js` con default `true` (durante el ciclo 6.x).
- **Pipes `_v2`**: experimento de rendimiento "on hold"; cabeceras de los ficheros: "NOT currently in use … will be removed in a future release" (`api_top_pages_v2.pipe:1-2`). El código los mantiene solo en la lista de scopes del JWT.
- **`api_top_pages_v3` + `api_top_pages_router` + `_mv_daily_pages`**: segundo experimento (MV diaria, 3x más rápido), también aparcado (`api_top_pages_v3.pipe:1-4`; benchmarks en `scripts/benchmark-top-pages.sh`, `compare-top-pages.sh`).
- **Cuál usa el código actual**: **los pipes sin sufijo (v1)**, sin condición — la versión solo cambia si alguien setea `tinybird:stats:version` o pasa `{version}` explícito (no ocurre en el código productivo).
- Guía de referencia del workspace Tinybird completo: `ghost/core/core/server/data/tinybird/README.md` (setup CLI/Docker) y `ARCHITECTURE.md` (modelo de datos MySQL+Tinybird, ratios de mock data `:358-402`).

---

## 8. Checklist para el drop-in replacement en Go

1. **Query API**: `GET /v0/pipes/{name}.json` respondiendo `{"data":[{...}]}` (200). Columnas y semántica EXACTAS de §2.3. Alias `*_v2`/`*_v3` opcionales.
2. **Auth**: validar `Authorization: Bearer` con JWT **HS256** (secreto = `adminToken` compartido), claims `exp` + `scopes[].type=="PIPES:READ"` + `scopes[].resource==pipe` + `fixed_params.site_uuid == query.site_uuid` (forzar multi-tenant). Aceptar también tokens estáticos si se quiere parity con `stats.token`/tokens `.pipe`.
3. **Pipes mínimos**: `api_top_pages` (server-side, con cascada de `filtered_sessions`: filtros de sesión por primer hit + "todos los hits de las sesiones que clasifican"), `api_post_visitor_counts` (CSV en `post_uuids`), y del navegador: `api_kpis` (serie diaria/horaria completa con ceros, `bounce_rate` fracción 0-1 truncada a 2, expansión `paid`→+`comped`+`gift`), `api_top_sources`, `api_top_locations`, `api_top_devices`, `api_top_utm_*` (5), `api_active_visitors` (ventana 5 min), `api_gift_link_visits` (match exacto de token).
4. **Params**: `site_uuid` siempre; `date_from`/`date_to` `YYYY-MM-DD` con default 7 días; `timezone` IANA (default `Etc/UTC`); `member_status` CSV; `limit`(50)/`skip`(0); `gift_link` booleano ('false'/'0' → `gift_link=''`); `post_type` ('post' → posts, resto → no-posts).
5. **Collector/ingesta**: `POST {host}?name=analytics_events[&token=]` con body `{timestamp, action:'page_hit', version:'1', payload:{...}}` (+header `x-site-uuid`), generar `session_id`, `meta.received_timestamp`, y almacenar (equivalente a Events API `POST /v0/events`). Derivar `source` normalizado, `os`, `browser`, `device`, sesiones (`is_bounce`, duración, atributos de primer hit).
6. **Token endpoint parity**: no necesario (lo sirve Ghost), pero el `exp` ISO del endpoint admin deriva del JWT — mantener 180 min > refetch de 120 min del Admin.
7. **Robustez**: timeouts de cliente de 10 s (Ghost no reintenta activamente); errores → Ghost trata `null` como vacío (mejor devolver `{data:[]}` que 500 si hay datos parciales); ojo con el guard SSRF de IPs privadas de `request-external.js` en producción.
