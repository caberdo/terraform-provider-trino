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
