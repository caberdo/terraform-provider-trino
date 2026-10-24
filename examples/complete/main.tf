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
