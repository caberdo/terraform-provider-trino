# Terraform Provider for Trino

A Terraform provider for the [Trino](https://trino.io/) control plane: catalogs,
schemas, catalog roles, role grants and table privilege grants, managed through
SQL DDL.

The provider is a thin translation layer between Terraform state and Trino. It
owns no truth of its own: every read is a SQL query, every mutation is a DDL
statement, and every unsupported Trino capability is either rejected with an
actionable diagnostic or documented as a known limitation.

> **Status:** early development. The module, tooling and CI are scaffolded; the
> provider configuration, resources and data sources land in the follow-up tasks.

## Requirements

- Terraform 1.9 or later (plugin protocol 6.0).
- Trino **476** or later. CI runs the acceptance suite against the versions in
  [`.github/trino-versions.txt`](.github/trino-versions.txt).

## Quick start

```hcl
terraform {
  required_providers {
    trino = {
      source = "caberdo/trino"
    }
  }
}

provider "trino" {
  endpoint = "http://localhost:8080"
  user     = "admin"
  auth     = "none"
}
```

Provider configuration and the resource reference will be published in the
Terraform Registry once v1 is released. Until then, run `make install` and
Terraform discovers the binary from `~/.terraform.d/plugins`. Alternatively,
point a `dev_overrides` block at the directory that holds the built binary.

## Development

```bash
make build             # build bin/terraform-provider-trino
make test              # unit tests
make coverage          # unit coverage profile + summary
make lint              # golangci-lint
make validate-examples # terraform fmt/validate every example
make generate          # regenerate docs/ with tfplugindocs
```

Acceptance tests need Docker Compose (Podman works too: `COMPOSE=podman-compose`):

```bash
make testacc-docker TRINO_VERSION=484
make testacc-docker COMPOSE=podman-compose
make testacc-matrix
```

<!-- coverage:start -->
![Coverage floor](https://img.shields.io/badge/coverage%20floor-80%25-green)

The coverage gate fails below **80%** (`./scripts/coverage-gate.sh`).
<!-- coverage:end -->

## Documentation

- [Architecture](ARCHITECTURE.md) — request flow, executor internals, error
  taxonomy and test layers.
- [Contributing](CONTRIBUTING.md) — development workflow and review gates.
- [Security](SECURITY.md) — supported versions, threat model and reporting.
- [Changelog](CHANGELOG.md).
- [Generated provider docs](docs/index.md).

## Licence

Mozilla Public License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
