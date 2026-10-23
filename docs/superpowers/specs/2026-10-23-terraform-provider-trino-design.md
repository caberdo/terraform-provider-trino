# Terraform Provider for Trino — Design

**Status:** Draft for approval
**Date:** 2026-10-23
**Reference implementation:** [`caberdo/terraform-provider-gravitino`](https://github.com/caberdo/terraform-provider-gravitino)
**Module:** `github.com/caberdo/terraform-provider-trino`
**Registry address:** `registry.terraform.io/caberdo/trino`

---

## 1. Intent

Give platform teams a declarative, reviewable way to manage the Trino control
plane from Terraform: catalogs, schemas, roles, role grants and privilege
grants — with the same engineering standard as the Gravitino provider
(plugin framework, per-resource packages, layered tests, lint/security gates,
generated docs, signed releases).

The provider is a thin, honest translation layer between Terraform state and
Trino SQL DDL. It owns no truth of its own: every read is a SQL query, every
mutation is a DDL statement, and every unsupported Trino capability is either
rejected with an actionable diagnostic or documented as a known limitation.

### Why this shape

- Trino has no REST control-plane API for these objects. The only public,
  stable interface is SQL over the client protocol. So the "client" is a
  statement executor, not an HTTP resource client.
- Trino's control plane is narrower than Gravitino's. Users are not a Trino
  object, catalogs cannot be altered, and several grants cannot be read back.
  A provider that pretends otherwise would silently drift. This design makes
  the read-back envelope explicit per resource.

---

## 2. Goals and non-goals

### Goals

| # | Goal |
|---|------|
| G1 | Manage `CREATE/DROP CATALOG` (dynamic catalog management) declaratively. |
| G2 | Manage `CREATE/ALTER/DROP SCHEMA` including properties, owner and force-destroy. |
| G3 | Manage catalog roles (`CREATE/DROP ROLE … IN catalog`). |
| G4 | Manage role grants (`GRANT role TO USER/ROLE … [WITH ADMIN OPTION]`). |
| G5 | Manage table privilege grants (`GRANT`/`REVOKE`). |
| G6 | Read-only data sources for catalogs, schemas, roles and table privileges. |
| G7 | Authenticate with none, basic/LDAP/password-file, static JWT, OAuth2 client credentials or Kerberos over TLS. |
| G8 | Production quality gates identical in spirit to the reference repo: lint, gosec, race tests, generated-doc drift, acceptance suite, signed GoReleaser release. |
| G9 | Every resource has a documented read-back contract, import format (or a documented reason it has none) and limitation list. |

### Non-goals (v1)

| # | Non-goal | Reason |
|---|----------|--------|
| N1 | `trino_user` resource or data source | Trino has no `CREATE USER`/`DROP USER` and no user listing. Users come from the authenticator (LDAP, OAuth2, JWT, Kerberos, password file). Users appear only as grantees. |
| N1b | System roles (`CREATE ROLE` without `IN catalog`) | Stock Trino binds `SystemSecurityMetadata` to `DisabledSystemSecurityMetadata`: "System roles are not enabled", and no bundled access-control plugin implements it. Shipping a resource that only works with a custom plugin would be untestable. |
| N1c | Schema-level privilege grants (`GRANT … ON SCHEMA`) | The SQL exists, but `SqlStandardAccessControlMetadata` (Hive, the only bundled implementation of table grants and roles) does not implement `grantSchemaPrivileges`; the SPI default throws "This connector does not support grants on schemas". No bundled connector can exercise it. |
| N2 | Password-file / LDAP / IdP administration | Requires out-of-band filesystem or directory access; not a Trino SQL capability. |
| N3 | `ALTER CATALOG` | Does not exist. Catalog changes are drop+create (`RequiresReplace`). |
| N4 | Kerberos **end-to-end CI** | Kerberos is implemented and unit-tested (config mapping + validation); a full KDC/HTTPS acceptance environment is a documented manual runbook rather than a CI job. The driver owns the SPNEGO implementation. |
| N4b | Browser-based external authentication | Requires an interactive human and a token cache; unsuitable for Terraform runs and CI. |
| N5 | Table/view/function/materialized-view management | Data-plane objects, out of scope; this provider manages the control plane. |
| N6 | `DENY` | Available in Trino but semantically distinct from grants; follow-up. |
| N7 | Row filters, column masks, session-property administration | Follow-up. |
| N8 | Drift detection where Trino exposes no read path | Documented per resource; never faked. |

### Anti-goals

- A provider that reports "no drift" by trusting state when a read path exists.
- Regex-parsing arbitrary Trino DDL as the primary source of truth.
- String-interpolating user input into SQL without identifier/literal quoting.
- Hiding destructive semantics behind in-place updates that Trino cannot perform.
- Logging statements that contain catalog credentials.

---

## 3. Hard constraints (verified against Trino 484 docs and source)

These are design inputs, not preferences. Each is enforced by a test or a
documented limitation.

| ID | Constraint | Consequence |
|----|------------|-------------|
| C1 | No `CREATE USER`, no `DROP USER`, no user listing. | No user resource. Grantees are arbitrary principal strings (LDAP DNs, `alice@example.com`, `DOMAIN\alice`), validated as non-empty, control-character-free, ≤255 bytes and always quoted. They are **not** restricted to SQL identifier grammar. |
| C2 | `CREATE CATALOG`/`DROP CATALOG` require `catalog.management=dynamic`. | Catalog create failure is mapped to an actionable diagnostic. |
| C3 | No `ALTER CATALOG`. | Every catalog configuration attribute is `RequiresReplace`. |
| C4 | `SHOW CREATE SCHEMA` exists; `ALTER SCHEMA` only supports `RENAME` and `SET AUTHORIZATION`. Hive implements `setSchemaAuthorization`, so owner is updatable in place. | Schema properties are `RequiresReplace`; owner is updated in place; properties are read best-effort by parsing `SHOW CREATE SCHEMA`. |
| C5 | `GRANT`/`REVOKE` support `ON TABLE` and `ON SCHEMA`; no catalog-level privileges. Only table grants are implemented by a bundled connector. | The grant resource models tables only in v1 (see N1c). |
| C6 | `SHOW GRANTS`/`SHOW ROLE GRANTS` list only the session user's grants. `information_schema.applicable_roles` is current-user scoped. | Role grants have no general read path → state-authoritative read, documented. Table grants are read via `information_schema.table_privileges`. |
| C7 | `information_schema.table_privileges` exposes grantor, grantor_type, grantee, grantee_type, catalog/schema/table, privilege_type, is_grantable. | Table privilege drift is detectable; use it. |
| C8 | `system.metadata.catalogs` exposes `catalog_name`, `connector_id`, `connector_name` on 476+; `state` exists only on 484+. | Catalog existence + connector drift are detectable; properties are not. v1 selects only the columns common to 476 and 484. |
| C9 | `SHOW ROLES [FROM catalog]` lists catalog roles. | Role existence is detectable. |
| C10 | `DROP CATALOG` has no `IF EXISTS`; `DROP ROLE`/`DROP SCHEMA` do. | Catalog delete checks error name before failing. |
| C11 | Trino DDL is not transactional. | Multi-statement updates are minimal diffs and must converge on retry. |
| C12 | The driver requires HTTPS for password auth. | Provider rejects `password` + `http://` with a targeted diagnostic. |
| C13 | `trino-go-client` requires Trino ≥ 372 and Go ≥ 1.27. | Version floor for the provider is Trino 476 (project decision). |
| C14 | Driver polling hides `nextUri` handling; context deadline and `QueryTimeout` are the only bounds. | Every operation runs under a per-operation context; `query_timeout` default 5m. |
| C15 | System roles are disabled on stock Trino (`DisabledSystemSecurityMetadata`). | `trino_role.catalog` is **required**; system roles are a documented non-goal. |
| C16 | Hive lowercases schema, role and principal names; a quoted `CREATE ROLE "Analyst"` reads back as `analyst`. | Reads match names case-insensitively and keep the configured spelling in state; follow-up statements use the configured name (Hive normalizes lookups too); grant lookups use `lower(col) = lower(?)`. Acceptance pins the no-perpetual-diff behaviour. |
| C17 | SQL-standard Hive requires the `admin` role to be enabled for DDL, and catalog roles must be created with `CREATE ROLE <role> IN <catalog>`. | The provider exposes `roles = { <catalog> = "<role>" }`, sent on every statement as `X-Trino-Role: <catalog>=ROLE{<role>}`; Trino rejects the bare role name with HTTP 400. The file metastore auto-grants `admin` to the users `admin`, `hive` and `hdfs`, which solves bootstrap; acceptance uses `user = "admin"` plus `roles = { hive = "admin" }`. |
| C18 | Kerberos requires HTTPS; keytab and credential cache are mutually exclusive; the service principal pattern must not name a realm. | The provider validates all three before dialing; the driver's own validators are a second line of defence. SPNEGO is set per request by the driver, so a custom `HTTPClient` does not bypass it. |
| C19 | OAuth2 client-credentials endpoint discovery exists only on Trino 484+; 476 is supported. | `oauth2_token_endpoint` is required whenever `auth = "oauth2"`, so one provider build works on both. |
| C20 | The Go driver defaults the Kerberos service name to `trino`; Trino's coordinator convention is `HTTP`. | The provider defaults `kerberos_service_name` to `HTTP` and always sets it explicitly. |

### Version policy

- **Minimum supported server: Trino 476.**
- One build supports 476 and newer. Features are gated at runtime from
  `SELECT version()` where behaviour is known to differ.
- CI acceptance matrix: `476` and the latest stable release at implementation
  time (484 as of this document), single-sourced in `.github/trino-versions.txt`.
- If version detection fails, the provider logs a warning and proceeds
  permissively; the server remains the final authority on errors.

---

## 4. Architecture

```
Terraform CLI
     │ plugin protocol 6.0
     ▼
main.go ── providerserver.Serve ──► internal/provider
                                        │  schema, env fallback, Configure
                                        │  builds Config → Executor
                                        ▼
                              internal/providerdata.Data
                                   │            │
                    ┌──────────────┘            └──────────────┐
                    ▼                                          ▼
          internal/resources/*                       internal/datasources/*
          catalog, schema, role,                     catalogs, schemas,
          grantrole, grantprivileges                 roles, tableprivileges
                    │                                          │
                    └──────────────► internal/trino ◄───────────┘
                                     Executor (database/sql)
                                     quoting, errors, version, rows
                                            │
                                            ▼
                                  trino-go-client /v1/statement
                                            │
                                            ▼
                                      Trino coordinator
```

### Package layout

```
.
├── .github/
│   ├── ISSUE_TEMPLATE/{bug_report.md,feature_request.md}
│   ├── workflows/{test.yml,release.yml,create-release-tag.yml,scorecard.yml}
│   ├── CODEOWNERS
│   ├── PULL_REQUEST_TEMPLATE.md
│   ├── dependabot.yml
│   ├── goreleaser.yml
│   ├── github-action-markdown-link-check-config.json
│   └── trino-versions.txt
├── .golangci.yml                  # root, per the requested layout
├── .editorconfig
├── .gitignore
├── AGENTS.md
├── ARCHITECTURE.md
├── CHANGELOG.md
├── CODE_OF_CONDUCT.md
├── CONTRIBUTING.md
├── GNUmakefile
├── LICENSE                        # MPL-2.0
├── NOTICE
├── README.md
├── SECURITY.md
├── docker-compose.yml
├── test/acceptance/trino/
│   ├── etc/
│   │   ├── config.properties     # catalog.management=dynamic, catalog.store=memory
│   │   ├── jvm.config
│   │   ├── node.properties
│   │   └── log.properties
│   ├── healthcheck.sh            # ready only once /v1/info reports starting:false
│   └── init-catalogs.sh          # creates the memory + hive catalogs over SQL
├── go.mod
├── go.sum
├── main.go
├── terraform-registry-manifest.json
├── internal/
│   ├── provider/
│   │   ├── provider.go            # provider schema + Configure + Metadata
│   │   ├── config.go              # Config struct, env fallback, validation, HTTP client
│   │   ├── provider_test.go
│   │   └── config_test.go
│   ├── providerdata/
│   │   └── providerdata.go        # Data{Executor, Timeout, Version}; FromProviderData
│   ├── trino/
│   │   ├── executor.go            # Executor interface + sqlExecutor
│   │   ├── rows.go                # Row + typed accessors
│   │   ├── quoting.go             # QuoteIdentifier, QuoteLiteral, QuoteQualified
│   │   ├── errors.go              # ErrTrino classification + diagnostic builders
│   │   ├── version.go             # Version probing and comparison
│   │   ├── tls.go                 # *tls.Config / *http.Client construction
│   │   └── *_test.go
│   ├── trinotest/
│   │   ├── fake_executor.go       # scripted Executor fake
│   │   ├── fake_server.go         # /v1/statement protocol fake
│   │   ├── harness.go             # CRUD harness for resource unit tests
│   │   └── *_test.go
│   ├── resourceutil/
│   │   ├── import.go              # import ID parsing
│   │   └── state.go               # null/unknown/set helpers
│   ├── acctest/
│   │   └── acctest.go             # TF_ACC guards, SQL setup helpers, factories
│   ├── resources/
│   │   ├── catalog/{resource.go,resource_test.go,acceptance_test.go}
│   │   ├── schema/{resource.go,resource_test.go,acceptance_test.go}
│   │   ├── role/{resource.go,resource_test.go,acceptance_test.go}
│   │   ├── grantrole/{resource.go,resource_test.go,acceptance_test.go}
│   │   └── grantprivileges/{resource.go,resource_test.go,acceptance_test.go}
│   └── datasources/
│       ├── catalogs/{datasource.go,datasource_test.go}
│       ├── schemas/{datasource.go,datasource_test.go}
│       ├── roles/{datasource.go,datasource_test.go}
│       └── tableprivileges/{datasource.go,datasource_test.go}
├── examples/
│   ├── provider/provider.tf
│   ├── complete/main.tf
│   ├── resources/<tf_name>/{resource.tf,import.sh}
│   └── data-sources/<tf_name>/data-source.tf
├── templates/{index.md.tmpl,resources.md.tmpl,data-sources.md.tmpl}
├── docs/                          # generated by tfplugindocs, never hand-edited
├── scripts/{validate-examples.sh,coverage-summary.sh,coverage-gate.sh}
└── tools/tools.go
```

**Deviation from the requested tree, deliberately:** the flat
`internal/provider/resource_*.go` layout is replaced by one package per
resource. The reference repository's real structure is per-resource packages;
it is what makes parallel agents safe (no shared file, tests travel with code)
and it matches the reference's `ARCHITECTURE.md`. `internal/provider` stays
small and is touched by exactly one lane.

### What we take from the reference repository

| Pattern | Reference | Adoption here |
|---|---|---|
| Entry point | `main.go` → `providerserver.Serve` with a version injected by ldflags | Identical, address changed to `registry.terraform.io/caberdo/trino`. |
| Provider identity | `Metadata` sets `TypeName`; `New(version)` closure | Identical, `trino`. |
| Provider config | Attribute → env fallback → validation → client in `Configure`; `AddAttributeError` on `path.Root(...)` | Identical shape; validation rules are Trino-specific (TLS/auth conflicts). |
| Client | One struct handed to resources via `resp.ResourceData`/`DataSourceData`; typed provider-data extraction | `providerdata.Data` wrapping `trino.Executor`; same hand-off, `FromProviderData` mirrors `client.FromProviderData`. |
| Resource layout | One package per resource with schema + CRUD + tests co-located | Identical (deliberate deviation from the flat `resource_*.go` in the brief). |
| Schema/state | `types.String/Bool/Map/Set/Object`, `UseStateForUnknown`, `RequiresReplace`, merge-response helper | Identical semantics; our merge helper is per resource because the wire shape is SQL rows, not JSON objects. |
| Diagnostics | `AddError` for API failures, `AddAttributeError` for input, `tflog.Debug` per operation | Identical, plus a typed error classifier for `ErrTrino`. |
| Import | `resourceutil.SplitImportID` + `SetAttribute` | Same helper shape, separator-aware; catalog opt-out documented. |
| Tests | Unit tests with a fake server (`httptest`) + `resource.Test` acceptance | Same three layers; the fake speaks `/v1/statement` instead of REST, plus a scripted executor for resource logic. |
| Tooling | `.golangci.yml`, gosec, race tests, docs-drift gate, coverage gate, GoReleaser + manifest, pinned actions | Identical set; paths kept at the root where the brief asked for them. |
| Agent docs | `AGENTS.md`, `ARCHITECTURE.md`, `CONTRIBUTING.md`, PR/issue templates, contract-style plans | Identical, with a lane-ownership table added for parallel work. |

Not adopted: the REST `client/auth` package (Trino auth is driver configuration),
the `models` package (no JSON envelope), server-version feature gates for
capabilities absent from 476+, and the wide 21-resource surface.

### Statement execution model

The executor is the only path to Trino. It does not reimplement the client
protocol; `trino-go-client` already implements it correctly, including spooled
results and retries. What the provider owns is the lifecycle around it.

| Concern | Behaviour |
|---|---|
| Request | `POST /v1/statement` with the SQL body and `X-Trino-User`, `X-Trino-Source`, `X-Trino-Catalog`, `X-Trino-Schema`, `X-Trino-Role`, `X-Trino-Session`, `X-Trino-Extra-Credential` and `Authorization` headers, all derived from provider configuration. Identical headers on every request; no per-statement `SET SESSION`, so pooled connections cannot leak session state between resources. |
| Response | JSON pages `{id, columns, data, nextUri, stats, error}`. The driver follows `nextUri` until it is absent, then returns the accumulated rows or the terminal `error` object. |
| DDL | `ExecContext` drains every page and returns only the terminal error. DDL produces no rows; the provider never parses `stats`. |
| Queries | `QueryContext` scans all pages into `[]Row`; `rows.Close()` runs before returning. |
| Polling | Owned by the driver (`nextUri`); the provider adds no polling loop of its own. |
| Timeout | Each Terraform operation wraps the context with `providerdata.Data.Timeout` (from `query_timeout`, default 5m). `trino.Config.QueryTimeout` additionally asks the server to cancel a runaway query. A context deadline shorter than the server timeout wins. |
| Retry | Driver-level: 5 attempts over a 2-minute window for transient network errors and 502/503/504. Trino semantic errors (`ErrTrino`) are never retried; they are classified and returned as diagnostics. |
| Cancellation | Cancelling the Terraform context cancels the in-flight HTTP request; Trino's `nextUri` chain stops. The provider surfaces `context.Canceled` as a diagnostic naming the operation. |
| Version probe | One `SELECT version()` per process, cached; failure is non-fatal and logged. |

### Dependency rules

1. `internal/trino` imports only stdlib + `trino-go-client` + `go-version`.
2. `internal/providerdata` imports `internal/trino` and framework `diag`.
3. `internal/resources/*` and `internal/datasources/*` import `trino`,
   `providerdata`, `resourceutil`, `models`-equivalent local structs only.
4. Nothing under `internal/resources` imports `internal/provider` (no cycles).
5. `internal/provider` imports every resource/datasource package and is the
   only place registration lives.

---

## 5. Frozen interfaces

These signatures are the contract between parallel lanes. They are declared in
`internal/trino` and `internal/providerdata` in the foundation task and must not
change without a contract-change note in the plan.

```go
// package trino

type Executor interface {
    // Exec runs a statement that returns no rows (DDL).
    Exec(ctx context.Context, statement string, args ...any) error
    // Query runs a query and returns all rows.
    Query(ctx context.Context, statement string, args ...any) ([]Row, error)
    // QueryOne returns the first row and false when the result is empty.
    QueryOne(ctx context.Context, statement string, args ...any) (Row, bool, error)
    // Version returns the cached server version, probing once.
    Version(ctx context.Context) (string, error)
    // Close releases the pool.
    Close() error
}

// Row is keyed by lower-case column name.
type Row map[string]any

func (r Row) String(col string) (string, bool)
func (r Row) Bool(col string) (bool, bool)
func (r Row) Int64(col string) (int64, bool)
// SoleString returns the only value in a single-column SHOW result.
func (r Row) SoleString() (string, bool)

func QuoteIdentifier(s string) string            // `a"b` -> `"a""b"`
func QuoteLiteral(s string) string               // `a'b` -> `'a''b'`
func QuoteQualified(parts ...string) string      // `"a"."b"`
func SourceOf(err error) ErrorKind               // typed classification
func IsErrorName(err error, names ...string) bool
func IsNotFound(err error) bool
func DiagnosticFor(op, object string, err error) diag.Diagnostics
func AtLeast(version, minimum string) bool
```

```go
// package trino — connection construction, used only by internal/provider

type Options struct {
    Auth                                string // resolved: none|basic|jwt|oauth2|kerberos
    ServerURI, User, Password           string
    Source, Catalog, Schema             string
    SessionProperties, ExtraCredentials map[string]string
    Roles                               map[string]string
    AccessToken                         string
    OAuth2ClientID, OAuth2ClientSecret  string
    OAuth2TokenEndpoint, OAuth2Scope    string
    KerberosPrincipal                   string
    KerberosRealm                       string
    KerberosKeytab                      string
    KerberosCredentialCache             string
    KerberosConfigFile                  string
    KerberosServiceName                 string
    KerberosServicePrincipalPattern     string
    KerberosUseCanonicalHostname        bool
    QueryTimeout                        time.Duration
    HTTPClient                          *http.Client
}

type TLSOptions struct {
    CAFile, CAPEM                       string
    ClientCertFile, ClientCertPEM       string
    ClientKeyFile, ClientKeyPEM         string
    InsecureSkipVerify                  bool
    Headers                             map[string]string
}

func Open(Options) (Executor, error)
func NewHTTPClient(TLSOptions) (*http.Client, error)
```

```go
// package providerdata

type Data struct {
    Executor trino.Executor
    Timeout  time.Duration // per-operation bound derived from query_timeout
    Version  string        // detected server version, "" when unknown
}

func FromProviderData(v any) (*Data, diag.Diagnostics)
```

```go
// package trinotest

type FakeExecutor struct {
    // Queued responses, one per Exec/Query call, in order.
    Steps []Step
    // Calls records every statement with its args, for SQL assertions.
    Calls []Call
}

type Step struct {
    Rows []trino.Row
    Err  error
}

func (f *FakeExecutor) Exec(ctx context.Context, statement string, args ...any) error
func (f *FakeExecutor) Query(ctx context.Context, statement string, args ...any) ([]trino.Row, error)
// ... same interface as trino.Executor
```

```go
// package resourceutil

// SplitImportID splits id on sep and requires exactly n non-empty segments.
// It returns an error naming the expected format; callers append it as an
// "Invalid import ID" diagnostic.
func SplitImportID(id, sep string, n int, expected string) ([]string, error)

// StringSet/StringMap convert framework container types, tolerating null and
// unknown values by returning an empty container and no diagnostics.
func StringSet(ctx context.Context, v types.Set, diags *diag.Diagnostics) []string
func StringMap(ctx context.Context, v types.Map, diags *diag.Diagnostics) map[string]string
```

```go
// package trinotest — CRUD harness shared by all resource unit tests

func Configure(t *testing.T, r resource.Resource, fake *FakeExecutor)
func Create[Model any](t *testing.T, r resource.Resource, plan Model) (Model, diag.Diagnostics)
func Read[Model any](t *testing.T, r resource.Resource, state Model) (Model, diag.Diagnostics)
func Update[Model any](t *testing.T, r resource.Resource, state, plan Model) (Model, diag.Diagnostics)
func Delete[Model any](t *testing.T, r resource.Resource, state Model) diag.Diagnostics
```

### Resource constructors (frozen names)

| Package | Constructor | Terraform type |
|---|---|---|
| `resources/catalog` | `New()` | `trino_catalog` |
| `resources/schema` | `New()` | `trino_schema` |
| `resources/role` | `New()` | `trino_role` |
| `resources/grantrole` | `New()` | `trino_grant_role` |
| `resources/grantprivileges` | `New()` | `trino_grant_privileges` |
| `datasources/catalogs` | `New()` | `trino_catalogs` |
| `datasources/schemas` | `New()` | `trino_schemas` |
| `datasources/roles` | `New()` | `trino_roles` |
| `datasources/tableprivileges` | `New()` | `trino_table_privileges` |

---

## 6. Provider configuration

### Schema

| Attribute | Type | Req. | Sensitive | Env fallback | Notes |
|---|---|---|---|---|---|
| `endpoint` | string | yes | no | `TRINO_ENDPOINT` | `https://host:8443`; scheme http/https; no path. |
| `auth` | string | no | no | `TRINO_AUTH` | `none`, `basic`, `jwt`, `oauth2` or `kerberos`. Inferred from the credentials when unset. |
| `user` | string | no | no | `TRINO_USER` | Default `terraform`. Sent as `X-Trino-User`. |
| `password` | string | no | **yes** | `TRINO_PASSWORD` | Basic auth; HTTPS required (C12). |
| `access_token` | string | no | **yes** | `TRINO_ACCESS_TOKEN` | Static JWT bearer token. |
| `oauth2_client_id` | string | no | no | `TRINO_OAUTH2_CLIENT_ID` | Client-credentials flow. |
| `oauth2_client_secret` | string | no | **yes** | `TRINO_OAUTH2_CLIENT_SECRET` | Requires id + endpoint. |
| `oauth2_token_endpoint` | string | no | no | `TRINO_OAUTH2_TOKEN_ENDPOINT` | **Required for `auth = "oauth2"`**: endpoint discovery is 484+, and 476 is supported. |
| `oauth2_scope` | string | no | no | `TRINO_OAUTH2_SCOPE` | Optional. |
| `kerberos_principal` | string | no | no | `TRINO_KERBEROS_PRINCIPAL` | Without realm. Required with a keytab; must match a cache ticket when set. |
| `kerberos_realm` | string | no | no | `TRINO_KERBEROS_REALM` | Required with a keytab (the driver does not infer it); with a credential cache the ticket supplies it. |
| `kerberos_keytab` | string | no | **yes** | `TRINO_KERBEROS_KEYTAB` | Path to a keytab; mutually exclusive with `kerberos_credential_cache`. |
| `kerberos_credential_cache` | string | no | **yes** | `TRINO_KERBEROS_CREDENTIAL_CACHE` | Path to a FILE ccache (e.g. `FILE:/tmp/krb5cc_1000`); defaults to `KRB5CCNAME`, then `/tmp/krb5cc_<uid>`. |
| `kerberos_config_file` | string | no | no | `TRINO_KERBEROS_CONFIG_FILE` | `krb5.conf` path; system default when unset. |
| `kerberos_service_name` | string | no | no | `TRINO_KERBEROS_SERVICE_NAME` | Default `HTTP`, matching Trino's `http-server.authentication.krb5.service-name`. The driver's own default is `trino`, so the provider always sets this explicitly. |
| `kerberos_service_principal_pattern` | string | no | no | `TRINO_KERBEROS_SERVICE_PRINCIPAL_PATTERN` | Default `${SERVICE}@${HOST}`; must not name a realm. |
| `kerberos_use_canonical_hostname` | bool | no | no | `TRINO_KERBEROS_USE_CANONICAL_HOSTNAME` | Default `true`; maps to the driver's inverted `KerberosDisableCanonicalHostname`. |
| `source` | string | no | no | `TRINO_SOURCE` | Default `terraform-provider-trino`. |
| `catalog` | string | no | no | `TRINO_CATALOG` | Default catalog. |
| `schema` | string | no | no | `TRINO_SCHEMA` | Default schema. |
| `session_properties` | map(string) | no | no | — | `Config.SessionProperties`. |
| `extra_credentials` | map(string) | no | **yes** | — | `Config.ExtraCredentials`. |
| `roles` | map(string) | no | no | — | Catalog → role to enable, e.g. `{ hive = "admin" }`; `Config.Roles`, sent per catalog as `X-Trino-Role: <catalog>=ROLE{<role>}`. Required for SQL-standard Hive DDL. |
| `http_headers` | map(string) | no | **yes** | — | Static headers via a wrapping `RoundTripper` (gateway/proxy auth). |
| `query_timeout` | string | no | no | `TRINO_QUERY_TIMEOUT` | Duration, default `5m`. Parsed in `Configure`. |
| `insecure_skip_verify` | bool | no | no | `TRINO_INSECURE_SKIP_VERIFY` | TLS `InsecureSkipVerify`; docs warn. |
| `ca_cert` | string | no | no | `TRINO_CA_CERT` | PEM content; conflicts with `ca_cert_file`. |
| `ca_cert_file` | string | no | no | `TRINO_CA_CERT_FILE` | Path to PEM. |
| `client_cert` | string | no | **yes** | `TRINO_CLIENT_CERT` | PEM content; mTLS; conflicts with `client_cert_file`. |
| `client_cert_file` | string | no | no | `TRINO_CLIENT_CERT_FILE` | Path to PEM. |
| `client_key` | string | no | **yes** | `TRINO_CLIENT_KEY` | PEM content; conflicts with `client_key_file`. |
| `client_key_file` | string | no | no | `TRINO_CLIENT_KEY_FILE` | Path to PEM. |

Validation in `Configure` (not only in schema, because cross-field rules are
not expressible as attribute validators):

1. `endpoint` required and parseable; scheme `http` or `https`; no path/query.
2. `auth` is one of `none`, `basic`, `jwt`, `oauth2`, `kerberos`. When unset,
   infer: kerberos attributes present → `kerberos`; else password → `basic`;
   else access token → `jwt`; else OAuth2 client id → `oauth2`; else `none`.
3. `auth = "none"`: no credential attribute may be set. A stray password or
   token is an error, never silently ignored.
4. `auth = "basic"`: `password` required; `access_token`, `oauth2_*` and
   `kerberos_*` forbidden; `http` endpoint rejected on `password` (C12).
5. `auth = "jwt"`: `access_token` required; all other credential groups
   forbidden.
6. `auth = "oauth2"`: `oauth2_client_id`, `oauth2_client_secret` and
   `oauth2_token_endpoint` required (discovery is 484+; 476 is supported);
   other credential groups forbidden.
7. `auth = "kerberos"`: endpoint must be `https`; `kerberos_keytab` and
   `kerberos_credential_cache` are mutually exclusive; with a keytab,
   `kerberos_principal` **and** `kerberos_realm` are required (the driver's
   keytab path does not infer the realm); `kerberos_service_principal_pattern`
   must not name a realm (the driver rejects `service/realm@host`); password,
   token and OAuth2 attributes are forbidden.
8. `ca_cert` + `ca_cert_file`, `client_cert` + `client_cert_file`,
   `client_key` + `client_key_file` → conflict errors.
9. `client_cert*` without `client_key*` (and vice versa) → error.
10. `query_timeout` parses as a positive duration.
11. `session_properties` keys must be non-empty printable ASCII without
    spaces/`=`/`,` (driver rule); validate before opening the pool.
12. `roles` keys must be non-empty catalog names and values non-empty role
    names; this is the only way to enable `admin` on SQL-standard Hive.
13. Configured file paths (`ca_cert_file`, `client_cert_file`,
    `client_key_file`, `kerberos_keytab`, `kerberos_config_file`,
    `kerberos_credential_cache`) must exist and be readable; report the
    offending attribute, not a generic connection error.

### Connection construction

```
Config ──► trino.Config{
    ServerURI:               scheme://user[:password]@host,
    Source, Catalog, Schema: from config,
    SessionProperties:       map copy,
    ExtraCredentials:        map copy,
    Roles:                   map copy,
    QueryTimeout:            &timeout,
    RequestRetryTimeout:     &2m,
    RequestRetryMaxAttempts: &5,
    AccessToken:             config.access_token,
    OAuth2ClientID/Secret/TokenEndpoint/Scope: config oauth2_*,
    KerberosEnabled:         auth == "kerberos",
    KerberosPrincipal:       config.kerberos_principal,
    KerberosRealm:           config.kerberos_realm,
    KerberosKeytabPath:      config.kerberos_keytab,
    KerberosCredentialCachePath: config.kerberos_credential_cache,
    KerberosConfigPath:      config.kerberos_config_file,
    KerberosRemoteServiceName:   config.kerberos_service_name,        // default HTTP
    KerberosServicePrincipalPattern: config.kerberos_service_principal_pattern,
    KerberosDisableCanonicalHostname: !config.kerberos_use_canonical_hostname,
    HTTPClient:              custom client (always),
}
        │
        └─► trino.NewConnector → sql.OpenDB
```

`HTTPClient` is always supplied, because it is the only way to add
`http_headers` and a TLS config built from provider attributes. The driver
forbids combining `HTTPClient` with `SSL*` params, so TLS is configured on our
own transport and the `SSL*` config fields stay empty. Kerberos is **not**
bypassed by this: the driver sets the SPNEGO header per request on the
connection (`Conn.setSPNEGOHeader`), not on the transport. The client has
`Timeout: 0`; bounds come from the per-operation context and `QueryTimeout`.
`db.SetMaxOpenConns(2)`, `SetMaxIdleConns(1)`, `SetConnMaxLifetime(5m)` —
DDL does not need a pool.

`Configure` stores `*providerdata.Data` in both `resp.ResourceData` and
`resp.DataSourceData`. Version detection is best-effort: a failure logs a
warning and leaves `Version` empty.

---

## 7. Resource specifications

Common rules:

- Every identifier is quoted with `trino.QuoteIdentifier`. Every string value
  is quoted with `trino.QuoteLiteral`. No `fmt.Sprintf` of raw user input.
- Every `Create`/`Read`/`Update`/`Delete` starts by loading state/plan and
  returns immediately on `resp.Diagnostics.HasError()`.
- `Configure` converts provider data with `providerdata.FromProviderData` and
  stores the executor.
- `Create` reads the resource back before setting state when a read path exists.
  When no read path exists, the plan is the state (documented).
- `Delete` is idempotent: absence at delete time is success.
- Every resource implements `ResourceWithImportState` (except `trino_catalog`,
  see D13) and documents its ID format.
- All computed `id` attributes use `UseStateForUnknown()` only where the ID is
  stable for the resource's life; otherwise recompute on read.

### 7.1 `trino_catalog`

```hcl
resource "trino_catalog" "lakehouse" {
  name      = "lakehouse"
  connector = "iceberg"
  properties = {
    "iceberg.catalog.type" = "rest"
    "iceberg.rest.uri"     = "http://iceberg:8181"
  }
  sensitive_properties = {
    "iceberg.rest.oauth2.credential" = var.iceberg_credential
  }
}
```

| Attribute | Type | Flags |
|---|---|---|
| `id` | string | Computed |
| `name` | string | Required, `RequiresReplace` |
| `connector` | string | Required, `RequiresReplace` |
| `properties` | map(string) | Optional, `RequiresReplace` |
| `sensitive_properties` | map(string) | Optional, **Sensitive**, `RequiresReplace` |

- **Create**: read existence first; if present → error
  `"Catalog \"<name>\" already exists. trino_catalog does not support import because catalog properties cannot be read back; drop the existing catalog or choose another name."`.
  Then `CREATE CATALOG "<name>" USING "<connector>" WITH (<properties>)` with
  `properties` and `sensitive_properties` merged, keys quoted as identifiers,
  values as literals. Trino requires all property values to be varchars, so
  values are always single-quoted strings.
- **Read**: `SELECT catalog_name, connector_name FROM system.metadata.catalogs
  WHERE catalog_name = ?`. Not found → `State.RemoveResource`. `connector`
  drift is updated in state; Terraform then plans a replacement. Catalog
  properties are **not** readable; `properties`/`sensitive_properties` are
  preserved from state (documented limitation).
- **Update**: unreachable in practice (all attributes replace). Implement it to
  copy the plan to state and log, so a framework-driven no-op update cannot
  corrupt state.
- **Delete**: `DROP CATALOG "<name>"`; `CATALOG_NOT_FOUND` → success. Other
  errors → map `NOT_SUPPORTED`/dynamic-management messages to an actionable
  diagnostic.
- **Import: deliberately not implemented.** Catalog properties have no read
  path, so an import would produce empty `properties`; because every catalog
  attribute is `RequiresReplace`, the first plan would silently drop and
  recreate a live catalog. `ImportState` returns a diagnostic explaining why
  and what to do instead. Documented in the resource page. This is the one
  exception to the "import everywhere" rule (DoD #7).

### 7.2 `trino_schema`

```hcl
resource "trino_schema" "sales" {
  catalog      = "lakehouse"
  name         = "sales"
  owner        = "alice"
  owner_type   = "USER"        # or ROLE
  properties   = { location = "s3://bucket/sales" }
  force_destroy = false
}
```

| Attribute | Type | Flags |
|---|---|---|
| `id` | string | Computed (`catalog.name`) |
| `catalog` | string | Required, `RequiresReplace` |
| `name` | string | Required, `RequiresReplace` |
| `owner` | string | Optional, Computed |
| `owner_type` | string | Optional, Computed; `OneOf("USER","ROLE")`; default `USER` |
| `properties` | map(string) | Optional, `RequiresReplace` |
| `force_destroy` | bool | Optional; default `false`; delete-only |

- **Create**: `CREATE SCHEMA "<catalog>"."<name>"` + optional
  `AUTHORIZATION USER|ROLE "<owner>"` + optional `WITH (...)`.
  Then read back.
- **Read**: `SHOW SCHEMAS FROM "<catalog>"`, filter rows in Go (avoids LIKE
  escaping bugs). Match case-insensitively and keep the configured spelling in
  state (C16). Not found — including `CATALOG_NOT_FOUND` — removes the resource
  from state. Then best-effort
  `SHOW CREATE SCHEMA "<catalog>"."<name>"` and parse:
  - `AUTHORIZATION USER x` / `AUTHORIZATION ROLE x` → `owner_type`/`owner`;
  - `WITH (...)` → properties (only keys configured in state are compared).
  Parse failure logs `tflog.Warn` and preserves prior state values; it never
  fails the refresh. If `owner`/`owner_type` are unknown in state, they are set
  from the parse when available.
- **Update**: when only `owner`/`owner_type`/`force_destroy` changed:
  `ALTER SCHEMA "<catalog>"."<name>" SET AUTHORIZATION USER|ROLE "<owner>"`.
  Renames are not modeled. A `force_destroy`-only change writes plan to state
  without a statement.
- **Delete**: `DROP SCHEMA "<catalog>"."<name>"`, or `... CASCADE` when
  `force_destroy = true`. Non-empty schema error is mapped to a diagnostic that
  names `force_destroy`.
- **Import ID**: `catalog.schema`, split once on the first `.`; the schema name
  may therefore contain dots.
- **Ownership conflict note**: `owner` is optional/computed and adopting it on
  refresh means Trino's implicit owner (the creating user) appears in state.
  This is intentional and documented. `owner_type` without `owner` is rejected
  at plan time with an attribute diagnostic (and vice versa is allowed:
  `owner` defaults `owner_type = "USER"`).

### 7.3 `trino_role`

```hcl
resource "trino_role" "analyst" {
  name    = "analyst"
  catalog = "hive"       # required: system roles are disabled on stock Trino
}
```

| Attribute | Type | Flags |
|---|---|---|
| `id` | string | Computed (`catalog.name`) |
| `name` | string | Required, `RequiresReplace` |
| `catalog` | string | **Required**, `RequiresReplace` |

- **Create**: `CREATE ROLE "<name>" IN "<catalog>"`. The creating user becomes
  role admin; `WITH ADMIN` is not exposed in v1.
- **Read**: `SHOW ROLES FROM "<catalog>"`, filter in Go, case-insensitive
  match keeping configured spelling (C16). Not found — including
  `CATALOG_NOT_FOUND` — removes the resource from state.
- **Update**: `RequiresReplace` only; no `ALTER ROLE` exists in Trino.
- **Delete**: `DROP ROLE IF EXISTS "<name>" IN "<catalog>"`.
- **Import ID**: `catalog.name`.
- **System roles** are a documented non-goal (N1b). The validator rejects an
  empty `catalog` with a message that names the Trino limitation.

### 7.4 `trino_grant_role`

```hcl
resource "trino_grant_role" "analyst_to_alice" {
  role         = "analyst"
  role_catalog = "hive"        # required, matches trino_role.catalog
  grantee      = "alice"
  grantee_type = "USER"        # USER | ROLE
  admin_option = false
}
```

| Attribute | Type | Flags |
|---|---|---|
| `id` | string | Computed |
| `role` | string | Required, `RequiresReplace` |
| `role_catalog` | string | **Required**, `RequiresReplace` |
| `grantee` | string | Required, `RequiresReplace`; arbitrary principal string |
| `grantee_type` | string | Required, `OneOf("USER","ROLE")`, `RequiresReplace` |
| `admin_option` | bool | Optional; default `false`; in-place |

- **Create**: `GRANT "<role>" TO USER|ROLE "<grantee>" [WITH ADMIN OPTION]
  IN "<role_catalog>"`.
- **Read**: **no reliable read path** (C6). State is authoritative; Read logs
  debug and returns without modifying state. Documented limitation.
- **Update**: only `admin_option` changes:
  - `false → true`: `GRANT ... WITH ADMIN OPTION`;
  - `true → false`: `REVOKE ADMIN OPTION FOR "<role>" FROM USER|ROLE "<grantee>" IN "<role_catalog>"`.
- **Delete**: `REVOKE "<role>" FROM USER|ROLE "<grantee>" IN "<role_catalog>"`;
  name-based "not found" errors are treated as success.
- **Import ID**: `<role_catalog>.<role>:<grantee_type>:<grantee>`.
  Import writes state only (no verification), documented.
- **Naming note**: a `.` in a catalog or role name makes the import ID
  ambiguous; documented. Config-time names are always quoted, so dots are safe
  in normal use.

### 7.5 `trino_grant_privileges`

```hcl
resource "trino_grant_privileges" "analyst_select" {
  privileges = ["SELECT"]
  on = {
    catalog = "hive"
    schema  = "sales"
    table   = "orders"
  }
  grantee      = "analyst"
  grantee_type = "ROLE"
  grant_option = false
}
```

| Attribute | Type | Flags |
|---|---|---|
| `id` | string | Computed |
| `privileges` | set(string) | Required, `SizeAtLeast(1)`, values `OneOf("SELECT","INSERT","UPDATE","DELETE")` |
| `on` | single nested object | Required, `RequiresReplace` |
| `on.catalog` | string | Required, `RequiresReplace` |
| `on.schema` | string | Required, `RequiresReplace` |
| `on.table` | string | Required, `RequiresReplace` |
| `grantee` | string | Required, `RequiresReplace`; arbitrary principal string |
| `grantee_type` | string | Required, `OneOf("USER","ROLE")`, `RequiresReplace` |
| `grant_option` | bool | Optional, default `false`, in-place |

- **Create**: `GRANT <priv, …> ON TABLE "<catalog>"."<schema>"."<table>"
  TO USER|ROLE "<grantee>" [WITH GRANT OPTION]`.
- **Read**: `SELECT grantee, grantee_type, privilege_type, is_grantable
  FROM information_schema.table_privileges
  WHERE table_catalog = ? AND table_schema = ? AND table_name = ?
    AND lower(grantee) = lower(?) AND grantee_type = ?`
  (C16). `information_schema` renders `is_grantable` as `YES`/`NO`, so
  `Row.Bool` accepts those. Compare the returned privilege set to state; update
  `privileges` and `grant_option`. No rows → remove from state. Visibility is
  subject to Trino's authorization: a non-admin provider user may see only its
  own grants; the resource documentation recommends an admin identity for
  reliable drift detection.
- **Update**: compute the diff between state and plan:
  - additions → `GRANT ...`;
  - removals → `REVOKE ...`;
  - `grant_option` toggled → `WITH GRANT OPTION` / `REVOKE GRANT OPTION FOR`.
- **Delete**: `REVOKE <priv, …> ON TABLE ... FROM USER|ROLE ...`.
- **Import ID**: `<catalog>.<schema>.<table>:<grantee_type>:<grantee>`.
  Import is verified by the first refresh.
- **`ALL PRIVILEGES` is rejected** in v1. Trino expands it to
  `SELECT, INSERT, UPDATE, DELETE` before storing, so a config containing
  `ALL PRIVILEGES` and a read returning the expanded set would produce a
  permanent diff (or spurious revokes). Users list the four explicitly; the
  validator's error says so.

### Read-back matrix (single source of truth)

| Resource | Existence | Configured drift | Out-of-band revocation |
|---|---|---|---|
| `trino_catalog` | `system.metadata.catalogs` | `connector` only; properties not readable | detected only if the current connector differs |
| `trino_schema` | `SHOW SCHEMAS` | owner (parsed), properties (parsed, best effort) | schema dropped → detected |
| `trino_role` | `SHOW ROLES` | `catalog`/`name` only | role dropped → detected |
| `trino_grant_role` | none | none | not detected |
| `trino_grant_privileges` | `information_schema.table_privileges` | privileges + grant option | detected |

The table is reproduced in `docs/` resource pages via template partials and in
`README.md` under "Known limitations". It is a hard review gate: no resource may
claim drift detection it does not implement.

---

## 8. Data sources

| Type | Attributes in | Attributes out | Query |
|---|---|---|---|
| `trino_catalogs` | `name_prefix` (optional) | `catalogs` list of `{name, connector}` | `SELECT catalog_name, connector_name FROM system.metadata.catalogs ORDER BY catalog_name` |
| `trino_schemas` | `catalog` (required) | `schemas` list of strings | `SHOW SCHEMAS FROM "<catalog>"` |
| `trino_roles` | `catalog` (required) | `roles` list of strings | `SHOW ROLES FROM "<catalog>"` |
| `trino_table_privileges` | `catalog`, `schema`, `table` | `privileges` list of `{grantee, grantee_type, privilege_type, is_grantable}` | `information_schema.table_privileges` filtered |

All data sources:

- implement `Metadata`, `Schema`, `Configure`, `Read`;
- sort results deterministically before writing state;
- return an empty list, not an error, when the parent object has no children;
- return an error when the parent catalog/schema/table does not exist (the user
  asked for a specific object).

---

## 9. Error handling and diagnostics

### Classification

`internal/trino/errors.go`:

```go
type ErrorKind int
const (
    KindUnknown ErrorKind = iota
    KindNotFound
    KindPermissionDenied
    KindNotSupported
    KindSyntax
    KindConnection
    KindConflict
)

func SourceOf(err error) ErrorKind
```

Mapping:

| Source | Kind | Diagnostic title / detail |
|---|---|---|
| `*trino.ErrTrino` `ErrorName` contains `_NOT_FOUND` | not found | `"<Object> not found"` |
| `ErrorName == "PERMISSION_DENIED"` / `"ACCESS_DENIED"` | permission | Title names the required capability; detail names the provider user and the object. |
| `ErrorName == "NOT_SUPPORTED"` + message mentions dynamic catalog management | not supported | instructs `catalog.management=dynamic` and restart. |
| `ErrorName == "NOT_SUPPORTED"` (other) | not supported | "the connector does not support this operation". |
| `SqlState` prefix `42` | syntax | includes the server message and object, never the statement. |
| `net.Error`, `*url.Error`, TLS errors | connection | endpoint/TLS hint, redacts credentials. |
| HTTP 409 | conflict | "already exists / concurrent change". |

Never include the raw statement in a diagnostic; include operation, object
name and the server's message.

### Diagnostics style

- `AddAttributeError(path.Root("..."))` for provider config and cross-field
  validation.
- `AddError` for server-side failures.
- Titles are sentence case, no trailing period; details are complete sentences.
- No `panic`, no `log.Fatal`, no `os.Exit` outside `main`.
- All logging goes through `tflog` with structured fields
  (`operation`, `object_type`, `object_name`). Values that may contain
  credentials are never logged.

---

## 10. Testing strategy

Four layers, each with a clear owner and failure mode.

### L1 — Pure unit tests (no network)

- `quoting_test.go`: identifiers with quotes, dots, unicode, empty; literals
  with `'`, backslash, newline; qualified names.
- `errors_test.go`: table of `ErrTrino` payloads → `ErrorKind`; wrapped errors;
  non-Trino errors.
- `version_test.go`: comparison, unknown version.
- `resourceutil/import_test.go`: valid/invalid IDs for each format.
- `provider/config_test.go`: env fallback, conflict rules, auth-mode
  inference and exclusivity, password-over-http rejection, OAuth2 endpoint
  requirement, Kerberos validation (HTTPS, keytab XOR cache, principal and
  realm with keytab, realm in pattern), file-existence checks, duration
  parsing.

### L2 — Resource logic with a fake executor

`trinotest.FakeExecutor` records `(statement, args)` and replays queued
`Step`s. Each resource test asserts:

1. the exact SQL emitted (golden string, including quoting);
2. the args passed (never interpolated);
3. the resulting state values;
4. diagnostics for injected errors;
5. removal from state on not-found;
6. import ID handling.

Additionally, every resource has a `TestResourceSchema`-style test that
round-trips the schema through `schema.Schema` and asserts required/optional,
sensitivity and plan-modifier presence for the documented attributes.

### L3 — Protocol tests for the executor

`trinotest.FakeServer` implements `POST /v1/statement` and `GET /v1/statement/{id}`
with the Trino wire shape (`id`, `columns`, `data`, `nextUri`, `stats`,
`error`). Tests cover:

- header propagation (`X-Trino-User`, `X-Trino-Source`, `X-Trino-Catalog`,
  `X-Trino-Schema`, `X-Trino-Session`, `X-Trino-Extra-Credential`,
  `X-Trino-Authorization-User` for token auth);
- polling until no `nextUri` (3-page result);
- error payload → `ErrTrino` classification;
- HTTP 503 then success (driver retry);
- context cancellation mid-poll surfaces `context.Canceled`;
- TLS: self-signed CA accepted only with `ca_cert`, rejected otherwise;
- basic auth header encoding; password over HTTP rejected before dial;
- OAuth2 client credentials: endpoint required, token request and refresh path
  exercised against a fake HTTPS token endpoint;
- Kerberos: `Options` → driver config mapping (enabled, principal, realm,
  keytab/cache, service name default `HTTP`, canonical-hostname inversion),
  with no KDC required;
- statement logging redaction (no literals in log output).

### L4 — Acceptance tests (real Trino, Docker)

- `TF_ACC=1`, `resource.Test` with `ProtoV6ProviderFactories`.
- Environment: `TRINO_ENDPOINT`, `TRINO_USER`, `TRINO_PASSWORD` (when TLS),
  `TRINO_TEST_CATALOG` (default `memory`).
- Each resource has: create → check → update (where supported) → import →
  check → destroy → `CheckDestroy`.
- **Two catalogs:**
  - `memory` — schema lifecycle and property-free schema tests; no access
    control, no roles.
  - `hive` — `hive.metastore=file` (catalog dir `file:///tmp/hive`, which
    requires `fs.local.enabled=true`), `hive.security=sql-standard`. Roles, role
    grants and table grants are tested here. The provider config uses
    `user = "admin"` (the file metastore auto-grants `admin` to
    `admin`/`hive`/`hdfs`) and `roles = { hive = "admin" }` (C17).
- Table and schema fixtures for grant tests are created by
  `internal/acctest` helpers through the same executor in `PreConfig`, with
  cleanup, because the provider intentionally does not manage tables.
- Role/catalog tests must **not** skip in CI. If the environment lacks
  dynamic catalog management or SQL-standard Hive, the test fails with an
  environment diagnostic rather than silently passing (a skip is only allowed
  when `TF_ACC` is unset).
- `docker-compose.yml` provides Trino 484 with:
  - `catalog.management=dynamic`, `catalog.store=memory`;
  - a one-shot `trino-init` service that creates the `memory` and `hive`
    catalogs over SQL before the test container starts. With
    `catalog.store=memory` the coordinator ignores `etc/catalog/*.properties`,
    so the script — not a properties file — is the source of truth;
  - a healthcheck that waits for `/v1/info` to report `starting:false`, not just
    for the first HTTP response;
  - no TLS (acceptance uses no password; the TLS path is covered in L3).
- CI runs the matrix from `.github/trino-versions.txt`; each version gets its
  own compose project so failures do not cascade.
- **Kerberos E2E is manual** and documented in
  `contributing/kerberos-acceptance.md`: a KDC (realm `EXAMPLE.COM`,
  `HTTP/trino@EXAMPLE.COM`), Trino with `http-server.authentication.type=kerberos`
  and HTTPS, a mounted keytab, and `TRINO_AUTH=kerberos` + `TRINO_KERBEROS_*`.
  The runbook states the exact commands and the expected outcome. CI covers the
  config mapping and validation only; this gap is recorded in the risk table.

### Coverage policy

- Unit + protocol coverage is measured with
  `go test -race -covermode=atomic -coverprofile`.
- Initial gate: **80%** for `./internal/...`, excluding generated docs and
  `main.go`, measured on unit + protocol tests only. Acceptance coverage is
  reported (merged profile) but is **not** part of the v1 gate, because Docker
  test execution is environment-dependent.
- Baseline in `.github/coverage-baseline.txt`; `make coverage-gate` fails below
  it.
- A coverage drop is a review finding, not a silent regression.

---

## 11. Tooling, CI/CD and documentation

### Tool versions (pinned)

| Tool | Version |
|---|---|
| Go | 1.27.x (`go.mod` `go 1.27.1`) |
| `terraform-plugin-framework` | v1.19.0 |
| `terraform-plugin-framework-validators` | v0.19.0 |
| `terraform-plugin-go` | v0.31.0 |
| `terraform-plugin-log` | v0.11.0 |
| `terraform-plugin-testing` | v1.16.0 |
| `terraform-plugin-docs` | v0.25.0 |
| `trino-go-client` | latest at implementation (≥ v0.333.0) |
| `golangci-lint` | v2.13.2 (config `version: "2"`) |
| GoReleaser | v2 |
| Terraform (CI) | `~> 1.9` |

### `.golangci.yml`

Adopt the reference linter set verbatim: `errcheck`, `govet` (all except
`fieldalignment`, `shadow`), `ineffassign`, `staticcheck`, `unused`,
`bodyclose`, `noctx`, `unconvert`; `errcheck` excluded in tests; no issue caps.

### `GNUmakefile` targets

`build`, `test`, `coverage`, `coverage-html`, `coverage-merge`,
`coverage-readme`, `coverage-readme-check`, `coverage-gate`, `testacc`,
`testacc-docker`, `testacc-matrix`, `lint`, `lint-fix`, `fmt`, `vet`,
`install`, `validate-examples`, `generate`.

### CI (`test.yml`)

`lint` job: golangci-lint, gosec → SARIF upload, fail on findings.
`test` job (needs lint): build, vet, `go mod tidy` diff, cross-platform build
(windows/amd64, darwin/arm64), Terraform setup, `validate-examples`,
`go generate ./...` + `git diff --exit-code -- docs/`, `go test -race -cover`.
`acceptance` job: Docker matrix from `trino-versions.txt`, coverage merge,
coverage gate.
`release.yml`: GoReleaser with GPG signing, `terraform-registry-manifest.json`
attached as `_manifest.json`, build provenance attestation.
`create-release-tag.yml`, `scorecard.yml`: as in the reference.
Concurrency: cancel in progress per workflow+ref. Actions pinned by SHA.

### `goreleaser.yml`

`CGO_ENABLED=0`, `-trimpath`, `-s -w -X main.version={{.Version}}`, linux/darwin/
windows × amd64/arm64, zip archives, SHA256SUMS, GPG detached signature,
manifest extra file, GitHub-native changelog.

### Documentation

- `docs/` is generated by `tfplugindocs` from `templates/` + `examples/`.
  Never hand-edited.
- `examples/resources/<type>/resource.tf` for every resource and `import.sh`
  for every importable resource;
  `examples/data-sources/<type>/data-source.tf` for every data source.
- `examples/complete/main.tf` is a realistic end-to-end configuration
  (catalog → schema → role → grants) and is validated in CI.
- `README.md`: install, quick start, provider config, resource table, the
  read-back matrix, known limitations (users, catalog alter, grant drift),
  Trino version policy, links to generated docs.
- `AGENTS.md`: documentation map, package layout, "read these before changing
  behaviour", commands, review gates, contract-change protocol.
- `ARCHITECTURE.md`: request flow, executor internals, error taxonomy, test
  layers.
- `SECURITY.md`: supported versions, threat model (provider holds DDL
  credentials), secret handling, reporting.
- `CHANGELOG.md`: `## Unreleased` + Keep a Changelog.

---

## 12. Security model

| Concern | Control |
|---|---|
| SQL injection through names/properties | `QuoteIdentifier`/`QuoteLiteral` used everywhere; args passed via prepared statements for queries; a unit test per resource asserts the quoted SQL. |
| Credential leakage in state | `password`, `access_token`, `oauth2_client_secret`, `extra_credentials`, `http_headers`, `sensitive_properties`, `client_cert`, `client_key`, `kerberos_keytab`, `kerberos_credential_cache` are `Sensitive`. Provider-level attributes are never logged. |
| Credential leakage in logs | No statement logging with literals; structured `tflog` fields only. |
| Credential leakage in Trino history | Documented: `CREATE CATALOG` is logged by Trino including its properties. Users are steered to `sensitive_properties` and warned in the resource docs. |
| TLS downgrade | Password auth over HTTP is rejected; `insecure_skip_verify` is documented as test-only. |
| Least privilege | README states the required Trino privileges per resource (CREATE CATALOG, CREATE SCHEMA, role admin, GRANT OPTION). |
| Dependency risk | Dependabot + `go mod verify` + gosec; minimal direct dependency surface (`gokrb5` arrives through `trino-go-client`, not as a direct import). |
| Release integrity | Signed checksums, manifest, provenance attestation. |

---

## 13. Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Acceptance environment cannot create roles or grants | Suite silently passes without coverage | Compose creates `hive` with `hive.metastore=file` + `hive.security=sql-standard` (via `trino-init`, because `catalog.store=memory` ignores catalog files); the provider enables `admin` via `roles`. A test that skips under `TF_ACC` fails the job. |
| SQL-standard Hive requires an enabled `admin` role | Every DDL statement fails with a permission error | `roles` provider attribute is documented as required for such deployments; acceptance pins it. |
| `SHOW CREATE SCHEMA` output differs across versions/connectors | Owner/property drift misread | Parser is narrow, fixture-driven, failure-tolerant: log + preserve state, never error. Feature can be disabled by leaving owner unset. |
| Trino renames `information_schema` columns | Table grant read breaks | Read by lower-case column name with fallback to the first column for single-value queries; protocol tests pin the schema. |
| Dynamic catalogs disabled in prod | Catalog apply fails | Actionable diagnostic with the exact property and restart instruction. |
| Mid-update partial failure (non-transactional DDL) | Divergent grants until next apply | Idempotent statements; grant updates diff against state/server truth so retries re-issue; documented. |
| Provider credentials are powerful | Blast radius | Sensitive marking, least-privilege docs, no logging, HTTPS enforcement. |
| Two resources managing the same grant | Terraform fight | Docs warn; role grants are state-authoritative and cannot detect the conflict. Out of scope to enforce. |
| Hive lowercases names | Perpetual replacement or spurious diff | Case-insensitive reads that preserve configured spelling (C16), plus grant queries with `lower(col) = lower(?)`. |
| Kerberos is not exercised end-to-end in CI | A regression in the mapping could reach Kerberos users | Mapping and validation are unit-tested; the driver owns SPNEGO; a documented KDC runbook is part of the release checklist and is executed manually before a release that touches auth. |
| OAuth2 endpoint discovery differs on 476 | Auth fails on the minimum version | Endpoint is mandatory for `auth = "oauth2"`; one code path for 476 and 484. |
| Catalog import would recreate a live catalog | Data-plane outage | `ImportState` is deliberately unsupported for `trino_catalog`, with an explanatory diagnostic. |

---

## 14. Definition of done (v1)

1. `go build ./...`, `go vet ./...`, `golangci-lint run`, `gosec` clean.
2. `go test -race ./internal/...` green; unit + protocol coverage ≥ 80%.
3. Acceptance suite green against Trino 476 and 484; catalog, role and grant
   tests not skipped in CI.
4. `go generate ./...` leaves `docs/` unchanged; every resource/data source has
   an example and every resource except `trino_catalog` has an import snippet.
5. `terraform validate` passes for every example; `examples/complete` applies
   and destroys against the compose environment.
6. Read-back matrix matches implementation; limitations are in README and each
   resource page.
7. Import tested for every resource that supports it; IDs documented;
   `trino_catalog` import rejection is tested and explained.
8. Release workflow produces a signed, manifest-carrying artifact set.
9. No secrets in diagnostics or logs; sensitivity tested by schema assertions.
10. Auth modes `none`, `basic`, `jwt`, `oauth2` and `kerberos` validated and
    unit-tested; the Kerberos runbook was executed successfully against a KDC.
11. `AGENTS.md` + `ARCHITECTURE.md` accurate enough for a fresh agent to make a
    change without re-deriving the design.

---

## 15. Resolved decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | No user resource; users are grantees only. | Trino has no user DDL/listing. |
| D2 | Minimum Trino 476, runtime gates, CI matrix 476 + latest. | Modern baseline with honest compatibility. |
| D3 | Catalog resource in v1 with `RequiresReplace` everywhere and dynamic-management diagnostics. | Real user need; limitations are enforceable and documented. |
| D4 | Per-resource packages instead of flat `resource_*.go`. | Parallel-safe, test co-location, matches reference reality. |
| D5 | State-authoritative read for role grants only. | No read path exists; table-grant drift *is* readable and therefore must be read. |
| D6 | `sensitive_properties` split for catalogs. | Keeps plans usable without exposing credentials. |
| D7 | Kerberos included; browser external auth excluded. | Kerberos is a first-class Trino enterprise auth mode; the driver owns SPNEGO. External browser auth is interactive and unsuitable for Terraform. |
| D8 | Docs and code in English. | Public provider, reference conventions, agent interoperability. |
| D9 | System roles dropped; `catalog` required on roles and role grants. | Stock Trino disables `SystemSecurityMetadata`; shipping it would be untestable. |
| D10 | Schema grants dropped; table grants only. | Every bundled connector that supports grants rejects schema grants. |
| D11 | `ALL PRIVILEGES` rejected; explicit privilege names only. | Read-back expands it, so accepting it would cause permanent diffs. |
| D12 | Grantees are arbitrary quoted principal strings. | LDAP/OAuth2/JWT identities contain `@`, `.`, `\` and are not SQL identifiers. |
| D13 | No import for `trino_catalog`. | Unreadable properties + `RequiresReplace` would silently recreate live catalogs. |
| D14 | Provider exposes `roles` (catalog → role) to enable `admin`. | Required for SQL-standard Hive DDL; the driver already supports it. |
| D15 | Explicit `auth` attribute, with inference when unset. | Ticket-cache-only Kerberos has no distinguishing credential attribute; explicit mode also produces clearer errors. |
| D16 | `kerberos_service_name` defaults to `HTTP`, not the driver's `trino`. | Matches Trino's coordinator convention and the JDBC driver; always sent explicitly. |
