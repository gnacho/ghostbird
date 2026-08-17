# GhostBird

A free, self-hosted drop-in replacement for the Tinybird backend behind
Ghost's native analytics.

One Go binary. One SQLite file. Your analytics never leave your server.

**Live landing page:** [ghostbird.cloudless.club](https://ghostbird.cloudless.club/) — screenshots, feature summary and the one-liner installer.

**Status**: working end to end against Ghost 6.x (tested with Ghost
6.57.1). See [Verified](#verified-against-the-real-thing). Licensed
AGPL-3.0, forever.

---

## The gap this fills

Ghost 6 moved its native analytics to a hosted Tinybird workspace. If you
self-host Ghost, that decision landed on you in an awkward way:

- The analytics tab in your admin panel talks to Tinybird's cloud API.
  Tinybird is a commercial, proprietary service.
- Without it, the dashboard shows nothing. The settings page calls it
  "not configured".
- If you point it at Tinybird, every pageview your visitors generate is
  shipped to a third-party cloud, signed with a token you do not rotate.

So the most privacy-conscious publishing platform on the web ended up
with an analytics pipeline that self-hosters either break or surrender.
The Ghost side is open source. The data side is not. That asymmetry is
the gap.

GhostBird fills it. It speaks the exact protocol Ghost already speaks:
the events API the tracker posts to, the query API the dashboard reads
from, the JWT authentication Ghost signs on its own. Ghost does not know
anything changed. You change two URLs in a config file and the dashboard
you already have keeps working, fed by a process on your own machine.

## The pledge

- **AGPL-3.0, forever.** Not "open core". No feature is ever held back
  for a paid tier, because there is no paid tier and there will not be
  one.
- **Community driven.** Issues, patches and roadmap decisions happen in
  the open. The project is useful to its author's production sites on
  day one, so it is maintained, not abandoned.
- **Your data stays put.** No telemetry, no phone-home, no "anonymous
  usage statistics". The binary makes zero outbound connections except
  the ones you configure (and by default, none).
- **Honest metrics.** The counting rules are not invented here: they are
  the same rules the original service uses, verified by running the
  original provider's own test suite (see below).

## What it is, what it is not

**It is** a backend. A small server that ingests pageviews and answers
the queries of the Ghost admin dashboard you already have.

**It is not** another analytics panel. If you want a standalone
dashboard with its own charts and UI, use Umami, Plausible or
GoatCounter (all excellent, two of them also AGPL). Those tools answer
"what happened on my site" with a new interface to learn. GhostBird
answers a different question: "how do I keep the analytics tab of my
Ghost install working without a cloud vendor". If you run both, they do
not compete; they cover different needs. GhostBird additionally counts
things the generic tools do not model: member status (free, paid,
comped, gift), per-post attribution and gift-link usage.

## How it works

```
visitor's browser      GhostBird (1 binary)              your server
─────────────────      ─────────────────────              ───────────
ghost-stats.min.js ──► collector  POST /api/v1/page_hit
                       bots filtered, sessions signed,
                       referrers parsed  ──► SQLite (WAL)
                                   │
Ghost admin panel  ──► pipes  GET/POST /v0/pipes/*.json
(browser, JWT that     JWT HS256 verified, queries served
 Ghost signs itself)   from an aggregated sessions table

Ghost server        ──► same pipes, server-side (top content,
                          post visitor counts)
```

Ghost signs its own JWTs with a shared secret you set on both sides
(`adminToken`). The token's scopes pin each query to your site's UUID,
so one GhostBird instance can serve several Ghost sites with clean
isolation.

## Technical brief

- **Single static binary**, about 11 MB, CGO-free. Cross-compiles to
  amd64 and arm64. RAM footprint measured under 10 MB in production.
- **SQLite** as the only required state, in WAL mode with a single
  writer connection. A `sessions` aggregate table (the equivalent of the
  original's materialized view) is maintained incrementally on ingest,
  so dashboard queries stay fast no matter how old your data is.
  PostgreSQL is a possible future path, not a present need.
- **Contract faithful, not approximate.** The 13 query endpoints
  reimplement the exact semantics of the original pipes, including the
  two-stage session filtering, the daily and hourly KPI series with
  zero-filled gaps, member status expansion, and timezone handling.
  The upstream provider's official YAML test suite (shipped under
  license in `internal/pipes/testdata/`) runs green in this repo against
  the same fixture data.
- **Integrated collector.** The piece that receives hits from visitors'
  browsers is inside the same binary: bot filtering, user-agent
  parsing, referrer classification (a port of the original 2,381-domain
  reference table), and per-day, per-site session hashing. No Node.js
  sidecars, no message queues, no container zoo.
- **Operable by one person.** Verified daily backups (`VACUUM INTO`,
  quick-checked, rotated), configurable retention, a write-probe
  healthcheck that fails loudly when the disk is full, slow-query
  logging, and a `/metrics` endpoint in Prometheus text format.

## Install

Once the repository is public, the one-liner:

```sh
curl -fsSL https://raw.githubusercontent.com/gnacho/ghostbird/main/install.sh | sh
```

The script detects your platform, downloads a release binary with
mandatory sha256 verification, creates a dedicated system user, writes a
hardened systemd unit (sandboxed, `ProtectSystem=strict`, memory cap)
and a random admin token shown exactly once. It is idempotent: upgrades
keep your data, port and token. See `install.sh --help` for options
including `--uninstall` and `--purge`.

From source:

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ghostbird ./cmd/ghostbird
./ghostbird -addr 127.0.0.1:18181 -db /var/lib/ghostbird/ghostbird.db
```

### Point Ghost at it

Serve the binary behind nginx on the same domain as your site (the
visitor's browser and your admin's browser both call it cross-origin,
so keep it under your site's hostname and pass the client IP through):

```nginx
location /ghb/ {
    limit_req zone=ghb_ingest burst=60 nodelay;   # 30r/s zone, anti junk
    proxy_pass http://127.0.0.1:18181/;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
}
```

Then in Ghost's `config.production.json`:

```json
{
  "tinybird": {
    "workspaceId": "ghostbird",
    "adminToken": "THE SAME SECRET you passed as -admin-token",
    "tracker": {
      "endpoint": "https://YOUR-SITE/ghb/api/v1/page_hit",
      "datasource": "analytics_events"
    },
    "stats": {
      "endpoint": "http://127.0.0.1:18181",
      "endpointBrowser": "https://YOUR-SITE/ghb"
    }
  }
}
```

Restart Ghost (it caches the tracker in memory). The analytics tab now
renders from your server.

Note for strict production setups: Ghost's own HTTP client refuses
private-range IPs in `env: production`, so `stats.endpoint` should be a
resolvable hostname (or the same public `/ghb/` path). With
`env: development` localhost works as-is.

### Endpoints

| Route | Purpose |
|---|---|
| `POST /api/v1/page_hit` | Collector called by visitors' browsers. Bots get the same 202 and are dropped. |
| `POST /v0/events` | Events API, for compatibility with the official TrafficAnalytics collector if you prefer to run one. NDJSON, deduplicated by event id. |
| `GET/POST /v0/pipes/{name}.json` | The 13 query endpoints the dashboard consumes. JWT HS256 (Bearer or `?token=`), a static token, or a GoatCounter API token. |
| `GET /healthz` | Read probe plus a write probe (`last_write_ok_sec`): 503 when the database cannot accept writes. |
| `GET /metrics` | Prometheus text format. Ingest counters, per-pipe latency, database sizes. |

## Shared identity with GoatCounter (optional)

If you also run [GoatCounter](https://github.com/arp242/goatcounter), point
GhostBird at its database and GoatCounter API tokens become a third way to
authenticate pipe reads:

```sh
./ghostbird -goatcounter-db /path/to/goatcounter/db.sqlite3 ...
```

Rules (mirroring GoatCounter's own semantics, from its source):

- The token must exist in GoatCounter's `api_tokens` and carry the
  "Read statistics" permission (bit 64).
- A token scoped to `[-1]` (all sites) can read any site's pipes; the query
  must name the `site_uuid` explicitly.
- A token scoped to specific site IDs can only read the Ghost sites mapped
  to them in the `gc_site_map` table (migration v4). If the token maps to
  exactly one site, the `site_uuid` can be omitted and is injected.
- Lookups are read-only (`mode=ro` + `PRAGMA query_only`) and cached in
  memory: 5 minutes for valid tokens, 30 seconds for unknown ones.
- Once any authentication mechanism is configured (JWT secret, static token
  or GoatCounter), the open local mode is disabled: fail closed.

The mapping is just rows; adapt it to your setup:

```sql
INSERT INTO gc_site_map (gc_site_id, site_uuid)
VALUES (10, '507a78ce-6b95-4913-884b-18138db8f048');
```

One identity system for your whole analytics stack: the same token that
reads GoatCounter reads GhostBird, with the same permission model.

## Verified against the real thing

Two kinds of verification, both reproducible from this repo:

1. **Fidelity suite.** The upstream provider publishes a YAML test suite
   with exact expected outputs per query endpoint. Those cases (all of
   them) plus their fixture dataset are vendored in
   `internal/pipes/testdata/` and run as ordinary Go tests: same input,
   same output, including tie-order tolerance.
2. **End to end.** A real browser visiting a real Ghost 6.57.1 site
   produces events in the database, and the admin dashboard renders
   visitors, sessions and sources from GhostBird. This included catching
   a detail invisible to any spec reading: the dashboard's chart library
   calls the query API with POST and the token in the query string, not
   as a Bearer header.

The project also went through a three-lens audit (security, SRE,
latent bugs). Two P1 findings were fixed test-first: an expired-token
acceptance path and a broken "direct traffic" filter. The audit trail
lives in the commit history.

## Roadmap

- ✅ **v0.5.0 released** — public repository, tagged releases and a live landing page at [ghostbird.cloudless.club](https://ghostbird.cloudless.club/).
- CI/CD workflows for tests and releases.
- Optional PostgreSQL backend for very high traffic sites.
- Rate limiting and per-site quotas in-process (nginx covers it today).
- Localization of the (minimal) operator-facing strings.

## Credits

- The query semantics, test suite and fixture data come from
  [Ghost](https://github.com/TryGhost/Ghost) (MIT).
- The collector behavior (bot rules, session hashing, referrer
  classification table) replicates
  [TrafficAnalytics](https://github.com/TryGhost/TrafficAnalytics) and
  `@tryghost/referrer-parser` (MIT).
- The single-binary-and-a-database philosophy follows
  [GoatCounter](https://github.com/arp242/goatcounter)'s example.

GhostBird is an independent project. It is not affiliated with, endorsed
by, or connected to the Ghost Foundation or Tinybird.

## License

AGPL-3.0. See [LICENSE](LICENSE). The vendor credits above keep their
own licenses. If you run a modified GhostBird on a public server, the
AGPL asks you to offer your changes back to everyone: that clause is
the point, not a footnote.
