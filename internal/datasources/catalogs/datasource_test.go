package catalogs

import (
	"context"
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

func TestCatalogsReadSQL(t *testing.T) {
	fake := &trinotest.FakeExecutor{
		Steps: []trinotest.Step{{
			Rows: []trino.Row{
				{"catalog_name": "system", "connector_name": "system"},
				{"catalog_name": "hive", "connector_name": "hive"},
				{"catalog_name": "memory", "connector_name": "memory"},
			},
		}},
	}
	ds := newTestDataSource(t, fake)

	result, diags := readCatalogs(t, ds, catalogsDataSourceModel{
		NamePrefix: types.StringNull(),
		Catalogs:   types.ListNull(catalogElementType),
	})
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}

	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.Calls))
	}
	if fake.Calls[0].Statement != catalogsQuery {
		t.Fatalf("statement = %q, want %q", fake.Calls[0].Statement, catalogsQuery)
	}
	if len(fake.Calls[0].Args) != 0 {
		t.Fatalf("expected no args, got %v", fake.Calls[0].Args)
	}

	var entries []catalogEntry
	if diags := result.Catalogs.ElementsAs(context.Background(), &entries, false); diags.HasError() {
		t.Fatalf("decode catalogs: %v", diags)
	}
	got := make([]string, len(entries))
	for i, entry := range entries {
		got[i] = entry.Name.ValueString()
	}
	want := []string{"hive", "memory", "system"}
	if len(got) != len(want) {
		t.Fatalf("catalogs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("catalogs = %v, want %v", got, want)
		}
	}
}

func TestCatalogsNamePrefixFilter(t *testing.T) {
	fake := &trinotest.FakeExecutor{
		Steps: []trinotest.Step{{
			Rows: []trino.Row{
				{"catalog_name": "memory", "connector_name": "memory"},
				{"catalog_name": "hive", "connector_name": "hive"},
				{"catalog_name": "mysql", "connector_name": "mysql"},
			},
		}},
	}
	ds := newTestDataSource(t, fake)

	result, diags := readCatalogs(t, ds, catalogsDataSourceModel{
		NamePrefix: types.StringValue("mem"),
		Catalogs:   types.ListNull(catalogElementType),
	})
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}

	var entries []catalogEntry
	if diags := result.Catalogs.ElementsAs(context.Background(), &entries, false); diags.HasError() {
		t.Fatalf("decode catalogs: %v", diags)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 catalog, got %d", len(entries))
	}
	if got := entries[0].Name.ValueString(); got != "memory" {
		t.Fatalf("catalog name = %q, want memory", got)
	}
	if got := entries[0].Connector.ValueString(); got != "memory" {
		t.Fatalf("connector = %q, want memory", got)
	}
}

func TestCatalogsEmptyResult(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}}}
	ds := newTestDataSource(t, fake)

	result, diags := readCatalogs(t, ds, catalogsDataSourceModel{
		NamePrefix: types.StringNull(),
		Catalogs:   types.ListNull(catalogElementType),
	})
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if result.Catalogs.IsNull() {
		t.Fatal("catalogs is null, want an empty list")
	}
	if got := len(result.Catalogs.Elements()); got != 0 {
		t.Fatalf("expected 0 catalogs, got %d", got)
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

func readCatalogs(t *testing.T, ds datasource.DataSource, config catalogsDataSourceModel) (catalogsDataSourceModel, diag.Diagnostics) {
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

	var result catalogsDataSourceModel
	if !resp.Diagnostics.HasError() {
		if diags := resp.State.Get(ctx, &result); diags.HasError() {
			t.Fatalf("decode state: %v", diags)
		}
	}
	return result, resp.Diagnostics
}
