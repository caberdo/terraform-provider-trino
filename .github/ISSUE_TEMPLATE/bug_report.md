---
name: Bug Report
about: Report a bug in the Trino Terraform provider
title: "bug: "
labels: bug
assignees: ""
---

## Bug Description

<!-- Clear, concise description of the bug -->

## Steps to Reproduce

1. Terraform config:
```hcl
resource "trino_..." "example" {
  # ...
}
```
2. Run `terraform apply`
3. See error

## Expected Behavior

<!-- What should happen? -->

## Actual Behavior

<!-- What actually happened? Include error output -->

```
Error: ...
```

## Environment

- Provider version: `v0.x.x`
- Terraform version: `terraform version`
- Go version (if building from source): `go version`
- Trino version:
- Trino authentication mode:
- OS:

## Additional Context

<!-- Logs, TF_LOG=DEBUG output with secrets removed, etc -->
