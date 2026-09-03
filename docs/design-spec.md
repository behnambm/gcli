# Grafana CLI (`gcli`) — Design Spec

Date: 2026-08-30
Status: Approved in brainstorming (all 5 sections)

## Live Instance Findings (probed 2026-08-30)

Grafana **10.4.3** at a company-internal instance. Service account token = read-only.

**Datasource inventory** (12 total):

| Type | Count | Names | Notes |
|------|-------|-------|-------|
| `prometheus` | 9 | Metrics Alpha … Metrics Iota (anonymized in fixture) | All VictoriaMetrics backends register as prometheus type. `?ds_type=prometheus` + per-query `datasource` object works |
| `grafana-postgresql-datasource` | 1 | PostgreSQL Metrics | `rawSql` + `format:"table"` works |
| `victoriametrics-logs-datasource` | 1 | Logs | **Logs backend — NOT Loki.** LogsQL. Works |
| `victoriametrics-datasource` | 1 | Legacy Metrics | **Dead:** empty URL, plugin returns 404 `plugin.notRegistered` on `/api/ds/query` |

**Probed API behavior:**

- `/api/ds/query` requires datasource identity **inside each query object** (`queries[].datasource.{type,uid}`), not top-level. Top-level gets 400 `query.invalidDatasourceId`.
- Response envelope: `{"results":{"<refId>":{"status":200,"frames":[...]}}}`. Errors per-refId: `{"error":"...","errorSource":"downstream","status":500,"frames":[]}` — surfaced with refId.
- Prometheus instant → `numeric-multi` frames; range → `timeseries-multi` (`custom.resultType: matrix`), fields Time+Value, labels on field.
- VictoriaLogs: fields `expr`, `queryType` (`instant`|`range`|`stats`), `limit` (**must be number, not string** — string ignored, default 1000). Range returns Time/Line/labels(`json.RawMessage`) frames; stats returns aggregate Time/Value frames. Plugin translates to `/select/logsql/query` upstream.
- Postgres: `format:"table"` → frames with typed fields (`int32`, etc.), no time field.
- Alerts: **`/api/v2/alerts/statuses` returns 404** on this instance (RBAC/disabled). Primary = `/api/prometheus/grafana/api/v1/rules` (works: rule groups, per-alert `state`, `activeAt`, labels, annotations). Rule definitions = `/api/ruler/grafana/api/v1/rules` (expr, for, severity labels, `grafana_alert.id`). States seen: `inactive`, `Normal (Error)` — parse state prefix before ` (`.
- `/api/datasources/uid/:uid/health` works: `{"status":"OK","details":{...}}`.
- Dashboards search (`/api/search`), annotations (`/api/annotations`) work. Annotations include unified-alert state-change entries (`alertId`, `newState`/`prevState`, `data.values`).
- `/api/user` returns all-null for SA tokens — do not rely on it for anything.

## Purpose

A generic CLI for reading data from the company's Grafana instance using a service-account token. Reads metrics (Prometheus), logs (Loki), SQL datasources, and Grafana's own state (datasources, dashboards, alerts, annotations). Designed as a personal tool first, with a future path to internal rollout for other teams.

## Locked Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Language | **Go** | Single static binary, zero runtime deps — best distribution story for future team use |
| Config | **Env vars only**: `GRAFANA_URL`, `GRAFANA_TOKEN` | Simplest; `--url`/`--token` flags override for one-off calls |
| Token scope | **Read-only** | Tool has no write commands; no write endpoints touched |
| Architecture | **Generic core + helpers** | Raw `/api/ds/query` pass-through works with any datasource day one; typed helpers cover the common paths |
| Binary name | **`gcli`** | Grafana's own server-side admin tool is already `grafana-cli`; avoid collision |
| Usage mode | Interactive-first with `--json` for scripting | Table default when TTY, machine formats for automation |

## Command Surface

