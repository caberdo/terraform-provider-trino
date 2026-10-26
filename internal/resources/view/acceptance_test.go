package view_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

// TestAccViewResource_hive covers the lifecycle on the connector with
// sql-standard security, where the provider session needs the admin role.
func TestAccViewResource_hive(t *testing.T) {
	acctest.PreCheck(t)
	t.Cleanup(acctest.Fixture(t,
		"DROP SCHEMA IF EXISTS hive.acc_view_sales CASCADE",
		"CREATE SCHEMA IF NOT EXISTS hive.acc_view_sales",
		"CREATE TABLE IF NOT EXISTS hive.acc_view_sales.orders (region varchar, amount bigint)",
		"INSERT INTO hive.acc_view_sales.orders VALUES ('eu', 1), ('us', 2)",
	))

	runViewLifecycle(t, "hive", "acc_view_sales", "acc_orders", false)
}

// TestAccViewResource_memory covers the lifecycle plus the security attribute:
// the memory connector accepts SECURITY INVOKER explicitly, and it is the
// attribute Read actually drifts.
func TestAccViewResource_memory(t *testing.T) {
	acctest.PreCheck(t)
	t.Cleanup(acctest.Fixture(t,
		"DROP SCHEMA IF EXISTS memory.acc_view_sales CASCADE",
		"CREATE SCHEMA IF NOT EXISTS memory.acc_view_sales",
		"CREATE TABLE IF NOT EXISTS memory.acc_view_sales.orders (region varchar, amount bigint)",
		"INSERT INTO memory.acc_view_sales.orders VALUES ('eu', 1), ('us', 2)",
	))

	runViewLifecycle(t, "memory", "acc_view_sales", "acc_orders", true)
}

func runViewLifecycle(t *testing.T, catalog, schema, name string, testInvoker bool) {
	t.Helper()

	const address = "trino_view.test"

	source := trino.QuoteQualified(catalog, schema, "orders")
	view := trino.QuoteQualified(catalog, schema, name)
	firstDefinition := "SELECT region, amount FROM " + source
	secondDefinition := "SELECT region FROM " + source + " WHERE amount > 1"

	steps := []resource.TestStep{
		{
			Config: viewConfig(catalog, schema, name, firstDefinition, ""),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(address, "id", catalog+"."+schema+"."+name),
				resource.TestCheckResourceAttr(address, "definition", firstDefinition),
				resource.TestCheckResourceAttr(address, "security", "DEFINER"),
				checkViewRows(t, view, 2),
			),
		},
		{
			// A definition change is an in-place CREATE OR REPLACE VIEW.
			Config: viewConfig(catalog, schema, name, secondDefinition, ""),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(address, "definition", secondDefinition),
				checkViewRows(t, view, 1),
			),
		},
	}

	lastConfig := viewConfig(catalog, schema, name, secondDefinition, "")
	if testInvoker {
		invokerConfig := viewConfig(catalog, schema, name, secondDefinition, `  security = "INVOKER"`)
		steps = append(steps,
			resource.TestStep{
				Config: invokerConfig,
				Check:  resource.TestCheckResourceAttr(address, "security", "INVOKER"),
			},
			resource.TestStep{
				// Security is the one readable attribute, so an out-of-band
				// change must surface as a plan and be restored.
				PreConfig:          replaceViewOutOfBand(t, catalog, schema, name, "DEFINER", secondDefinition),
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
			},
			resource.TestStep{
				Config: invokerConfig,
				Check:  resource.TestCheckResourceAttr(address, "security", "INVOKER"),
			},
		)
		lastConfig = invokerConfig
	}

	steps = append(steps,
		resource.TestStep{
			// An out-of-band drop must surface as a plan and be recreated.
			PreConfig:          dropViewOutOfBand(t, view),
			RefreshState:       true,
			ExpectNonEmptyPlan: true,
		},
		resource.TestStep{
			Config: lastConfig,
			Check:  checkViewRows(t, view, 1),
		},
		resource.TestStep{
			// SHOW CREATE VIEW normalises the definition, so import verifies
			// the identity and the security mode but leaves definition for
			// configuration to supply.
			ResourceName:            address,
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateId:           catalog + "." + schema + "." + name,
			ImportStateVerifyIgnore: []string{"definition"},
		},
	)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		CheckDestroy:             checkViewDestroy(t, catalog, schema, name),
		Steps:                    steps,
	})
}

func viewConfig(catalog, schema, name, definition, extra string) string {
	return fmt.Sprintf(`
provider "trino" {
  user  = "admin"
  roles = { hive = "admin" }
}

resource "trino_view" "test" {
  catalog    = %q
  schema     = %q
  name       = %q
  definition = %q
%s
}
`, catalog, schema, name, definition, extra)
}

func checkViewRows(t *testing.T, view string, want int64) resource.TestCheckFunc {
	t.Helper()
	return func(_ *terraform.State) error {
		row, ok, err := acctest.Executor(t).QueryOne(context.Background(), "SELECT count(*) FROM "+view)
		if err != nil {
			return fmt.Errorf("count rows in %s: %w", view, err)
		}
		if !ok {
			return fmt.Errorf("%s returned no count row", view)
		}
		got, ok := row.Int64("_col0")
		if !ok {
			got, ok = row.Int64("count")
		}
		if !ok {
			return fmt.Errorf("%s returned an unreadable count row: %v", view, row)
		}
		if got != want {
			return fmt.Errorf("%s has %d rows, want %d", view, got, want)
		}
		return nil
	}
}

func replaceViewOutOfBand(t *testing.T, catalog, schema, name, security, definition string) func() {
	t.Helper()
	return func() {
		t.Helper()
		statement := "CREATE OR REPLACE VIEW " + trino.QuoteQualified(catalog, schema, name) +
			" SECURITY " + security + " AS " + definition
		if err := acctest.Executor(t).Exec(context.Background(), statement); err != nil {
			t.Fatalf("out-of-band replace: %v", err)
		}
	}
}

func dropViewOutOfBand(t *testing.T, view string) func() {
	t.Helper()
	return func() {
		t.Helper()
		if err := acctest.Executor(t).Exec(context.Background(), "DROP VIEW IF EXISTS "+view); err != nil {
			t.Fatalf("out-of-band drop: %v", err)
		}
	}
}

func checkViewDestroy(t *testing.T, catalog, schema, name string) resource.TestCheckFunc {
	t.Helper()
	return func(_ *terraform.State) error {
		row, ok, err := acctest.Executor(t).QueryOne(context.Background(),
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE table_schema = ? AND table_name = ? AND table_type = 'VIEW'`,
				trino.QuoteQualified(catalog, "information_schema", "tables")),
			schema, name)
		if err != nil {
			return fmt.Errorf("count views: %w", err)
		}
		if !ok {
			return fmt.Errorf("information_schema.tables returned no count row")
		}
		count, ok := row.Int64("_col0")
		if !ok {
			count, ok = row.Int64("count")
		}
		if !ok {
			return fmt.Errorf("unreadable count row: %v", row)
		}
		if count != 0 {
			return fmt.Errorf("view %s.%s.%s still present after destroy", catalog, schema, name)
		}
		return nil
	}
}
