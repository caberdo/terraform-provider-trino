package catalogs_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
)

var catalogNameKey = regexp.MustCompile(`^catalogs\.\d+\.name$`)

func TestAccCatalogsDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: `data "trino_catalogs" "test" {}`,
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.trino_catalogs.test", "catalogs.#"),
				catalogsContain("data.trino_catalogs.test", "memory", "hive"),
			),
		}},
	})
}

func catalogsContain(name string, want ...string) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		instance, ok := state.RootModule().Resources[name]
		if !ok || instance.Primary == nil {
			return fmt.Errorf("%s not found in state", name)
		}
		attributes := instance.Primary.Attributes
		names := make(map[string]bool)
		for key, value := range attributes {
			if catalogNameKey.MatchString(key) {
				names[value] = true
			}
		}
		for _, catalog := range want {
			if !names[catalog] {
				return fmt.Errorf("%s does not contain catalog %q", name, catalog)
			}
		}
		return nil
	}
}
