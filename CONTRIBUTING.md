# Contributing

Thanks for contributing to the Terraform provider for Trino.

## Before you start

Read, in this order:

1. [`docs/superpowers/specs/2026-10-23-terraform-provider-trino-design.md`](docs/superpowers/specs/2026-10-23-terraform-provider-trino-design.md)
2. [`docs/superpowers/plans/2026-10-23-terraform-provider-trino-v1.md`](docs/superpowers/plans/2026-10-23-terraform-provider-trino-v1.md)
3. [`ARCHITECTURE.md`](ARCHITECTURE.md) and [`AGENTS.md`](AGENTS.md)

The spec is the binding authority for interfaces and read-back behaviour. If a
change needs a different contract, say so in the pull request and update the
spec in the same change; do not improvise a shared interface.

## Development setup

Requirements:

- Go 1.27.x (the version in `go.mod`).
- Terraform 1.9 or later for example validation.
- Docker Compose (or `podman-compose`) for acceptance tests.

```bash
make build
make test
make lint
make vet
make validate-examples
make generate
```

## Pull requests

- One logical change per pull request; keep the diff inside the paths the task
  owns.
- Add or update tests with the change. Unit tests live next to the code; CRUD
  tests drive the shared fake harness rather than hand-built framework values.
- Do not hand-edit `docs/`; run `make generate` and commit the result.
- `go.mod`/`go.sum` changes need the lead's approval.
- Fill in the scope / owned paths section of the pull request template.
- CI must be green, including the generated-doc drift check and coverage gate.

## Commits

Use conventional commit prefixes:

- `feat(<area>): ...` for user-visible behaviour
- `fix(<area>): ...` for bug fixes
- `chore: ...` for tooling, CI and scaffolding
- `docs: ...` for documentation-only changes
- `test: ...` for test-only changes

## Reporting bugs and requesting features

Use the issue templates. For a bug, include the provider version, Terraform
version, Trino version, the configuration that triggered it, and the relevant
diagnostic with secrets removed.

## Security

Do not open a public issue for a vulnerability. Follow [`SECURITY.md`](SECURITY.md).
