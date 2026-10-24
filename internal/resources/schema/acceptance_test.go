package schema_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

const testAccSchemaHiveProvider = `
provider "trino" {
  user  = "admin"
  roles = { hive = "admin" }
}
`

func testAccSchemaConfig(catalog, name, attributes string) string {
	return fmt.Sprintf(`
resource "trino_schema" "test" {
  catalog = %q
  name    = %q
%s
}
`, catalog, name, attributes)
}

func testAccCheckSchemaDestroy(t *testing.T, catalog, name string) resource.TestCheckFunc {
	t.Helper()
	return func(_ *terraform.State) error {
		executor := acctest.Executor(t)
		rows, err := executor.Query(context.Background(), "SHOW SCHEMAS FROM "+trino.QuoteIdentifier(catalog))
		if err != nil {
			return fmt.Errorf("list schemas in %s: %w", catalog, err)
		}
		for _, row := range rows {
			value, ok := row.SoleString()
			if ok && strings.EqualFold(value, name) {
				return fmt.Errorf("schema %s.%s still exists after destroy", catalog, name)
			}
		}
		return nil
	}
}

func TestAccSchemaResource_memoryLifecycle(t *testing.T) {
	acctest.PreCheck(t)

	const catalog = "memory"
	const name = "acc_schema_memory"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		CheckDestroy:             testAccCheckSchemaDestroy(t, catalog, name),
		Steps: []resource.TestStep{
			{
				Config: testAccSchemaConfig(catalog, name, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("trino_schema.test", "id", catalog+"."+name),
					resource.TestCheckResourceAttr("trino_schema.test", "catalog", catalog),
					resource.TestCheckResourceAttr("trino_schema.test", "name", name),
				),
			},
		},
	})
}

func TestAccSchemaResource_hivePropertiesAndOwner(t *testing.T) {
	acctest.PreCheck(t)

	const catalog = "hive"
	const name = "acc_schema_owner"

	config := testAccSchemaHiveProvider + testAccSchemaConfig(catalog, name, `
  owner       = "admin"
  owner_type  = "USER"
  properties  = { location = "file:///tmp/acc_schema_owner" }
  force_destroy = true
`)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		CheckDestroy:             testAccCheckSchemaDestroy(t, catalog, name),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("trino_schema.test", "owner", "admin"),
					resource.TestCheckResourceAttr("trino_schema.test", "owner_type", "USER"),
					resource.TestCheckResourceAttr("trino_schema.test", "properties.location", "file:///tmp/acc_schema_owner"),
				),
			},
		},
	})
}

func TestAccSchemaResource_forceDestroy(t *testing.T) {
	acctest.PreCheck(t)

	const catalog = "memory"
	const name = "acc_schema_force"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		CheckDestroy:             testAccCheckSchemaDestroy(t, catalog, name),
		Steps: []resource.TestStep{
			{
				Config: testAccSchemaConfig(catalog, name, `  force_destroy = false`),
				Check:  resource.TestCheckResourceAttr("trino_schema.test", "force_destroy", "false"),
			},
			{
				Config: testAccSchemaConfig(catalog, name, `  force_destroy = true`),
				Check:  resource.TestCheckResourceAttr("trino_schema.test", "force_destroy", "true"),
			},
		},
	})
}

func TestAccSchemaResource_import(t *testing.T) {
	acctest.PreCheck(t)

	const catalog = "hive"
	const name = "acc_schema_import"

	config := testAccSchemaHiveProvider + testAccSchemaConfig(catalog, name, `  force_destroy = true`)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		CheckDestroy:             testAccCheckSchemaDestroy(t, catalog, name),
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				ResourceName:      "trino_schema.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     catalog + "." + name,
			},
		},
	})
}

func TestAccSchemaResource_uppercaseName(t *testing.T) {
	acctest.PreCheck(t)

	const catalog = "hive"
	const name = "Acc_Schema_Upper"

	config := testAccSchemaHiveProvider + testAccSchemaConfig(catalog, name, `  force_destroy = true`)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		CheckDestroy:             testAccCheckSchemaDestroy(t, catalog, name),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  resource.TestCheckResourceAttr("trino_schema.test", "id", catalog+"."+name),
			},
			{
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
		},
	})
}
