# gcli Genericization — Multi-Profile + Onboarding + Brand Neutrality

Date: 2026-09-03
Status: Approved in brainstorming (design sections 1–5, research findings folded in)

## Purpose

Team lead wants `gcli` usable by all teams, not a personal tool for one Grafana instance. Scope agreed in brainstorming:

1. **Multi-instance profiles** — teams point at different Grafana instances/orgs.
2. **Onboarding UX** — non-power users can set up without editing env vars by hand.
3. **Brand neutrality** — no company-specific names, hosts, or datasources anywhere in the repo.

Explicitly **out of scope**: write operations (read-only invariant stands), streaming/log tailing, caching, token encryption beyond file perms.

## Research Findings (folded in)

Surveyed existing Grafana CLIs ([monobilisim/grafana-cli](https://github.com/monobilisim/grafana-cli), [matiasvillaverde/grafana-cli](https://github.com/matiasvillaverde/grafana-cli), [m110/grafcli](https://github.com/m110/grafcli)):

- **Active-profile pattern is the convention**: both Go tools use `config use <name>` / `context use <name>`. Our `profiles use` matches.
- **Basic auth is expected**: monobilisim supports `--user --pass`. Teams without service accounts use user/pass. We add it.
- **Auth diagnostics**: matiasvillaverde has `auth doctor`; our `profiles test` covers the same ground.
- **Keyring storage**: matiasvillaverde uses OS keyring. We stay with chmod-0600 file + warning (keeps cobra-only dependency rule). Revisit if compliance demands.

## 1. Profiles Subsystem

### Config file

- Path: `~/.config/gcli/profiles.yaml` (XDG standard).
- chmod 0600 on every write. If file exists with looser perms and contains tokens, print warning: `profiles.yaml is world-readable; run: chmod 600 ~/.config/gcli/profiles.yaml`.
- Shape:

```yaml
default: prod
profiles:
  prod:
    url: https://grafana.example.com
    token: glsa_xxx
    # optional:
    orgId: 1                 # sent as X-Grafana-Org-Id header
    defaultDatasource: my-prom
  team-b:
    url: https://grafana.example.org
    user: alice              # basic auth — mutually exclusive with token
    pass: hunter2            # sent as Authorization: Basic base64(user:pass)
```

- `token` XOR (`user`+`pass`): validation error listing both if both set.
- Unknown yaml keys → error (catches typos like `tokn:`).
- `default:` names a profile; omitted = no default.

### Resolution precedence

Highest wins:

1. `--profile <name>` flag (global)
2. `GCLI_PROFILE` env var
3. `default:` marker in profiles.yaml
4. `GRAFANA_URL` / `GRAFANA_TOKEN` env vars (legacy path, unchanged behavior)
5. Error: 2-line hint pointing at `gcli profiles add` and `gcli help`

`--url` / `--token` flags still override everything (one-off calls, current behavior preserved).

### Auth header construction

- Token → `Authorization: Bearer <token>`
- user/pass → `Authorization: Basic base64(user:pass)`
- orgId → extra header `X-Grafana-Org-Id: <id>`
- Redaction: token/pass never printed by any command; `--verbose` request dumps already redact — extend to cover Basic header and pass field.

## 2. Command Surface

New command group `gcli profiles`:

```
gcli profiles add <name>          # interactive: prompts url, auth (token OR user/pass), optional orgId, default datasource.
                                  #   token/pass prompt hidden (no echo).
                                  # flags for scripting: --url --token --user --pass --org-id --default-datasource --set-default
gcli profiles list                # table: name, url, auth method (token|basic), default marker, orgId.
                                  #   NEVER prints token or pass.
gcli profiles use <name>          # sets default: marker; error if name unknown (with list of known names)
gcli profiles remove <name>       # deletes profile; refuses if it is the yaml default unless --force.
                                  #   GCLI_PROFILE env pointing at a removed profile: next command
                                  #   errors with the standard 2-line config hint.
gcli profiles test [name]         # smoke test: GET /api/health + version probe; prints OK/FAIL + reason; exit 0 only when healthy
```

- `profiles add` with no TTY (piped stdin) errors and points at flag form.
- `profiles test` default target = resolved profile (precedence chain above).
- No `gcli profiles edit` — users edit the yaml directly (0600 preserved on next write); `profiles list` shows path.

## 3. Brand Neutrality Scrub

Included in this work (carried over from pending bounded task):

- `testdata/datasources.json`: URLs → `*.example.com` synthetic hosts; datasource names → generic (`Metrics Alpha`, `Metrics Beta`, `PostgreSQL Metrics`, `Logs`); scrub `user: "postgres"` field. Keep UIDs + JSON schema (integration test asserts count only — verified safe).
- `internal/cmd/help.txt`: example `GRAFANA_URL` → `https://grafana.example.com`.
- `internal/cmd/query.go`: doc examples "Universal"/"Billing" → generic datasource names.
- `internal/api/integration_test.go`: comment — drop real host reference.
- `docs/design-spec.md`: scrub real host + org details.
- `docs/plan-v1.md`, `docs/plan-v2.md`: replace internal hostnames with example.com equivalents.
- Grep-guard at end: `cafebazaa[r]|bazaa[r]|sotoo[n]|roo\.cloud|cluster\.local|skube[l]|rasa[d]|cli[q]` must return nothing outside `.git`.

Note: git history still contains originals. History rewrite is a separate decision, out of scope for this spec.

## 4. Testing

- **Profiles package** (`internal/profiles`): parse valid/invalid yaml, precedence chain (every step: flag > env > default > legacy env > error), auth header construction (bearer vs basic vs orgId), token XOR user/pass validation, unknown-key rejection, file-perms warning logic.
- **Command tests** (behavioral, httptest): `profiles add` (flag form + interactive path via stdin), `list` never leaks token/pass, `use` unknown-name error, `remove` default-refusal, `test` against fake `/api/health` (OK + 401 + timeout cases).
- **Existing suite must stay green** — config resolution refactor must not break current commands/tests.
- **Scrub verification**: `make test` + grep-guard.

## 5. Non-Goals

- No write endpoints. No streaming. No caching.
- No keyring (0600 + warning only).
- No profile encryption at rest.
- No `profiles edit` subcommand.
- No git history rewrite in this work.
- No multi-instance failover/load-balancing.

## Impact on Existing Invariants

- CLAUDE.md updates on commit: config story changes (env vars → profiles.yaml + precedence), new package `internal/profiles`, new command group, "single instance" non-goal in design-spec.md removed.
- Read-only invariant unchanged. Cobra-only dep unchanged.
- `update`/`uninstall` commands untouched.
