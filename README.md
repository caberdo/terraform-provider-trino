# Terraform Provider for Trino

A Terraform provider for the [Trino](https://trino.io/) control plane: catalogs,
schemas, catalog roles, role grants, table privilege grants and catalog
user-defined functions, managed through SQL DDL.

The provider is a thin translation layer between Terraform state and Trino. It
owns no truth of its own: every read is a SQL query, every mutation is a DDL
statement, and every unsupported Trino capability is either rejected with an
actionable diagnostic or documented as a known limitation.

> **Status:** v0.1.x released. The v0.2 line adds `trino_function`, the
> `trino_tables` data source and schema-wide privilege listing; the six
> resources and five data sources are implemented, documented and covered by
> unit, protocol and Docker acceptance tests.

## Requirements

- Terraform 1.9 or later (plugin protocol 6.0).
- Trino **476** or later. CI runs the whole list in
  [`.github/trino-versions.txt`](.github/trino-versions.txt) on `main`; a pull
  request runs the declared floor (476) and the newest release. 476 is the
  minimum supported server.
- Go 1.27.x to build from source (the version in [`go.mod`](go.mod)).
  Installing a published release needs neither Go nor the source tree.

## Installation

The provider is published to the Terraform Registry as `caberdo/trino`:

```hcl
terraform {
  required_providers {
    trino = {
      source  = "caberdo/trino"
      version = "~> 0.1"
    }
  }
}
```

To build from source or test unreleased changes, install a local build and
point Terraform at it with a `dev_overrides` block (or install a release-shaped
plugin directory with `make install VERSION=<version>`). See
[`examples/complete/main.tf`](examples/complete/main.tf) for a full module.

## Quick start

```hcl
provider "trino" {
  endpoint = "http://localhost:8080"
  user     = "admin"
  auth     = "none"
}

resource "trino_catalog" "analytics" {
  name      = "analytics"
  connector = "memory"
}

resource "trino_schema" "sales" {
  catalog = trino_catalog.analytics.name
  name    = "sales"
}

resource "trino_role" "analyst" {
  name    = "analyst"
  catalog = "hive"
}
```

Every provider attribute falls back to a `TRINO_*` environment variable, so
credentials do not have to live in configuration:

```hcl
provider "trino" {
  # TRINO_ENDPOINT, TRINO_USER, TRINO_PASSWORD, ...
}
```

## Provider configuration

| Attribute | Type | Environment | Notes |
|---|---|---|---|
| `endpoint` | string | `TRINO_ENDPOINT` | Coordinator URL, for example `https://trino.example:8443`. |
| `auth` | string | `TRINO_AUTH` | `none`, `basic`, `jwt`, `oauth2` or `kerberos`. Inferred from the credentials when unset. |
| `user` | string | `TRINO_USER` | Defaults to `terraform`. |
| `password` | string, sensitive | `TRINO_PASSWORD` | Basic auth. Requires an `https` endpoint. |
| `access_token` | string, sensitive | `TRINO_ACCESS_TOKEN` | Static JWT; also sent as `X-Trino-Authorization-User`. |
| `oauth2_client_id` | string | `TRINO_OAUTH2_CLIENT_ID` | Client-credentials flow. |
| `oauth2_client_secret` | string, sensitive | `TRINO_OAUTH2_CLIENT_SECRET` | Client-credentials flow. |
| `oauth2_token_endpoint` | string | `TRINO_OAUTH2_TOKEN_ENDPOINT` | **Required** for `auth = "oauth2"`: Trino 476 has no discovery. |
| `oauth2_scope` | string | `TRINO_OAUTH2_SCOPE` | Optional scope. |
| `kerberos_principal` | string | `TRINO_KERBEROS_PRINCIPAL` | Principal without a realm. |
| `kerberos_realm` | string | `TRINO_KERBEROS_REALM` | Required with a keytab. |
| `kerberos_keytab` | string, sensitive | `TRINO_KERBEROS_KEYTAB` | Mutually exclusive with the credential cache. |
| `kerberos_credential_cache` | string, sensitive | `TRINO_KERBEROS_CREDENTIAL_CACHE` | `FILE:` cache, for example `FILE:/tmp/krb5cc_1000`. |
| `kerberos_config_file` | string | `TRINO_KERBEROS_CONFIG_FILE` | Path to `krb5.conf`. |
| `kerberos_service_name` | string | `TRINO_KERBEROS_SERVICE_NAME` | Defaults to `HTTP` (the coordinator convention, not the driver default). |
| `kerberos_service_principal_pattern` | string | `TRINO_KERBEROS_SERVICE_PRINCIPAL_PATTERN` | Defaults to `${SERVICE}@${HOST}`. Must not name a realm. |
| `kerberos_use_canonical_hostname` | bool | `TRINO_KERBEROS_USE_CANONICAL_HOSTNAME` | Defaults to `true`. |
| `source` | string | `TRINO_SOURCE` | Defaults to `terraform-provider-trino`. |
| `catalog` | string | `TRINO_CATALOG` | Default catalog for every statement. |
| `schema` | string | `TRINO_SCHEMA` | Default schema for every statement. |
| `session_properties` | map(string) | — | `X-Trino-Session` on every request. |
| `extra_credentials` | map(string), sensitive | — | `X-Trino-Extra-Credential` on every request. |
| `roles` | map(string) | — | Catalog to role mapping, for example `{ hive = "admin" }`. |
| `http_headers` | map(string), sensitive | — | Static headers for gateway or proxy auth. |
| `query_timeout` | string | `TRINO_QUERY_TIMEOUT` | Duration, default `5m`. Bounds every operation. |
| `insecure_skip_verify` | bool | `TRINO_INSECURE_SKIP_VERIFY` | Test only. |
| `ca_cert` | string | `TRINO_CA_CERT` | CA PEM content; conflicts with `ca_cert_file`. |
| `ca_cert_file` | string | `TRINO_CA_CERT_FILE` | CA PEM file. |
| `client_cert` | string, sensitive | `TRINO_CLIENT_CERT` | mTLS certificate PEM content. |
| `client_cert_file` | string | `TRINO_CLIENT_CERT_FILE` | mTLS certificate file. |
| `client_key` | string, sensitive | `TRINO_CLIENT_KEY` | mTLS private key PEM content. |
| `client_key_file` | string | `TRINO_CLIENT_KEY_FILE` | mTLS private key file. |

### Authentication modes

| Mode | Requirements |
|---|---|
| `none` | No credentials. Rejected if a password, token or OAuth2 secret is set. |
| `basic` | `user` + `password` over `https`. Password over `http` is rejected before dialling. |
| `jwt` | `access_token`. |
| `oauth2` | `oauth2_client_id`, `oauth2_client_secret` and `oauth2_token_endpoint`. |
| `kerberos` | `https` endpoint, `kerberos_principal` + `kerberos_realm`, and **either** `kerberos_keytab` **or** `kerberos_credential_cache`. |

See [`contributing/kerberos-acceptance.md`](contributing/kerberos-acceptance.md)
for a reproducible KDC and Trino setup.

## Resources

| Resource | Manages | Import ID |
|---|---|---|
| [`trino_catalog`](docs/resources/catalog.md) | `CREATE/DROP CATALOG` with connector properties | Not supported (see limitations) |
| [`trino_schema`](docs/resources/schema.md) | `CREATE/DROP SCHEMA`, owner, properties | `catalog.schema` |
| [`trino_role`](docs/resources/role.md) | `CREATE/DROP ROLE` in a catalog | `catalog.role` |
| [`trino_grant_role`](docs/resources/grant_role.md) | `GRANT/REVOKE` a role to a principal | `catalog.role:grantee_type:grantee` |
| [`trino_grant_privileges`](docs/resources/grant_privileges.md) | `GRANT/REVOKE` table privileges | `catalog.schema.table:grantee_type:grantee` |
| [`trino_function`](docs/resources/function.md) | `CREATE/DROP FUNCTION` catalog UDFs (SQL routines) | `catalog.schema.name` |

## Data sources

| Data source | Returns |
|---|---|
| [`trino_catalogs`](docs/data-sources/catalogs.md) | Catalog names and connectors, optional `name_prefix` filter. |
| [`trino_schemas`](docs/data-sources/schemas.md) | Schema names for a catalog. |
| [`trino_roles`](docs/data-sources/roles.md) | Catalog role names. |
| [`trino_tables`](docs/data-sources/tables.md) | Table and view names with `table_type`, optional prefix and type filters. |
| [`trino_table_privileges`](docs/data-sources/table_privileges.md) | Grantees, privilege types and grantability for a table, or for every table in a schema when `table` is omitted. |

## Read-back matrix

The provider never claims drift detection it does not implement. This table is
the contract; the generated resource pages document attributes and import IDs,
while the matrix records what each `Read` can detect.

| Resource | Existence | Configured drift | Out-of-band revocation |
|---|---|---|---|
| `trino_catalog` | `system.metadata.catalogs` | `connector` only; properties are not readable | Detected only if the current connector differs |
| `trino_schema` | `SHOW SCHEMAS` | Owner and properties parsed from `SHOW CREATE SCHEMA` (best effort) | Schema dropped → detected |
| `trino_role` | `SHOW ROLES` | `catalog`/`name` only | Role dropped → detected |
| `trino_grant_role` | None | None | **Not detected** (state-authoritative) |
| `trino_grant_privileges` | `information_schema.table_privileges` | Privileges and grant option | Detected |
| `trino_function` | `SHOW FUNCTIONS` | Definition compared token by token from `SHOW CREATE FUNCTION` | Function dropped → detected |

## Known limitations

- **No user resource.** Trino has no `CREATE USER` and no user listing. Grantees
  are arbitrary principal strings (LDAP DN, `alice@example.com`,
  `DOMAIN\alice`), validated and always quoted.
- **No system roles.** Stock Trino disables system security; `trino_role`
  therefore requires `catalog` and creates catalog roles only.
- **No schema-level grants.** Trino only supports `ON TABLE` grants through a
  bundled connector, so `trino_grant_privileges` models tables.
- **No `ALTER CATALOG` and no catalog import.** Catalog properties have no read
  path, so every catalog attribute forces replacement and `ImportState` returns
  a diagnostic instead of silently recreating a live catalog.
- **Catalog properties are write-only.** They stay in state; property drift is
  not detected. `sensitive_properties` are merged into the same `CREATE CATALOG`
  statement and are logged by Trino's query history — treat that history as
  sensitive.
- **Role grants are state-authoritative.** Trino cannot list another principal's
  role grants, so an out-of-band revocation stays in state. Re-apply or
  `terraform state rm` + import to reconcile.
- **Table-grant visibility follows authorization.** A non-admin provider user
  may only see its own grants; use an admin identity for reliable drift
  detection.
- **`ALL PRIVILEGES` is rejected.** Trino expands it before storing it, which
  would create a permanent diff. List `SELECT`, `INSERT`, `UPDATE`, `DELETE`.
- **Hive lowercases names.** Reads match case-insensitively and keep the
  configured spelling in state, so a quoted `CREATE ROLE "Analyst"` does not
  produce a perpetual diff.
- **Dynamic catalogs must be enabled.** `CREATE CATALOG` requires
  `catalog.management=dynamic` on the coordinator; the provider maps the
  failure to a diagnostic naming the property.
- **SQL-standard Hive needs an enabled role.** Set
  `roles = { hive = "admin" }` (C17). The file metastore auto-grants `admin` to
  `admin`/`hive`/`hdfs`, which is how a fresh environment bootstraps.
- **Catalog UDFs need a connector that stores them.** `trino_function` works on
  the Hive and Memory connectors. A connector without UDF storage produces a
  diagnostic that names the connector and the requirement instead of a raw
  `NOT_SUPPORTED`. Python and other external-language functions are out of
  scope; only SQL routines are managed.
- **`trino_function.definition` is raw SQL.** The provider quotes the catalog,
  schema and function name, but the routine body is authored SQL and is not
  escaped. It must start with the parameter list and include `RETURNS`.
- **Function definitions are normalised by Trino.** `SHOW CREATE FUNCTION`
  lowercases keywords, adds parentheses around compound `RETURN` expressions
  and drops comments. The provider compares definitions token by token, so
  those cosmetic differences do not cause a diff; a semantic change does.
- **One function signature per resource.** Trino allows overloads with the same
  name. The provider matches the signature in `definition`, but the import ID
  `catalog.schema.name` cannot choose between existing overloads; importing an
  overloaded name fails with an actionable diagnostic until one remains.
- **Schema-wide privilege visibility follows authorization.** Omitting `table`
  on `trino_table_privileges` returns every grant the provider user can see in
  the schema. Use an admin identity for a complete picture.
- **No view or materialized-view resource.** `trino_tables` lists views, but the
  provider does not manage them. Views are deferred because Hive can keep a
  stale stored definition after a column rename (see the
  [spike findings](contributing/view-spike.md)); materialized views are blocked
  on an Iceberg catalog in the acceptance environment.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `Error: Operation not supported` with `Dynamic catalog management is not enabled. Set catalog.management=dynamic in the Trino coordinator configuration and restart it: ...` | The coordinator was started without dynamic catalog management, so `CREATE CATALOG` cannot run. | Set `catalog.management=dynamic` in `etc/config.properties` and restart the coordinator. |
| A warning `Trino version is older than the supported minimum` with `version` and `minimum` fields, or `Trino version probe failed; continuing without a detected version` | The coordinator is older than the supported minimum, or `SELECT version()` could not be read. A failed probe is non-fatal and is retried by the next call. | Upgrade to a supported Trino release. The warning does not stop the provider; the server remains the authority on errors. |
| `terraform plan` proposes `# trino_grant_privileges.<name> will be created` for a grant that already exists, and `data.trino_table_privileges` returns an empty `privileges` list | The provider identity cannot see other principals' grants in `information_schema.table_privileges`; Trino filters that view by authorization. | Run Terraform as an identity with admin rights or `SELECT` on `information_schema.table_privileges`, then `terraform apply` or re-import the resource. |
| A `trino_grant_role` revoked out of band still shows `No changes. Your infrastructure matches the configuration.` | Trino cannot list another principal's role grants, so the resource is state-authoritative by design. | Re-apply the grant, or `terraform state rm` the resource and import it again with `catalog.role:USER:grantee`. |
| `Error: Password requires TLS` with `Basic auth sends the password to the server, so the endpoint must use https.` | `auth = "basic"` is combined with an `http://` endpoint. | Use an `https://` endpoint (with `ca_cert_file` or `insecure_skip_verify` if needed), or `auth = "none"` for a coordinator without authentication. |
| `SHOW ROLES FROM hive` reports `analyst` for a role configured as `Analyst`, while `terraform plan` stays clean | Hive normalises role names to lowercase; the provider matches them case-insensitively and keeps the configured spelling in state. | Use lowercase role names. Do not rewrite state to the server spelling. |
| `Error: Failed to query available provider packages` with either `provider registry.terraform.io/caberdo/trino was not found in any of the search locations` or `no available releases match the given constraints ~> 0.1` | `make install` names the plugin directory after `git describe`, for example `v0.1.1-4-gd1d6e16`, which is not a release version Terraform's filesystem discovery accepts. | Install with an explicit version (`make install VERSION=0.1.2`) or use a `dev_overrides` block, which ignores version constraints. |

The error classification behind these diagnostics is described in
[Architecture → Error taxonomy](ARCHITECTURE.md#error-taxonomy).

## Required Trino privileges

| Resource | Identity needs |
|---|---|
| `trino_catalog` | `CREATE CATALOG` / `DROP CATALOG`, plus `catalog.management=dynamic` on the coordinator. |
| `trino_schema` | `CREATE SCHEMA`, `DROP SCHEMA` (`CASCADE` for `force_destroy`), and `ALTER SCHEMA ... SET AUTHORIZATION` when changing the owner. |
| `trino_role` | Role-admin rights on the catalog; the creating user becomes role admin. |
| `trino_grant_role` | `WITH ADMIN OPTION` on the role being granted. |
| `trino_grant_privileges` | Ownership or admin rights on the table, and `GRANT OPTION` to delegate it. |
| `trino_function` | `CREATE FUNCTION` / `DROP FUNCTION` on the catalog and schema, and a connector that implements catalog UDF storage. |

## Version policy

- The minimum supported server is **Trino 476**. One build supports 476 and
  newer; behaviour that differs by version is gated at runtime from
  `SELECT version()`.
- `.github/trino-versions.txt` is the single source of truth for the CI matrix
  and `make testacc-matrix`. It starts at the declared floor and lists the last
  five stable releases. A pull request runs the floor and the newest release;
  `main` and the merge queue run the whole window. A monthly workflow opens an
  issue when the window goes stale.
- If the version probe fails, the provider logs a warning and proceeds
  permissively; the server remains the authority on errors. A failed probe is
  not cached, so the next caller retries it.
- Kerberos requires HTTPS; keytab and credential cache are mutually exclusive,
  and the service principal pattern must not name a realm.

## Development

The toolchain versions are listed under [Requirements](#requirements). These are
the `GNUmakefile` targets:

| Target | Purpose |
|---|---|
| `make build` | Build `bin/terraform-provider-trino`. |
| `make install` | Build and copy into `~/.terraform.d/plugins`; pass `VERSION=0.1.2` for a release-shaped plugin directory. |
| `make test` | Unit tests. |
| `make fuzz` | Fuzz the quoting and schema parsers for `FUZZTIME` (default `10s`) per target. |
| `make coverage` | Unit coverage profile (`coverage.out`) and summary. |
| `make coverage-gate` | Fail when `coverage.out` is below the module floor in `.github/coverage-baseline.txt` or a package floor in `.github/coverage-floors.txt`; run `make coverage` first. |
| `make coverage-readme` / `make coverage-readme-check` | Regenerate the README coverage badge and floor statement, or fail when they drift from the baseline. |
| `make coverage-html` | Generate the HTML coverage report. |
| `make coverage-merge` | Merge the unit and acceptance profiles into one number. |
| `make lint` / `make lint-fix` | Run `golangci-lint`, optionally applying fixes. |
| `make fmt` / `make vet` | `gofmt` and `go vet`. |
| `make testacc` | Acceptance tests against an already reachable coordinator (`TF_ACC=1`). |
| `make testacc-docker` | Acceptance tests against Trino in Docker. |
| `make testacc-matrix` | Acceptance tests against every version in `.github/trino-versions.txt`. |
| `make validate-examples` | `terraform fmt`/`validate` every example. |
| `make generate` | Regenerate `docs/` with `tfplugindocs`. |

Acceptance tests need Docker Compose (Podman works too: `COMPOSE=podman-compose`):

```bash
make testacc-docker TRINO_VERSION=484
make testacc-docker COMPOSE=podman-compose
make testacc-matrix
```

The contribution workflow, review gates and contract-change protocol are in
[`CONTRIBUTING.md`](CONTRIBUTING.md); request flow and test layers are in
[`ARCHITECTURE.md`](ARCHITECTURE.md).

<!-- coverage:start -->
![Coverage floor](https://img.shields.io/badge/coverage%20floor-80%25-green)

The coverage gate fails below **80%** (`./scripts/coverage-gate.sh`).
<!-- coverage:end -->

## Verified unsupported

These capabilities are rejected or absent for reasons verifiable in Trino
itself, not because they were deferred. Checked on 2026-10-26 against the
Trino 484 documentation and the connectors shipped with Trino.

| Capability | Why a bundled connector cannot support it | Re-check trigger |
|---|---|---|
| `DENY` | Trino 484, [DENY — Limitations](https://trino.io/docs/484/sql/deny.html): "The system access controls as well as the connectors provided by default in Trino have no support for `DENY`." | A bundled connector or system access control implements `DENY`. |
| Schema-level grants | `GRANT ... ON SCHEMA` parses, but `SqlStandardAccessControlMetadata` (the Hive implementation of roles and table grants) throws "This connector does not support grants on schemas". See [GRANT](https://trino.io/docs/484/sql/grant.html). | A bundled connector implements `grantSchemaPrivileges`. |
| System roles | Stock Trino binds `DisabledSystemSecurityMetadata`, which reports "System roles are not enabled"; no bundled access-control plugin enables it. See [CREATE ROLE](https://trino.io/docs/484/sql/create-role.html). | A bundled connector enables system security metadata. |
| Catalog property read-back | [`system.metadata.catalogs`](https://trino.io/docs/484/connector/system.html#metadata-tables) exposes catalog names and connectors, not the configured property values. Without a read path there is no import or property drift. See [`trino_catalog`](docs/resources/catalog.md). | `system.metadata` exposes catalog properties, or a connector adds a read path for them. |

These entries are the public record for the corresponding design non-goals.

## Documentation

- [Architecture](ARCHITECTURE.md) — request flow, executor internals, error
  taxonomy, auth matrix and test layers.
- [Contributing](CONTRIBUTING.md) — development workflow and review gates.
- [Releasing](contributing/releasing.md) — maintainer runbook for the tag,
  publish, verification and recovery path.
- [Security](SECURITY.md) — supported versions, threat model and reporting.
- [Changelog](CHANGELOG.md).
- [View spike findings](contributing/view-spike.md) — why `trino_view` is not
  shipped yet and what a future implementation may claim.
- [Generated provider docs](docs/index.md).

## Licence

Mozilla Public License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
