# Terraform Provider for Trino — v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build and release a production-grade Terraform provider that manages Trino catalogs, schemas, catalog roles, role grants and table privilege grants through SQL DDL.

**Architecture:** A plugin-framework provider whose only runtime dependency is a `internal/trino.Executor` wrapping `database/sql` + `trino-go-client`. Each Terraform resource lives in its own package and maps CRUD to quoted SQL statements; reads use Trino's metadata surfaces where they exist and are explicitly state-authoritative where they do not.

**Tech Stack:** Go 1.27, `terraform-plugin-framework` v1.19, `terraform-plugin-testing` v1.16, `terraform-plugin-docs` v0.25, `trino-go-client`, `golangci-lint` v2, GoReleaser v2, Docker Compose (Trino 476/484, Hive file metastore).

**Spec:** `docs/superpowers/specs/2026-10-23-terraform-provider-trino-design.md`

## Global Constraints

- Module: `github.com/caberdo/terraform-provider-trino`; provider address `registry.terraform.io/caberdo/trino`; Terraform type prefix `trino`.
- Minimum Trino: **476**. CI matrix: contents of `.github/trino-versions.txt` (476 and 484).
- No new third-party dependency without the lead's approval and a `go.mod` change owned by the lead. Allowed: framework, validators, plugin-go, plugin-log, plugin-testing, plugin-docs, `trino-go-client` (which already brings `gokrb5`), `hashicorp/go-version`. No direct `gokrb5` import in provider code.
- Every SQL identifier goes through `trino.QuoteIdentifier`; every literal through `trino.QuoteLiteral`; query values are bound with `?`. No `fmt.Sprintf` of raw user input into SQL.
- No `panic`, `log.Fatal` or `os.Exit` outside `main`.
- No secret may appear in a diagnostic, log field, test failure message or committed file.
- Read-back claims must match the spec's matrix. Never silently zero an unreadable attribute; preserve state.
- Names are matched case-insensitively on read and keep the configured spelling in state (Hive lowercases).
- Docs are generated; never hand-edit `docs/`.
- Every task ends with `go build ./...`, its package tests, and a commit.
- Only the lead edits `go.mod`, `go.sum`, `internal/provider/provider.go`, CI files and `docs/` generation outputs. Lanes edit only their owned files (table below).
- Resource unit tests live in the **same package** as the resource (`package catalog`, `package schema`, …) so they can use the unexported model and parser; acceptance tests live in `<name>_test`. All unit tests drive CRUD through the shared `trinotest` harness (Task 3), never through hand-built `tfsdk` values.

## Review Focus

These are the failure modes most likely to reach a user. Each has a test in the task that owns the code.

1. **Quoting**: a name or property containing `"`, `'`, `.`, `\` or unicode must round-trip without a syntax error or injection — `TestQuoteIdentifier`, `TestQuoteLiteral`, and each resource's golden-SQL test.
2. **Case normalization**: Hive returns lowercased schema/role/grantee names; a naive equality check causes a permanent replacement plan — `TestSchemaReadCaseInsensitiveName`, `TestRoleReadCaseInsensitive`, `TestGrantPrivilegesReadCaseInsensitiveGrantee`.
3. **False drift**: a resource with no read path must preserve state, not write zero values — `TestGrantRoleReadIsStateAuthoritative`, `TestCatalogReadPreservesUnreadableProperties`.
4. **Destructive updates**: Trino cannot alter catalogs or schema properties; a missing `RequiresReplace` produces a failed apply, and an accidental import can drop a live catalog — `TestCatalogSchemaPlanModifiers`, `TestSchemaPropertiesRequireReplace`, `TestCatalogImportUnsupported`.
5. **Partial failure**: non-transactional multi-statement updates must converge when re-applied — `TestGrantPrivilegesUpdateAddsAndRemoves` (apply twice), `TestGrantPrivilegesUpdateGrantOption`.
6. **Config validation**: conflicting auth/TLS inputs must fail before dialing, with an attribute-scoped diagnostic — `TestValidatePasswordRequiresTLS`, `TestValidateAuthModesExclusive`, `TestValidateCertConflicts`.
7. **Auth mode correctness**: `auth=kerberos` without a keytab or cache, keytab + cache together, or a realm in the service pattern must fail at plan time; `auth=oauth2` without an endpoint must fail on 476 — `TestValidateKerberos`, `TestValidateOAuth2RequiresEndpoint`, `TestInferAuthMode`.

---

## Execution Model (parallel agents)

### Dependency graph

```
Wave 0 (serial)
  Task 1 ──► Task 2 ──► Task 3
                         │
Wave 1 (parallel, 6 lanes)│
  ┌──────────────────────┼────────────────────────────────────────┐
  ▼          ▼           ▼            ▼             ▼              ▼
Task 4     Task 5      Task 6       Task 7        Task 8         Task 9
catalog    schema      role         grant_role    grant_privs    data sources
  └──────────┴───────────┴────────────┴─────────────┴──────────────┘
                         │
Wave 2 (serial)          ▼
  Task 10 ──► Task 11 ──► Task 12
  wiring      CI/gates    review
