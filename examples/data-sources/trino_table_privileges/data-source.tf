# Schema-wide: every privilege recorded for every table in the schema.
data "trino_table_privileges" "sales" {
  catalog = "hive"
  schema  = "sales"
}

# Exact table: the same query shape the data source used before schema-wide
# listing existed. Each row also names its table.
data "trino_table_privileges" "orders" {
  catalog = "hive"
  schema  = "sales"
  table   = "orders"
}
