package tableprivileges_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
)

func TestAccTablePrivilegesDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			acctest.PreCheck(t)
			t.Cleanup(acctest.Fixture(t,
				"CREATE SCHEMA IF NOT EXISTS hive.acc_ds_sales",
				"CREATE TABLE IF NOT EXISTS hive.acc_ds_sales.acc_ds_orders (id BIGINT)",
			))
		},
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: `provider "trino" {
  user  = "admin"
  roles = { hive = "admin" }
}

data "trino_table_privileges" "test" {
  catalog = "hive"
  schema  = "acc_ds_sales"
  table   = "acc_ds_orders"
}`,
			Check: resource.ComposeTestCheckFunc(
				// sql-standard security grants the table owner its default
				// privileges, so a freshly created table never returns an
				// empty list. Assert the set is populated and includes one of
				// the owner grants rather than a fixed count, which would be
				// brittle across releases.
				resource.TestCheckResourceAttrSet("data.trino_table_privileges.test", "privileges.#"),
				tablePrivilegesContain("data.trino_table_privileges.test", "SELECT"),
			),
		}},
	})
}

// tablePrivilegesContain reports whether the data source listed a privilege of
// the given type for any grantee.
func tablePrivilegesContain(name, privilege string) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		instance, ok := state.RootModule().Resources[name]
		if !ok || instance.Primary == nil {
			return fmt.Errorf("%s not found in state", name)
		}
		for key, value := range instance.Primary.Attributes {
			if strings.HasSuffix(key, ".privilege_type") && strings.EqualFold(value, privilege) {
				return nil
			}
		}
		return fmt.Errorf("%s does not list a %s privilege", name, privilege)
	}
}
