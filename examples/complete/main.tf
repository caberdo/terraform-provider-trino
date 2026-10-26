terraform {
  required_providers {
    trino = {
      source = "caberdo/trino"
    }
  }
}

# Endpoint, user and auth come from the environment (TRINO_ENDPOINT,
# TRINO_USER, TRINO_AUTH) so the same module runs against the compose
# acceptance environment and a real coordinator.
#
# `roles` enables the catalog role SQL-standard Hive requires before any DDL.
# The file metastore in the acceptance environment auto-grants `admin` to the
# `admin` user, which is why the test identity can create roles at all.
provider "trino" {
  roles = {
    hive = "admin"
  }
}

variable "grantee" {
  type        = string
  description = "Trino principal that receives the analyst role and the table grant."
  default     = "alice"
}

# A dynamic catalog. Its properties are write-only: Trino exposes no read path,
# so Terraform keeps the configured values in state and cannot detect property
# drift. Every catalog attribute forces replacement, and import is unsupported
# for the same reason.
resource "trino_catalog" "analytics" {
  name      = "analytics"
  connector = "memory"
}

# A catalog UDF stored by the connector. The Hive and Memory connectors both
# implement UDF storage. Trino normalises the definition, so the provider
# compares it token by token; import reads the normalised form back.
resource "trino_function" "add_one" {
  catalog    = trino_catalog.analytics.name
  schema     = trino_schema.sales.name
  name       = "add_one"
  definition = "(x bigint) RETURNS bigint RETURN x + 1"

  depends_on = [trino_schema.sales]
}

# The schema lives in the catalog above, so the catalog is created first.
# `owner` is optional/computed: Trino reports the creating user and the next
# refresh adopts it, which is intentional rather than a diff.
resource "trino_schema" "sales" {
  catalog = trino_catalog.analytics.name
  name    = "sales"

  depends_on = [trino_catalog.analytics]
}

# Catalog roles are the unit of access control in v1; system roles are not
# supported because stock Trino disables them. `catalog` is required.
resource "trino_role" "analyst" {
  name    = "analyst"
  catalog = "hive"
}

# Trino cannot list another principal's role grants, so Read is
# state-authoritative: a grant revoked outside Terraform is not detected. The
# grant is applied after the role exists.
resource "trino_grant_role" "analyst_to_grantee" {
  role         = trino_role.analyst.name
  role_catalog = trino_role.analyst.catalog
  grantee      = var.grantee
  grantee_type = "USER"
  admin_option = false

  depends_on = [trino_role.analyst]
}

# The provider does not manage tables, so `hive.default.orders` must exist
# before this module is applied. Against the compose environment:
#   docker compose up -d --wait trino
#   docker compose run --rm -T trino-init
#   docker compose exec trino trino --execute "CREATE SCHEMA IF NOT EXISTS hive.default"
#   docker compose exec trino trino --execute "CREATE TABLE IF NOT EXISTS hive.default.orders (id bigint)"
# Table grants are read back from information_schema.table_privileges, so
# out-of-band revocation is detected as long as the provider user can see the
# grant.
resource "trino_grant_privileges" "analyst_select" {
  privileges = ["SELECT"]
  on = {
    catalog = "hive"
    schema  = "default"
    table   = "orders"
  }
  grantee      = "analyst"
  grantee_type = "ROLE"
  grant_option = false

  depends_on = [trino_grant_role.analyst_to_grantee]
}

# Materialized views need a connector that stores them. In the acceptance
# environment that is `iceberg`, backed by a Hive metastore; the Iceberg REST
# catalog rejects CREATE MATERIALIZED VIEW. The provider does not manage source
# tables, so create one before this module is applied:
#   docker compose exec trino trino --execute "CREATE SCHEMA IF NOT EXISTS iceberg.default"
#   docker compose exec trino trino --execute "CREATE TABLE IF NOT EXISTS iceberg.default.events (id bigint, region varchar)"
#
# The query is resolved in the provider's session, so it is fully qualified.
# Read never refreshes: `refresh_token` is the explicit trigger, and
# `grace_period` keeps queries on the stored data fast until the view goes
# stale.
resource "trino_materialized_view" "events_by_region" {
  catalog = "iceberg"
  schema  = "default"
  name    = "events_by_region"
  query   = "SELECT region, count(*) AS events FROM iceberg.default.events GROUP BY region"

  properties = {
    format = "'ORC'"
  }

  grace_period      = "1h"
  refresh_on_create = true
  refresh_token     = "2026-10-26"
}

# Views are definitions, not data. `definition` is write-only: Trino stores a
# normalised form and Hive keeps a stale definition after an underlying column
# rename, so only `security` is read back. The source table must exist before
# this module is applied; the commands above create `hive.default.orders`.
resource "trino_view" "orders_by_id" {
  catalog    = "hive"
  schema     = "default"
  name       = "orders_by_id"
  definition = "SELECT id FROM hive.default.orders"
}
