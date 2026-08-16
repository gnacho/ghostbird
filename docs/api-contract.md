# Contrato API Ghost ↔ GhostBird — Resumen ejecutivo (Fase 0)

> **Qué es esto**: síntesis del contrato verificado contra código real para implementar un
> drop-in replacement de Tinybird en Go. Los dos documentos completos con evidencia
> (`ruta:línea`) están en [contract/ghost-side.md](contract/ghost-side.md) (Ghost 6.58.0-rc.0)
> y [contract/trafficanalytics-side.md](contract/trafficanalytics-side.md) (TrafficAnalytics 1.0.351).
> Todo lo de aquí está verificado salvo lo marcado como hipótesis.

## 1. Arquitectura REAL (corrige la asumida en el plan inicial)

"Tinybird" en Ghost 6.x son **cuatro flujos**, y dos de ellos salen del navegador (no del
servidor Ghost):

```
1. INGESTA   navegador visitante ──POST──► collector (TrafficAnalytics en Pro)
             POST {tracker.endpoint}?name=analytics_events
             El collector valida, filtra bots, firma (session_id) y hace
             POST /v0/events (NDJSON, Bearer) al backend de datos.

2. QUERY-SRV servidor Ghost ──GET──► backend de datos
             GET {stats.endpoint}/v0/pipes/{pipe}.json  (Bearer JWT)
             Solo 2 pipes: api_top_pages, api_post_visitor_counts

3. TOKEN     navegador Admin ──GET──► Ghost (NO es proxy)
             GET /ghost/api/admin/tinybird/token → {tinybird:{token,exp}}
             Ghost AUTO-FIRMA el JWT (HS256, secreto = tinybird.adminToken)

4. QUERY-BRW navegador Admin ──GET──► backend de datos DIRECTAMENTE
             GET {endpointBrowser|endpoint}/v0/pipes/{pipe}.json  (Bearer JWT)
             11 pipes: api_kpis, api_top_sources, api_top_locations,
             api_top_devices, api_top_utm_* (5), api_active_visitors,
             api_gift_link_visits
```

**Implicaciones de diseño (decisiones)**:

- El servicio debe ser **alcanzable desde navegadores** (visitante + Admin), no solo desde
  Ghost → **CORS obligatorio** en collector y pipes. TrafficAnalytics usa
  `origin:'*'`, `allowedHeaders: [...,'x-site-uuid','Authorization']`
  (`src/plugins/cors.ts:6-10`). Replicar eso.
- **Opción B (recomendada): single binary que integra también el collector**
  (`POST /api/v1/page_hit` con el mismo pipeline que TrafficAnalytics: bots, UA parsing,
  session_id = sha256(salt:site_uuid:ip:ua) con sal diaria por sitio). Así el self-hoster
  despliega UN binario y no necesita TrafficAnalytics (Node 22 + Pub/Sub/Firestore opcional).
- **Opción A (compatibilidad)**: exponer también `POST /v0/events` (NDJSON/JSON, Bearer o
  `?token=`, 2XX con body libre) para quien quiera mantener TrafficAnalytics oficial como
  collector. Barato de implementar: mismo almacenamiento.
- **SSRF guard**: `request-external.js` de Ghost bloquea en producción URLs que resuelvan a
  IP privada (afecta al flujo 2). Opciones: `env: development` en Ghost, DNS público, o
  servir GhostBird en el propio host del site. OJO en la fase de integración.

## 2. Ingesta — contrato exacto

### 2.1 Lo que envía el navegador (`ghost-stats.min.js`, servido por Ghost)

`POST {tracker.endpoint}?name=analytics_events[&token=...]` (token solo si `env != production`),
headers `Content-Type: application/json` + `x-site-uuid: <site_uuid>`, body:

```jsonc
{
  "timestamp": "2026-08-16T12:00:00.000Z",   // ISO con ms
  "action": "page_hit",
  "version": "1",
  "payload": {
    "event_id": "<UUIDv4 cliente>", "user-agent": "...", "locale": "es-ES",
    "location": "ES",                          // país del timezone del navegador (NO geo server)
    "pathname": "/blog/x/", "href": "https://site.com/blog/x/?utm_...",
    "parsedReferrer": {"url": "...", "source": "...", "medium": "..."},
    "site_uuid": "...", "post_uuid": "undefined", "post_type": "null"|"post"|"page",
    "member_uuid": "undefined", "member_status": "free|paid|comped|gift|undefined",
    "gift_link": "", "utm_source": null, "utm_medium": null, "utm_campaign": null,
    "utm_term": null, "utm_content": null
  }
}
```

