# AGENTS.md

Guidance for coding agents working in this repository.

## Read these before changing behaviour

1. `docs/superpowers/specs/2026-10-23-terraform-provider-trino-design.md` — the
   binding design: constraints, frozen interfaces, resource contracts and the
   read-back matrix.
2. `docs/superpowers/plans/2026-10-23-terraform-provider-trino-v1.md` — the
   task-by-task implementation plan and lane ownership.
3. `ARCHITECTURE.md` — how the pieces fit together.
4. `CONTRIBUTING.md` — commands, review gates and the contract-change protocol.

The spec is the authority. If code and spec disagree, the spec wins or the spec
is changed deliberately, in the same change.

## Package layout

```
main.go                     provider entry point (providerserver.Serve)
internal/provider/          provider schema, config validation, registration
internal/providerdata/      typed data handed to resources
internal/trino/             Executor, quoting, errors, version, TLS
internal/trinotest/         fake executor, fake /v1/statement server, harness
internal/resourceutil/      import-ID parsing and state helpers
internal/resources/<name>/  one package per resource, tests co-located
internal/datasources/...    one package per data source, tests co-located
internal/acctest/           TF_ACC guards and acceptance helpers
```

Do not create a flat `internal/provider/resource_*.go` layout. The per-resource
packages are what make parallel work safe: no shared files, and tests travel
with the code they cover.

## Commands

| Command | Purpose |
|---|---|
| `make build` | build `bin/terraform-provider-trino` |
| `make test` | unit tests |
| `make coverage` | unit coverage profile and summary |
| `make lint` | golangci-lint (config at `.golangci.yml`) |
| `make fmt` / `make vet` | formatting and vet |
| `make validate-examples` | `terraform fmt`/`validate` every example |
| `make generate` | regenerate `docs/` with `tfplugindocs` |
| `make testacc-docker` | acceptance suite against Trino in Docker |
| `make testacc-matrix` | acceptance suite against every supported Trino version |

## Rules

- Only the lead edits `go.mod`, `go.sum`, `internal/provider/**`, CI files and
  generated docs. Lane work stays inside its owned paths.
- No new third-party dependency without the lead's approval.
- Every SQL identifier goes through `trino.QuoteIdentifier`; every literal
  through `trino.QuoteLiteral`; query values are bound with `?`.
- No `panic`, `log.Fatal` or `os.Exit` outside `main`.
- No secret in a diagnostic, log field, test failure message or committed file.
- Docs under `docs/` are generated. Never hand-edit them.
- Coverage must be checked before claiming work is done, and it must never drop
  below the floor in `.github/coverage-baseline.txt`. Run `make test` (and the
  acceptance suite where relevant) and confirm `scripts/coverage-gate.sh` passes
  before opening or merging a pull request; if a change adds uncovered code,
  add tests or lower the floor deliberately in the same change.
- Read-back behaviour must match the spec's matrix. Never silently zero an
  unreadable attribute; preserve state.

## Review gates

A change is ready when `make build`, `make vet`, `make lint` and the relevant
`go test` scope pass, generated docs show no diff, and no write escaped the
task's owned paths. Acceptance tests must compile even when they are not run
locally.
