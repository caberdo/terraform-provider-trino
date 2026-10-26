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
				tablePrivilegesAllForTable("data.trino_table_privileges.test", "acc_ds_orders"),
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

func TestAccTablePrivilegesDataSource_schemaWide(t *testing.T) {
	acctest.PreCheck(t)
	t.Cleanup(acctest.Fixture(t,
		"DROP SCHEMA IF EXISTS hive.acc_ds_priv_sales CASCADE",
		"CREATE SCHEMA IF NOT EXISTS hive.acc_ds_priv_sales",
		"CREATE TABLE IF NOT EXISTS hive.acc_ds_priv_sales.acc_ds_orders (id BIGINT)",
		"CREATE TABLE IF NOT EXISTS hive.acc_ds_priv_sales.acc_ds_customers (id BIGINT)",
		`GRANT SELECT ON TABLE hive.acc_ds_priv_sales.acc_ds_orders TO USER acc_ds_alice`,
		`GRANT INSERT ON TABLE hive.acc_ds_priv_sales.acc_ds_customers TO USER acc_ds_bob`,
	))

	const address = "data.trino_table_privileges.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: `provider "trino" {
  user  = "admin"
  roles = { hive = "admin" }
}

data "trino_table_privileges" "test" {
  catalog = "hive"
  schema  = "acc_ds_priv_sales"
}`,
			Check: resource.ComposeTestCheckFunc(
				tablePrivilegesContainForTable(address, "acc_ds_orders", "SELECT", "acc_ds_alice"),
				tablePrivilegesContainForTable(address, "acc_ds_customers", "INSERT", "acc_ds_bob"),
			),
		}},
	})
}

// tablePrivilegesAllForTable asserts every returned row names the exact table
// an exact-table query asked about.
func tablePrivilegesAllForTable(name, table string) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		instance, ok := state.RootModule().Resources[name]
		if !ok || instance.Primary == nil {
			return fmt.Errorf("%s not found in state", name)
		}
		found := false
		for key, value := range instance.Primary.Attributes {
			if strings.HasSuffix(key, ".table_name") {
				found = true
				if value != table {
					return fmt.Errorf("%s returned row with table_name %q, want %q", name, value, table)
				}
			}
		}
		if !found {
			return fmt.Errorf("%s did not return any privilege with a table_name", name)
		}
		return nil
	}
}

// tablePrivilegesContainForTable reports whether a row combines the given
// table, privilege and grantee.
func tablePrivilegesContainForTable(name, table, privilege, grantee string) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		instance, ok := state.RootModule().Resources[name]
		if !ok || instance.Primary == nil {
			return fmt.Errorf("%s not found in state", name)
		}
		attributes := instance.Primary.Attributes
		for key, value := range attributes {
			var index int
			if _, err := fmt.Sscanf(key, "privileges.%d.table_name", &index); err != nil {
				continue
			}
			prefix := fmt.Sprintf("privileges.%d.", index)
			if value == table &&
				strings.EqualFold(attributes[prefix+"privilege_type"], privilege) &&
				strings.EqualFold(attributes[prefix+"grantee"], grantee) {
				return nil
			}
		}
		return fmt.Errorf("%s does not list %s on %s for %s", name, privilege, table, grantee)
	}
}