```
gcli query <datasource> --json '<raw payload>'        # generic: any datasource, raw /api/ds/query JSON
gcli prom <datasource> 'expr' [--from --to --step]    # Prometheus instant/range
gcli logs <datasource> 'logsql' [--from --to --limit --mode instant|range|stats]  # VictoriaLogs (LogsQL); generic engine also serves Loki if ever added
gcli sql  <datasource> 'SELECT ...' [--from --to]     # SQL datasources

gcli datasources        # list: name, uid, type, url, health
gcli dashboards [query] # search; --get <uid> for full JSON export
gcli alerts             # current alert states (Grafana 10+ v2 API, version-detected)
gcli annotations [--dashboard --tags --from --to]
gcli health             # Grafana ping + per-datasource health probe
gcli version            # Grafana version + build info
gcli capabilities       # probe what this token can access (command-group level)
gcli help               # full setup + usage guide: env vars, token creation, every command with examples, output formats, exit codes, permission notes
```

`gcli help` prints a comprehensive embedded guide (go:embed — one source file `internal/cmd/help.txt`), covering: install, `GRAFANA_URL`/`GRAFANA_TOKEN` setup, how to create a service-account token in the Grafana UI, command reference with examples, output formats, exit codes, permission/RBAC notes. `gcli help <command>` falls through to cobra's per-command help.

Global flags: `--output table|json|csv`, `--url`, `--token`, `--no-color`, `--verbose`, `--timeout` (default 30s).

Datasource identified by uid or name (name resolved via `/api/datasources`).

## Query Engine

- Everything goes through **`/api/ds/query`** — Grafana's unified datasource proxy. Same endpoint for Prometheus, Loki, SQL, Tempo, and any future datasource. Payload shape: `{"queries":[{...refId...}], "from":"...", "to":"...", "datasource":{...}}`.
- **Datasource resolution**: input uid or name → `/api/datasources` lookup → type+uid in request. `gcli datasources` shows the catalog.
- **Time range parsing**: Grafana-compatible relative syntax (`now-1h`, `now-1d/d`, `now-5m`) plus absolute RFC3339. Default `now-1h → now`. Unit-test heavy (DST, `now-1d/d` truncation).
- **Helpers** build the payload:
  - `prom`: instant query vs range query — range when `--step` given (`instant: true/false`, `range: true`, `interval` fields).
  - `logs`: targets `victoriametrics-logs-datasource` — `expr` (LogsQL), `queryType: instant|range|stats` (`--mode`, default `range`), `limit` sent as **number** (gotcha: string silently ignored → 1000 default). Generic `query` covers Loki/other log sources if ever added.
  - `sql`: `rawSql` field + `format:"table"`; `$__timeFrom`/`$__timeTo` macro values passed when `--from/--to` given.
- **Raw `query` command**: user passes complete `queries[]` JSON; tool fills only datasource uid + time range unless present. `--json @file.json` for complex payloads.
- **Response normalization**: every datasource type returns different frame formats (Prometheus matrix/vector, VictoriaLogs streams/stats, SQL tables). Engine converts all to one internal shape:

```
frames: [{columns: [], rows: []}]
```

Renderers only know this shape.

## Grafana-Internal Commands (read-only APIs)

- `gcli datasources` — GET `/api/datasources`. Table: name, uid, type, url, isDefault. `--json` = full payload.
- `gcli dashboards` — GET `/api/search` (query/folder filters). `--get <uid>` fetches full model via `/api/dashboards/uid/:uid`; `--export` writes provisioning-format JSON to file (backup use).
- `gcli alerts` — primary: `/api/prometheus/grafana/api/v1/rules` (rule groups, per-alert `state`, `activeAt`, labels, annotations). Merged with `/api/ruler/grafana/api/v1/rules` for definitions (expr, for, severity). States include `Normal (Error)` / `NoData (Error)` variants — strip suffix before comparing. Fallbacks: `/api/v2/alerts/statuses` if present (404 on this instance), legacy `/api/alerts` for Grafana <9. Table: name, state, severity, folder, labels, activeSince. `--firing` filter shows only Alerting/Pending/Error.
- `gcli annotations` — GET `/api/annotations` with `tags`, `dashboardUID`, `from/to`. Table: time, text, tags, source.
- `gcli health` — GET `/api/health` + `/api/datasources/uid/:uid/health` per datasource. First-run smoke test command.
- `gcli version` — probe version/build; cheap since version probe happens anyway.

