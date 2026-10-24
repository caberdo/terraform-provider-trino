terraform {
  required_providers {
    trino = {
      source = "caberdo/trino"
    }
  }
}

provider "trino" {
  endpoint = "http://localhost:8080"
  user     = "admin"
  auth     = "none"
}

variable "iceberg_credential" {
  type        = string
  description = "OAuth2 credential for the Iceberg REST catalog."
  sensitive   = true
}

# Catalog properties are write-only: Trino exposes no read path for them, so
# Terraform keeps the configured values in state and cannot detect property
# drift. Import is unsupported for the same reason: every attribute forces
# replacement, so importing would drop and recreate the live catalog.
resource "trino_catalog" "lakehouse" {
  name      = "lakehouse"
  connector = "iceberg"
  properties = {
    "iceberg.catalog.type" = "rest"
    "iceberg.rest.uri"     = "http://iceberg:8181"
  }
  sensitive_properties = {
    "iceberg.rest.oauth2.credential" = var.iceberg_credential
  }
}
