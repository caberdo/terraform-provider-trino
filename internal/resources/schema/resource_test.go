package schema

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/caberdo/terraform-provider-trino/internal/trino"
	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

func newTestResource(t *testing.T, steps ...trinotest.Step) (resource.Resource, *trinotest.FakeExecutor) {
	t.Helper()
	fake := &trinotest.FakeExecutor{Steps: steps}
	r := New()
	trinotest.Configure(t, r, fake)
	return r, fake
}

func testModel(catalog, name string) model {
	return model{
		ID:           types.StringValue(catalog + "." + name),
		Catalog:      types.StringValue(catalog),
		Name:         types.StringValue(name),
		Owner:        types.StringNull(),
		OwnerType:    types.StringNull(),
		Properties:   types.MapNull(types.StringType),
		ForceDestroy: types.BoolNull(),
	}
}

func stringMap(t *testing.T, value types.Map) map[string]string {
	t.Helper()
	result := map[string]string{}
	diags := value.ElementsAs(trinotest.Ctx(t), &result, false)
	if diags.HasError() {
		t.Fatalf("map conversion diagnostics: %v", diags)
	}
	return result
}

func mustMap(t *testing.T, values map[string]string) types.Map {
	t.Helper()
	value, diags := types.MapValueFrom(trinotest.Ctx(t), types.StringType, values)
	if diags.HasError() {
		t.Fatalf("map value diagnostics: %v", diags)
	}
	return value
}

func rawValue(t *testing.T, r resource.Resource, value model) (tftypes.Value, resourceschema.Schema) {
	t.Helper()
	schema := trinotest.SchemaOf(t, r)
	objectType, ok := schema.Type().(types.ObjectType)
	if !ok {
		t.Fatalf("schema type = %T, want types.ObjectType", schema.Type())
	}
	object, diags := types.ObjectValueFrom(trinotest.Ctx(t), objectType.AttributeTypes(), value)
	if diags.HasError() {
		t.Fatalf("object value diagnostics: %v", diags)
	}
	raw, err := object.ToTerraformValue(trinotest.Ctx(t))
	if err != nil {
		t.Fatalf("encode value: %v", err)
	}
	return raw, schema
}

func readRaw(t *testing.T, r resource.Resource, state model) (tfsdk.State, diag.Diagnostics) {
	t.Helper()
	raw, schema := rawValue(t, r, state)
	resp := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	r.Read(trinotest.Ctx(t), resource.ReadRequest{
		State: tfsdk.State{Raw: raw, Schema: schema},
	}, &resp)
	return resp.State, resp.Diagnostics
}

func TestSchemaCreateMinimalSQL(t *testing.T) {
	r, fake := newTestResource(t,
		trinotest.Step{},
		trinotest.Step{Rows: []trino.Row{{"schema_name": "sales"}}},
		trinotest.Step{Rows: []trino.Row{{"create_schema": `CREATE SCHEMA "hive"."sales"`}}},
	)

	plan := testModel("hive", "sales")
	plan.ID = types.StringUnknown()

	state, _ := trinotest.Create(t, r, plan)

	if got := fake.Calls[0].Statement; got != `CREATE SCHEMA "hive"."sales"` {
		t.Fatalf("create SQL = %q, want %q", got, `CREATE SCHEMA "hive"."sales"`)
	}
	if got := state.ID.ValueString(); got != "hive.sales" {
		t.Fatalf("id = %q, want hive.sales", got)
	}
	if got := state.OwnerType.ValueString(); got != "USER" {
		t.Fatalf("owner_type = %q, want USER", got)
	}
}

func TestSchemaCreateWithOwnerAndProperties(t *testing.T) {
	r, fake := newTestResource(t,
		trinotest.Step{},
		trinotest.Step{Rows: []trino.Row{{"schema_name": "sales"}}},
		trinotest.Step{Rows: []trino.Row{{"create_schema": `CREATE SCHEMA "hive"."sales" AUTHORIZATION USER "alice" WITH ("location" = 's3://bucket/sales')`}}},
	)

	plan := testModel("hive", "sales")
	plan.ID = types.StringUnknown()
	plan.Owner = types.StringValue("alice")
	plan.OwnerType = types.StringValue("USER")
	plan.Properties = mustMap(t, map[string]string{"location": "s3://bucket/sales"})

	state, _ := trinotest.Create(t, r, plan)

	wantSQL := `CREATE SCHEMA "hive"."sales" AUTHORIZATION USER "alice" WITH ("location" = 's3://bucket/sales')`
	if got := fake.Calls[0].Statement; got != wantSQL {
		t.Fatalf("create SQL = %q, want %q", got, wantSQL)
	}
	if got := state.Owner.ValueString(); got != "alice" {
		t.Fatalf("owner = %q, want alice", got)
	}
	if got := stringMap(t, state.Properties)["location"]; got != "s3://bucket/sales" {
		t.Fatalf("location = %q, want s3://bucket/sales", got)
	}
}

func TestSchemaReadCaseInsensitiveName(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{Rows: []trino.Row{{"schema_name": "sales"}}},
		trinotest.Step{Rows: []trino.Row{{"create_schema": `CREATE SCHEMA "hive"."sales"`}}},
	)

	state, _ := trinotest.Read(t, r, testModel("hive", "Sales"))

	if got := state.Name.ValueString(); got != "Sales" {
		t.Fatalf("name = %q, want configured spelling Sales", got)
	}
	if got := state.ID.ValueString(); got != "hive.Sales" {
		t.Fatalf("id = %q, want hive.Sales", got)
	}
}

