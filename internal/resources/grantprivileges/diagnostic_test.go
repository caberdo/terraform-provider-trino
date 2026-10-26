package grantprivileges_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

// Layer: protocol fake (the httptest /v1/statement endpoint). The acceptance
// coordinator connects as the admin user, which sql-standard security
// auto-grants, so the matrix cannot produce a non-admin grant. The fake makes
// the server answer GRANT with ACCESS_DENIED, which is exactly what a
// non-admin caller receives.
//
// The assertion covers the classification as well as the text: a permission
// failure must not degrade to the generic "Trino request failed" diagnostic.
func TestAccGrantPrivilegesPermissionDenied_ProtocolFake(t *testing.T) {
	server := trinotest.NewStatementServer(t, func(string) []trinotest.Page {
		return []trinotest.Page{{Error: &trinotest.ProtoError{
			Message:   `Access Denied: Cannot grant privilege SELECT on table hive.acc_priv_sales.acc_priv_orders`,
			ErrorName: "ACCESS_DENIED",
			ErrorType: "USER_ERROR",
			SqlState:  "42000",
		}}}
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      testAccGrantPrivilegesFakeConfig(server.URL(), "bob", `["SELECT"]`),
			ExpectError: regexp.MustCompile(`(?is)permission\s+denied.*not\s+allowed\s+to\s+grant\s+privileges\s+on\s+hive\.acc_priv_sales\.acc_priv_orders`),
		}},
	})
}

// Layer: provider schema validation, before any SQL is sent. The documented
// reason is part of the contract: Trino expands ALL PRIVILEGES before storing
// it, so accepting it would produce a permanent diff on read-back.
func TestAccGrantPrivilegesAllPrivilegesDiagnostic(t *testing.T) {
	server := trinotest.NewStatementServer(t, func(string) []trinotest.Page {
		return []trinotest.Page{{}}
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      testAccGrantPrivilegesFakeConfig(server.URL(), "admin", `["ALL PRIVILEGES"]`),
			ExpectError: regexp.MustCompile(`(?is)all\s+privileges\s+is\s+not\s+supported.*list\s+select,\s*insert,\s*update,\s*delete\s+explicitly`),
		}},
	})
}

func testAccGrantPrivilegesFakeConfig(endpoint, user, privileges string) string {
	return fmt.Sprintf(`
provider "trino" {
  endpoint = %q
  user     = %q
  auth     = "none"
}

resource "trino_grant_privileges" "test" {
  privileges = %s

  on = {
    catalog = %q
    schema  = %q
    table   = %q
  }

  grantee      = %q
  grantee_type = "USER"
}
`, endpoint, user, privileges, accCatalog, accSchema, accTable, accGrantee)
}
