resource "trino_materialized_view" "orders_by_region" {
  catalog = "iceberg"
  schema  = "sales"
  name    = "orders_by_region"

  # The query is resolved in the provider's session, so qualify table names.
  query = "SELECT region, count(*) AS orders FROM iceberg.sales.orders GROUP BY region"

  # Storage-table properties as raw SQL expressions; the provider quotes the
  # names and emits the values verbatim.
  properties = {
    format = "'ORC'"
  }

  grace_period = "1h"

  # Populate the view when it is created, and refresh explicitly by changing
  # refresh_token. Read never refreshes.
  refresh_on_create = true
  refresh_token     = "2026-10-26"
}
