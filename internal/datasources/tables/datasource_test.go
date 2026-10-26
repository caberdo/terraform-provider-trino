package tables

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

func TestTablesReadSQLAndSorts(t *testing.T) {
	fake := &trinotest.FakeExecutor{
		Steps: []trinotest.Step{
			{Rows: []trino.Row{{"schema_name": "acc_ds_sales"}}},
			{Rows: []trino.Row{
				{"table_name": "orders", "table_type": "BASE TABLE"},
				{"table_name": "customers", "table_type": "BASE TABLE"},
				{"table_name": "order_view", "table_type": "VIEW"},
			}},
		},
	}
	ds := newTestDataSource(t, fake)

	result, diags := readTables(t, ds, tablesDataSourceModel{
		Catalog: types.StringValue("hive"),
		Schema:  types.StringValue("acc_ds_sales"),
		Tables:  types.ListNull(tableElementType),
	})
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}

	if len(fake.Calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(fake.Calls))
	}
	if fake.Calls[0].Statement != `SHOW SCHEMAS FROM "hive"` {
		t.Fatalf("schema statement = %q", fake.Calls[0].Statement)
	}
	wantStatement := `SELECT table_name, table_type FROM "hive"."information_schema"."tables" WHERE table_schema = ? ORDER BY table_name`
	if fake.Calls[1].Statement != wantStatement {
		t.Fatalf("tables statement = %q, want %q", fake.Calls[1].Statement, wantStatement)
	}
	if !reflect.DeepEqual(fake.Calls[1].Args, []any{"acc_ds_sales"}) {
		t.Fatalf("tables args = %v, want [acc_ds_sales]", fake.Calls[1].Args)
	}

	var entries []tableEntry
	if diags := result.Tables.ElementsAs(context.Background(), &entries, false); diags.HasError() {
		t.Fatalf("decode tables: %v", diags)
	}
	got := make([]string, len(entries))
	for i, entry := range entries {
		got[i] = entry.Name.ValueString() + ":" + entry.TableType.ValueString()
	}
	want := []string{"customers:BASE TABLE", "order_view:VIEW", "orders:BASE TABLE"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
}

func TestTablesFilters(t *testing.T) {
	fake := &trinotest.FakeExecutor{
		Steps: []trinotest.Step{
			{Rows: []trino.Row{{"schema_name": "sales"}}},
			{Rows: []trino.Row{
				{"table_name": "orders", "table_type": "BASE TABLE"},
				{"table_name": "order_view", "table_type": "VIEW"},
				{"table_name": "customers", "table_type": "BASE TABLE"},
			}},
		},
	}
	ds := newTestDataSource(t, fake)

	result, diags := readTables(t, ds, tablesDataSourceModel{
		Catalog:    types.StringValue("hive"),
		Schema:     types.StringValue("sales"),
		NamePrefix: types.StringValue("ORDER"),
		TableType:  types.StringValue("base table"),
		Tables:     types.ListNull(tableElementType),
	})
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}

	var entries []tableEntry
	if diags := result.Tables.ElementsAs(context.Background(), &entries, false); diags.HasError() {
		t.Fatalf("decode tables: %v", diags)
	}
	if len(entries) != 1 || entries[0].Name.ValueString() != "orders" {
		t.Fatalf("tables = %+v, want only orders", entries)
	}
}

func TestTablesEmptyResultIsEmptyList(t *testing.T) {
	fake := &trinotest.FakeExecutor{
		Steps: []trinotest.Step{
			{Rows: []trino.Row{{"schema_name": "empty"}}},
			{},
		},
	}
	ds := newTestDataSource(t, fake)

	result, diags := readTables(t, ds, tablesDataSourceModel{
		Catalog: types.StringValue("memory"),
		Schema:  types.StringValue("empty"),
		Tables:  types.ListNull(tableElementType),
	})
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if result.Tables.IsNull() {
		t.Fatal("tables is null, want an empty list")
	}
	if got := len(result.Tables.Elements()); got != 0 {
		t.Fatalf("expected 0 tables, got %d", got)
	}
}

