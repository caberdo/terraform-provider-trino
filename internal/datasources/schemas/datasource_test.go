package schemas

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

func TestSchemasCatalogIsRequired(t *testing.T) {
	ds := newTestDataSource(t, &trinotest.FakeExecutor{})
	attribute := testSchema(t, ds).Attributes["catalog"]
	stringAttribute, ok := attribute.(datasourceschema.StringAttribute)
	if !ok {
		t.Fatalf("catalog attribute = %T, want datasourceschema.StringAttribute", attribute)
	}
	if !stringAttribute.Required {
		t.Fatal("catalog attribute must be required")
	}
}

func TestSchemasReadSQL(t *testing.T) {
	fake := &trinotest.FakeExecutor{
		Steps: []trinotest.Step{{
			Rows: []trino.Row{
				{"Schema": "tpch"},
				{"Schema": "information_schema"},
				{"Schema": "default"},
			},
		}},
	}
	ds := newTestDataSource(t, fake)

	result, diags := readSchemas(t, ds, schemasDataSourceModel{
		Catalog: types.StringValue("hive"),
		Schemas: types.ListNull(types.StringType),
	})
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}

	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.Calls))
	}
	if got, want := fake.Calls[0].Statement, `SHOW SCHEMAS FROM "hive"`; got != want {
		t.Fatalf("statement = %q, want %q", got, want)
	}
	if len(fake.Calls[0].Args) != 0 {
		t.Fatalf("expected no args, got %v", fake.Calls[0].Args)
	}

	var schemas []string
	if diags := result.Schemas.ElementsAs(context.Background(), &schemas, false); diags.HasError() {
		t.Fatalf("decode schemas: %v", diags)
	}
	want := []string{"default", "information_schema", "tpch"}
	if len(schemas) != len(want) {
		t.Fatalf("schemas = %v, want %v", schemas, want)
	}
	for i := range want {
		if schemas[i] != want[i] {
			t.Fatalf("schemas = %v, want %v", schemas, want)
		}
	}
}

func TestSchemasEmptyResult(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}}}
	ds := newTestDataSource(t, fake)

	result, diags := readSchemas(t, ds, schemasDataSourceModel{
		Catalog: types.StringValue("hive"),
		Schemas: types.ListNull(types.StringType),
	})
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if result.Schemas.IsNull() {
		t.Fatal("schemas is null, want an empty list")
	}
	if got := len(result.Schemas.Elements()); got != 0 {
		t.Fatalf("expected 0 schemas, got %d", got)
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

func readSchemas(t *testing.T, ds datasource.DataSource, config schemasDataSourceModel) (schemasDataSourceModel, diag.Diagnostics) {
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

	var result schemasDataSourceModel
	if !resp.Diagnostics.HasError() {
		if diags := resp.State.Get(ctx, &result); diags.HasError() {
			t.Fatalf("decode state: %v", diags)
		}
	}
	return result, resp.Diagnostics
}
