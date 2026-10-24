data "trino_table_privileges" "example" {
  catalog = "hive"
  schema  = "default"
  table   = "example"
}
