package grantrole_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

const (
	acceptanceRole    = "acc_grantrole_lifecycle"
	acceptanceCatalog = "hive"
	acceptanceGrantee = "admin"
)

func grantRoleConfig(admin bool) string {
	return fmt.Sprintf(`
provider "trino" {
  user  = "admin"
  roles = { hive = "admin" }
}

resource "trino_grant_role" "test" {
  role         = %q
  role_catalog = %q
  grantee      = %q
  grantee_type = "USER"
  admin_option = %t
}
`, acceptanceRole, acceptanceCatalog, acceptanceGrantee, admin)
}

func TestAccGrantRoleResource_lifecycle(t *testing.T) {
	acctest.PreCheck(t)

	ctx := context.Background()
	executor := acctest.Executor(t)
	createRole := fmt.Sprintf("CREATE ROLE %s IN %s", trino.QuoteIdentifier(acceptanceRole), trino.QuoteIdentifier(acceptanceCatalog))
	dropRole := fmt.Sprintf("DROP ROLE IF EXISTS %s IN %s", trino.QuoteIdentifier(acceptanceRole), trino.QuoteIdentifier(acceptanceCatalog))

	_ = executor.Exec(ctx, dropRole)
	if err := executor.Exec(ctx, createRole); err != nil {
		t.Fatalf("create role fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := executor.Exec(context.Background(), dropRole); err != nil {
			t.Logf("drop role fixture: %v", err)
		}
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: grantRoleConfig(false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("trino_grant_role.test", "role", acceptanceRole),
					resource.TestCheckResourceAttr("trino_grant_role.test", "role_catalog", acceptanceCatalog),
					resource.TestCheckResourceAttr("trino_grant_role.test", "grantee", acceptanceGrantee),
					resource.TestCheckResourceAttr("trino_grant_role.test", "grantee_type", "USER"),
					resource.TestCheckResourceAttr("trino_grant_role.test", "admin_option", "false"),
					resource.TestCheckResourceAttr("trino_grant_role.test", "id", "hive.acc_grantrole_lifecycle:USER:admin"),
				),
			},
			{
				Config: grantRoleConfig(true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("trino_grant_role.test", "admin_option", "true"),
				),
			},
			{
				Config: grantRoleConfig(false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("trino_grant_role.test", "admin_option", "false"),
				),
			},
			{
				ResourceName:      "trino_grant_role.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(state *terraform.State) (string, error) {
					instance, ok := state.RootModule().Resources["trino_grant_role.test"]
					if !ok {
						return "", fmt.Errorf("trino_grant_role.test not found in state")
					}
					return instance.Primary.ID, nil
				},
			},
		},
	})
}
