# CLAUDE.md

Guidance for Claude Code working in this repository.

## What this repo is

`gcli` — a read-only Grafana CLI in Go. Single binary, cobra-based, talks to Grafana's HTTP API (`/api/ds/query` generic engine + read-only REST endpoints). No write endpoints, ever.

## Layout

- `internal/api/` — HTTP client, ds/query engine, Grafana API accessors (datasources, dashboards, alerts, annotations, health, capabilities), datasource proxy helper
- `internal/cmd/` — cobra commands (one file per command group: query/discovery/state/profiles), embedded help guide (`help.txt`), shared flag vars in `query.go`
- `internal/frames/` — dataplane → columnar normalization (the single data shape renderers consume)
- `internal/render/` — table/json/csv renderers
- `internal/timeparse/` — Grafana-relative time parsing (`now-1h`, `now-1d/d`)
- `internal/profiles/` — profiles.yaml load/save/resolve (multi-instance config)
- `internal/config/` — env config (`GRAFANA_URL`, `GRAFANA_TOKEN`); profiles.yaml resolves first (`--profile` > `GCLI_PROFILE` > `default:` > `GRAFANA_URL`/`GRAFANA_TOKEN` env); `--url`/`--token` flags override
- `testdata/` — anonymized recorded API fixtures for hermetic tests
- `docs/` — design spec + implementation plans (design history)
- `.github/workflows/release.yml` — CI: test/vet/build on push to main, rolling `latest` pre-release
- `install.sh` — release asset, the README one-liner installer (posix sh, checksum-verified)

## Invariants

- **Read-only**: only GET and the ds/query POST. Never add write endpoints.
- All commands render `frames.Result` through `run()`/`result{res, raw}` (exception: `update`/`uninstall` and `profiles add/list/use/remove` — local self operations, no Grafana call; `profiles test` calls `/api/health` directly).
- `update` binary naming + checksum parsing must stay in sync with `.github/workflows/release.yml` assets.
- Flag vars live in the `cmd/query.go` vars block; reset in `resetAllFlags()`.
- Tests: hermetic (httptest + fixtures), behavioral style (contract comment + `TestX_input_expected` naming). TDD.
- External deps: cobra + golang.org/x/term + gopkg.in/yaml.v3.
- Commit style: conventional (`feat(gcli): ...`, `fix(gcli): ...`).
- On every commit, update AI guidance files (CLAUDE.md and any other AI-related files) so they stay current with the code — new packages, commands, invariants, or layout changes must be reflected.
- Module path: `github.com/behnambm/gcli`. Binary version stamped via Makefile ldflags.

## Build

```bash
make build     # ./gcli
make test      # go test ./...
make vet
make install   # /usr/local/bin/gcli
```

Releases: every push to `main` runs CI (`.github/workflows/release.yml`) — tests, vet, cross-compile (darwin/linux × amd64/arm64), then replaces the rolling `latest` pre-release on GitHub. `install.sh` must stay in sync with the binary naming (`gcli-<os>-<arch>`) and gets attached as a release asset. Download URLs must use the literal tag path `releases/download/latest/...` — `releases/latest/...` resolves to the newest NON-prerelease release and 404s for this repo (guarded by `TestDefaultUpdateBaseURL_usesTaggedDownloadPath`).