```

### Lane ownership

| Lane | Tasks | Owned paths (exclusive write) |
|---|---|---|
| Lead | 1–3, 10–12 | `go.mod`, `go.sum`, `main.go`, `internal/provider/**`, `internal/trino/**`, `internal/trinotest/**`, `internal/providerdata/**`, `internal/resourceutil/**`, `internal/acctest/**`, `.github/**`, `GNUmakefile`, `scripts/**`, `templates/**`, `docs/**`, root docs, `docker-compose.yml`, `test/**` |
| A | 4 | `internal/resources/catalog/**`, `examples/resources/trino_catalog/**` |
| B | 5 | `internal/resources/schema/**`, `examples/resources/trino_schema/**` |
| C | 6 | `internal/resources/role/**`, `examples/resources/trino_role/**` |
| D | 7 | `internal/resources/grantrole/**`, `examples/resources/trino_grant_role/**` |
| E | 8 | `internal/resources/grantprivileges/**`, `examples/resources/trino_grant_privileges/**` |
| F | 9 | `internal/datasources/**`, `examples/data-sources/**` |

### Worktree and merge protocol

1. Lead completes Wave 0 through a branch and pull request; the
   `main-protection` ruleset forbids direct pushes to `main`. The lead freezes
   the contracts in the spec (§5) when Task 3 closes.
2. Each lane works in its own worktree, branched from the Wave 0 commit:
   `git worktree add ../tptrino-lane-A -b lane/a-catalog <wave0-sha>`.
3. Lanes never rebase onto each other; they rebase only on `main` if the lead announces a contract change.
4. Each lane delivers a branch with one commit per task and opens one PR. The lead merges lanes A–F into `main` in task order through the merge queue; conflicts are impossible by construction unless a lane wrote outside its paths.
5. If a lane needs a contract change (signature, attribute name, SQL shape, import format), it stops and messages the lead. The lead updates the spec and notifies all lanes; no lane improvises a shared contract.
6. Wave 2 starts only after all six lanes are merged and the full unit suite is green.

### Lane definition of done

- Owned tests pass: `go test ./internal/<owned>/... -count=1`.
- `go build ./...` and `go vet ./...` pass from the lane worktree.
- No writes outside owned paths (`git diff --name-only main...HEAD` reviewed).
- Acceptance tests written but not required to run locally; they must compile with `go vet ./...`.
- One commit per task, message `feat(<resource>): <summary>`.

---

## Task 1: Repository scaffold, tooling and CI

**Files:**
- Create: `go.mod`, `main.go`, `.gitignore`, `.editorconfig`, `.golangci.yml`, `GNUmakefile`, `terraform-registry-manifest.json`, `tools/tools.go`
- Create: `LICENSE` (MPL-2.0), `NOTICE`, `CHANGELOG.md`, `README.md`, `AGENTS.md`, `ARCHITECTURE.md`, `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`
- Create: `.github/CODEOWNERS`, `.github/PULL_REQUEST_TEMPLATE.md`, `.github/dependabot.yml`, `.github/ISSUE_TEMPLATE/{bug_report.md,feature_request.md}`, `.github/workflows/{test.yml,release.yml,create-release-tag.yml,scorecard.yml}`, `.github/goreleaser.yml`, `.github/trino-versions.txt`, `.github/github-action-markdown-link-check-config.json`, `.github/coverage-baseline.txt`
- Create: `templates/{index.md.tmpl,resources.md.tmpl,data-sources.md.tmpl}`, `scripts/{validate-examples.sh,coverage-summary.sh,coverage-gate.sh}`
- Create: `examples/provider/provider.tf`
- Create: `internal/provider/provider.go` (stub: `Metadata` only, empty `Resources`/`DataSources`)

**Interfaces:**
- Produces: a compiling module whose `main.go` serves `provider.New(version)()` at `registry.terraform.io/caberdo/trino`.

- [ ] **Step 1: Initialise the module and pin dependencies**

```bash
go mod init github.com/caberdo/terraform-provider-trino
go get github.com/hashicorp/terraform-plugin-framework@v1.19.0
go get github.com/hashicorp/terraform-plugin-framework-validators@v0.19.0
go get github.com/hashicorp/terraform-plugin-go@v0.31.0
go get github.com/hashicorp/terraform-plugin-log@v0.11.0
go get github.com/hashicorp/terraform-plugin-testing@v1.16.0
go get github.com/hashicorp/terraform-plugin-docs@v0.25.0
go get github.com/trinodb/trino-go-client/trino@latest
go get github.com/hashicorp/go-version@latest
```

If a pinned version cannot be resolved, use the latest available and record the deviation in `CHANGELOG.md` under `## Unreleased`.

- [ ] **Step 2: Write `main.go`**

`providerserver.Serve` with `Address: "registry.terraform.io/caberdo/trino"`, `Debug` from `-debug`, `version` injected via `-ldflags "-X main.version=..."`, `log.Fatal` only here.

- [ ] **Step 3: Write the provider stub**

```go
package provider

import (
    "context"
    "github.com/hashicorp/terraform-plugin-framework/datasource"
    "github.com/hashicorp/terraform-plugin-framework/provider"
    "github.com/hashicorp/terraform-plugin-framework/resource"
)

var _ provider.Provider = (*TrinoProvider)(nil)

type TrinoProvider struct{ version string }

func New(version string) func() provider.Provider {
    return func() provider.Provider { return &TrinoProvider{version: version} }
}

func (p *TrinoProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
    resp.TypeName = "trino"
    resp.Version = p.version
}

func (p *TrinoProvider) Schema(context.Context, provider.SchemaRequest, *provider.SchemaResponse) {}
func (p *TrinoProvider) Configure(context.Context, provider.ConfigureRequest, *provider.ConfigureResponse) {}
func (p *TrinoProvider) Resources(context.Context) []func() resource.Resource   { return nil }
func (p *TrinoProvider) DataSources(context.Context) []func() datasource.DataSource { return nil }
```

- [ ] **Step 4: Write tooling**

`.golangci.yml` (module mode `readonly`, 5m timeout, linters `errcheck govet ineffassign staticcheck unused bodyclose noctx unconvert`, `govet enable-all` minus `fieldalignment`/`shadow`, errcheck excluded in `_test.go`). `GNUmakefile` targets from the spec §11. `terraform-registry-manifest.json` with `"protocol_versions": ["6.0"]`. GoReleaser v2 config with linux/darwin/windows × amd64/arm64, zip, SHA256SUMS, GPG sign, manifest extra file, `release.mode: replace`.

- [ ] **Step 5: Write the CI workflows**

`test.yml`: `lint` (golangci-lint v2.13.2, gosec SARIF upload + fail), `test` (build, vet, `go mod tidy` diff, cross-build, Terraform `~> 1.9`, `validate-examples`, `go generate` + `git diff --exit-code -- docs/`, `go test -race -cover`), `acceptance` (matrix from `trino-versions.txt`, docker compose, coverage gate). Pin action SHAs as in the reference repo. `release.yml`: tag/workflow_call/dispatch, GPG import, GoReleaser with `.github/goreleaser.yml`, `terraform-registry-manifest.json` as `_manifest.json`, attestations.

- [ ] **Step 6: Verify**

```bash
go build ./...
go vet ./...
go generate ./...   # creates docs/index.md; commit it
git diff --exit-code -- docs/ || true   # first generation is expected to add files
```

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "chore: scaffold provider module, tooling and CI"
```

---

## Task 1b: Enforce required PR checks (ruleset, merge queue, drift check)

**Issue:** #18. **Blocked by:** Task 1. **Must be live before Task 3 closes.**

**Files:**
- Create: `scripts/lib/workflow-jobs.sh`
- Create: `scripts/setup-branch-protection.sh`
- Create: `scripts/check-branch-protection.sh`
- Create: `.github/workflows/protection-drift.yml`
- Modify: `.github/workflows/test.yml` (add `merge_group:` so the queue gets checks)
- Modify: `CONTRIBUTING.md`

**Interfaces:**
- Produces: `main-protection` repository ruleset requiring `Lint & Security`
  and `Test`, a merge queue, linear history, no deletion or force-push, and no
  bypass actors; setup/check scripts that read context names from the workflow
  job `name:` values instead of guessing them.

- [x] **Step 1: Install the ruleset**

```bash
./scripts/setup-branch-protection.sh
./scripts/check-branch-protection.sh
```

- [x] **Step 2: Verify idempotency and the drift gate**

Run the setup script twice; the check script must exit 0 both times. Set
`REQUIRED_CONTEXTS="Bogus"` and confirm setup exits non-zero before touching the
API. Remove one context from the live ruleset (or point the check at a fixture)
and confirm the check exits non-zero.

- [ ] **Step 3: Prove the gate**

A throwaway PR with an unused import must fail `Lint & Security` with the merge
button disabled. A `README.md`-only PR must still report both required checks.

- [x] **Step 4: Commit**

```bash
git add scripts .github/workflows CONTRIBUTING.md docs/superpowers/plans
git commit -m "ci: require lint and test checks on main and enable merge queue"
```

---

## Task 2: Statement executor, protocol fakes and SQL utilities

**Files:**
- Create: `internal/trino/{executor.go,options.go,rows.go,quoting.go,errors.go,version.go,tls.go}`
- Create: `internal/trino/{executor_test.go,rows_test.go,quoting_test.go,errors_test.go,version_test.go,tls_test.go}`
- Create: `internal/trinotest/{fake_server.go,fake_executor.go,fake_server_test.go}`

**Interfaces:**
- Produces (frozen):
  - `trino.Executor` (spec §5)
  - `trino.Row` + `String/Bool/Int64`
  - `trino.QuoteIdentifier/QuoteLiteral/QuoteQualified`
  - `trino.SourceOf(err) ErrorKind`, `trino.IsErrorName(err, ...string) bool`, `trino.IsNotFound(err) bool`
  - `trino.AtLeast(version, minimum string) bool`
  - `trino.Options` + `trino.Open(Options) (Executor, error)`
  - `trino.TLSOptions` + `trino.NewHTTPClient(TLSOptions) (*http.Client, error)`
    (`CAFile, CAPEM, ClientCertFile, ClientCertPEM, ClientKeyFile, ClientKeyPEM string`,
    `InsecureSkipVerify bool`, `Headers map[string]string`)
  - `trinotest.FakeExecutor` + `Steps`/`Calls`
  - `trinotest.FakeServer` implementing `POST /v1/statement` and `GET /v1/statement/{id}`

- [ ] **Step 1: Write the failing quoting tests**

```go
func TestQuoteIdentifier(t *testing.T) {
    cases := map[string]string{
        `simple`:    `"simple"`,
        `has"quote`: `"has""quote"`,
        `with space`: `"with space"`,
        `a.b`:       `"a.b"`,
        `alice@example.com`: `"alice@example.com"`,
        ``:          `""`,
    }
    for in, want := range cases {
        if got := QuoteIdentifier(in); got != want {
            t.Errorf("QuoteIdentifier(%q) = %q, want %q", in, got, want)
        }
    }
}

func TestQuoteLiteral(t *testing.T) {
    cases := map[string]string{
        `plain`:      `'plain'`,
        `O'Reilly`:   `'O''Reilly'`,
        `back\slash`: `'back\slash'`,
        ``:           `''`,
    }
    // assert each
}

func TestQuoteQualified(t *testing.T) {
    if got := QuoteQualified("hive", "sales", "orders"); got != `"hive"."sales"."orders"` {
        t.Fatalf("got %q", got)
    }
    if got := QuoteQualified(`a"b`); got != `"a""b"` { t.Fatalf("got %q", got) }
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/trino -run TestQuote -count=1`
Expected: FAIL, `undefined: QuoteIdentifier`.

- [ ] **Step 3: Implement quoting**

Use `strings.ReplaceAll(s, `"`, `""`)` inside double quotes and `strings.ReplaceAll(s, `'`, `''`)` inside single quotes. Do not escape backslashes: Trino does not use backslash escaping in string literals.

- [ ] **Step 4: Write and implement `Row` accessors**

`Row map[string]any`, keys lowercased at construction. `String` accepts `string` and `[]byte`; `Bool` accepts `bool`, `"true"/"false"` and `"YES"/"NO"` (Trino's `information_schema` uses `YES`/`NO`); `Int64` accepts `int64`, `int`, `int32`, `json.Number`, and numeric strings. Missing or wrong type → `(zero, false)`. Add `SoleString() (string, bool)` for single-column `SHOW …` results: it returns the only value and `false` when the row does not have exactly one column.

Tests: `TestRowString`, `TestRowBool`, `TestRowInt64`, `TestRowMissing`.

- [ ] **Step 5: Write the failing classification tests**

```go
func TestSourceOf(t *testing.T) {
    cases := []struct{ err error; want ErrorKind }{
        {&trino.ErrTrino{ErrorName: "SCHEMA_NOT_FOUND"}, trino.KindNotFound},
        {&trino.ErrTrino{ErrorName: "CATALOG_NOT_FOUND"}, trino.KindNotFound},
        {&trino.ErrTrino{ErrorName: "ROLE_NOT_FOUND"}, trino.KindNotFound},
        {&trino.ErrTrino{ErrorName: "PERMISSION_DENIED"}, trino.KindPermissionDenied},
        {&trino.ErrTrino{ErrorName: "NOT_SUPPORTED", Message: "This connector does not support grants on schemas"}, trino.KindNotSupported},
        {fmt.Errorf("wrapped: %w", &trino.ErrTrino{ErrorName: "TABLE_NOT_FOUND"}), trino.KindNotFound},
        {&url.Error{Op: "Post", URL: "https://x", Err: errors.New("dial tcp: refused")}, trino.KindConnection},
        {nil, trino.KindUnknown},
    }
    // assert with errors.As semantics
}
```

Also `TestAtLeast` (`"476">=476` true, `"484">=476` true, `"475"` false, `""` true permissive), and `TestDiagnosticFor` asserting the dynamic-management hint appears for a `NOT_SUPPORTED` error whose message contains `dynamic`.

- [ ] **Step 6: Implement errors and version**

`SourceOf` uses `errors.As` for `*trino.ErrTrino`; fall back to `*url.Error`/`net.Error` → connection; `nil` → unknown. `DiagnosticFor(op, object, err)` returns one `diag.ErrorDiagnostic` with a title naming the operation and a detail built from `kind` + the server message, never the statement. `version.go` caches `SELECT version()` behind `sync.Once`; `AtLeast` parses with `go-version` and returns `true` when the version is unknown.

- [ ] **Step 7: Implement `Options` and `Open`**

```go
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

func Open(opts Options) (Executor, error)
```

Build `trino.Config`; `Options.ServerURI` is `scheme://host[:port]` (no userinfo, no path) and `Open` composes `scheme://user:password@host` with `url.UserPassword`; reject password + `http`; set `QueryTimeout`, `RequestRetryTimeout=2m`, `RequestRetryMaxAttempts=5`; map auth:

- basic → password in the URI userinfo;
- jwt → `AccessToken`;
- oauth2 → `OAuth2ClientID/Secret/TokenEndpoint/Scope`;
- kerberos → `KerberosEnabled=true`, principal/realm/keytab/cache/config path,
  `KerberosRemoteServiceName` (default `HTTP`, always set explicitly),
  `KerberosServicePrincipalPattern`, and
  `KerberosDisableCanonicalHostname = !opts.KerberosUseCanonicalHostname`;
- none → no credentials.

Extract this mapping into a pure `buildDriverConfig(opts Options) (*trino.Config, error)` so it is testable without a KDC. Pass `HTTPClient`; `trino.NewConnector` → `sql.OpenDB`; set pool limits (2/1/5m).

Tests: `TestOpenRejectsPasswordOverHTTP`, `TestBuildDriverConfigBasic`, `TestBuildDriverConfigJWT`, `TestBuildDriverConfigOAuth2`, `TestBuildDriverConfigKerberos` (assert enabled, principal, realm, keytab, config path, service name `HTTP`, canonical inversion), `TestBuildDriverConfigKerberosCacheMode` (no keytab), `TestBuildDriverConfigNone`, `TestOpenDefaults`.

- [ ] **Step 8: Implement `FakeExecutor`**

Records every call (`Op`, `Statement`, `Args`) and replays `Steps` in order; a call beyond the queue returns an error naming the unexpected statement. Implements the full `Executor` interface, including `Version`.

Test: `TestFakeExecutorRecordsCalls`.

- [ ] **Step 9: Implement `FakeServer` and executor protocol tests**

`FakeServer` serves `POST /v1/statement` with `{"id","columns","data","nextUri","stats"}` and `GET /v1/statement/{id}` by page index; `Error` produces `{"error":{"message","errorName","errorType","sqlState"}}`.

Tests (use `trino.Open` with the fake URL):
- `TestExecutorExecSendsUserAndSourceHeaders`
- `TestExecutorPollsUntilDone` (3 pages; assert 4 HTTP requests)
- `TestExecutorMapsTrinoError` (assert `SourceOf == KindNotFound`)
- `TestExecutorContextCancellation` (server blocks; cancel; assert `errors.Is(err, context.Canceled)`)
- `TestExecutorQueryReturnsRows`
- `TestOpenAppliesSessionAndExtraCredentialHeaders`
- `TestOpenAppliesRolesHeader` (`X-Trino-Role: hive=ROLE{admin}`)

- [ ] **Step 10: Implement TLS construction**

`tls.go`: `NewHTTPClient(opts TLSOptions) (*http.Client, error)` with `ca_cert`/`ca_cert_file`, `client_cert*`/`client_key*`, `insecure_skip_verify`, and an optional header `RoundTripper`. Tests: `TestTLSClientRejectsUnknownCA`, `TestTLSClientAcceptsConfiguredCA` (self-signed `httptest.NewTLSServer`), `TestTLSClientPresentsClientCert`, `TestTLSHeaderRoundTripper`.

- [ ] **Step 11: Run the package tests**

Run: `go test -race ./internal/trino ./internal/trinotest -count=1`
Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add internal/trino internal/trinotest
git commit -m "feat(trino): add statement executor, quoting, error classification and protocol fakes"
```

---

## Task 3: Provider configuration, provider data and shared utilities

**Files:**
- Modify: `internal/provider/provider.go`
- Create: `internal/provider/config.go`, `internal/provider/provider_test.go`, `internal/provider/config_test.go`
- Create: `internal/providerdata/providerdata.go`
- Create: `internal/resourceutil/{import.go,state.go,import_test.go,state_test.go}`
- Create: `internal/acctest/acctest.go`
- Create: `internal/trinotest/harness.go`, `internal/trinotest/harness_test.go`

**Interfaces:**
- Consumes: `trino.Open`, `trino.Executor`, `trino.Options`.
- Produces:
  - `provider.TrinoProvider` with the full schema from spec §6 and empty registries.
  - `providerdata.Data{Executor, Timeout, Version}` + `FromProviderData(any) (*Data, diag.Diagnostics)`.
  - `resourceutil.SplitImportID(id, sep string, n int, expected string) ([]string, error)`.
  - `resourceutil.StringSet(ctx, types.Set, *diag.Diagnostics) []string`, `resourceutil.StringMap(ctx, types.Map, *diag.Diagnostics) map[string]string`.
  - `acctest.ProtoV6ProviderFactories()`, `acctest.PreCheck(t)`, `acctest.Executor(t)`, `acctest.Fixture(t, statements ...string) (cleanup func())`.
  - Test harness (used by every resource lane):
    ```go
    func Ctx(t *testing.T) context.Context
    func SchemaOf(t *testing.T, r resource.Resource) resourceschema.Schema
    func Configure(t *testing.T, r resource.Resource, fake *FakeExecutor)
    func Create[Model any](t *testing.T, r resource.Resource, plan Model) (Model, diag.Diagnostics)
    func Read[Model any](t *testing.T, r resource.Resource, state Model) (Model, diag.Diagnostics)
    func Update[Model any](t *testing.T, r resource.Resource, state, plan Model) (Model, diag.Diagnostics)
    func Delete[Model any](t *testing.T, r resource.Resource, state Model) diag.Diagnostics
    ```
    Each helper loads the resource schema, converts the struct model to a `tftypes.Value` with `types.ObjectValueFrom`, invokes the resource method, and decodes the response state back into `Model`.

- [ ] **Step 1: Write failing config tests**

```go
func TestValidatePasswordRequiresTLS(t *testing.T) {
    cfg := Config{Endpoint: "http://localhost:8080", User: "u", Password: "p"}
    diags := cfg.Validate()
    if !diags.HasError() { t.Fatal("expected error") }
    if got := diags[0].Detail(); !strings.Contains(got, "https") { t.Fatalf("detail %q", got) }
}

func TestValidateAuthModesExclusive(t *testing.T) {
    cfg := Config{Endpoint: "https://x:8443", User: "u", Auth: "basic", Password: "p", AccessToken: "t"}
    // expect AddAttributeError on access_token
}

func TestInferAuthMode(t *testing.T) {
    // no credentials → none; password → basic; token → jwt;
    // oauth2 client id → oauth2; any kerberos attribute → kerberos;
    // explicit auth always wins.
}

func TestValidateKerberos(t *testing.T) {
    // http endpoint → error; keytab + cache → error; keytab without principal or realm → error;
    // pattern "svc/realm@host" → error; valid keytab config → no diagnostics.
}

func TestValidateOAuth2RequiresEndpoint(t *testing.T) {
    cfg := Config{Endpoint: "https://x:8443", Auth: "oauth2", OAuth2ClientID: "id", OAuth2ClientSecret: "s"}
    // expect error on oauth2_token_endpoint
}

func TestValidateCertConflicts(t *testing.T) {
    cfg := Config{Endpoint: "https://x:8443", CACert: "pem", CACertFile: "/tmp/ca.pem"}
    // expect conflict error naming both attributes
}
```

Also: `TestResolveEnvFallbacks` (unset attrs read `TRINO_*`), `TestParseQueryTimeoutDefault5m`, `TestValidateRoles`, `TestValidateMissingFiles` (keytab/cert paths), `TestProviderSchemaSensitiveAttributes` (password, access_token, oauth2_client_secret, kerberos_keytab, kerberos_credential_cache, extra_credentials, http_headers, client_cert, client_key).

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/provider -count=1`
Expected: FAIL, `undefined: Config`.

- [ ] **Step 3: Implement `config.go`**

Attribute model mirrors the schema; `Resolve()` applies env fallbacks in the order attribute → env → default and resolves `Auth` (explicit or inferred per spec rule 2); `Validate()` returns `diag.Diagnostics` with `path.Root(...)` attribute errors for rules 1–13 of spec §6, including Kerberos and file-existence checks. `BuildOptions()` returns `trino.Options` (including the resolved auth mode and Kerberos fields) plus `*http.Client` from `trino.NewHTTPClient`.

- [ ] **Step 4: Implement the provider schema and `Configure`**

Copy the spec's attribute table into `schema.Schema`; mark sensitivity exactly; map to `Config`; on success `trino.Open`, probe `Version` best-effort with `tflog.Warn` on failure, and warn when a detected version is below `476` via `trino.AtLeast`; build `providerdata.Data{Executor, Timeout, Version}` and assign it to `ResourceData` and `DataSourceData`. Never log the endpoint with userinfo.

- [ ] **Step 5: Write failing resourceutil tests**

`TestSplitImportID` cases: `("hive.sales", ".", 2)` → `["hive","sales"]`; `("hive.a.b", ".", 2)` → `["hive","a.b"]`; `("hive", ".", 2)` → error containing `catalog.schema`; `(":USER:alice", ":", 3)` → error (empty segment); `("hive.analyst:USER:alice@example.com", ":", 3)` → ok.

`TestStringSetNullAndUnknown` → empty slice, no diagnostics. `TestStringMapNull` → empty map.

- [ ] **Step 6: Implement shared utilities**

`SplitImportID` uses `strings.SplitN` for `n` and rejects empty/whitespace segments. `state.go` converts `types.Set`/`types.Map` with `ElementsAs`/`IsNull`/`IsUnknown` guards.

- [ ] **Step 7: Implement `acctest`**

```go
func PreCheck(t *testing.T) {
    if os.Getenv("TF_ACC") == "" { t.Skip("TF_ACC not set") }
    if os.Getenv("TRINO_ENDPOINT") == "" { t.Fatal("TRINO_ENDPOINT must be set for acceptance tests") }
}
```

`ProtoV6ProviderFactories` returns `providerserver.NewProtocol6WithError(provider.New("test")())`. `Executor` opens the same config from `TRINO_*` env for fixtures (including `TRINO_AUTH=kerberos` and the `TRINO_KERBEROS_*` variables). `Fixture` runs statements and returns a cleanup that drops what it created.

- [ ] **Step 8: Implement the `trinotest` CRUD harness**

`Configure` wraps the fake in `providerdata.Data{Executor: fake, Timeout: 5 * time.Minute}` and calls the resource's `Configure`. The generic helpers use `resourceschema.Schema.Type().(types.ObjectType).AttributeTypes()` + `types.ObjectValueFrom` → `ToTerraformValue`, build `tfsdk.Plan`/`tfsdk.State`, call the method, and `resp.State.Get` back into a fresh `Model`. They fail the test on diagnostics only when the caller did not ask to inspect them (`CreateExpectError` variants return diagnostics).

Test: `TestHarnessRoundTripsStates` with a trivial in-package fake resource whose `Create` writes a computed `id`.

- [ ] **Step 9: Run the tests**

Run: `go test -race ./internal/provider ./internal/resourceutil ./internal/providerdata ./internal/trinotest -count=1`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/provider internal/providerdata internal/resourceutil internal/acctest
git commit -m "feat(provider): add configuration, client wiring and shared utilities"
```

---

## Task 4: `trino_catalog` resource (Lane A)

**Files:**
- Create: `internal/resources/catalog/{resource.go,resource_test.go,acceptance_test.go}`
- Create: `examples/resources/trino_catalog/resource.tf`

**Interfaces:**
- Consumes: `providerdata.FromProviderData`, `trino.Executor`, `trino.QuoteIdentifier`, `trino.QuoteLiteral`, `trino.DiagnosticFor`.
- Produces: `catalog.New() resource.Resource` implementing `Resource`, `ResourceWithConfigure`, `ResourceWithImportState` (rejection), type name `trino_catalog`.

- [ ] **Step 1: Write the golden-SQL test**

```go
func TestCatalogCreateEmitsSQL(t *testing.T) {
    fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}, {Rows: []trino.Row{{"catalog_name": "lakehouse", "connector_name": "iceberg"}}}}}
    r := New()
    trinotest.Configure(t, r, fake)
    plan := catalogModel{Name: types.StringValue("lakehouse"), Connector: types.StringValue("iceberg"), Properties: types.MapValueMust(types.StringType, map[string]attr.Value{
        "iceberg.catalog.type": types.StringValue("rest"), "iceberg.rest.uri": types.StringValue("http://iceberg:8181"),
    })}
    state, diags := trinotest.Create(t, r, plan)
    // assert no diags, state.ID == "lakehouse"
    want := `CREATE CATALOG "lakehouse" USING "iceberg" WITH ("iceberg.catalog.type" = 'rest', "iceberg.rest.uri" = 'http://iceberg:8181')`
    if fake.Calls[1].Statement != want { t.Fatalf("got %q", fake.Calls[1].Statement) }
}
```

Additional tests: `TestCatalogCreateRejectsExisting`, `TestCatalogReadNotFoundRemovesState`, `TestCatalogReadPreservesUnreadableProperties`, `TestCatalogReadDetectsConnectorDrift`, `TestCatalogDeleteIsIdempotent`, `TestCatalogImportUnsupported`, `TestCatalogSensitivePropertiesSensitive`, `TestCatalogAllAttributesRequireReplace`.

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/resources/catalog -count=1`
Expected: FAIL, package does not exist.

- [ ] **Step 3: Implement the resource**

Schema exactly as spec §7.1. Create: existence query, error if present, execute create, read back, set state. Read: `SELECT catalog_name, connector_name FROM system.metadata.catalogs WHERE catalog_name = ?`; not found → `State.RemoveResource`; update `connector`; preserve properties. Update: copy plan to state, log. Delete: `DROP CATALOG`, `CATALOG_NOT_FOUND` → success. ImportState: `AddError("Import is not supported for trino_catalog", ...)`.

- [ ] **Step 4: Run the unit tests**

Run: `go test -race ./internal/resources/catalog -count=1`
Expected: PASS.

- [ ] **Step 5: Write the acceptance test**

`TestAccCatalogResource_basic`: create `trino_catalog` with `connector = "memory"`; check `id`; import unsupported step using `ImportState: true` + `ExpectError`; `CheckDestroy` asserts `SHOW CATALOGS` no longer lists it. `TestAccCatalogResource_connectorDrift` updates `connector` from `memory` to `tpch` and asserts replacement.

- [ ] **Step 6: Write the example and commit**

`examples/resources/trino_catalog/resource.tf` with the HCL from spec §7.1.

```bash
git add internal/resources/catalog examples/resources/trino_catalog
git commit -m "feat(catalog): add trino_catalog resource with dynamic-management diagnostics"
```

---

## Task 5: `trino_schema` resource (Lane B)

**Files:**
- Create: `internal/resources/schema/{resource.go,resource_test.go,schema_parse.go,schema_parse_test.go,acceptance_test.go}`
- Create: `examples/resources/trino_schema/{resource.tf,import.sh}`

**Interfaces:**
- Produces: `schema.New()`, type name `trino_schema`; parser `parseShowCreateSchema(ddl string) (owner *Owner, properties map[string]string, err error)`.

- [ ] **Step 1: Write the parser tests first**

The parser must be tokenizer-based, not regex-based: identifiers may be quoted,
values may contain spaces, commas, `'` (escaped as `''`) and newlines.

```go
func TestParseShowCreateSchema(t *testing.T) {
    cases := []struct{ ddl string; owner *Owner; props map[string]string }{
        {`CREATE SCHEMA hive.sales WITH ("location" = 's3://bucket/sales')`, nil, map[string]string{"location": "s3://bucket/sales"}},
        {`CREATE SCHEMA hive.sales AUTHORIZATION ROLE analyst WITH ("location" = '/tmp/x')`, &Owner{Type: "ROLE", Name: "analyst"}, map[string]string{"location": "/tmp/x"}},
        {`CREATE SCHEMA "hive"."sales" AUTHORIZATION USER "alice@example.com"`, &Owner{Type: "USER", Name: "alice@example.com"}, nil},
        {"CREATE SCHEMA hive.sales\nWITH (\"custom.key\" = 'v''al, with space')", nil, map[string]string{"custom.key": "v'al, with space"}},
    }
    // assert owner and props
}

func TestParseShowCreateSchemaMalformed(t *testing.T) { /* returns error, never panics */ }
```

- [ ] **Step 2: Write the resource tests**

`TestSchemaCreateMinimalSQL` → `CREATE SCHEMA "hive"."sales"`;
`TestSchemaCreateWithOwnerAndProperties` → `CREATE SCHEMA "hive"."sales" AUTHORIZATION USER "alice" WITH ("location" = 's3://bucket/sales')`;
`TestSchemaReadCaseInsensitiveName` (server returns `sales`, state `Sales` → state keeps `Sales`);
`TestSchemaReadNotFoundRemovesState`;
`TestSchemaReadParseFailurePreservesState`;
`TestSchemaUpdateOwnerEmitsAlter` → `ALTER SCHEMA "hive"."sales" SET AUTHORIZATION ROLE "analyst"`;
`TestSchemaForceDestroyOnlyUpdateNoSQL`;
`TestSchemaDeleteCascade` → `DROP SCHEMA "hive"."sales" CASCADE`;
`TestSchemaPropertiesRequireReplace`;
`TestSchemaImportID` (`hive.a.b` → catalog `hive`, name `a.b`).

- [ ] **Step 3: Run to confirm failure**

Run: `go test ./internal/resources/schema -count=1`

- [ ] **Step 4: Implement**

Schema per spec §7.2. Read: `SHOW SCHEMAS FROM "<catalog>"`, case-insensitive match; not found (including `CATALOG_NOT_FOUND`) → remove. Then `SHOW CREATE SCHEMA`; parse owner/properties; on parse error `tflog.Warn` and leave state untouched. Update: owner change → ALTER; force_destroy-only → state only; anything else should be impossible because of plan modifiers.

- [ ] **Step 5: Run unit tests and write acceptance**

Acceptance: `TestAccSchemaResource_memoryLifecycle`, `TestAccSchemaResource_hivePropertiesAndOwner`, `TestAccSchemaResource_forceDestroy`, `TestAccSchemaResource_import`, `TestAccSchemaResource_uppercaseName` (config `Sales` against Hive; asserts the second plan is empty). Use `user = "admin"` + `roles = { hive = "admin" }` for the Hive steps.

- [ ] **Step 6: Commit**

```bash
git add internal/resources/schema examples/resources/trino_schema
git commit -m "feat(schema): add trino_schema resource with owner and best-effort drift detection"
```

---

## Task 6: `trino_role` resource (Lane C)

**Files:**
- Create: `internal/resources/role/{resource.go,resource_test.go,acceptance_test.go}`
- Create: `examples/resources/trino_role/{resource.tf,import.sh}`

**Interfaces:**
- Produces: `role.New()`, type name `trino_role`.

- [ ] **Step 1: Write the tests**

`TestRoleCreateSQL` → `CREATE ROLE "analyst" IN "hive"`;
`TestRoleReadIgnoresOtherRoles` (`SHOW ROLES FROM "hive"` returns `admin`, `public`, `analyst`; only `analyst` matches);
`TestRoleReadCaseInsensitive` (`SHOW ROLES FROM "hive"` returns `analyst`, state `Analyst` → keeps `Analyst`);
`TestRoleReadNotFound` → remove;
`TestRoleDeleteSQL` → `DROP ROLE IF EXISTS "analyst" IN "hive"`;
`TestRoleDeleteNotFoundIsSuccess`;
`TestRoleImportID` (`hive.analyst` → catalog `hive`, name `analyst`);
`TestRoleCatalogRequired` (schema validator rejects empty).

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/resources/role -count=1`

