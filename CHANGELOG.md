# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

## [0.2.0] - 2026-10-26

### Added

- `trino_view` resource for SQL views: quoted `CREATE`/`DROP VIEW`,
  `CREATE OR REPLACE VIEW` for definition and security changes, import by
  `catalog.schema.name`, existence from `information_schema.tables` with
  case-insensitive resolution of the stored name, `security` read from
  `SHOW CREATE VIEW`, and a write-only `definition` because Trino normalises it
  and Hive keeps a stale definition after an underlying column rename. The
  acceptance suite runs the lifecycle on `hive` (sql-standard security with the
  `admin` role) and `memory` (including `SECURITY INVOKER` drift).
- `trino_materialized_view` resource for connector-backed materialized views:
  quoted `CREATE`/`DROP MATERIALIZED VIEW` with raw-SQL `properties` and
  `grace_period`, import by `catalog.schema.name`, existence read-back from
  `system.metadata.materialized_views`, configured-property drift from `SHOW
  CREATE MATERIALIZED VIEW`, and a definition read from the same statement on
  import. `Read` never refreshes; `refresh_on_create` populates the view at
  creation and changing `refresh_token` asks for a `REFRESH MATERIALIZED VIEW`
  on the next apply. The acceptance suite runs it against the Iceberg catalog on
  every supported release.
- The acceptance environment runs an `iceberg` catalog backed by a Hive
  metastore (Hive 3.1.3, `iceberg-warehouse` volume). Trino rejects
  `CREATE MATERIALIZED VIEW` on Iceberg REST catalogs, so the metastore is the
  prerequisite for `trino_materialized_view` (#49); an acceptance test now
  proves create, refresh, definition read-back and freshness without an
  implicit refresh on the supported floor and newest release.
- `trino_tables` data source: table and view names with `table_type`, optional
  case-insensitive `name_prefix` and `table_type` filters, deterministic
  ordering and a missing-schema diagnostic.
- `trino_table_privileges` accepts schema-wide queries: `table` is now
  optional, each returned privilege names its `table_name`, and exact-table
  queries keep their original SQL and rows.
- `trino_function` resource for catalog UDFs (SQL routines) on connectors with
  UDF storage, including import, token-normalised definition drift detection,
  signature-aware `DROP FUNCTION`, and connector-named diagnostics for
  unsupported catalogs.
- Documented the `trino_view` spike and its recommendation in
  `contributing/view-spike.md`.
- Added a "Verified unsupported" README section for `DENY`, schema-level
  grants, system roles and catalog property read-back, with sources,
  re-check triggers and the check date.

### Changed

- A release is now a pull request: merging a change to `VERSION` starts the
  `Create Release Tag` workflow, which signs the tag and publishes the release
  without a manual dispatch. `make release-check` and the `Test` job fail when
  `VERSION`, `CHANGELOG.md` and the README install constraint disagree.
- CI enforces per-package coverage floors from `.github/coverage-floors.txt`
  in addition to the module total, discovers the package list with
  `go list ./internal/...`, and names every package below its floor.
- `Lint & Security` runs `govulncheck` and fails on vulnerabilities that affect
  called code; accepted findings live in `.github/govulncheck-accepted.txt`.
  The Go toolchain is now 1.27.3 and `google.golang.org/grpc` is 1.83.2, which
  clears the reachable findings in the standard library and grpc.
- The fuzz targets for identifier/literal quoting and the schema parser run
  their committed seed corpora in CI; `make fuzz` explores new inputs.
- The unused `.github/github-action-markdown-link-check-config.json` was
  removed: no workflow consumed it.
- Documentation now covers operating the project end to end: a maintainer
  release runbook (`contributing/releasing.md`) with the mandatory GraphQL
  merge-queue step, artifact verification and recovery; a README troubleshooting
  table for the first-run failures; the complete `make` target list; and the Go
  version required to build from source.

### Fixed

- The version probe no longer caches a failed `SELECT version()`: a transport
  error or coordinator blip is retried by the next caller instead of poisoning
  every later call in the provider process. Concurrent callers share one probe.
- Mutating statements are no longer retried at the HTTP layer. Trino DDL is not
  idempotent, so a timed-out `CREATE`/`GRANT` that had already succeeded could
  be re-sent and surface as `ALREADY_EXISTS`; Terraform's apply/refresh cycle is
  the retry boundary instead. Read-only queries keep the driver's retries.
- `providerdata.Data` no longer carries an unused `Version` field.

## [0.1.1] - 2026-10-25

### Fixed

- The README installation constraint and status now match the published
  release instead of saying "pending release" and `~> 1.0`.
- The changelog records the `0.1.0` release published on 2026-10-24.

## [0.1.0] - 2026-10-24

### Added

- Provider `trino` with environment fallbacks for every attribute, auth modes
  `none`, `basic`, `jwt`, `oauth2` and `kerberos`, TLS/mTLS options, session
  properties, extra credentials, static headers and a per-operation
  `query_timeout`.
- Resources: `trino_catalog`, `trino_schema`, `trino_role`, `trino_grant_role`
  and `trino_grant_privileges`, with import support for every type except
  `trino_catalog` (properties have no read path).
- Data sources: `trino_catalogs`, `trino_schemas`, `trino_roles` and
  `trino_table_privileges`.
- Generated provider, resource and data-source reference under `docs/`, plus a
  complete end-to-end example in `examples/complete/`.
- Documented read-back matrix: catalog properties are write-only, role grants
  are state-authoritative, and table-grant drift is detected through
  `information_schema.table_privileges`.
- Repository scaffold: Go module, provider entry point and provider stub.
- Tooling: `.golangci.yml`, `GNUmakefile`, `tfplugindocs` templates and coverage
  scripts.
- CI: lint/security, build/test, cross-platform build, generated-doc drift and
  acceptance matrix workflows; GoReleaser release pipeline.
- Fake acceptance environment: Docker Compose with Trino and a Hive file
  metastore.
- Community files: README, AGENTS, ARCHITECTURE, CONTRIBUTING, SECURITY,
  CODE_OF_CONDUCT, issue and pull-request templates.
