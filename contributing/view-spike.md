# `trino_view` spike findings

Status: phase 1 complete; the resource shipped with the recommended contract
(`internal/resources/view`), with `definition` write-only and `security` drift.
Checked on 2026-10-26 against Trino **476** and **484**.

## Environment

Both versions ran the acceptance coordinator with dynamic catalog management and
two catalogs:

- `hive`: file metastore, `hive.security=sql-standard`, connecting user `admin`
  with `roles = { hive = "admin" }`.
- `memory`: no access control, no roles.

The SQL was executed through the provider's own executor, so the session
carried the same `X-Trino-Role` header the provider uses.

## Findings

### `SHOW CREATE VIEW` normalises the definition

Input:

```sql
CREATE VIEW hive.spike.v AS SELECT a, b FROM hive.spike.t
```

`SHOW CREATE VIEW hive.spike.v`:

```sql
CREATE VIEW hive.spike.v SECURITY DEFINER AS
SELECT
  a
, b
FROM
  hive.spike.t
```

Differences from the input:

- the security mode is always present (`SECURITY DEFINER` by default, or the
  configured `SECURITY INVOKER`);
- the select list is split one expression per line;
- `FROM` is followed by a newline and two spaces;
- `WHERE` predicates are wrapped in parentheses;
- there is no trailing semicolon.

The normalisation is identical on 476 and 484 and on the memory connector.

### `CREATE OR REPLACE VIEW` works on both connectors and versions

`CREATE OR REPLACE VIEW ... AS ...` succeeded on the Hive and memory catalogs,
on 476 and 484. The explicit `SECURITY INVOKER` clause was accepted on the
memory catalog, and the default `SECURITY DEFINER` form on Hive.

### Hive does not rewrite stored views when the underlying table changes

`ALTER TABLE hive.spike.t RENAME COLUMN b TO c` succeeds, but the stored view
definition is not updated:

```sql
SHOW CREATE VIEW hive.spike.v
-- CREATE VIEW hive.spike.v SECURITY DEFINER AS
-- SELECT
--   a
-- , b
-- FROM
--   hive.spike.t
```

Querying the view then fails:

```
Failed analyzing stored view 'hive.spike.v': line 3:3: Column 'b' cannot be resolved
```

Dropping the referenced column produces the same result: the view definition
still names `b` and the view remains broken until it is replaced. A column
rename is therefore a silent invalidation vector that `SHOW CREATE VIEW` does
not reveal.

The memory connector rejected `ALTER TABLE ... DROP COLUMN` in this
environment, so the invalidation case could not be exercised there.

### Existence has a reliable read path

A view appears in `information_schema.tables` with `table_type = 'VIEW'`, and
`SHOW CREATE VIEW` returns a not-found error (`View '<name>' does not exist`)
when the object is absent. Existence and removal from state are therefore reliable on both
connectors.

### Hive lowercases the stored name

`CREATE VIEW hive.spike."MyView"` succeeds and stores `myview`.
`SHOW CREATE VIEW hive.spike."MyView"` then fails with
`objectName is not lowercase: MyView`, so a quoted read with the configured
spelling is not reliable on Hive. Queried with
`lower(table_schema) = lower(?)`, `information_schema.tables` reports the stored
spelling (`zz_case`, `myview`), which a quoted `SHOW CREATE VIEW` accepts. The
resource therefore resolves existence and the stored spelling in the
information schema and uses the result for the follow-up statement. `DROP VIEW`
accepts the configured spelling on Hive.

The memory connector preserves the configured spelling, so the exact-case match
wins there.

## Recommendation

**Build `trino_view`, but do not claim definition drift detection.**

The delivered resource models:

- `catalog`, `schema`, `name`;
- `definition` (the `SELECT` query) as **write-only**, like catalog
  properties: state keeps the configured text and `Read` does not overwrite it;
- `security` (`DEFINER` or `INVOKER`, default `DEFINER`) with **drift detection**
  from the `SECURITY` clause in `SHOW CREATE VIEW`, which is stable and
  unambiguous;
- import ID `catalog.schema.name`; the first read verifies existence but cannot
  recover the configured definition, so the import documentation must say that
  `definition` must be supplied by configuration.

Reasons for not comparing the SQL body:

- Trino's normal form is not byte-stable with user input, so a raw comparison
  is either a permanent diff or requires a full SQL canonicaliser.
- A token comparison that ignores formatting still cannot detect the dangerous
  case: Hive keeps the old definition after a column rename, so the stored text
  looks unchanged while the view is broken. Reporting "no drift" there would
  violate the provider's rule against faking drift detection.
- Existence changes (drop/recreate) are detected, which is the failure mode
  Terraform can actually repair.

This is the "existence-only" option from the issue, plus the stable `security`
attribute. It keeps the read-back matrix honest: the resource documents that
`definition` is write-only.

## Delivered contract

- `internal/resources/view` implements `trino_view` with the contract above.
- Acceptance covers Hive (sql-standard + admin role) and memory on the full
  version window, including an out-of-band security change and drop.
- The README limitation records that renaming or dropping a referenced column
  can invalidate a Hive view without changing its stored definition.
