resource "trino_view" "orders_by_region" {
  catalog = "hive"
  schema  = "sales"
  name    = "orders_by_region"

  # Raw SQL placed after AS. Table references are resolved in the provider's
  # session, so qualify them fully.
  definition = "SELECT region, count(*) AS orders FROM hive.sales.orders GROUP BY region"

  # DEFINER (the default) runs the view with the definer's privileges;
  # INVOKER runs it with the querying user's.
  security = "DEFINER"
}
