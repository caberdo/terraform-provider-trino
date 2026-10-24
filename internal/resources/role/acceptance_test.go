package role_test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
)

const roleProviderConfig = `
provider "trino" {
  user  = "admin"
  roles = { hive = "admin" }
}
`

func roleConfig(name string) string {
	return roleProviderConfig + fmt.Sprintf(`
resource "trino_role" "test" {
  name    = %q
  catalog = "hive"
}
`, name)
}

func duplicateRoleConfig(name string) string {
	return roleProviderConfig + fmt.Sprintf(`
resource "trino_role" "first" {
  name    = %q
  catalog = "hive"
}

resource "trino_role" "second" {
  name       = %q
  catalog    = "hive"
  depends_on = [trino_role.first]
}
`, name, name)
}

func checkRoleDestroyed(t *testing.T, name string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		executor := acctest.Executor(t)
		rows, err := executor.Query(context.Background(), `SHOW ROLES FROM "hive"`)
		if err != nil {
			return fmt.Errorf("list roles during destroy check: %w", err)
		}
		for _, row := range rows {
			candidate, ok := row.SoleString()
			if ok && strings.EqualFold(candidate, name) {
				return fmt.Errorf("role %q still exists after destroy", name)
			}
		}
		return nil
	}
}

func TestAccRoleResource_lifecycle(t *testing.T) {
	const name = "acc_role_lifecycle"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		CheckDestroy:             checkRoleDestroyed(t, name),
		Steps: []resource.TestStep{
			{
				Config: roleConfig(name),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("trino_role.test", "id", "hive."+name),
					resource.TestCheckResourceAttr("trino_role.test", "name", name),
					resource.TestCheckResourceAttr("trino_role.test", "catalog", "hive"),
				),
			},
			{
				ResourceName:      "trino_role.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccRoleResource_duplicateCreateFails(t *testing.T) {
	const name = "acc_role_duplicate"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		CheckDestroy:             checkRoleDestroyed(t, name),
		Steps: []resource.TestStep{
			{
				Config:      duplicateRoleConfig(name),
				ExpectError: regexp.MustCompile(`(?i)already exists|ROLE_ALREADY_EXISTS`),
			},
		},
	})
}

func TestAccRoleResource_uppercaseName(t *testing.T) {
	const name = "acc_role_Analyst"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		CheckDestroy:             checkRoleDestroyed(t, name),
		Steps: []resource.TestStep{
			{
				Config: roleConfig(name),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("trino_role.test", "id", "hive."+name),
					resource.TestCheckResourceAttr("trino_role.test", "name", name),
				),
			},
			{
				// Hive lower-cases role names on read. Replanning the same
				// configuration must stay empty (C16).
				Config:   roleConfig(name),
				PlanOnly: true,
			},
		},
	})
}
