package tableprivileges_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

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
			Config: `data "trino_table_privileges" "test" {
  catalog = "hive"
  schema  = "acc_ds_sales"
  table   = "acc_ds_orders"
}`,
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("data.trino_table_privileges.test", "privileges.#", "0"),
			),
		}},
	})
}
