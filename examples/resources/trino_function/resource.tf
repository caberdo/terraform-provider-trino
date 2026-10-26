resource "trino_function" "add_one" {
  catalog    = "hive"
  schema     = "default"
  name       = "add_one"
  definition = "(x bigint) RETURNS bigint RETURN x + 1"
}
