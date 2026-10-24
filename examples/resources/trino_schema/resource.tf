terraform {
  required_providers {
    trino = {
      source = "caberdo/trino"
    }
  }
}

resource "trino_schema" "sales" {
  catalog       = "lakehouse"
  name          = "sales"
  owner         = "alice"
  owner_type    = "USER"
  properties    = { location = "s3://bucket/sales" }
  force_destroy = false
}