El collector debe responder `202 {"message":"Page hit event received"}` (o el 2XX del
upstream en modo proxy). Bots → mismo 202 pero sin almacenar.

### 2.2 Pipeline del collector (lo que hace TrafficAnalytics, a replicar)

1. **Bots**: una única regex case-insensitive sobre el UA:
   `/wget|ahrefsbot|curl|bot|crawler|spider|urllib|bitdiscovery|\+https:\/\/|googlebot/i`.
2. **UA parsing** (ua-parser-js): `browser` minúsculas sin prefijo "mobile ", `os` con
   `mac os→macos`; `device` DERIVADO: `bot | mobile-ios | mobile-android | desktop
   (macos|windows|linux|chrome os|chromium os|ubuntu) | unknown`. Excepciones → `unknown`.
3. **session_id** (sustituye al "user_signature" del plan inicial — NO existe ese campo):
   `sha256_hex(salt:site_uuid:ip:user_agent)`, salt aleatorio de 64 hex por (día UTC,
   site_uuid), IP = primera de `X-Forwarded-For` (trustProxy). Mismo usuario+UA el mismo
   día = misma sesión.
4. **timestamp**: se descarta el del cliente; se usa la hora del SERVIDOR de ingesta
   (`serverReceivedAt`). `meta.received_timestamp` = del header `x-ghost-analytics-start`.
5. **referrer**: `@tryghost/referrer-parser` SOLO si `parsedReferrer.url` es truthy →
   `referrerUrl/referrerSource/referrerMedium` (si no, ausentes). El `source` que agrupan
   los pipes se normaliza ADEMÁS en las vistas (mapa Facebook/Twitter/Reddit/Gmail/…
   → `domainWithoutWWW(referrer)`) — ver mv_hits en ghost-side.md §5.5.
6. **Sin geolocalización server-side** (0 MaxMind). `location` = país del navegador.
7. HMAC opcional del navegador (HMAC-SHA1 sobre URL+t) solo si se configura — MVP: omitir.

### 2.3 El evento almacenado (formato interno tras el collector)

Raíz: `timestamp` (servidor), `action:'page_hit'`, `version:'1'`, `site_uuid`,
`session_id` (la firma), `payload{...}` con TODO lo anterior + `event_id`, `os`, `browser`,
`device`, `referrerUrl/Source/Medium?`, `meta.received_timestamp`, `user-agent` (¡key con
guión!), y `site_uuid` duplicado dentro del payload.

**Trampas de tipos (crítico al ingerir)**: strings que llegan como `null`, `""` o ausentes;
`member_uuid`/`post_uuid`/`member_status` = string literal `"undefined"`; `post_type` =
string `"null"`; `event_id` puede no ser UUID válido. Tolerancia máxima al parsear.

**Entrega at-least-once** (si se usa TrafficAnalytics delante): batches de 50/1000 ms con
nack+redelivery completos → **dedup por `event_id`** recomendable en GhostBird
(vía `INSERT OR IGNORE` con UNIQUE index). Hipótesis razonable: para eso existe event_id.

## 3. Queries — contrato exacto

### 3.1 HTTP

```
GET /v0/pipes/{name}.json?site_uuid=...&date_from=YYYY-MM-DD&date_to=...&timezone=Etc/UTC&...
Authorization: Bearer <JWT>
→ 200 {"data":[{fila},...]}     (Ghost SOLO consume .data; meta/statistics ignorados)
```

- `date_from`/`date_to` default = últimos 7 días. `timezone` IANA. `limit`(50)/`skip`(0).
  Arrays como CSV (`member_status=free,paid`, `post_uuids=u1,u2`).
- Timeout cliente Ghost: 10 s, sin reintentos. Cualquier 4xx/5xx → Ghost trata como "sin
  datos" (mejor `{data:[]}` que 500 cuando haya respuesta parcial).
- Alias `_v2`/`_v3` de los pipes: están APARCADOS en Ghost ("not currently in use");
  implementar los 13 v1 sin sufijo basta (aliases opcionales por tolerancia).

### 3.2 Pipes y sus columnas de salida

