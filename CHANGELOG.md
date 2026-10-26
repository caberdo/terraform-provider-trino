# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

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
