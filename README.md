# gcli — read-only Grafana CLI


Generic CLI for reading data from a Grafana instance. Reads metrics (Prometheus/VictoriaMetrics), logs (VictoriaLogs), SQL (PostgreSQL) and Grafana state (datasources, dashboards, alerts, annotations). **Read-only — no write endpoints, ever.**

Built for Grafana 10.4.x. Works with limited (personal) tokens and full-permission tokens; `gcli capabilities` diagnoses which commands a given token can use.

## How to install

Single command — downloads the latest release binary (macOS or Linux, amd64/arm64) and installs it:

```bash
curl -sSL https://github.com/behnambm/gcli/releases/download/latest/install.sh | sh
```

Installs to `/usr/local/bin` (falls back to `~/.local/bin` if not writable). Prefer to inspect before running? `curl -sSLo install.sh https://github.com/behnambm/gcli/releases/download/latest/install.sh && less install.sh`.

## Install (build from source)

```bash
git clone git@github.com:behnambm/gcli.git
cd gcli
make install        # builds and installs /usr/local/bin/gcli
# or: make build    # binary at ./gcli
```

Requires Go 1.27+. Makefile targets: `make build`, `make test`, `make vet`, `make install` (clean with `make clean`). `gcli --version` prints the tool version (git tag, or `dev` when built without tags); `gcli version` is the Grafana server version.

## Setup

```bash
export GRAFANA_URL=https://your-grafana.example.com
export GRAFANA_TOKEN=glsa_...   # service-account token (UI: Administration → Service accounts)
```

Run `gcli help` for the full embedded guide (setup, every command with examples, output formats, exit codes, permission notes).

## Profiles (multiple Grafana instances)

`gcli profiles add prod` (interactive) stores connections in
`~/.config/gcli/profiles.yaml` (chmod 600). Select with `--profile`,
`GCLI_PROFILE`, or the `default:` marker. Legacy `GRAFANA_URL` /
`GRAFANA_TOKEN` env vars keep working. See `gcli help` for the full guide.

## Commands

| Command | Purpose |
|---------|---------|
| `gcli query <ds> --json '...'` | raw `/api/ds/query` object or array — ANY datasource type |
| `gcli prom <ds> '<promql>' [--step 1m]` | Prometheus instant/range queries |
| `gcli logs <ds> '<logsql>' [--mode range\|instant\|stats]` | VictoriaLogs (LogsQL) |
| `gcli sql <ds> '<sql>'` | SQL datasources, `$__timeFrom/$__timeTo` macros |
| `gcli metrics <ds> [pattern]` | metric name discovery (Prometheus-type) |
| `gcli labels <ds> [metric]` / `gcli values <ds> <label>` | label browsing |
| `gcli tables <ds>` / `gcli columns <ds> <t>` | SQL schema introspection |
| `gcli datasources` | list datasources (uid/name/type/url) |
| `gcli dashboards [q]` / `--get <uid>` / `--export f` | search / fetch / export dashboards |
| `gcli alerts [--firing]` | alert states (unified alerting, fallback chain) |
| `gcli alert <name>` | one alert's definition + current state |
| `gcli panels <uid> [--queries]` | dashboard panel query mining |
| `gcli annotations [--tags --dashboard --from --to]` | annotations incl. alert state changes |
| `gcli health` | Grafana + per-datasource health |
| `gcli version` | Grafana version/build |
| `gcli capabilities` | what this token can access, per group |
| `gcli update` | replace self with the latest release binary (checksum-verified, progress bar) |
| `gcli uninstall [--yes]` | remove the gcli binary (confirmation prompt unless `--yes`) |

Global flags: `--url`, `--token`, `-o table|json|csv`, `--frame N`, `--timeout`, `--no-color`, `-v`.

## Design notes (state doc)

- **Generic engine**: everything goes through `/api/ds/query` (Grafana's unified datasource proxy). Datasource identity must live inside each query object; path takes `?ds_type=<type>`. The `query` command is the escape hatch for any future datasource.
- **VictoriaLogs gotcha**: `limit` must be a JSON number — the plugin silently ignores string values (defaults to 1000).
- **Alerts**: `/api/v2/alerts/statuses` returns 404 for limited tokens (RBAC hides it). Primary endpoint = `/api/prometheus/grafana/api/v1/rules`; fallbacks v2 statuses → legacy `/api/alerts`. Alert detail merges `/api/ruler/grafana/api/v1/rules`.
- **Errors**: per-refId query errors come back as HTTP 200 with `results.<refId>.error` — the engine inspects results, not just status codes.
- **Permissions**: tokens vary by role. Grafana returns 404 for both "endpoint missing" and "permission hidden". `gcli capabilities` diagnoses; error hints explain.
- **Metrics discovery** reads through the datasource proxy (`/api/datasources/proxy/uid/:uid/api/v1/...`) — GET-only, read-only.
- **Provisioning export**: `--export` strips server-assigned id/version and the meta envelope.
- **Shell completion**: `gcli completion bash|zsh|fish`.
- **Testing**: hermetic — `httptest` fakes + anonymized recorded fixtures in `testdata/`. No live Grafana needed. `go test ./...`.

## Design history

- [Design spec](docs/design-spec.md) — full architecture + live-instance findings that shaped v1
- [Implementation plan v1](docs/plan-v1.md) — 19 TDD tasks, runnable code per step
- [Implementation plan v2](docs/plan-v2.md) — features, gap fixes, housekeeping (13 tasks)
