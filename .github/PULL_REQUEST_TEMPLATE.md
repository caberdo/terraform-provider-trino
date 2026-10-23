## Description

<!-- What changes and why. -->

## Linked Issues

<!-- Fixes #123 -->

## Scope / owned paths

<!--
State the task's exclusive write paths and confirm the diff stays inside them.
For a lane task, copy the paths from the plan's lane-ownership table.
-->

- Owned paths:
- [ ] No writes outside the owned paths (`git diff --name-only <base>...HEAD`)

## Testing

- [ ] `make build` succeeds
- [ ] `make vet` is clean
- [ ] `make test` passes (unit tests)
- [ ] `make lint` is clean
- [ ] `make generate` run and `docs/` committed, if a schema changed
- [ ] `make validate-examples` passes, if an example changed
- [ ] New or changed resources/data sources have unit tests for schema, create, update, delete and import (valid + invalid)
- [ ] A regression test covers this change, if it is a bug fix
- [ ] `make testacc` passes, if CRUD behaviour through Terraform Core changed
- [ ] `make coverage-merge && make coverage-gate` passes

## Documentation

- [ ] `make generate` run (if a schema changed; never edit `docs/` by hand)
- [ ] Examples added or updated under `examples/` (if user-facing behaviour changed)
- [ ] README/CHANGELOG updated, if the change affects usage or releases

## Checklist

- [ ] SQL identifiers go through `trino.QuoteIdentifier`; literals through `trino.QuoteLiteral`; values are bound with `?`
- [ ] Trino errors go through `trino.SourceOf` / `trino.IsNotFound`
- [ ] `tflog.Debug` at the start and end of Create/Read/Update/Delete
- [ ] Import parses and validates the documented ID format
- [ ] Attributes Trino cannot update in place carry `RequiresReplace()`
- [ ] Unreadable attributes preserve state rather than being zeroed
- [ ] Resource or data source registered in `internal/provider/provider.go`

See [CONTRIBUTING.md](../CONTRIBUTING.md) for the development setup and the
full pull request process, and [ARCHITECTURE.md](../ARCHITECTURE.md) for how the
provider is put together.
