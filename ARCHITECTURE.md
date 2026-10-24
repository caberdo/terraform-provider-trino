# Architecture

## Request flow

```
Terraform CLI
     │ plugin protocol 6.0
     ▼
main.go ── providerserver.Serve ──► internal/provider
                                        │ schema, env fallback, Configure
                                        │ builds Config → Executor
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

## Executor

`internal/trino.Executor` is the only path to Trino. It does not reimplement the
client protocol; `trino-go-client` already implements it, including spooled
results and retries. The provider owns the lifecycle around it:

- **Request headers** (`X-Trino-User`, `X-Trino-Source`, `X-Trino-Catalog`,
  `X-Trino-Schema`, `X-Trino-Role`, `X-Trino-Session`, `X-Trino-Extra-Credential`,
  `Authorization`) come from provider configuration and are identical on every
  request. There is no per-statement `SET SESSION`, so pooled connections cannot
  leak session state between resources.
- **DDL** uses `ExecContext`, drains every page and returns only the terminal
  error. DDL produces no rows.
- **Queries** use `QueryContext`, scan all pages into `[]Row` and close the rows
  before returning.
- **Polling** belongs to the driver (`nextUri`); the provider adds no polling
  loop.
- **Timeout** is a per-operation context from `providerdata.Data.Timeout`
  (`query_timeout`, default 5m). `trino.Config.QueryTimeout` additionally asks
  the server to cancel a runaway query; the shorter deadline wins.
- **Retry** is driver-level for transient network errors and 502/503/504. Trino
  semantic errors are classified and never retried.
- **Version probe** runs `SELECT version()` once per process and is cached; a
  failure is non-fatal and logged.

## Authentication matrix

`internal/provider/config.go` resolves attribute → environment → default,
infers the mode when `auth` is unset, and validates the cross-field rules before
any request is made.

| Mode | Credentials | Transport | Failure the provider catches early |
|---|---|---|---|
| `none` | none | `http` or `https` | Any password/token/OAuth2 secret set alongside `none` |
| `basic` | `user` + `password` | `https` only | Password over `http`; missing password |
| `jwt` | `access_token` | `http` or `https` | Token plus another mode's credentials |
| `oauth2` | client ID + secret + token endpoint | `http` or `https` | Missing endpoint (476 has no discovery), missing credentials |
| `kerberos` | principal + realm + keytab **or** ccache | `https` only | Keytab and ccache together, realm in the service pattern, missing realm with a keytab |

TLS options (`ca_cert`/`ca_cert_file`, `client_cert`/`client_cert_file`,
`client_key`/`client_key_file`, `insecure_skip_verify`) are built once into an
`*http.Client` and reused for every request. `query_timeout` bounds each
operation through a per-call context; the driver's own query timeout is set to
the same value so the server cancels a runaway query even if the client dies.

## Error taxonomy

`internal/trino` classifies errors by Trino error name (`ErrTrino`). Resources
translate the classification into diagnostics:

- `NOT_FOUND` → remove from state on read.
- `ALREADY_EXISTS` / `NOT_SUPPORTED` → actionable attribute or resource
  diagnostic.
- Anything else → operation-scoped error with the Trino message, never a
  credential.

## Test layers

| Layer | What it proves |
|---|---|
| L1 — pure unit | quoting, validation, parsers, version comparison |
| L2 — resource logic | CRUD against a scripted fake `Executor` |
| L3 — protocol | `internal/trino` against a fake `/v1/statement` server |
| L4 — acceptance | real Trino in Docker, `TF_ACC=1` |

Acceptance fixtures (schemas and tables used by grant tests) are created through
the same executor in `internal/acctest`, with cleanup, because the provider
deliberately does not manage tables. The compose environment provides two
catalogs: `memory` for schema lifecycle and `hive` (file metastore,
`sql-standard` security) for roles, role grants and table grants. Catalog, role
and grant tests must **not** skip when `TF_ACC` is set: a missing capability is
an environment failure, not a pass.

Unit tests live in the same package as the code they cover. Acceptance tests
live in `<name>_test` and must compile with `go vet ./...` even when they are
not run locally.

## Dependency rules

1. `internal/trino` imports only stdlib, `trino-go-client` and `go-version`.
2. `internal/providerdata` imports `internal/trino` and framework `diag`.
3. `internal/resources/*` and `internal/datasources/*` import `trino`,
   `providerdata`, `resourceutil` and their local models only.
4. Nothing under `internal/resources` imports `internal/provider`.
5. `internal/provider` imports every resource/data-source package and is the
   only place registration lives.
