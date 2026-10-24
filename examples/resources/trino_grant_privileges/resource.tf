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
