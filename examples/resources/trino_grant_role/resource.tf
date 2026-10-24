terraform {
  required_providers {
    trino = {
      source = "caberdo/trino"
    }
  }
}

provider "trino" {
  endpoint = "http://localhost:8080"
  user     = "admin"
  auth     = "none"
}

# Trino cannot list another principal's role grants, so Read is
# state-authoritative: a grant revoked outside Terraform is not detected and
# stays in state. Re-apply the grant (or import after removing it from state)
# if it is revoked out of band.
resource "trino_grant_role" "analyst_to_alice" {
  role         = "analyst"
  role_catalog = "hive" # required, matches trino_role.catalog
  grantee      = "alice"
  grantee_type = "USER" # USER | ROLE
  admin_option = false
}