- [ ] **Step 3: Implement per spec §7.3 and run**

Run: `go test -race ./internal/resources/role -count=1`
Expected: PASS.

- [ ] **Step 4: Acceptance and example**

`TestAccRoleResource_lifecycle` (Hive), `TestAccRoleResource_import`, `TestAccRoleResource_duplicateCreateFails`, `TestAccRoleResource_uppercaseName` (asserts no perpetual diff).

- [ ] **Step 5: Commit**

```bash
git add internal/resources/role examples/resources/trino_role
git commit -m "feat(role): add catalog role resource"
```

---

## Task 7: `trino_grant_role` resource (Lane D)

**Files:**
- Create: `internal/resources/grantrole/{resource.go,resource_test.go,acceptance_test.go}`
- Create: `examples/resources/trino_grant_role/{resource.tf,import.sh}`

**Interfaces:**
- Produces: `grantrole.New()`, type name `trino_grant_role`.

- [ ] **Step 1: Write the tests**

`TestGrantRoleCreateSQL` → `GRANT "analyst" TO USER "alice" IN "hive"`;
`TestGrantRoleCreateWithAdmin` → `... WITH ADMIN OPTION IN "hive"`;
`TestGrantRoleUpdateAdminOn` / `TestGrantRoleUpdateAdminOff` → `REVOKE ADMIN OPTION FOR ...`;
`TestGrantRoleDeleteSQL` → `REVOKE "analyst" FROM USER "alice" IN "hive"`;
`TestGrantRoleReadIsStateAuthoritative` (no calls, state unchanged);
`TestGrantRoleGranteeAllowsEmail` (`alice@example.com` quoted, accepted);
`TestGrantRoleImportID` (`hive.analyst:USER:alice@example.com`).

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/resources/grantrole -count=1`

- [ ] **Step 3: Implement per spec §7.4 and run**

Run: `go test -race ./internal/resources/grantrole -count=1`
Expected: PASS.

- [ ] **Step 4: Acceptance and example**

`TestAccGrantRoleResource_lifecycle` (create role first, grant to user, toggle admin, revoke, import).

- [ ] **Step 5: Commit**

```bash
git add internal/resources/grantrole examples/resources/trino_grant_role
git commit -m "feat(grantrole): add role grant resource with admin option"
```

---

## Task 8: `trino_grant_privileges` resource (Lane E)

**Files:**
- Create: `internal/resources/grantprivileges/{resource.go,resource_test.go,acceptance_test.go}`
- Create: `examples/resources/trino_grant_privileges/{resource.tf,import.sh}`

**Interfaces:**
- Produces: `grantprivileges.New()`, type name `trino_grant_privileges`.

- [ ] **Step 1: Write the tests**

```go
func TestGrantPrivilegesCreateSQL(t *testing.T) {
    // privileges configured as {"INSERT","SELECT"} → sorted output
    want := `GRANT INSERT, SELECT ON TABLE "hive"."sales"."orders" TO ROLE "analyst"`
}

