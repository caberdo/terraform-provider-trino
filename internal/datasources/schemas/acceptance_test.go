package schemas_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
)

var schemaNameKey = regexp.MustCompile(`^schemas\.\d+$`)

func TestAccSchemasDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: `data "trino_schemas" "test" {
  catalog = "memory"
}`,
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.trino_schemas.test", "schemas.#"),
				schemasContain("data.trino_schemas.test", "information_schema"),
			),
		}},
	})
}

func schemasContain(name string, want ...string) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		instance, ok := state.RootModule().Resources[name]
		if !ok || instance.Primary == nil {
			return fmt.Errorf("%s not found in state", name)
		}
		attributes := instance.Primary.Attributes
		names := make(map[string]bool)
		for key, value := range attributes {
			if schemaNameKey.MatchString(key) {
				names[value] = true
			}
		}
		for _, schema := range want {
			if !names[schema] {
				return fmt.Errorf("%s does not contain schema %q", name, schema)
			}
		}
		return nil
	}
}