func TestTablesMissingSchema(t *testing.T) {
	fake := &trinotest.FakeExecutor{
		Steps: []trinotest.Step{
			{Rows: []trino.Row{{"schema_name": "other"}}},
		},
	}
	ds := newTestDataSource(t, fake)

	_, diags := readTables(t, ds, tablesDataSourceModel{
		Catalog: types.StringValue("hive"),
		Schema:  types.StringValue("missing"),
		Tables:  types.ListNull(tableElementType),
	})
	if !diags.HasError() {
		t.Fatal("expected an error when the schema does not exist")
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("expected only the schema check, got %d calls", len(fake.Calls))
	}
	if !strings.Contains(diags[0].Summary(), "Schema not found") {
		t.Fatalf("diagnostic summary = %q, want Schema not found", diags[0].Summary())
	}
}

func TestTablesMissingCatalog(t *testing.T) {
	fake := &trinotest.FakeExecutor{
		Steps: []trinotest.Step{
			{Err: &trino.ErrTrino{ErrorName: "CATALOG_NOT_FOUND", Message: "Catalog 'missing' does not exist"}},
		},
	}
	ds := newTestDataSource(t, fake)

	_, diags := readTables(t, ds, tablesDataSourceModel{
		Catalog: types.StringValue("missing"),
		Schema:  types.StringValue("sales"),
		Tables:  types.ListNull(tableElementType),
	})
	if !diags.HasError() {
		t.Fatal("expected an error when the catalog does not exist")
	}
	if !strings.Contains(diags[0].Summary(), "Catalog not found") {
		t.Fatalf("diagnostic summary = %q, want Catalog not found", diags[0].Summary())
	}
}

func TestTablesSchemaAttributes(t *testing.T) {
	schema := testSchema(t, New())

	for name, want := range map[string]struct{ required, optional bool }{
		"catalog":     {required: true},
		"schema":      {required: true},
		"name_prefix": {optional: true},
		"table_type":  {optional: true},
		"tables":      {},
	} {
		attribute, ok := schema.Attributes[name]
		if !ok {
			t.Fatalf("missing attribute %q", name)
		}
		if attribute.IsRequired() != want.required {
			t.Fatalf("%s required = %t, want %t", name, attribute.IsRequired(), want.required)
		}
		if attribute.IsOptional() != want.optional {
			t.Fatalf("%s optional = %t, want %t", name, attribute.IsOptional(), want.optional)
		}
	}
}

func newTestDataSource(t *testing.T, fake *trinotest.FakeExecutor) datasource.DataSource {
	t.Helper()
	ds := New()
	configurable, ok := ds.(datasource.DataSourceWithConfigure)
	if !ok {
		t.Fatalf("%T does not implement datasource.DataSourceWithConfigure", ds)
	}
	var resp datasource.ConfigureResponse
	configurable.Configure(context.Background(), datasource.ConfigureRequest{
		ProviderData: &providerdata.Data{Executor: fake, Timeout: time.Minute},
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", resp.Diagnostics)
	}
	return ds
}

func testSchema(t *testing.T, ds datasource.DataSource) datasourceschema.Schema {
	t.Helper()
	var resp datasource.SchemaResponse
	ds.Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return resp.Schema
}

func readTables(t *testing.T, ds datasource.DataSource, config tablesDataSourceModel) (tablesDataSourceModel, diag.Diagnostics) {
	t.Helper()
	ctx := context.Background()
	schema := testSchema(t, ds)
	objectType, ok := schema.Type().(types.ObjectType)
	if !ok {
		t.Fatalf("schema type = %T, want types.ObjectType", schema.Type())
	}
	value, diags := types.ObjectValueFrom(ctx, objectType.AttributeTypes(), config)
	if diags.HasError() {
		t.Fatalf("encode config: %v", diags)
	}
	raw, err := value.ToTerraformValue(ctx)
	if err != nil {
		t.Fatalf("encode config value: %v", err)
	}

	resp := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
	ds.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Raw: raw, Schema: schema}}, &resp)

	var result tablesDataSourceModel
	if !resp.Diagnostics.HasError() {
		if diags := resp.State.Get(ctx, &result); diags.HasError() {
			t.Fatalf("decode state: %v", diags)
		}
	}
	return result, resp.Diagnostics
}