func TestGrantPrivilegesReadUpdatesDrift(t *testing.T) {
    // fake returns rows with is_grantable "YES"/"NO" → state privileges becomes {SELECT,DELETE}, grant_option true
}

func TestGrantPrivilegesReadCaseInsensitiveGrantee(t *testing.T) {
    // assert args[3] == "Alice@Example.com"; SQL contains lower(grantee) = lower(?)
}

func TestGrantPrivilegesUpdateAddsAndRemoves(t *testing.T) {
    // state {SELECT}, plan {SELECT,INSERT} → one GRANT INSERT; apply twice → only one call
}

func TestGrantPrivilegesRejectsAllPrivileges(t *testing.T) { /* validator error names the four */ }
```

Plus `TestGrantPrivilegesCreateWithGrantOption`, `TestGrantPrivilegesReadRemovesWhenNoRows`, `TestGrantPrivilegesUpdateGrantOption`, `TestGrantPrivilegesDeleteSQL`, `TestGrantPrivilegesImportID`, `TestGrantPrivilegesOnRequiresReplace`.

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/resources/grantprivileges -count=1`

- [ ] **Step 3: Implement per spec §7.5 and run**

Run: `go test -race ./internal/resources/grantprivileges -count=1`
Expected: PASS.

- [ ] **Step 4: Acceptance and example**

