package materializedview_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

// TestAccMaterializedViewResource_iceberg covers the lifecycle against the
// connector that can host materialized views. The compose environment's
// `iceberg` catalog uses a Hive metastore because the Iceberg REST catalog
// rejects CREATE MATERIALIZED VIEW.
//
// The steps also pin the read contract: a refresh after a source change leaves
// the stored data and the freshness reported by Trino untouched, and only an
// explicit refresh_token change runs REFRESH MATERIALIZED VIEW.
func TestAccMaterializedViewResource_iceberg(t *testing.T) {
	acctest.PreCheck(t)

	catalog := icebergCatalog()
	const schema = "acc_mv"
	const source = "events"
	const name = "events_by_region"
	const address = "trino_materialized_view.test"

	sourceTable := trino.QuoteQualified(catalog, schema, source)
	viewName := trino.QuoteQualified(catalog, schema, name)

	acctest.Fixture(t,
		"DROP SCHEMA IF EXISTS "+trino.QuoteQualified(catalog, schema)+" CASCADE",
		"CREATE SCHEMA IF NOT EXISTS "+trino.QuoteQualified(catalog, schema),
		"CREATE TABLE IF NOT EXISTS "+sourceTable+" (id bigint, region varchar)",
		"INSERT INTO "+sourceTable+" VALUES (1, 'eu'), (2, 'us')",
	)

	query := "SELECT region, count(*) AS events FROM " + trino.QuoteQualified(catalog, schema, source) + " GROUP BY region"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		CheckDestroy:             checkMaterializedViewDestroy(t, catalog, schema, name),
		Steps: []resource.TestStep{
			{
				Config: materializedViewConfig(catalog, schema, name, query, `  grace_period = "1h"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "id", catalog+"."+schema+"."+name),
					resource.TestCheckResourceAttr(address, "query", query),
					resource.TestCheckResourceAttr(address, "grace_period", "1h"),
					resource.TestCheckResourceAttr(address, "refresh_on_create", "true"),
					checkViewFreshness(t, catalog, schema, name, "FRESH"),
					checkViewEvents(t, viewName, 2),
				),
			},
			{
				// A source change outside Terraform leaves the view stale. The
				// refresh Terraform runs after an apply must not rebuild it:
				// the stored aggregation still covers the two pre-change
				// events.
				PreConfig:    insertRow(t, sourceTable, 3, "eu"),
				RefreshState: true,
				Check: resource.ComposeAggregateTestCheckFunc(
					checkViewFreshness(t, catalog, schema, name, "STALE"),
					checkViewEvents(t, viewName, 2),
				),
			},
			{
				// Bumping the token is the explicit refresh; freshness and the
				// aggregation both move.
				Config: materializedViewConfig(catalog, schema, name, query, `  grace_period = "1h"
  refresh_token = "1"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkViewFreshness(t, catalog, schema, name, "FRESH"),
					checkViewEvents(t, viewName, 3),
				),
			},
			{
				// An out-of-band drop must surface as a plan and be recreated.
				PreConfig:          dropViewOutOfBand(t, viewName),
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: materializedViewConfig(catalog, schema, name, query, `  grace_period = "1h"
  refresh_token = "1"`),
				Check: checkViewEvents(t, viewName, 3),
			},
			{
				// Trino stores a normalised query and derived storage
				// properties, so import recovers the identity and the
				// normalised query but not the configured spellings.
				ResourceName:      address,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     catalog + "." + schema + "." + name,
				ImportStateVerifyIgnore: []string{
					"query", "properties", "grace_period", "refresh_on_create", "refresh_token",
				},
			},
		},
	})
}

func TestAccMaterializedViewResource_unsupportedConnector(t *testing.T) {
	acctest.PreCheck(t)

	const catalog = "acc_mv_tpch"
	executor := acctest.Executor(t)
	if err := executor.Exec(context.Background(), "CREATE CATALOG "+trino.QuoteIdentifier(catalog)+" USING tpch"); err != nil {
		t.Fatalf("create the tpch catalog: %v", err)
	}
	t.Cleanup(func() {
		// DROP CATALOG has no IF EXISTS, so ignore a missing catalog.
		_ = executor.Exec(context.Background(), "DROP CATALOG "+trino.QuoteIdentifier(catalog))
	})

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			// No properties: Trino validates property names against the
			// connector before createMaterializedView, so a property would
			// mask the unsupported-connector diagnostic.
			Config: fmt.Sprintf(`
provider "trino" {
  user  = "admin"
  roles = { hive = "admin" }
}

resource "trino_materialized_view" "test" {
  catalog = %q
  schema  = "tiny"
  name    = "acc_mv_orders"
  query   = %q
}
`, catalog, "SELECT orderkey FROM "+catalog+".tiny.orders"),
			ExpectError: regexp.MustCompile("Materialized views are not supported"),
		}},
	})
}

