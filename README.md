# Terraform Provider for Trino

[![CI](https://img.shields.io/github/actions/workflow/status/caberdo/terraform-provider-trino/test.yml?branch=main&label=CI)](https://github.com/caberdo/terraform-provider-trino/actions/workflows/test.yml)
<!-- coverage-badge:start -->
[![Coverage floor](https://img.shields.io/badge/coverage%20floor-80%25-green)](#testing-and-coverage)
<!-- coverage-badge:end -->
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/caberdo/terraform-provider-trino/badge)](https://scorecard.dev/viewer/?uri=github.com/caberdo/terraform-provider-trino)
[![Release](https://img.shields.io/github/v/release/caberdo/terraform-provider-trino?label=release)](https://github.com/caberdo/terraform-provider-trino/releases)
[![Terraform Registry](https://img.shields.io/badge/registry-caberdo%2Ftrino-7b42bc)](https://registry.terraform.io/providers/caberdo/trino)
[![OpenTofu](https://img.shields.io/badge/OpenTofu-available-ffda18)](https://search.opentofu.org/provider/caberdo/trino)
[![Go](https://img.shields.io/github/go-mod/go-version/caberdo/terraform-provider-trino)](https://golang.org/doc/devel/release.html)
[![License](https://img.shields.io/github/license/caberdo/terraform-provider-trino)](LICENSE)
[![Last commit](https://img.shields.io/github/last-commit/caberdo/terraform-provider-trino)](https://github.com/caberdo/terraform-provider-trino/commits/main)

Manage the **[Trino](https://trino.io/)** control plane as code: catalogs, schemas, catalog roles,
role grants, table privilege grants, catalog user-defined functions, views and materialized views,
managed through SQL DDL. The provider is a thin translation layer between Terraform state and Trino.
It owns no truth of its own: every read is a SQL query, every mutation is a DDL statement, and every
unsupported Trino capability is either rejected with an actionable diagnostic or documented as a
known limitation.

**8 resources and 5 data sources.**

| | |
|---|---|
| 🎯 **The SQL control plane** | Catalogs, schemas, catalog roles, role grants, table privilege grants, catalog UDFs, views and materialized views, plus five read-only data sources |
| 🔍 **Read-back you can audit** | Every `Read` claims only what it can prove; the [read-back matrix](#read-back-matrix) records what each resource detects, and unreadable attributes are preserved from state rather than zeroed |
| 🔐 **Every Trino auth mode** | `none`, `basic`, `jwt`, `oauth2` and `kerberos`, over TLS or mTLS, with every provider attribute also readable from a `TRINO_*` environment variable |
| 🧪 **Tested against real Trino** | Unit tests for every package, protocol tests through a fake coordinator, plus Docker acceptance tests against the six Trino releases in [`.github/trino-versions.txt`](./.github/trino-versions.txt) |
| ⚡ **Modern internals** | terraform-plugin-framework, plugin protocol 6.0, Terraform 1.9 or later, Go 1.27.4 |

## Contents

- [Quick start](#quick-start)
- [Requirements and compatibility](#requirements-and-compatibility)
- [Read-back matrix](#read-back-matrix)
- [Known limitations](#known-limitations)
- [Verified unsupported](#verified-unsupported)
- [Required Trino privileges](#required-trino-privileges)
- [Provider configuration](#provider-configuration)
- [Resources](#resources)
- [Data sources](#data-sources)
- [Examples](#examples)
- [Verifying a release](#verifying-a-release)
- [Testing and coverage](#testing-and-coverage)
- [Development](#development)
- [Troubleshooting and FAQ](#troubleshooting-and-faq)
- [Security, contributing and support](#security-contributing-and-support)
- [License](#license)

## Quick start

```terraform
terraform {
  required_providers {
    trino = {
      source  = "caberdo/trino"
      version = "~> 0.3"
    }
  }
}

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

```bash
terraform init
terraform plan
```

Every provider attribute falls back to a `TRINO_*` environment variable, so
credentials do not have to live in configuration:

```terraform
provider "trino" {
  # TRINO_ENDPOINT, TRINO_USER, TRINO_PASSWORD, ...
}
```

**v0.3.0** is the current release: every resource and data source is implemented, documented and
covered by unit, protocol and Docker acceptance tests. [`examples/complete`](examples/complete/main.tf)
is a working module that manages one of each resource against a local coordinator.

## Requirements and compatibility

| | |
|---|---|
| Terraform | 1.9 or later (plugin protocol 6.0); also available for [OpenTofu](https://search.opentofu.org/provider/caberdo/trino) |
| Trino | **476** or later — the minimum supported server. The CI window lives in [`.github/trino-versions.txt`](./.github/trino-versions.txt) |
| Go (to build from source) | 1.27.4 (the version in [`go.mod`](go.mod)); installing a published release needs neither Go nor the source tree |

### Supported Trino versions

- The minimum supported server is **Trino 476**. One build supports 476 and
  newer; behaviour that differs by version is gated at runtime from
  `SELECT version()`.
- `.github/trino-versions.txt` is the single source of truth for the CI matrix
  and `make testacc-matrix`. It starts at the declared floor and lists the last
  five stable releases — currently `476`, `480`, `481`, `482`, `483` and `484`.
  A pull request runs the floor and the newest release; `main` and the merge
  queue run the whole window. A monthly workflow opens an issue when the window
  goes stale.
- If the version probe fails, the provider logs a warning and proceeds
  permissively; the server remains the authority on errors. A failed probe is
  not cached, so the next caller retries it.
- Kerberos requires HTTPS; keytab and credential cache are mutually exclusive,
  and the service principal pattern must not name a realm.

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
| `trino_view` | `information_schema.tables` (`table_type = 'VIEW'`) | `security` parsed from `SHOW CREATE VIEW`; `definition` is write-only because Trino normalises it and Hive keeps a stale definition after a column rename | View dropped → detected |
| `trino_materialized_view` | `system.metadata.materialized_views` | `properties` for configured keys from `SHOW CREATE MATERIALIZED VIEW` (derived keys ignored); `query` and `grace_period` are write-only because Trino stores a normalised query and the 476 read path omits the grace period | View dropped → detected; `Read` never refreshes |

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
- **`trino_view.definition` is write-only.** Trino stores a normalised
  definition, and Hive keeps a stale one after an underlying column is renamed
  or dropped. A text comparison would either produce a permanent diff or report
  no drift for a broken view, so `Read` preserves the configured text and
  detects only existence and the `security` mode. Renaming or dropping a
  referenced column can therefore invalidate a Hive view without a plan (see the
  [spike findings](contributing/view-spike.md)).
- **Views need a connector that implements them.** `trino_view` runs its full
  lifecycle on the Hive and Memory connectors, which the acceptance suite
  exercises. Hive views require `hive.security=sql-standard` and the `admin`
  role.
- **Importing a view leaves `definition` to configuration.** `SHOW CREATE VIEW`
  returns a normalised statement and cannot recover the configured text, so an
  import fills the identity and `security` and the next apply runs
  `CREATE OR REPLACE VIEW` with the configured `definition`.
- **Materialized views need a connector that stores them.** `trino_materialized_view`
  works on Iceberg catalogs backed by a Hive metastore. The Iceberg REST
  catalog rejects `CREATE MATERIALIZED VIEW`, and the acceptance matrix runs the
  `iceberg` catalog from `docker-compose.yml` for this reason. A connector
  without materialized-view support produces a diagnostic that names the
  connector instead of a raw `NOT_SUPPORTED`.
- **`trino_materialized_view` query and properties are raw SQL.** The provider
  quotes the catalog, schema, name and property names, but the query and
  property values are authored SQL and are not escaped. Trino stores a
  normalised query, so `Read` keeps the configured `query` and `grace_period`
  and cannot detect an out-of-band change to either; `grace_period` is also
  absent from the 476 read path. `properties` are compared for the keys
  configured in state only: Iceberg also reports values derived from the
  storage table (`format`, `location`, `format_version`, `storage_schema`), so
  derived keys are ignored and a configured key the server does not report is
  dropped from state, which surfaces as a plan. Import recovers the identity
  and the normalised query; the first apply after import replaces the view,
  because the unreadable attributes require replacement.
- **Materialized-view refresh is explicit.** `Read` never runs
  `REFRESH MATERIALIZED VIEW`. The view is populated at create time when
  `refresh_on_create` is true (the default), and otherwise only when
  `refresh_token` changes, the view is replaced, or an external scheduler
  refreshes it. Change `refresh_token` to request a refresh from Terraform.

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

## Required Trino privileges

| Resource | Identity needs |
|---|---|
| `trino_catalog` | `CREATE CATALOG` / `DROP CATALOG`, plus `catalog.management=dynamic` on the coordinator. |
| `trino_schema` | `CREATE SCHEMA`, `DROP SCHEMA` (`CASCADE` for `force_destroy`), and `ALTER SCHEMA ... SET AUTHORIZATION` when changing the owner. |
| `trino_role` | Role-admin rights on the catalog; the creating user becomes role admin. |
| `trino_grant_role` | `WITH ADMIN OPTION` on the role being granted. |
| `trino_grant_privileges` | Ownership or admin rights on the table, and `GRANT OPTION` to delegate it. |
| `trino_function` | `CREATE FUNCTION` / `DROP FUNCTION` on the catalog and schema, and a connector that implements catalog UDF storage. |
| `trino_view` | `CREATE VIEW` / `DROP VIEW` on the schema. Hive views additionally require `hive.security=sql-standard` and the `admin` role. |
| `trino_materialized_view` | `CREATE MATERIALIZED VIEW`, `DROP MATERIALIZED VIEW` and `REFRESH MATERIALIZED VIEW` on the schema, and a connector that implements materialized-view storage. |

Symptoms and fixes for the diagnostics these sections reference are in
[Troubleshooting and FAQ](#troubleshooting-and-faq).

## Provider configuration

| Attribute | Type | Environment | Notes |
|---|---|---|---|
| `endpoint` | string | `TRINO_ENDPOINT` | Coordinator URL, for example `https://trino.example:8443`. |
| `auth` | string | `TRINO_AUTH` | `none`, `basic`, `jwt`, `oauth2` or `kerberos`. Inferred from the credentials when unset. |
| `user` | string | `TRINO_USER` | Defaults to `terraform`. |
| `password` | string, sensitive | `TRINO_PASSWORD` | Basic auth. Requires an `https` endpoint. |
| `access_token` | string, sensitive | `TRINO_ACCESS_TOKEN` | Static JWT; requires an `https` endpoint; also sent as `X-Trino-Authorization-User`. |
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
| `extra_credentials` | map(string), sensitive | — | `X-Trino-Extra-Credential` on every request; keys must be valid header names. |
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
| `jwt` | `access_token` over `https`. Token over `http` is rejected before dialling. |
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
| [`trino_view`](docs/resources/view.md) | `CREATE/DROP VIEW`, `CREATE OR REPLACE VIEW` for definition and security changes | `catalog.schema.name` |
| [`trino_materialized_view`](docs/resources/materialized_view.md) | `CREATE/DROP MATERIALIZED VIEW`, explicit `REFRESH MATERIALIZED VIEW` | `catalog.schema.name` |

## Data sources

| Data source | Returns |
|---|---|
| [`trino_catalogs`](docs/data-sources/catalogs.md) | Catalog names and connectors, optional `name_prefix` filter. |
| [`trino_schemas`](docs/data-sources/schemas.md) | Schema names for a catalog. |
| [`trino_roles`](docs/data-sources/roles.md) | Catalog role names. |
| [`trino_tables`](docs/data-sources/tables.md) | Table and view names with `table_type`, optional prefix and type filters. |
| [`trino_table_privileges`](docs/data-sources/table_privileges.md) | Grantees, privilege types and grantability for a table, or for every table in a schema when `table` is omitted. |

## Examples

| Path | What it shows |
|---|---|
| [`examples/complete`](./examples/complete) | A full module: catalog, schema, role, role grant, table grant, function, view and materialized view |
| [`examples/resources/<resource>`](./examples/resources) | One `resource.tf` per resource, used to generate the docs |
| [`examples/data-sources/<data source>`](./examples/data-sources) | One `data-source.tf` per data source |
| [`examples/provider`](./examples/provider) | Provider configuration |

`make validate-examples` checks every example against the provider schema.

## Verifying a release

Every release is built by [GoReleaser](https://github.com/goreleaser/goreleaser) from a GPG-signed,
GitHub-verified tag, and ships eleven assets:

| Asset | Purpose |
|---|---|
| `terraform-provider-trino_<version>_{darwin,linux,windows}_{amd64,arm64}.zip` | The six provider binaries |
| `terraform-provider-trino_<version>_SHA256SUMS` | Checksums for every artifact, including the registry manifest |
| `terraform-provider-trino_<version>_SHA256SUMS.sig` | GPG signature over the checksums |
| `terraform-provider-trino_<version>_manifest.json` | The Terraform registry manifest (plugin protocol versions) |
| `terraform-provider-trino_<version>_provenance.intoto.jsonl` | SLSA build provenance over every checksummed artifact |
| `sbom.spdx.json` | SPDX software bill of materials |

Replace `<version>` with the release you are verifying. Start with the release itself:

```bash
gh release view v<version> --repo caberdo/terraform-provider-trino \
  --json tagName,isDraft,isPrerelease,assets \
  --jq '{tagName, isDraft, isPrerelease, assets: [.assets[].name]}'
```

A healthy release is neither draft nor prerelease and carries all eleven assets. Download and verify
the checksums:

```bash
WORK=$(mktemp -d)
cd "$WORK"
gh release download v<version> --repo caberdo/terraform-provider-trino \
  --pattern 'terraform-provider-trino_<version>_*'
gpg --verify terraform-provider-trino_<version>_SHA256SUMS.sig \
  terraform-provider-trino_<version>_SHA256SUMS
shasum -a 256 -c terraform-provider-trino_<version>_SHA256SUMS   # macOS
# sha256sum -c terraform-provider-trino_<version>_SHA256SUMS      # Linux
```

`shasum`/`sha256sum` must report every archive and the manifest as `OK`. If `gpg` reports
`Can't check signature: No public key`, import the signing key first:

```bash
gh api users/dennismdejong/gpg_keys --jq '.[].raw_key' | gpg --import
```

The checksum signature proves the checksums are ours; the build provenance proves *where the
binaries come from*:

```bash
gh attestation verify terraform-provider-trino_<version>_linux_amd64.zip \
  --repo caberdo/terraform-provider-trino
```

The registry manifest must report plugin protocol `["6.0"]`, and the signed tag must resolve to a tag
object with a valid signature (`git verify-tag v<version>` after `git fetch --tags` is the same check
locally). The SBOM is a separate release asset:

```bash
gh release download v<version> --repo caberdo/terraform-provider-trino --pattern sbom.spdx.json
jq -r '.spdxVersion, (.packages | length)' sbom.spdx.json
```

Terraform records the checksums in `.terraform.lock.hcl` when it installs a provider; commit that
file so every `terraform init` re-checks the same artifacts. The full runbook, including the Registry
check and the recovery path for a failed publish, is in
[`contributing/releasing.md`](contributing/releasing.md#4-verify).

## Testing and coverage

| Target | Purpose |
|---|---|
| `make test` | Unit tests. |
| `make fuzz` | Fuzz the quoting and schema parsers for `FUZZTIME` (default `10s`) per target. |
| `make coverage` | Unit coverage profile (`coverage.out`) and summary. |
| `make coverage-gate` | Fail when `coverage.out` is below the module floor in `.github/coverage-baseline.txt` or a package floor in `.github/coverage-floors.txt`; run `make coverage` first. |
| `make coverage-readme` / `make coverage-readme-check` | Regenerate the README coverage badge and floor statement, or fail when they drift from the baseline. |
| `make coverage-html` | Generate the HTML coverage report. |
| `make coverage-merge` | Merge the unit and acceptance profiles into one number. |
| `make testacc` | Acceptance tests against an already reachable coordinator (`TF_ACC=1`). |
| `make testacc-docker` | Acceptance tests against Trino in Docker. |
| `make testacc-matrix` | Acceptance tests against every version in `.github/trino-versions.txt`. |

Acceptance tests need Docker Compose (Podman works too: `COMPOSE=podman-compose`).
The compose environment runs three catalogs: `memory`, `hive` (file metastore)
and `iceberg` (Hive metastore, used for materialized-view behaviour and
Iceberg-specific reads):

```bash
make testacc-docker TRINO_VERSION=484
make testacc-docker COMPOSE=podman-compose
make testacc-matrix
```

<!-- coverage:start -->
The coverage gate fails below **80%** (`make coverage-gate`).
<!-- coverage:end -->

## Development

The toolchain versions are listed under
[Requirements and compatibility](#requirements-and-compatibility). These are
the `GNUmakefile` targets for building, checking and releasing:

| Target | Purpose |
|---|---|
| `make build` | Build `bin/terraform-provider-trino`. |
| `make install` | Build and copy into `~/.terraform.d/plugins`; pass `VERSION=0.1.2` for a release-shaped plugin directory. |
| `make lint` / `make lint-fix` | Run `golangci-lint`, optionally applying fixes. |
| `make fmt` / `make vet` | `gofmt` and `go vet`. |
| `make validate-examples` | `terraform fmt`/`validate` every example. |
| `make generate` | Regenerate `docs/` with `tfplugindocs`. |
| `make release-check` | Fail when the newest `CHANGELOG.md` section and the README install constraint disagree. The last git tag is reported, not required. |

To build from source or test unreleased changes, point Terraform at a local build with a
`dev_overrides` block in `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "caberdo/trino" = "/path/to/terraform-provider-trino/bin"
  }
}
```

The generated reference index is [`docs/index.md`](docs/index.md); `make generate` rewrites it, so
never hand-edit it. The contribution workflow, review gates and contract-change protocol are in
[`CONTRIBUTING.md`](CONTRIBUTING.md); request flow and test layers are in
[`ARCHITECTURE.md`](ARCHITECTURE.md). `main` is protected: every change lands through a pull request
with green CI.

## Troubleshooting and FAQ

| Symptom | Cause | Fix |
|---|---|---|
| `Error: Operation not supported` with `Dynamic catalog management is not enabled. Set catalog.management=dynamic in the Trino coordinator configuration and restart it: ...` | The coordinator was started without dynamic catalog management, so `CREATE CATALOG` cannot run. | Set `catalog.management=dynamic` in `etc/config.properties` and restart the coordinator. |
| A warning `Trino version is older than the supported minimum` with `version` and `minimum` fields, or `Trino version probe failed; continuing without a detected version` | The coordinator is older than the supported minimum, or `SELECT version()` could not be read. A failed probe is non-fatal and is retried by the next call. | Upgrade to a supported Trino release. The warning does not stop the provider; the server remains the authority on errors. |
| `terraform plan` proposes `# trino_grant_privileges.<name> will be created` for a grant that already exists, and `data.trino_table_privileges` returns an empty `privileges` list | The provider identity cannot see other principals' grants in `information_schema.table_privileges`; Trino filters that view by authorization. | Run Terraform as an identity with admin rights or `SELECT` on `information_schema.table_privileges`, then `terraform apply` or re-import the resource. |
| A `trino_grant_role` revoked out of band still shows `No changes. Your infrastructure matches the configuration.` | Trino cannot list another principal's role grants, so the resource is state-authoritative by design. | Re-apply the grant, or `terraform state rm` the resource and import it again with `catalog.role:USER:grantee`. |
| `Error: Password requires TLS` with `Basic auth sends the password to the server, so the endpoint must use https.` | `auth = "basic"` is combined with an `http://` endpoint. | Use an `https://` endpoint (with `ca_cert_file` or `insecure_skip_verify` if needed), or `auth = "none"` for a coordinator without authentication. |
| `SHOW ROLES FROM hive` reports `analyst` for a role configured as `Analyst`, while `terraform plan` stays clean | Hive normalises role names to lowercase; the provider matches them case-insensitively and keeps the configured spelling in state. | Use lowercase role names. Do not rewrite state to the server spelling. |
| `Error: Failed to query available provider packages` with either `provider registry.terraform.io/caberdo/trino was not found in any of the search locations` or `no available releases match the given constraints ~> 0.2` | `make install` names the plugin directory after `git describe`, for example `v0.1.1-4-gd1d6e16`, which is not a release version Terraform's filesystem discovery accepts. | Install with an explicit version (`make install VERSION=0.2.0`) or use a `dev_overrides` block, which ignores version constraints. |

The error classification behind these diagnostics is described in
[Architecture → Error taxonomy](ARCHITECTURE.md#error-taxonomy).

## Security, contributing and support

- [`SECURITY.md`](SECURITY.md) — supported versions, threat model and how to report a vulnerability.
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — development workflow, review gates and the contract-change protocol.
- [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) — the ground rules for the community.
- [`ARCHITECTURE.md`](ARCHITECTURE.md) — request flow, executor internals, error taxonomy, auth matrix and test layers.
- [`contributing/releasing.md`](contributing/releasing.md) — maintainer runbook for the tag, publish, verification and recovery path.
- [`contributing/kerberos-acceptance.md`](contributing/kerberos-acceptance.md) — a reproducible KDC and Trino setup for Kerberos.
- [`contributing/view-spike.md`](contributing/view-spike.md) — the read-back evidence behind `trino_view` and the limitation it accepts.
- [`CHANGELOG.md`](CHANGELOG.md) — what changed in every release.

## License

Mozilla Public License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