`TestAccGrantPrivilegesResource_lifecycle`: fixture creates `hive.sales.orders`; grant `SELECT`; add `INSERT`; toggle `grant_option`; import; delete. Assert `information_schema.table_privileges` is empty in `CheckDestroy`.

- [ ] **Step 5: Commit**

```bash
git add internal/resources/grantprivileges examples/resources/trino_grant_privileges
git commit -m "feat(grantprivileges): add table privilege grant resource with drift detection"
```

---

## Task 9: Data sources (Lane F)

**Files:**
- Create: `internal/datasources/catalogs/{datasource.go,datasource_test.go,acceptance_test.go}`
- Create: `internal/datasources/schemas/{datasource.go,datasource_test.go,acceptance_test.go}`
- Create: `internal/datasources/roles/{datasource.go,datasource_test.go,acceptance_test.go}`
- Create: `internal/datasources/tableprivileges/{datasource.go,datasource_test.go,acceptance_test.go}`
- Create: `examples/data-sources/<type>/data-source.tf` × 4

**Interfaces:**
- Produces: `catalogs.New()`, `schemas.New()`, `roles.New()`, `tableprivileges.New()`; type names `trino_catalogs`, `trino_schemas`, `trino_roles`, `trino_table_privileges`.

- [ ] **Step 1: Write the tests**

