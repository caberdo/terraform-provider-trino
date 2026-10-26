package catalog_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

// Layer: protocol fake (the httptest /v1/statement endpoint). The acceptance
// coordinator always runs with catalog.management=dynamic, and the version
// matrix cannot produce a coordinator without it, so this case drives the
// protocol fake instead of a real server.
//
// It asserts the diagnostic text, not only that the apply failed: the hint
// must name the property the operator has to set.
func TestAccCatalogCreateWithoutDynamicManagement_ProtocolFake(t *testing.T) {
	server := trinotest.NewStatementServer(t, func(statement string) []trinotest.Page {
		if strings.HasPrefix(statement, "CREATE CATALOG") {
			return []trinotest.Page{{Error: &trinotest.ProtoError{
				Message:   "Dynamic catalog management is not enabled",
				ErrorName: "NOT_SUPPORTED",
				ErrorType: "USER_ERROR",
				SqlState:  "0A000",
			}}}
		}
		return []trinotest.Page{{Columns: []trinotest.Column{
			{Name: "catalog_name", Type: "varchar"},
			{Name: "connector_name", Type: "varchar"},
		}}}
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "trino" {
  endpoint = %q
  user     = "admin"
  auth     = "none"
}

resource "trino_catalog" "test" {
  name      = "lakehouse"
  connector = "iceberg"
}
`, server.URL()),
			ExpectError: regexp.MustCompile(`(?is)dynamic\s+catalog\s+management\s+is\s+not\s+enabled.*catalog\.management=dynamic`),
		}},
	})
}
