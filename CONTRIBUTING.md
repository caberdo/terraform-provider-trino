# Contributing

Thanks for contributing to the Terraform provider for Trino.

## Before you start

Read, in this order:

1. `docs/superpowers/specs/2026-10-23-terraform-provider-trino-design.md` — kept
   locally (gitignored); obtain from the maintainer if missing.
2. `docs/superpowers/plans/2026-10-23-terraform-provider-trino-v1.md` — kept
   locally (gitignored); obtain from the maintainer if missing.
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
make coverage
make coverage-gate
```

## Acceptance tests and the version matrix

`.github/trino-versions.txt` is the single source of truth: the declared floor
on the first line, then the last five stable releases, ascending, one per line.

- A pull request runs the floor and the newest release (`Acceptance Tests
  (Trino <version>)`); `main` and the merge queue run the whole window.
- Run one version locally with `make testacc-docker TRINO_VERSION=484`, or a
  subset of the window with
  `TRINO_VERSIONS="484 483" make testacc-matrix`. The matrix gives each version
  its own compose project and prints a PASS/FAIL summary.
- `TRINO_EXPECT_VERSION` is set by the matrix and by CI. `PreCheck` fails when
  the coordinator reports another release, so a stale image cannot masquerade
  as a green run.
- **Adding a release:** put it on its own line in ascending order, run the
  matrix, and land a pull request. **Dropping a release:** remove the line; if
  the floor moves, update `README.md`, `SECURITY.md` and the spec in the same
  change.
- `.github/workflows/version-freshness.yml` opens one issue labelled
  `version-matrix` when the window drifts; it never edits the file.

## Coverage floors

`scripts/coverage-gate.sh` enforces two things on the unit profile
(`coverage.out`):

- the module total against `.github/coverage-baseline.txt`, which the README
  badge tracks;
- every package returned by `go list ./internal/...` against its entry in
  `.github/coverage-floors.txt`, or that file's `default` floor.

The package list is discovered, so a new `internal/resources/*` or
`internal/datasources/*` package is gated the moment it exists; a package with
no tests measures 0% and fails. The gate prints every package it checked and
names each one below its floor with the delta.

To **raise** a floor: improve coverage, run `make coverage`, then edit the
package's line. To **lower** one: do it in the same pull request as the change
that dropped coverage and explain why in the pull request. Lowering a floor
without that justification is a review finding. `make coverage-gate` checks the
current `coverage.out`.

## Fuzzing

Three fuzz targets cover the parsers that consume untrusted input:

- `FuzzQuoteIdentifier` and `FuzzQuoteLiteral` in `internal/trino`;
- `FuzzParseShowCreateSchema` in `internal/resources/schema`.

Seeds are committed under `testdata/fuzz/<Target>` next to the code and run
with the normal test suite. CI runs only the seeds
(`go test ./internal/... -run '^Fuzz'`) so the job stays bounded. `make fuzz`
explores new inputs for `FUZZTIME` (default `10s`) per target. A crash is
written under `testdata/fuzz/<Target>`; commit it with the fix as a regression
seed.

## Dependency vulnerabilities

`Lint & Security` runs `govulncheck ./...` through `scripts/govulncheck.sh`.
Only findings that affect called code fail the job; module-level findings the
provider does not call are reported by the tool and do not block.

- The tool version is pinned in `.github/workflows/test.yml`
  (`golang.org/x/vuln/cmd/govulncheck@v1.9.0`); bump it deliberately together
  with the `go` directive it was tested against.
- `golangci-lint` and `gosec` are pinned and built with the job's Go toolchain
  (`install-mode: goinstall` and `go run …@version`). Released binaries are
  built with an older Go and cannot read the module's export data, so bump
  their versions together with the `go` directive.
- Fix a finding by upgrading the dependency or the Go toolchain in `go.mod`.
- If a reachable finding cannot be fixed yet, add its OSV ID and a reason to
  `.github/govulncheck-accepted.txt`. The gate prints accepted entries and warns
  when one no longer matches, so remove stale entries.

## Pull requests

- One logical change per pull request; keep the diff inside the paths the task
  owns.
- Add or update tests with the change. Unit tests live next to the code; CRUD
  tests drive the shared fake harness rather than hand-built framework values.
- Do not hand-edit `docs/`; run `make generate` and commit the result.
- `go.mod`/`go.sum` changes need the lead's approval.
- Fill in the scope / owned paths section of the pull request template.
- CI must be green, including the generated-doc drift check and coverage gate.

## Merging

`main` is protected by the `main-protection` repository ruleset. Do not push to
it directly: every change, including Wave 0 foundation work, goes through a
branch and a pull request. The ruleset requires:

- `Lint & Security` and `Test` to pass. These are the job `name:` values in
  `.github/workflows/test.yml`, not the job keys.
- A linear history (squash merges only) and no force-pushes or branch
  deletion.
- All review threads resolved.
- No bypass actors. There is deliberately no fast path around the gate.

Merge through the merge queue (`gh pr merge --squash --auto`, or the merge
button once both checks are green). The queue builds the prospective merge
commit and runs the same `pull_request` workflows on the `merge_group` event,
so stacked PRs are tested against the latest `main` without every lane
rebasing.

### Required checks and rename hazard

The required contexts are exactly `Lint & Security` and `Test`. A context is
matched by job `name:`, so **renaming a job `name:` silently un-requires the
gate**. Treat a rename as a breaking CI change:

1. Update the job `name:`.
2. Update the `REQUIRED_CONTEXTS` array in `scripts/setup-branch-protection.sh`
   and `scripts/check-branch-protection.sh` (and this section).
3. Run `./scripts/setup-branch-protection.sh` to install the new context.
4. Run `./scripts/check-branch-protection.sh`; it must exit 0.

Never require a job that has an `if:` condition or a matrix context. Such a
check is skipped on some PRs and sits at "Expected — Waiting for status to be
reported" forever. `Prepare Trino Matrix`, `Acceptance Tests (Trino <version>)`
and `Check the Trino release window` are therefore not required. Do not add
`paths:` or `branches:` filters to a workflow whose jobs are required.

`./scripts/check-branch-protection.sh` is read-only and exits non-zero on
drift. `.github/workflows/protection-drift.yml` runs it weekly and opens (or
updates) one issue labelled `ci`.

### Emergency procedure

If `main` must be fixed while the gate is red, disable the ruleset explicitly
(`./scripts/setup-branch-protection.sh` installs it with
`enforcement: active`; set it to `disabled` in repository settings or via the
API), land the smallest possible hotfix through a PR, then re-enable it with
`./scripts/setup-branch-protection.sh`. A visible disable/re-enable window is
the documented path; do not add a bypass actor for convenience.

### TODO: second maintainer

`required_approving_review_count` is `0` because GitHub does not allow
self-approval and there is one maintainer today. When a second maintainer is
added, raise it to `1` and set `require_code_owner_review: true` in
`scripts/setup-branch-protection.sh` in the same change.

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