For each data source: assert exact SQL, deterministic ordering, state shape, empty result (no error) and missing parent (error).
- `TestCatalogsReadSQL` → `SELECT catalog_name, connector_name FROM system.metadata.catalogs ORDER BY catalog_name`
- `TestCatalogsNamePrefixFilter`
- `TestSchemasCatalogIsRequired` (schema attribute is `Required`)
- `TestSchemasReadSQL` → `SHOW SCHEMAS FROM "hive"`
- `TestRolesCatalogIsRequired`; `TestRolesReadSQL` → `SHOW ROLES FROM "hive"`
- `TestTablePrivilegesReadSQL` (asserts the five args and `ORDER BY privilege_type`)
- `TestTablePrivilegesEmptyResult`

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./internal/datasources/... -count=1`

- [ ] **Step 3: Implement per spec §8 and run**

Run: `go test -race ./internal/datasources/... -count=1`
Expected: PASS.

- [ ] **Step 4: Acceptance and examples**

One `TestAcc<Type>DataSource_basic` per data source; Hive for roles/privileges, memory for schemas. Role assertions use "contains", because the file metastore always reports the built-in `admin` role.

- [ ] **Step 5: Commit**

```bash
git add internal/datasources examples/data-sources
git commit -m "feat(datasources): add read-only catalog, schema, role and privilege sources"
```

---

## Task 10: Provider wiring, docs and complete example

**Files:**
- Modify: `internal/provider/provider.go` (registration lists)
- Create: `examples/complete/main.tf`
- Modify: `README.md`, `AGENTS.md`, `ARCHITECTURE.md`, `CHANGELOG.md`
- Generated: `docs/**`

**Interfaces:**
- Consumes: every `New()` from Tasks 4–9.

- [ ] **Step 1: Register resources and data sources**

```go
func (p *TrinoProvider) Resources(context.Context) []func() resource.Resource {
    return []func() resource.Resource{
        catalog.New, schema.New, role.New, grantrole.New, grantprivileges.New,
    }
}
```

DataSources: `catalogs.New, schemas.New, roles.New, tableprivileges.New`.

- [ ] **Step 2: Verify the registry**

```bash
go build ./...
go test ./internal/provider -count=1
```

Expected: PASS; add `TestProviderRegistersAllTypes` asserting 5 resources and 4 data sources.

- [ ] **Step 3: Write `examples/complete/main.tf`**

Provider block with env-driven endpoint/user/roles; `trino_catalog` (memory), `trino_schema`, `trino_role`, `trino_grant_role`, `trino_grant_privileges` with `depends_on` where ordering matters. Add `TestAccProviderCompleteExample` that runs `resource.Test` on the example, or a `terraform validate` check in CI.

- [ ] **Step 4: Write README sections**

Install, quick start, provider config table, resource/data source tables, read-back matrix, known limitations (users, system roles, schema grants, catalog import, role-grant drift), required privileges, version policy.

- [ ] **Step 5: Generate docs and check drift**

```bash
go generate ./...
git add docs
git diff --cached --stat
go generate ./...
git diff --exit-code -- docs/
```

Expected: second generate produces no diff.

- [ ] **Step 6: Commit**

```bash
git add internal/provider examples/complete README.md AGENTS.md ARCHITECTURE.md CHANGELOG.md docs
git commit -m "feat(provider): wire resources, data sources and generated documentation"
```

---

## Task 11: Acceptance environment, coverage gates, release dry-run

**Files:**
- Modify: `docker-compose.yml`, `test/acceptance/trino/etc/**`, `.github/workflows/test.yml`, `GNUmakefile`, `.github/coverage-baseline.txt`
- Create: `test/acceptance/trino/healthcheck.sh`, `test/acceptance/trino/init-catalogs.sh`
- Create: `scripts/coverage-gate.sh` (if not already complete)
- Create: `contributing/kerberos-acceptance.md`

- [ ] **Step 1: Compose environment**

Trino 484, port 8080, config with `catalog.management=dynamic` and
`catalog.store=memory`. Dynamic management is required by the `trino_catalog`
resource; the memory store means the coordinator **ignores**
`etc/catalog/*.properties`, so a one-shot `trino-init` service creates both
catalogs over SQL before `test` starts (`test/acceptance/trino/init-catalogs.sh`):

- `memory`: `CREATE CATALOG memory USING memory`;
- `hive`: `CREATE CATALOG hive USING hive WITH ("hive.metastore" = 'file',
  "hive.metastore.catalog.dir" = 'file:///tmp/hive', "hive.security" =
  'sql-standard', "fs.local.enabled" = 'true')`.

`test` depends on `trino-init` with
`condition: service_completed_successfully`. The `trino` healthcheck runs
`test/acceptance/trino/healthcheck.sh`, which waits for `/v1/info` to report
`"starting":false` (the endpoint answers with `"starting":true` while the
coordinator boots, and the image has neither `grep` nor `sed`).

- [ ] **Step 2: Run the acceptance suite locally**

`docker compose up -d --wait` cannot be used here: it exits non-zero on a
one-shot init container (docker/compose#10596). Bring the coordinator up, run
the init service explicitly, then run the suite on the host:

```bash
docker compose up -d --wait trino
docker compose run --rm -T trino-init
TF_ACC=1 TRINO_ENDPOINT=http://localhost:8080 TRINO_USER=admin \
  go test -race -v ./internal/... -run '^TestAcc'
```

Or run the whole suite in the test container, which starts the init service as a
dependency:

```bash
docker compose run --rm test
```

Expected: PASS, no `SKIP` for catalog/role/grant tests.

- [ ] **Step 3: Coverage gate**

```bash
make coverage
make coverage-gate
```

Expected: ≥ 80%; otherwise add tests for the uncovered branch (not an exclusion).

- [ ] **Step 4: Release dry-run**

```bash
goreleaser check --config .github/goreleaser.yml
goreleaser build --snapshot --clean --config .github/goreleaser.yml
```

Expected: binaries for 6 targets; `terraform-registry-manifest.json` present in the archive set.

- [ ] **Step 5: Write the Kerberos manual runbook**

`contributing/kerberos-acceptance.md` documents, with copy-paste commands:
KDC realm + `HTTP/trino@EXAMPLE.COM` principal, keytab export, Trino
`config.properties` for Kerberos + HTTPS, the provider block with
`auth = "kerberos"`, `kerberos_keytab`, `kerberos_principal`,
`kerberos_realm`, `kerberos_config_file`, and the expected
`terraform plan/apply/destroy` outcome plus troubleshooting for
`Server not found in Kerberos database` and clock skew.

- [ ] **Step 6: Commit**

```bash
git add docker-compose.yml test .github GNUmakefile scripts .github/coverage-baseline.txt contributing/kerberos-acceptance.md
git commit -m "test: add docker acceptance environment, coverage gates and Kerberos runbook"
```

---

## Task 12: Whole-branch review and release readiness

- [ ] **Step 1: Run every gate**

```bash
go build ./... && go vet ./... && golangci-lint run --config .golangci.yml ./...
go test -race ./internal/... -count=1
go generate ./... && git diff --exit-code -- docs/
./scripts/validate-examples.sh
```

- [ ] **Step 2: Review against the spec**

Check the read-back matrix, import exceptions, sensitivity list, version policy and documented non-goals against the implementation. Every mismatch is either fixed or added to the spec with a reason.

- [ ] **Step 3: Independent review**

Dispatch `code-reviewer` on the full diff; fix findings; re-run Step 1.

- [ ] **Step 4: Release readiness**

Confirm `CHANGELOG.md` has an `## Unreleased` section, `README` install snippet matches `goreleaser` header, and the registry manifest is valid JSON.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: v1 release readiness"
```

---

## Self-Review

**Spec coverage**

| Spec section | Task |
|---|---|
| §4 package layout | 1–3 |
| §5 interfaces | 2, 3 |
| §6 provider config | 3 |
| §7.1 catalog | 4 |
| §7.2 schema | 5 |
| §7.3 role | 6 |
| §7.4 grant role | 7 |
| §7.5 grant privileges | 8 |
| §8 data sources | 9 |
| §9 diagnostics | 2, every resource task |
| §10 tests | every task; harness in 1, 3, 11 |
| §11 tooling/CI/docs | 1, 10, 11 |
| §12 security | 2 (quoting/logging), 3 (sensitivity), each resource |
| §13 risks | tests named in Review Focus |
| §14 DoD | 12 |
| §15 decisions | enforced by schemas and docs in 4–10 |

**Type consistency:** `trino.Executor`, `providerdata.Data`, `resourceutil.SplitImportID`, and all `New()` constructors are used with the exact signatures defined in Task 2/3 and spec §5. Resource models stay package-private; no cross-package model dependency.

**Proportion:** tasks specify signatures, golden SQL, test names and gate commands. Implementation bodies are left to the lane, except for the small provider stub and the frozen interface definitions.
