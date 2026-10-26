package tables_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
)

func TestAccTablesDataSource_hive(t *testing.T) {
	acctest.PreCheck(t)
	t.Cleanup(acctest.Fixture(t,
		"DROP SCHEMA IF EXISTS hive.acc_ds_tables CASCADE",
		"CREATE SCHEMA IF NOT EXISTS hive.acc_ds_tables",
		"CREATE TABLE IF NOT EXISTS hive.acc_ds_tables.customers (id BIGINT)",
		"CREATE TABLE IF NOT EXISTS hive.acc_ds_tables.orders (id BIGINT)",
		"CREATE VIEW hive.acc_ds_tables.order_view AS SELECT id FROM hive.acc_ds_tables.orders",
	))

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: tablesConfig("hive", "acc_ds_tables", ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("data.trino_tables.test", "tables.#", "3"),
					tablesHaveTypes("data.trino_tables.test", map[string]string{
						"customers":  "BASE TABLE",
						"orders":     "BASE TABLE",
						"order_view": "VIEW",
					}),
				),
			},
			{
				Config: tablesConfig("hive", "acc_ds_tables", "\n  table_type = \"BASE TABLE\""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("data.trino_tables.test", "tables.#", "2"),
					tablesHaveTypes("data.trino_tables.test", map[string]string{
						"customers": "BASE TABLE",
						"orders":    "BASE TABLE",
					}),
				),
			},
			{
				Config: tablesConfig("hive", "acc_ds_tables", "\n  name_prefix = \"ORDER\""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("data.trino_tables.test", "tables.#", "2"),
					tablesHaveTypes("data.trino_tables.test", map[string]string{
						"orders":     "BASE TABLE",
						"order_view": "VIEW",
					}),
				),
			},
		},
	})
}

func TestAccTablesDataSource_memory(t *testing.T) {
	acctest.PreCheck(t)
	t.Cleanup(acctest.Fixture(t,
		"DROP SCHEMA IF EXISTS memory.acc_ds_tables CASCADE",
		"DROP SCHEMA IF EXISTS memory.acc_ds_tables_empty CASCADE",
		"CREATE SCHEMA IF NOT EXISTS memory.acc_ds_tables",
		"CREATE TABLE IF NOT EXISTS memory.acc_ds_tables.acc_ds_one (id BIGINT)",
		"CREATE TABLE IF NOT EXISTS memory.acc_ds_tables.acc_ds_two (id BIGINT)",
		"CREATE SCHEMA IF NOT EXISTS memory.acc_ds_tables_empty",
	))

	const address = "data.trino_tables.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: tablesConfig("memory", "acc_ds_tables", ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(address, "tables.#", "2"),
					tablesHaveTypes(address, map[string]string{
						"acc_ds_one": "BASE TABLE",
						"acc_ds_two": "BASE TABLE",
					}),
				),
			},
			{
				Config: tablesConfig("memory", "acc_ds_tables_empty", ""),
				Check:  resource.TestCheckResourceAttr(address, "tables.#", "0"),
			},
			{
				Config:      tablesConfig("memory", "acc_ds_tables_missing", ""),
				ExpectError: regexp.MustCompile("Schema not found"),
			},
		},
	})
}

func tablesConfig(catalog, schema, filters string) string {
	return fmt.Sprintf(`
provider "trino" {
  user  = "admin"
  roles = { hive = "admin" }
}

data "trino_tables" "test" {
  catalog = %q
  schema  = %q%s
}
`, catalog, schema, filters)
}

// tablesHaveTypes asserts the data source returned exactly the named tables
// with the given information_schema table type.
func tablesHaveTypes(name string, want map[string]string) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		instance, ok := state.RootModule().Resources[name]
		if !ok || instance.Primary == nil {
			return fmt.Errorf("%s not found in state", name)
		}
		attributes := instance.Primary.Attributes
		got := map[string]string{}
		for key, value := range attributes {
			var index int
			if _, err := fmt.Sscanf(key, "tables.%d.name", &index); err == nil {
				got[value] = attributes[fmt.Sprintf("tables.%d.table_type", index)]
			}
		}
		if len(got) != len(want) {
			return fmt.Errorf("%s returned %v, want %v", name, got, want)
		}
		for table, tableType := range want {
			if got[table] != tableType {
				return fmt.Errorf("%s returned table %q with type %q, want %q", name, table, got[table], tableType)
			}
		}
		return nil
	}
}