## Output, Errors, Config

**Rendering** (all commands emit internal frames shape):
- `table` (default): aligned columns, truncation to terminal width, local-tz timestamps. Color only on TTY.
- `json`: `meta` (datasource, query, time range, duration) + `data`. jq-friendly.
- `csv`: first frame, or `--frame N` to pick.

**Errors** — no stack traces by default:
- Config errors (missing env vars): 2-line fix hint.
- HTTP errors: status + Grafana error body, mapped to exit codes 1–5; `--verbose` adds request dump.
- Query errors: Grafana returns per-refId error responses (`error` + `errorSource` + `status`); surfaced with refId + message. Downstream failures (e.g. backend connection refused) arrive as 200 HTTP with per-refId error — must inspect `results[]`, not just HTTP status. `plugin.notRegistered` 404 (dead datasource) gets dedicated hint: "plugin not registered — datasource may be broken/removed".

**Config/security**:
- Token in `Authorization: Bearer`, never logged; `--verbose` redacts it.
- Default timeout 30s, `--timeout` flag. Responses are bounded JSON; no streaming.

## Permission Variance (tokens differ by role)

Tokens vary: personal tokens may be limited; devops team tokens may have full permissions. Tool must handle both:

- **Error mapping by HTTP status**: 401 → "invalid/expired token"; 403 → "permission denied — token role lacks access to <endpoint>"; 404 on a known-good endpoint → "endpoint missing — either Grafana version lacks it or token permissions hide it" (Grafana RBAC returns 404 for hidden resources; cannot distinguish, say both).
- **Per-command degradation**: alerts command chain = rules endpoint (works for limited tokens, probed) → optional `/api/v2/alerts/statuses` enrichment when accessible → legacy `/api/alerts` for Grafana <9. Datasource listing naturally shows only datasources the token may access.
- **`gcli capabilities`**: one probe pass over all command groups (ds/query, datasources, dashboards, alerts, annotations, health), reports per group: OK / DENIED / MISSING. Teams use this to diagnose their token before filing issues. Exit 0 even when some groups denied (that's the point of the command).
- **Never hard-fail on unknown shape**: full-permission tokens may see extra API fields or extra alert states — parser ignores unknown JSON fields everywhere.

## Testing + Repo Placement

**Repo layout** — new dir `grafana-cli/` at repo root, self-contained Go module, matching the repo's per-service dir + README pattern. `grafana-cli/README.md` = canonical state doc (usage, endpoints, gotchas) like other dirs.

**Dependencies** — minimal: `cobra` for commands, stdlib `net/http`, `text/tabwriter` for tables if sufficient (decide at plan time; avoid heavy table libs).

**Testing** — no live Grafana needed:
- Unit: time-range parser, response normalizers (fixtures per datasource type: Prometheus matrix/vector, VictoriaLogs range/stats, SQL frames).
- `testdata/` holds real anonymized API responses captured from a live instance (captured 2026-08-30, real values scrubbed).
- Integration: `httptest.Server` fake Grafana implementing used endpoints; table-driven command tests against recorded fixtures.
- Version compat: fixtures for Grafana 10 path + legacy alerts path; no compile-time pinning.

**Future team rollout** — README has `go install` / prebuilt binary instructions. Single binary + two env vars = zero-config onboarding.

## Non-Goals

- Write operations (token is read-only): no annotation creation, no alert silencing, no dashboard provisioning.
- No streaming/tailing logs (bounded JSON responses only).
- No dashboards-as-code or diffing (export only).
- No multi-instance profiles (env vars only; revisit if second Grafana appears).
- No caching layer (revisit if teams hammer the API).
