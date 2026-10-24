package roles_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
)

var roleNameKey = regexp.MustCompile(`^roles\.\d+$`)

func TestAccRolesDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: `provider "trino" {
  user  = "admin"
  roles = { hive = "admin" }
}

data "trino_roles" "test" {
  catalog = "hive"
}`,
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.trino_roles.test", "roles.#"),
				rolesContain("data.trino_roles.test", "admin"),
			),
		}},
	})
}

func rolesContain(name string, want ...string) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		instance, ok := state.RootModule().Resources[name]
		if !ok || instance.Primary == nil {
			return fmt.Errorf("%s not found in state", name)
		}
		attributes := instance.Primary.Attributes
		names := make(map[string]bool)
		for key, value := range attributes {
			if roleNameKey.MatchString(key) {
				names[value] = true
			}
		}
		for _, role := range want {
			if !names[role] {
				return fmt.Errorf("%s does not contain role %q", name, role)
			}
		}
		return nil
	}
}