| Pipe | Salida (`data[]` fila tipo) | Semántica clave |
|---|---|---|
| `api_top_pages` | `{post_uuid, pathname, visits}` | visits = sesiones únicas (uniqExact session_id) que vieron la página; `post_uuid=''` si no aplica |
| `api_post_visitor_counts` | `{post_uuid, visits}` | all-time, sin fechas; `post_uuids` CSV |
| `api_kpis` | `{date, visits, pageviews, bounce_rate, avg_session_sec}` | serie COMPLETA con ceros; diaria u horaria (si date_from==date_to); bounce_rate fracción 0-1 truncada a 2; pagos: `paid`→+`comped`+`gift` |
| `api_active_visitors` | `{active_visitors}` | sesiones únicas en últimos 5 min; poll 60 s |
| `api_top_sources` | `{source, visits}` | visits = nº de SESIONES por source del primer hit; `''` = directo |
| `api_top_locations` | `{location, visits}` | location = código país ISO; uniqExact(session_id) |
| `api_top_devices` | `{device, visits}` | count() sesiones por device del primer hit |
| `api_top_utm_sources/mediums/campaigns/contents/terms` | `{utm_X, visits}` | count() sesiones cuyo primer hit tenga ese UTM no vacío |
| `api_gift_link_visits` | `{gift_link, visits, views, last_seen}` | gift_link = match EXACTO del token; aquí date_from/date_to SÍ filtran directo |

### 3.3 `filtered_sessions` — la semántica delicada

Los pipes "top_*" y kpis filtran en 2 etapas (corazón del modelo Tinybird):

1. **Hit-level**: sesiones con AL MENOS UN hit que cumpla fechas (`>= date_from`,
   `< date_to + 1 día`), member_status (con expansión paid), location, pathname, post_uuid,
   gift_link (booleano: `'false'`/`'0'` → `gift_link=''`).
2. **Session-level** (solo si llega source/device/utm_*): por atributos del PRIMER hit de
   la sesión + rango sobre first_pageview.

Y luego los endpoints devuelven **TODOS los hits de las sesiones que clasifican**: filtrar
por `utm_medium=social` responde "¿qué páginas vieron las sesiones que llegaron con
utm_medium=social?" — la suma de visits entre páginas puede superar el nº de sesiones.
Equivalente SQLite: vista/tabla de sesiones (primer hit por session_id con argMin-like:
`ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY timestamp)`), JOIN contra hits.

### 3.4 JWT (más simple de lo esperado)

Ghost **auto-firma** el JWT con `jsonwebtoken`: **HS256**, secreto = el string
`tinybird.adminToken` del config (compartido, no emitido por Tinybird). Payload:

```jsonc
{
  "workspace_id": "<workspaceId>", "name": "tinybird-jwt-<site_uuid>",
  "exp": <now+180min, sin iat (noTimestamp)>,
  "scopes": [ {"type":"PIPES:READ","resource":"api_kpis","fixed_params":{"site_uuid":"<uuid>"}}, ... 25 ]
}
```

GhostBird debe validar: firma HS256 con el mismo secreto compartido, `exp`, que exista
scope con `resource == pipe pedido` y `fixed_params.site_uuid == query.site_uuid`
(discrepancia → 403; es el aislamiento multi-tenant). Fallback: tokens estáticos
(`stats.token` / `stats.local.token`) aceptados como Bearer opaco.

## 4. Config de Ghost para apuntar a GhostBird

```json
{
  "tinybird": {
    "workspaceId": "ghostbird",
    "adminToken": "<secreto compartido con GhostBird>",
    "tracker": { "endpoint": "https://gb.example.com/api/v1/page_hit", "datasource": "analytics_events" },
    "stats": {
      "endpoint": "http://ghostbird:8080",
      "endpointBrowser": "https://gb.example.com"
    }
  }
}
```

`tracker.endpoint` + (`workspaceId` && `adminToken` | `stats.local.enabled`) activa
analytics (`isWebAnalyticsConfigured`). El setting BD `web_analytics` (default true) es el
interruptor de UI. La config de Tinybird NO es editable por Admin API (solo fichero/env).

## 5. Correcciones al plan inicial de fases

| Plan inicial (hipótesis) | Realidad verificada |
|---|---|
| `user_signature` en el schema | NO existe: es `session_id` (raíz), calculada por el collector |
| Ghost routea el page_hit | El navegador POSTea DIRECTO al collector (tracker.endpoint); Ghost OSS no tiene handler |
| Campo `location` con geo server | `location` la manda el navegador (país por timezone); 0 MaxMind |
| Campos browser/os/device del evento crudo | Los calcula el collector (ua-parser-js + derivación de device) |
| Pipes `_v2` a implementar | Aparcados ("not in use"); implementar los 13 v1 sin sufijo |
| JWT emitido por Tinybird | Ghost lo auto-firma HS256 con adminToken compartido |
| Dashboard pide vía Ghost | El Admin React consulta el backend DIRECTAMENTE desde el navegador (endpointBrowser) |
| — (no previsto) | CORS obligatorio (collector + pipes, navegadores cross-origin) |
| — (no previsto) | SSRF guard de Ghost en producción bloquea IPs privadas (flujo server-side) |
| — (no previsto) | at-least-once → dedup por event_id |
