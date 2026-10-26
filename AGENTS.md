# AGENTS.md

Guidance for coding agents working in this repository.

## Read these before changing behaviour

These two documents are kept locally (they live under `docs/superpowers/`,
which is gitignored) and are not part of the repository; obtain them from the
maintainer if they are missing:

1. `docs/superpowers/specs/2026-10-23-terraform-provider-trino-design.md` — the
   binding design: constraints, frozen interfaces, resource contracts and the
   read-back matrix.
2. `docs/superpowers/plans/2026-10-23-terraform-provider-trino-v1.md` — the
   task-by-task implementation plan and lane ownership.
3. `ARCHITECTURE.md` — how the pieces fit together.
4. `CONTRIBUTING.md` — commands, review gates and the contract-change protocol.

The spec is the authority. If code and spec disagree, the spec wins or the spec
is changed deliberately, in the same change.

## Documentation map

| File | Role | Edit by hand? |
|---|---|---|
| `docs/superpowers/specs/2026-10-23-terraform-provider-trino-design.md` | Binding design: constraints, frozen interfaces, resource contracts, read-back matrix. | Yes, deliberately, in the same change as the code it governs. |
| `docs/superpowers/plans/2026-10-23-terraform-provider-trino-v1.md` | Task-by-task plan, lane ownership, verification commands. | Yes, when scope changes. |
| `README.md` | User-facing install, configuration, resource tables, limitations. | Yes. |
| `ARCHITECTURE.md` | Request flow, executor internals, error taxonomy, auth matrix, test layers. | Yes. |
| `CONTRIBUTING.md` | Workflow, review gates, branch protection and merge protocol. | Yes. |
| `docs/index.md`, `docs/resources/*.md`, `docs/data-sources/*.md` | Generated reference. | **No.** Run `make generate`. |
| `examples/**` | Validated modules; also the source of the generated examples. | Yes, then `make validate-examples` and `make generate`. |
| `templates/**` | `tfplugindocs` templates. | Yes, then `make generate`. |

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
| `make coverage-gate` | fail below the global floor in `.github/coverage-baseline.txt` or a package floor in `.github/coverage-floors.txt` |
| `make coverage-readme-check` | fail when the README coverage badge drifts |

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
  below the global floor in `.github/coverage-baseline.txt` or a package floor
  in `.github/coverage-floors.txt`. Run `make coverage` (and the acceptance
  suite where relevant) and confirm `./scripts/coverage-gate.sh` passes before
  opening or merging a pull request; if a change adds uncovered code, add tests
  or lower the floor deliberately in the same change.
- Read-back behaviour must match the spec's matrix. Never silently zero an
  unreadable attribute; preserve state.

## Contract-change protocol

Shared interfaces are frozen in the spec §5. If a change needs a different
signature, attribute name, SQL shape or import format:

1. Stop. Do not improvise the change in a resource package.
2. Update the spec in the same change, with the reason.
3. Update every consumer (resources, data sources, provider, tests, docs) in
   that change.
4. Note the change in the pull request and the changelog if it is user-visible.

A contract change that lands without the spec is a review finding regardless of
how well it works.

## Review gates

A change is ready when `make build`, `make vet`, `make lint` and the relevant
`go test` scope pass, generated docs show no diff, and no write escaped the
task's owned paths. Acceptance tests must compile even when they are not run
locally.

## Lane history

| Wave | Work | Where it lives |
|---|---|---|
| 0 | Scaffold, provider stub, executor + protocol fakes, provider config and shared CRUD harness | `main`, PRs #17, #19, #21, #22 |
| 1 | Lane A `trino_catalog`; B `trino_schema`; C `trino_role`; D `trino_grant_role`; E `trino_grant_privileges`; F data sources and provider wiring | `internal/resources/*`, `internal/datasources/*`, PR #25 |
| 2 | T10 wiring/docs/complete example; T11 acceptance, coverage gate, release dry-run; T13 version matrix; T12 whole-branch review | `internal/provider` registration, `docs/`, `examples/complete`, CI, `contributing/kerberos-acceptance.md` |
| 3 | v0.2 control-plane surface: `trino_tables` data source, schema-wide `trino_table_privileges`, `trino_function` resource, view spike, verified non-goals | `internal/datasources/tables`, `internal/datasources/tableprivileges`, `internal/resources/function`, `contributing/view-spike.md`, README, spec |

Lane ownership existed to keep parallel work conflict-free: each lane owned its
resource package, tests and example, and no lane wrote `internal/provider`. In
Wave 2 the work is serial on purpose because it edits the shared files
(registration, generated docs, CI) that were frozen during Wave 1.
