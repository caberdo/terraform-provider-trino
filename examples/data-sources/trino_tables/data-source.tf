data "trino_tables" "sales" {
  catalog = "hive"
  schema  = "sales"

  name_prefix = "order_"
  table_type  = "BASE TABLE"
}
