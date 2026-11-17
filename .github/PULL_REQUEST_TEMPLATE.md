## Description

### Type of Change
- [ ] Bug fix (non-breaking change that fixes an issue)
- [ ] New feature (non-breaking change that adds functionality)
- [ ] Breaking change (fix or feature that would cause existing functionality to not work as expected)
- [ ] Documentation update
- [ ] Refactoring (no functional changes, code improvement)

### What does this PR do?

<!-- Provide a clear description of what this PR does and why. -->

### How was this change tested?

<!--
Describe how you tested this change:
- Unit tests
- Acceptance tests (run locally with Docker or Podman)
- Manual testing scenarios
-->

### Related Issues

<!-- Link to related issues using: Fixes #123, Related to #456 -->

## Testing Checklist

- [ ] Unit tests added/updated
- [ ] Acceptance tests added/updated (for new resources)
- [ ] `make fmt` passes
- [ ] `make vet` passes
- [ ] `make lint` passes
- [ ] Tests run locally before pushing

## Documentation Checklist

- [ ] Example added to `examples/` directory (the documentation source)
- [ ] `make generate` run and the regenerated `docs/` committed
- [ ] `changelog.d/` fragment added for a user-visible change
- [ ] Schema attributes have descriptions
- [ ] Import section documented (if applicable)

## Checklist for Specific Changes

### New Resources
- [ ] Client method added for the server API the resource needs
- [ ] Resource file implements `resource.Resource` and `resource.ResourceWithImportState`
- [ ] Registered in the provider's `Resources()` method
- [ ] Schema attributes described, enumerations validated, sensitive fields marked
- [ ] ImportState implemented
- [ ] Documentation + example

### New Data Sources
- [ ] Client method added for the server API the data source needs
- [ ] Data source file implements `datasource.DataSource`
- [ ] Registered in the provider's `DataSources()` method
- [ ] Schema attributes described
- [ ] Documentation + example

### Bug Fixes
- [ ] Root cause identified
- [ ] Regression test added
- [ ] Edge cases considered

## Provider-specific checks

Each repository appends its own checklist items below this heading; `tfkit scaffold fix` preserves everything below it verbatim and `tfkit scaffold check` ignores it.

- [ ] Every SQL identifier goes through `trino.QuoteIdentifier`; every literal
  through `trino.QuoteLiteral`; query values are bound with `?`.
