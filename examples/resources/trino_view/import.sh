# The definition is write-only, so it cannot be recovered from Trino on import.
# Supply it in configuration; the next apply runs CREATE OR REPLACE VIEW.
terraform import trino_view.orders_by_region hive.sales.orders_by_region
