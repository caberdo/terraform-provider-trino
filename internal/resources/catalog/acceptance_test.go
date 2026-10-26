package catalog_test

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
)

func TestAccCatalogResource_basic(t *testing.T) {
	name := fmt.Sprintf("acc_catalog_%d", time.Now().UnixNano())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		CheckDestroy:             testAccCatalogCheckDestroy(t, name),
		Steps: []resource.TestStep{
			{
				Config: testAccCatalogConfig(name, "memory"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("trino_catalog.test", "id", name),
					resource.TestCheckResourceAttr("trino_catalog.test", "name", name),
					resource.TestCheckResourceAttr("trino_catalog.test", "connector", "memory"),
				),
			},
			{
				// Layer: real coordinator. Import must report why it is
				// refused, not silently adopt (or recreate) the live catalog.
				ResourceName:      "trino_catalog.test",
				ImportState:       true,
				ImportStateVerify: false,
				ExpectError:       regexp.MustCompile(`(?is)import\s+is\s+not\s+supported\s+for\s+trino_catalog.*drop\s+and\s+recreate\s+the\s+live\s+catalog`),
			},
		},
	})
}

func TestAccCatalogResource_connectorDrift(t *testing.T) {
	name := fmt.Sprintf("acc_catalog_%d", time.Now().UnixNano())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		CheckDestroy:             testAccCatalogCheckDestroy(t, name),
		Steps: []resource.TestStep{
			{
				Config: testAccCatalogConfig(name, "memory"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("trino_catalog.test", "id", name),
					resource.TestCheckResourceAttr("trino_catalog.test", "connector", "memory"),
				),
			},
			{
				Config: testAccCatalogConfig(name, "tpch"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("trino_catalog.test", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("trino_catalog.test", "id", name),
					resource.TestCheckResourceAttr("trino_catalog.test", "connector", "tpch"),
				),
			},
		},
	})
}

func testAccCatalogConfig(name, connector string) string {
	return fmt.Sprintf(`
resource "trino_catalog" "test" {
  name      = %q
  connector = %q
}
`, name, connector)
}

func testAccCatalogCheckDestroy(t *testing.T, name string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		executor := acctest.Executor(t)
		rows, err := executor.Query(context.Background(), "SHOW CATALOGS")
		if err != nil {
			return fmt.Errorf("SHOW CATALOGS: %w", err)
		}
		for _, row := range rows {
			catalog, ok := row.SoleString()
			if ok && catalog == name {
				return fmt.Errorf("catalog %q still exists after destroy", name)
			}
		}
		return nil
	}
}
