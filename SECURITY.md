# Security Policy

## Supported versions

Only the latest released version receives security fixes. Pre-release builds and
development snapshots are unsupported.

## Threat model

The provider holds credentials that can execute DDL against a Trino coordinator.
Treat a Terraform run as equivalent to a privileged Trino session:

- The provider can create and drop catalogs, schemas and roles, and grant or
  revoke table privileges.
- Credentials live in Terraform configuration or environment variables. State
  contains sensitive provider attributes marked `Sensitive`, but state files are
  not encrypted by Terraform; protect the backend accordingly.
- Trino logs `CREATE CATALOG` statements, including their properties. Catalog
  credentials should be passed through `sensitive_properties` and the resource
  documentation warns about the history entry.
- `insecure_skip_verify` disables TLS verification and is documented as
  test-only. Password authentication is rejected over plain HTTP.

## Secret handling

- Provider attributes that carry secrets are marked `Sensitive` in the schema.
- Statements containing literals are never logged.
- No secret may appear in a diagnostic, log field, test failure message or
  committed file.
- Release checksums are signed and build provenance is attested; verify a
  download before running it.

## Reporting a vulnerability

Do not open a public issue. Report privately through GitHub's
[security advisory](https://github.com/caberdo/terraform-provider-trino/security/advisories/new)
flow, or email <dennis@dejong.ovh>.

Include:

- the affected version,
- a description of the issue and its impact,
- reproduction steps or a proof of concept,
- any suggested remediation.

You will receive an acknowledgement, and a coordinated disclosure date once the
issue is understood.
