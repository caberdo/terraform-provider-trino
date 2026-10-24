resource "trino_role" "analyst" {
  name    = "analyst"
  catalog = "hive" # required: system roles are disabled on stock Trino
}
