package grantprivileges_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
)

const (
	accCatalog = "hive"
	accSchema  = "acc_priv_sales"
	accTable   = "acc_priv_orders"
	accGrantee = "acc_priv_alice"
)

func TestAccGrantPrivilegesResource_lifecycle(t *testing.T) {
	acctest.PreCheck(t)
	t.Cleanup(acctest.Fixture(t,
		`CREATE SCHEMA "hive"."acc_priv_sales"`,
		`CREATE TABLE "hive"."acc_priv_sales"."acc_priv_orders" (id bigint)`,
	))

	const address = "trino_grant_privileges.test"

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			acctest.PreCheck(t)
		},
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		CheckDestroy: func(state *terraform.State) error {
			return checkGrantPrivilegesDestroy(t, state)
		},
		Steps: []resource.TestStep{
			{
				Config: testAccGrantPrivilegesConfig(`["SELECT"]`, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "id", "hive.acc_priv_sales.acc_priv_orders:USER:acc_priv_alice"),
					resource.TestCheckResourceAttr(address, "privileges.#", "1"),
					resource.TestCheckTypeSetElemAttr(address, "privileges.*", "SELECT"),
					resource.TestCheckResourceAttr(address, "grant_option", "false"),
				),
			},
			{
				Config: testAccGrantPrivilegesConfig(`["SELECT", "INSERT"]`, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "privileges.#", "2"),
					resource.TestCheckTypeSetElemAttr(address, "privileges.*", "SELECT"),
					resource.TestCheckTypeSetElemAttr(address, "privileges.*", "INSERT"),
					resource.TestCheckResourceAttr(address, "grant_option", "false"),
				),
			},
			{
				Config: testAccGrantPrivilegesConfig(`["SELECT", "INSERT"]`, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "grant_option", "true"),
					resource.TestCheckResourceAttr(address, "privileges.#", "2"),
				),
			},
			{
				ResourceName:      address,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func testAccGrantPrivilegesConfig(privileges string, grantOption bool) string {
	endpoint := os.Getenv("TRINO_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:8080"
	}
	return fmt.Sprintf(`
provider "trino" {
  endpoint = %q
  user     = "admin"
  auth     = "none"
  roles    = { hive = "admin" }
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
  grant_option = %t
}
`, endpoint, privileges, accCatalog, accSchema, accTable, accGrantee, grantOption)
}

func checkGrantPrivilegesDestroy(t *testing.T, _ *terraform.State) error {
	t.Helper()
	executor := acctest.Executor(t)
	rows, err := executor.Query(context.Background(),
		`SELECT privilege_type FROM information_schema.table_privileges WHERE table_catalog = ? AND table_schema = ? AND table_name = ? AND grantee_type = ? AND lower(grantee) = lower(?)`,
		accCatalog, accSchema, accTable, "USER", accGrantee,
	)
	if err != nil {
		return err
	}
	if len(rows) != 0 {
		return fmt.Errorf("grant privileges for %q still present after destroy: %d rows", accGrantee, len(rows))
	}
	return nil
}
