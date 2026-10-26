package acctest_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-trino/internal/acctest"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

// TestAccIcebergMaterializedViews proves the acceptance environment can host
// the Iceberg materialized-view behaviour that trino_materialized_view (#49)
// depends on, on the floor and the newest supported release alike: create,
// refresh, definition read-back and freshness without an implicit refresh.
//
// Trino only supports materialized views on Iceberg catalogs backed by a Hive
// metastore; the REST catalog rejects CREATE MATERIALIZED VIEW, which is why
// docker-compose.yml runs a metastore rather than a REST fixture.
func TestAccIcebergMaterializedViews(t *testing.T) {
	acctest.PreCheck(t)

	catalog := strings.TrimSpace(os.Getenv("TRINO_ICEBERG_CATALOG"))
	if catalog == "" {
		catalog = "iceberg"
	}
	const (
		schema = "acc_iceberg_mv"
		table  = "base"
		view   = "mv"
	)
	schemaName := trino.QuoteIdentifier(catalog) + "." + trino.QuoteIdentifier(schema)
	tableName := schemaName + "." + trino.QuoteIdentifier(table)
	viewName := schemaName + "." + trino.QuoteIdentifier(view)

	ctx := context.Background()
	executor := acctest.Executor(t)
	t.Cleanup(func() {
		for _, statement := range []string{
			"DROP MATERIALIZED VIEW IF EXISTS " + viewName,
			"DROP SCHEMA IF EXISTS " + schemaName + " CASCADE",
		} {
			if err := executor.Exec(ctx, statement); err != nil {
				t.Logf("cleanup %q failed: %v", statement, err)
			}
		}
	})

	for _, statement := range []string{
		"DROP SCHEMA IF EXISTS " + schemaName + " CASCADE",
		"CREATE SCHEMA " + schemaName + " WITH (location = " +
			trino.QuoteLiteral("file:///warehouse/"+schema) + ")",
		"CREATE TABLE " + tableName + " (id BIGINT, name VARCHAR)",
		"INSERT INTO " + tableName + " VALUES (1, 'alpha'), (2, 'alpha'), (3, 'beta')",
		"CREATE MATERIALIZED VIEW " + viewName +
			" AS SELECT name, count(*) AS n FROM " + tableName + " GROUP BY name",
		"REFRESH MATERIALIZED VIEW " + viewName,
	} {
		if err := executor.Exec(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	assertAggregate(t, executor, viewName, 2)
	assertFreshness(t, executor, catalog, schema, view, "FRESH")

	definition, ok, err := executor.QueryOne(ctx, "SHOW CREATE MATERIALIZED VIEW "+viewName)
	if err != nil {
		t.Fatalf("SHOW CREATE MATERIALIZED VIEW: %v", err)
	}
	if !ok {
		t.Fatal("SHOW CREATE MATERIALIZED VIEW returned no row")
	}
	create, ok := definition.SoleString()
	if !ok {
		t.Fatalf("SHOW CREATE MATERIALIZED VIEW row is not a single string: %v", definition)
	}
	if !strings.Contains(create, table) {
		t.Fatalf("SHOW CREATE MATERIALIZED VIEW does not name the source table %q:\n%s", table, create)
	}

	// A read must not refresh the view: after the source changes, freshness is
	// STALE and the materialized data is still the pre-change aggregation.
	if err := executor.Exec(ctx, "INSERT INTO "+tableName+" VALUES (4, 'alpha')"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	assertFreshness(t, executor, catalog, schema, view, "STALE")
	assertAggregate(t, executor, viewName, 2)
}

func assertAggregate(t *testing.T, executor trino.Executor, viewName string, want int64) {
	t.Helper()
	row, ok, err := executor.QueryOne(context.Background(), "SELECT n FROM "+viewName+" WHERE name = 'alpha'")
	if err != nil {
		t.Fatalf("SELECT from materialized view: %v", err)
	}
	if !ok {
		t.Fatal("SELECT from materialized view returned no row")
	}
	got, ok := row.Int64("n")
	if !ok {
		t.Fatalf("count column is not an integer: %v", row)
	}
	if got != want {
		t.Fatalf("count = %d, want %d", got, want)
	}
}

func assertFreshness(t *testing.T, executor trino.Executor, catalog, schema, view, want string) {
	t.Helper()
	row, ok, err := executor.QueryOne(context.Background(),
		`SELECT storage_schema, freshness FROM system.metadata.materialized_views
		 WHERE catalog_name = ? AND schema_name = ? AND name = ?`,
		catalog, schema, view)
	if err != nil {
		t.Fatalf("system.metadata.materialized_views: %v", err)
	}
	if !ok {
		t.Fatalf("no metadata row for %s.%s.%s", catalog, schema, view)
	}
	freshness, ok := row.String("freshness")
	if !ok {
		t.Fatalf("freshness is not a string: %v", row)
	}
	if freshness != want {
		t.Fatalf("freshness = %q, want %q", freshness, want)
	}
	storageSchema, ok := row.String("storage_schema")
	if !ok {
		t.Fatalf("storage_schema is not a string: %v", row)
	}
	if storageSchema != schema {
		t.Fatalf("storage_schema = %q, want %q", storageSchema, schema)
	}
}
