# Roadmap

What this project intends to do — and deliberately not do — over the next twelve months. The
living version of the near-term work is the [issue list](https://github.com/caberdo/terraform-provider-trino/issues); this file
records the direction those issues add up to. It is reviewed once per release; `CHANGELOG.md`
records what actually shipped.

## What the project is

A Terraform provider for [Trino](https://github.com/trinodb/trino): it manages the objects
Trino exposes as Terraform state, through the Trino API. The supported server versions
are the ones in [`.github/trino-versions.txt`](.github/trino-versions.txt); the
resource and data source inventory is the generated documentation under `docs/`.

## Now — supply chain, CI and the documentation spine

The current cycle makes the project's own guarantees as checkable as the ones it configures in
Trino. Open work, in the order it unblocks itself:

| Work | Where |
|---|---|
| Keep every GitHub Action pinned to a commit SHA and every kit caller on one pinned release | `.github/workflows/` |
| Keep the generated documentation and examples in step with the schema | `make generate`, `make validate-examples` |
| Keep the README, GOVERNANCE, ROADMAP, SECURITY and SUPPORT documents in the shape the kit owns | `tfkit scaffold check`, `.github/scaffold-allow.txt` |

Landed already: the docs-drift gate, example validation against the provider built from the tree,
a pinned `golangci-lint` and GoReleaser, a cross-platform build, `go mod tidy`/`verify` as a check,
the live acceptance suite against a real server, the README rebuilt as the front door, and the
release chain hardened — signed tags, signed checksums, the public signing key published on the
maintainer's GitHub account, and SLSA build provenance attached to every release. OpenSSF
Scorecard runs weekly on `main` and on branch-protection changes and publishes its result for the
README badge, and CI reports the merged unit + acceptance statement coverage in its job summaries.

## Next — correctness, coverage and honest compatibility

* **Raise the merged statement coverage.** The gated number is the merged unit + acceptance profile
  from `make coverage-merge`, measured and floored in `.github/coverage-baseline.txt`; `make
  coverage-gate` fails a pull request below a scope's floor and `make coverage-readme` keeps the
  README block in step, so the number can only move up. Take the exact figures from the profile
  rather than from prose: statement counts change with the Go patch release. What remains is
  concentrated where the tests are acceptance-shaped rather than unit-shaped: the error and drift
  paths a stock server never reaches.
* **Keep the version matrix honest.** `.github/trino-versions.txt` is the single source of
  truth for the acceptance matrix; a new Trino release is added there once its API is checked
  against the schema, and every version-restricted field keeps its union-schema-plus-runtime-gate
  treatment.
* **Close the live-coverage gaps** that a stock server cannot reach today. The live suite documents
  the exact configuration each one needs, so the work is a compose profile, not guesswork.

## Later — structure and reach

* **Discoverability:** keep the provider listed in the Terraform Registry and the OpenTofu
  registry, and link it from the Trino documentation's client list where that list is
  editable.
* **Release cadence:** patch releases for Trino compatibility and bug fixes, minor releases
  for new resources and data sources, deprecations announced one minor release ahead of removal.

## Explicit non-goals

* **Not a Trino server, installer or deployment tool.** The provider configures a *running*
  Trino instance; it does not provision the server, its backing storage or its containers. Use
  the Trino distribution, Ansible, Kubernetes or your cloud tooling for that.
* **Not a generic Trino API client.** Only the endpoints the Terraform surface needs are
  wrapped; arbitrary API calls are out of scope, and a new endpoint arrives with a resource or data
  source that uses it.
* **Not a data-plane tool.** The provider manages configuration and metadata; it does not move the
  data the server stores.
* **No breaking schema changes.** Renaming or removing a Terraform type or attribute breaks user
  state, so it only happens with a documented upgrade path and a major version.
* **No hosted service, paid tier or telemetry.** This repository is the whole product; the provider
  sends nothing anywhere except the server you configure.
* **No roadmap dates.** Work is ordered by value, not by calendar; the issues and this file are the
  commitment.

## How to influence this

Open an issue using the templates in `.github/ISSUE_TEMPLATE/`, or comment on an existing one.
Decisions and their reasoning are recorded in the issue before the code lands — see
[`GOVERNANCE.md`](GOVERNANCE.md).