func icebergCatalog() string {
	if catalog := strings.TrimSpace(os.Getenv("TRINO_ICEBERG_CATALOG")); catalog != "" {
		return catalog
	}
	return "iceberg"
}

func materializedViewConfig(catalog, schema, name, query, extra string) string {
	return fmt.Sprintf(`
provider "trino" {
  user  = "admin"
  roles = { hive = "admin" }
}

resource "trino_materialized_view" "test" {
  catalog = %q
  schema  = %q
  name    = %q
  query   = %q

  properties = {
    format = "'ORC'"
  }
%s
}
`, catalog, schema, name, query, extra)
}

func insertRow(t *testing.T, table string, id int, region string) func() {
	t.Helper()
	return func() {
		t.Helper()
		if err := acctest.Executor(t).Exec(context.Background(), fmt.Sprintf("INSERT INTO %s VALUES (%d, '%s')", table, id, region)); err != nil {
			t.Fatalf("insert into %s: %v", table, err)
		}
	}
}

func dropViewOutOfBand(t *testing.T, viewName string) func() {
	t.Helper()
	return func() {
		t.Helper()
		if err := acctest.Executor(t).Exec(context.Background(), "DROP MATERIALIZED VIEW IF EXISTS "+viewName); err != nil {
			t.Fatalf("drop %s: %v", viewName, err)
		}
	}
}

// checkViewEvents asserts the sum of the aggregated counts, which proves
// whether the stored data has been refreshed. Counting rows in the view would
// count regions, not events.
func checkViewEvents(t *testing.T, viewName string, want int64) resource.TestCheckFunc {
	t.Helper()
	return func(_ *terraform.State) error {
		row, ok, err := acctest.Executor(t).QueryOne(context.Background(), "SELECT coalesce(sum(events), 0) FROM "+viewName)
		if err != nil {
			return fmt.Errorf("sum events in %s: %w", viewName, err)
		}
		if !ok {
			return fmt.Errorf("%s returned no sum row", viewName)
		}
		got, ok := row.Int64("_col0")
		if !ok {
			got, ok = row.Int64("sum")
		}
		if !ok {
			return fmt.Errorf("%s returned an unreadable sum row: %v", viewName, row)
		}
		if got != want {
			return fmt.Errorf("%s sums to %d events, want %d", viewName, got, want)
		}
		return nil
	}
}

func checkViewFreshness(t *testing.T, catalog, schema, name, want string) resource.TestCheckFunc {
	t.Helper()
	return func(_ *terraform.State) error {
		return requireFreshness(t, catalog, schema, name, want)
	}
}

func requireFreshness(t *testing.T, catalog, schema, name, want string) error {
	t.Helper()
	row, ok, err := acctest.Executor(t).QueryOne(context.Background(),
		`SELECT freshness FROM system.metadata.materialized_views
		 WHERE catalog_name = ? AND schema_name = ? AND name = ?`,
		catalog, schema, name)
	if err != nil {
		return fmt.Errorf("read freshness for %s.%s.%s: %w", catalog, schema, name, err)
	}
	if !ok {
		return fmt.Errorf("materialized view %s.%s.%s is missing from system.metadata.materialized_views", catalog, schema, name)
	}
	got, ok := row.String("freshness")
	if !ok {
		return fmt.Errorf("materialized view %s.%s.%s returned no freshness column", catalog, schema, name)
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("freshness of %s.%s.%s = %q, want %q", catalog, schema, name, got, want)
	}
	return nil
}

func checkMaterializedViewDestroy(t *testing.T, catalog, schema, name string) resource.TestCheckFunc {
	t.Helper()
	return func(_ *terraform.State) error {
		row, ok, err := acctest.Executor(t).QueryOne(context.Background(),
			`SELECT count(*) FROM system.metadata.materialized_views
			 WHERE catalog_name = ? AND schema_name = ? AND name = ?`,
			catalog, schema, name)
		if err != nil {
			return fmt.Errorf("count materialized views: %w", err)
		}
		if !ok {
			return fmt.Errorf("system.metadata.materialized_views returned no count row")
		}
		count, ok := row.Int64("_col0")
		if !ok {
			count, ok = row.Int64("count")
		}
		if !ok {
			return fmt.Errorf("unreadable count row: %v", row)
		}
		if count != 0 {
			return fmt.Errorf("materialized view %s.%s.%s still present after destroy", catalog, schema, name)
		}
		return nil
	}
}
