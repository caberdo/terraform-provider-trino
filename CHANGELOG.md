# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

### Added

- Repository scaffold: Go module, provider entry point and provider stub.
- Tooling: `.golangci.yml`, `GNUmakefile`, `tfplugindocs` templates and coverage
  scripts.
- CI: lint/security, build/test, cross-platform build, generated-doc drift and
  acceptance matrix workflows; GoReleaser release pipeline.
- Fake acceptance environment: Docker Compose with Trino and a Hive file
  metastore.
- Community files: README, AGENTS, ARCHITECTURE, CONTRIBUTING, SECURITY,
  CODE_OF_CONDUCT, issue and pull-request templates.
