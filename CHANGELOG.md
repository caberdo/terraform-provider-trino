# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

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
