package function_test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

func TestAccFunctionResource_hive(t *testing.T) {
	acctest.PreCheck(t)
	t.Cleanup(acctest.Fixture(t,
		"DROP SCHEMA IF EXISTS hive.acc_fn_sales CASCADE",
		"CREATE SCHEMA IF NOT EXISTS hive.acc_fn_sales",
	))

	runFunctionLifecycle(t, "hive", "acc_fn_sales", "acc_add")
}

func TestAccFunctionResource_memory(t *testing.T) {
	acctest.PreCheck(t)
	t.Cleanup(acctest.Fixture(t,
		"DROP SCHEMA IF EXISTS memory.acc_fn_sales CASCADE",
		"CREATE SCHEMA IF NOT EXISTS memory.acc_fn_sales",
	))

	runFunctionLifecycle(t, "memory", "acc_fn_sales", "acc_add")
}

func runFunctionLifecycle(t *testing.T, catalog, schema, name string) {
	t.Helper()

	const address = "trino_function.test"
	firstDefinition := "(x bigint) RETURNS bigint RETURN x"
	secondDefinition := "(x bigint) RETURNS bigint RETURN (x * 2)"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		CheckDestroy:             checkFunctionDestroy(t, catalog, schema, name),
		Steps: []resource.TestStep{
			{
				Config: functionConfig(catalog, schema, name, firstDefinition),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "id", catalog+"."+schema+"."+name),
					resource.TestCheckResourceAttr(address, "definition", firstDefinition),
				),
			},
			{
				Config: functionConfig(catalog, schema, name, secondDefinition),
				Check:  resource.TestCheckResourceAttr(address, "definition", secondDefinition),
			},
			{
				// SHOW CREATE FUNCTION returns a normalised definition, so the
				// imported text differs from the configured spelling even when
				// the routines are equivalent. The provider's plan modifier
				// (covered by unit tests) prevents a cosmetic replace.
				ResourceName:            address,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"definition"},
			},
			{
				// An out-of-band definition change must surface as a plan.
				PreConfig:          replaceFunctionOutOfBand(t, catalog, schema, name, "(x bigint) RETURNS bigint RETURN (x + 42)"),
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
			},
			{
				// Applying the configuration restores the managed definition.
				Config: functionConfig(catalog, schema, name, secondDefinition),
				Check:  resource.TestCheckResourceAttr(address, "definition", secondDefinition),
			},
			{
				// An out-of-band drop must surface as a plan and be recreated.
				PreConfig:          dropFunctionOutOfBand(t, catalog, schema, name),
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: functionConfig(catalog, schema, name, secondDefinition),
				Check:  resource.TestCheckResourceAttr(address, "definition", secondDefinition),
			},
		},
	})
}

func TestAccFunctionResource_unsupportedConnector(t *testing.T) {
	acctest.PreCheck(t)

	const catalog = "acc_fn_tpch"
	executor := acctest.Executor(t)
	if err := executor.Exec(context.Background(), "CREATE CATALOG "+trino.QuoteIdentifier(catalog)+" USING tpch"); err != nil {
		t.Fatalf("create the tpch catalog: %v", err)
	}
	t.Cleanup(func() {
		// DROP CATALOG has no IF EXISTS, so ignore a missing catalog.
		_ = executor.Exec(context.Background(), "DROP CATALOG "+trino.QuoteIdentifier(catalog))
	})

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "trino" {
  user  = "admin"
  roles = { hive = "admin" }
}

resource "trino_function" "test" {
  catalog    = %q
  schema     = "tiny"
  name       = "acc_add"
  definition = "(x bigint) RETURNS bigint RETURN x"
}
`, catalog),
			ExpectError: regexp.MustCompile("Catalog UDF storage is not supported"),
		}},
	})
}

func functionConfig(catalog, schema, name, definition string) string {
	return fmt.Sprintf(`
provider "trino" {
  user  = "admin"
  roles = { hive = "admin" }
}

resource "trino_function" "test" {
  catalog    = %q
  schema     = %q
  name       = %q
  definition = %q
}
`, catalog, schema, name, definition)
}

func replaceFunctionOutOfBand(t *testing.T, catalog, schema, name, definition string) func() {
	t.Helper()
	return func() {
		t.Helper()
		statement := "CREATE OR REPLACE FUNCTION " + trino.QuoteQualified(catalog, schema, name) + " " + definition
		if err := acctest.Executor(t).Exec(context.Background(), statement); err != nil {
			t.Fatalf("out-of-band replace: %v", err)
		}
	}
}

func dropFunctionOutOfBand(t *testing.T, catalog, schema, name string) func() {
	t.Helper()
	return func() {
		t.Helper()
		statement := "DROP FUNCTION IF EXISTS " + trino.QuoteQualified(catalog, schema, name) + "(bigint)"
		if err := acctest.Executor(t).Exec(context.Background(), statement); err != nil {
			t.Fatalf("out-of-band drop: %v", err)
		}
	}
}

func checkFunctionDestroy(t *testing.T, catalog, schema, name string) resource.TestCheckFunc {
	t.Helper()
	return func(_ *terraform.State) error {
		rows, err := acctest.Executor(t).Query(context.Background(),
			"SHOW FUNCTIONS FROM "+trino.QuoteQualified(catalog, schema))
		if err != nil {
			return err
		}
		for _, row := range rows {
			functionName, ok := row.String("function")
			if ok && strings.EqualFold(functionName, name) {
				return fmt.Errorf("function %s still present after destroy", trino.QuoteQualified(catalog, schema, name))
			}
		}
		return nil
	}
}