func TestSchemaReadNotFoundRemovesState(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{Rows: []trino.Row{{"schema_name": "other"}}},
	)

	state, diags := readRaw(t, r, testModel("hive", "sales"))
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if !state.Raw.IsNull() {
		t.Fatalf("state was not removed: %#v", state.Raw)
	}
}

func TestSchemaReadParseFailurePreservesState(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{Rows: []trino.Row{{"schema_name": "sales"}}},
		trinotest.Step{Rows: []trino.Row{{"create_schema": `CREATE SCHEMA "hive"."sales" WITH (`}}},
	)

	state := testModel("hive", "sales")
	state.Owner = types.StringValue("alice")
	state.OwnerType = types.StringValue("USER")
	state.Properties = mustMap(t, map[string]string{"location": "/tmp/sales"})

	result, _ := trinotest.Read(t, r, state)

	if got := result.Owner.ValueString(); got != "alice" {
		t.Fatalf("owner = %q, want preserved alice", got)
	}
	if got := result.OwnerType.ValueString(); got != "USER" {
		t.Fatalf("owner_type = %q, want preserved USER", got)
	}
	if got := stringMap(t, result.Properties)["location"]; got != "/tmp/sales" {
		t.Fatalf("location = %q, want preserved /tmp/sales", got)
	}
}

func TestSchemaUpdateOwnerEmitsAlter(t *testing.T) {
	r, fake := newTestResource(t, trinotest.Step{})

	state := testModel("hive", "sales")
	state.Owner = types.StringValue("alice")
	state.OwnerType = types.StringValue("USER")

	plan := state
	plan.Owner = types.StringValue("analyst")
	plan.OwnerType = types.StringValue("ROLE")

	result, _ := trinotest.Update(t, r, state, plan)

	wantSQL := `ALTER SCHEMA "hive"."sales" SET AUTHORIZATION ROLE "analyst"`
	if len(fake.Calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fake.Calls))
	}
	if got := fake.Calls[0].Statement; got != wantSQL {
		t.Fatalf("alter SQL = %q, want %q", got, wantSQL)
	}
	if got := result.Owner.ValueString(); got != "analyst" {
		t.Fatalf("owner = %q, want analyst", got)
	}
}

func TestSchemaForceDestroyOnlyUpdateNoSQL(t *testing.T) {
	r, fake := newTestResource(t)

	state := testModel("hive", "sales")
	state.Owner = types.StringValue("alice")
	state.OwnerType = types.StringValue("USER")
	state.ForceDestroy = types.BoolValue(false)

	plan := state
	plan.ForceDestroy = types.BoolValue(true)

	result, _ := trinotest.Update(t, r, state, plan)

	if len(fake.Calls) != 0 {
		t.Fatalf("calls = %#v, want none", fake.Calls)
	}
	if !result.ForceDestroy.ValueBool() {
		t.Fatalf("force_destroy = false, want true")
	}
}

func TestSchemaDeleteCascade(t *testing.T) {
	r, fake := newTestResource(t, trinotest.Step{})

	state := testModel("hive", "sales")
	state.ForceDestroy = types.BoolValue(true)

	trinotest.Delete(t, r, state)

	if got := fake.Calls[0].Statement; got != `DROP SCHEMA "hive"."sales" CASCADE` {
		t.Fatalf("drop SQL = %q, want CASCADE", got)
	}
}

func TestSchemaPropertiesRequireReplace(t *testing.T) {
	attr, ok := trinotest.SchemaOf(t, New()).Attributes["properties"].(resourceschema.MapAttribute)
	if !ok {
		t.Fatalf("properties attribute is %T, want MapAttribute", trinotest.SchemaOf(t, New()).Attributes["properties"])
	}
	if len(attr.PlanModifiers) == 0 {
		t.Fatal("properties has no plan modifiers; want RequiresReplace")
	}
}

func TestSchemaOwnerTypeRequiresOwner(t *testing.T) {
	r := New()
	validate, ok := r.(resource.ResourceWithValidateConfig)
	if !ok {
		t.Fatalf("%T does not implement ResourceWithValidateConfig", r)
	}

	config := testModel("hive", "sales")
	config.OwnerType = types.StringValue("ROLE")
	raw, schema := rawValue(t, r, config)

	var resp resource.ValidateConfigResponse
	validate.ValidateConfig(trinotest.Ctx(t), resource.ValidateConfigRequest{
		Config: tfsdk.Config{Raw: raw, Schema: schema},
	}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error for owner_type without owner")
	}
	withPath, ok := resp.Diagnostics[0].(diag.DiagnosticWithPath)
	if !ok {
		t.Fatalf("diagnostic is %T, want one with an attribute path", resp.Diagnostics[0])
	}
	if !withPath.Path().Equal(path.Root("owner_type")) {
		t.Fatalf("diagnostic path = %s, want owner_type", withPath.Path())
	}
}

func TestSchemaImportID(t *testing.T) {
	r := New()
	importable, ok := r.(resource.ResourceWithImportState)
	if !ok {
		t.Fatalf("%T does not implement ResourceWithImportState", r)
	}

	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: trinotest.SchemaOf(t, r)}}
	importable.ImportState(trinotest.Ctx(t), resource.ImportStateRequest{ID: "hive.a.b"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", resp.Diagnostics)
	}

	var state model
	if diags := resp.State.Get(trinotest.Ctx(t), &state); diags.HasError() {
		t.Fatalf("state diagnostics: %v", diags)
	}
	if got := state.Catalog.ValueString(); got != "hive" {
		t.Fatalf("catalog = %q, want hive", got)
	}
	if got := state.Name.ValueString(); got != "a.b" {
		t.Fatalf("name = %q, want a.b", got)
	}
	if got := state.ID.ValueString(); got != "hive.a.b" {
		t.Fatalf("id = %q, want hive.a.b", got)
	}
}
