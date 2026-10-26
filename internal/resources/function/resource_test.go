package function

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/caberdo/terraform-provider-trino/internal/trino"
	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

func testFunctionModel(definition string) model {
	return model{
		ID:         types.StringNull(),
		Catalog:    types.StringValue("hive"),
		Schema:     types.StringValue("sales"),
		Name:       types.StringValue("my_add"),
		Definition: types.StringValue(definition),
		OrReplace:  types.BoolNull(),
	}
}

const storedAdd = "CREATE FUNCTION hive.sales.my_add(x bigint, y bigint)\nRETURNS bigint\nRETURN (x + y)"

func TestFunctionCreateSQL(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{},
		{Rows: []trino.Row{{"function": "my_add", "argument types": "bigint, bigint"}}},
		{Rows: []trino.Row{{"create function": storedAdd}}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	plan := testFunctionModel("(x bigint, y bigint) RETURNS bigint RETURN x + y")
	state, diags := trinotest.Create(t, r, plan)
	if diags.HasError() {
		t.Fatalf("create diagnostics: %v", diags)
	}

	if len(fake.Calls) != 3 {
		t.Fatalf("expected 3 calls, got %d: %+v", len(fake.Calls), fake.Calls)
	}
	want := `CREATE FUNCTION "hive"."sales"."my_add" (x bigint, y bigint) RETURNS bigint RETURN x + y`
	if fake.Calls[0].Op != "exec" || fake.Calls[0].Statement != want {
		t.Fatalf("create call = %#v, want exec %q", fake.Calls[0], want)
	}
	if got, want := fake.Calls[1].Statement, `SHOW FUNCTIONS FROM "hive"."sales"`; got != want {
		t.Fatalf("existence statement = %q, want %q", got, want)
	}
	if got, want := fake.Calls[2].Statement, `SHOW CREATE FUNCTION "hive"."sales"."my_add"`; got != want {
		t.Fatalf("definition statement = %q, want %q", got, want)
	}
	if got, want := state.ID.ValueString(), "hive.sales.my_add"; got != want {
		t.Fatalf("state id = %q, want %q", got, want)
	}
	// The configured text is preserved when the server only normalises it.
	if got, want := state.Definition.ValueString(), plan.Definition.ValueString(); got != want {
		t.Fatalf("state definition = %q, want %q", got, want)
	}
}

func TestFunctionCreateOrReplace(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{},
		{Rows: []trino.Row{{"function": "my_add", "argument types": "bigint, bigint"}}},
		{Rows: []trino.Row{{"create function": storedAdd}}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	plan := testFunctionModel("(x bigint, y bigint) RETURNS bigint RETURN x + y")
	plan.OrReplace = types.BoolValue(true)
	trinotest.Create(t, r, plan)

	want := `CREATE OR REPLACE FUNCTION "hive"."sales"."my_add" (x bigint, y bigint) RETURNS bigint RETURN x + y`
	if fake.Calls[0].Statement != want {
		t.Fatalf("create statement = %q, want %q", fake.Calls[0].Statement, want)
	}
}

func TestFunctionCreateExistingIsActionable(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Err: errors.New("Function already exists")},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	_, diags := trinotest.CreateExpectError(t, r, testFunctionModel("(x bigint) RETURNS bigint RETURN x"))
	if !diags.HasError() {
		t.Fatal("expected an error when the function exists")
	}
	if !strings.Contains(diags[0].Summary(), "Function already exists") {
		t.Fatalf("summary = %q", diags[0].Summary())
	}
	if !strings.Contains(diags[0].Detail(), "or_replace = true") {
		t.Fatalf("detail = %q, want or_replace guidance", diags[0].Detail())
	}
}

func TestFunctionCreateUnsupportedConnector(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Err: errors.New("This connector does not support creating functions")},
		{Rows: []trino.Row{{"connector_name": "tpch"}}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	_, diags := trinotest.CreateExpectError(t, r, testFunctionModel("(x bigint) RETURNS bigint RETURN x"))
	if !diags.HasError() {
		t.Fatal("expected an error for a connector without UDF storage")
	}
	if !strings.Contains(diags[0].Summary(), "Catalog UDF storage is not supported") {
		t.Fatalf("summary = %q", diags[0].Summary())
	}
	detail := diags[0].Detail()
	if !strings.Contains(detail, `"tpch"`) || !strings.Contains(detail, "Hive or Memory") {
		t.Fatalf("detail = %q, want connector name and requirement", detail)
	}
}

func TestFunctionCreateRejectsInvalidDefinition(t *testing.T) {
	fake := &trinotest.FakeExecutor{}
	r := New()
	trinotest.Configure(t, r, fake)

	_, diags := trinotest.CreateExpectError(t, r, testFunctionModel("(x bigint) RETURN x"))
	if !diags.HasError() {
		t.Fatal("expected a definition error")
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("invalid definition issued %d calls", len(fake.Calls))
	}
}

func TestFunctionReadDetectsDefinitionDrift(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{{"function": "my_add", "argument types": "bigint, bigint"}}},
		{Rows: []trino.Row{{"create function": "CREATE FUNCTION hive.sales.my_add(x bigint, y bigint)\nRETURNS bigint\nRETURN (x * y)"}}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	state, diags := trinotest.Read(t, r, testFunctionModel("(x bigint, y bigint) RETURNS bigint RETURN x + y"))
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if !strings.Contains(state.Definition.ValueString(), "x * y") {
		t.Fatalf("state definition = %q, want the server definition", state.Definition.ValueString())
	}
}

func TestFunctionReadKeepsEquivalentDefinition(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{{"function": "my_add", "argument types": "bigint, bigint"}}},
		{Rows: []trino.Row{{"create function": storedAdd}}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	configured := "(x bigint, y bigint) RETURNS bigint RETURN x + y"
	state, diags := trinotest.Read(t, r, testFunctionModel(configured))
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if got := state.Definition.ValueString(); got != configured {
		t.Fatalf("state definition = %q, want the configured text %q", got, configured)
	}
}

func TestFunctionReadDifferentOverloadIsGone(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{
			{"function": "my_add", "argument types": "varchar, varchar"},
		}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	state, _ := trinotest.ReadExpectError(t, r, testFunctionModel("(x bigint, y bigint) RETURNS bigint RETURN x + y"))
	if !state.ID.IsNull() && state.ID.ValueString() != "" {
		t.Fatalf("state id = %q, want null/empty after removal", state.ID.ValueString())
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("expected only the existence query, got %d calls", len(fake.Calls))
	}
}

func TestFunctionReadSelectsMatchingOverload(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{{"function": "dup", "argument types": "bigint"}}},
		{Rows: []trino.Row{
			{"create function": "CREATE FUNCTION hive.sales.dup(x varchar)\nRETURNS varchar\nRETURN x"},
			{"create function": "CREATE FUNCTION hive.sales.dup(x bigint)\nRETURNS bigint\nRETURN x"},
		}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	plan := testFunctionModel("(x bigint) RETURNS bigint RETURN x")
	plan.Name = types.StringValue("dup")
	state, diags := trinotest.Read(t, r, plan)
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if !strings.Contains(state.Definition.ValueString(), "RETURNS bigint") {
		t.Fatalf("state definition = %q, want the bigint overload", state.Definition.ValueString())
	}
}

func TestFunctionReadOverloadedImportIsRejected(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{
			{"function": "dup", "argument types": "bigint"},
			{"function": "dup", "argument types": "varchar"},
		}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	plan := testFunctionModel("")
	plan.Name = types.StringValue("dup")
	plan.Definition = types.StringNull()
	_, diags := trinotest.ReadExpectError(t, r, plan)
	if !diags.HasError() || !strings.Contains(diags[0].Summary(), "overloaded") {
		t.Fatalf("diagnostics = %v, want an overloaded-name error", diags)
	}
}

func TestFunctionUpdateReplacesDefinition(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{},
		{Rows: []trino.Row{{"function": "my_add", "argument types": "bigint, bigint"}}},
		{Rows: []trino.Row{{"create function": "CREATE FUNCTION hive.sales.my_add(x bigint, y bigint)\nRETURNS bigint\nRETURN (x * y)"}}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	stateModel := testFunctionModel("(x bigint, y bigint) RETURNS bigint RETURN x + y")
	plan := testFunctionModel("(x bigint, y bigint) RETURNS bigint RETURN x * y")
	state, diags := trinotest.Update(t, r, stateModel, plan)
	if diags.HasError() {
		t.Fatalf("update diagnostics: %v", diags)
	}
	want := `CREATE OR REPLACE FUNCTION "hive"."sales"."my_add" (x bigint, y bigint) RETURNS bigint RETURN x * y`
	if fake.Calls[0].Statement != want {
		t.Fatalf("update statement = %q, want %q", fake.Calls[0].Statement, want)
	}
	if state.Definition.ValueString() != plan.Definition.ValueString() {
		t.Fatalf("state definition = %q, want %q", state.Definition.ValueString(), plan.Definition.ValueString())
	}
}

func TestFunctionDeleteUsesParameterTypes(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}}}
	r := New()
	trinotest.Configure(t, r, fake)

	stateModel := testFunctionModel("(x decimal(10, 2), y array(varchar)) RETURNS decimal(10, 2) RETURN x")
	stateModel.Name = types.StringValue("f")
	trinotest.Delete(t, r, stateModel)

	want := `DROP FUNCTION IF EXISTS "hive"."sales"."f"(decimal(10, 2), array(varchar))`
	if len(fake.Calls) != 1 || fake.Calls[0].Statement != want {
		t.Fatalf("delete call = %#v, want %q", fake.Calls, want)
	}
}

func TestFunctionDeleteMissingIsSuccess(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Err: &trino.ErrTrino{ErrorName: "FUNCTION_NOT_FOUND", Message: "Function not found"}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	diags := trinotest.DeleteExpectError(t, r, testFunctionModel("(x bigint) RETURNS bigint RETURN x"))
	if diags.HasError() {
		t.Fatalf("delete diagnostics: %v", diags)
	}
}

func TestFunctionImportID(t *testing.T) {
	r := New()
	importable, ok := r.(resource.ResourceWithImportState)
	if !ok {
		t.Fatal("resource does not implement ResourceWithImportState")
	}

	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: trinotest.SchemaOf(t, r)}}
	importable.ImportState(context.Background(), resource.ImportStateRequest{ID: "hive.sales.my_add"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", resp.Diagnostics)
	}

	var state model
	resp.Diagnostics.Append(resp.State.Get(context.Background(), &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("decode imported state: %v", resp.Diagnostics)
	}
	if got, want := state.ID.ValueString(), "hive.sales.my_add"; got != want {
		t.Fatalf("import id = %q, want %q", got, want)
	}
	if got, want := state.Name.ValueString(), "my_add"; got != want {
		t.Fatalf("import name = %q, want %q", got, want)
	}
	if !state.Definition.IsNull() {
		t.Fatalf("import definition = %q, want null until the first read", state.Definition.ValueString())
	}
}

func TestFunctionImportInvalidID(t *testing.T) {
	r := New()
	importable := r.(resource.ResourceWithImportState)
	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: trinotest.SchemaOf(t, r)}}
	importable.ImportState(context.Background(), resource.ImportStateRequest{ID: "hive.my_add"}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error for an import ID without a schema")
	}
}

func TestFunctionSchemaAttributes(t *testing.T) {
	schema := trinotest.SchemaOf(t, New())
	for name, want := range map[string]struct{ required, optional bool }{
		"catalog":    {required: true},
		"schema":     {required: true},
		"name":       {required: true},
		"definition": {required: true},
		"or_replace": {optional: true},
		"id":         {},
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

func TestFunctionModifyPlanKeepsEquivalentDefinition(t *testing.T) {
	r := New()
	fake := &trinotest.FakeExecutor{}
	trinotest.Configure(t, r, fake)
	schema := trinotest.SchemaOf(t, r)

	configured := "(x bigint) RETURNS bigint RETURN x + 1"
	stored := "(x bigint)\nRETURNS bigint\nRETURN (x + 1)"

	for _, test := range []struct {
		name string
		plan string
		want string
	}{
		{name: "equivalent", plan: configured, want: stored},
		{name: "changed", plan: "(x bigint) RETURNS bigint RETURN x * 2", want: "(x bigint) RETURNS bigint RETURN x * 2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := testFunctionModel(stored)
			state.ID = types.StringValue("hive.sales.my_add")
			plan := testFunctionModel(test.plan)
			plan.ID = types.StringValue("hive.sales.my_add")

			stateRaw := rawModel(t, schema, state)
			planRaw := rawModel(t, schema, plan)
			req := resource.ModifyPlanRequest{
				State: tfsdk.State{Raw: stateRaw, Schema: schema},
				Plan:  tfsdk.Plan{Raw: planRaw, Schema: schema},
			}
			resp := resource.ModifyPlanResponse{Plan: tfsdk.Plan{Raw: planRaw, Schema: schema}}
			modifier, ok := r.(resource.ResourceWithModifyPlan)
			if !ok {
				t.Fatal("resource does not implement ResourceWithModifyPlan")
			}
			modifier.ModifyPlan(context.Background(), req, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("modify plan diagnostics: %v", resp.Diagnostics)
			}

			var result model
			resp.Diagnostics.Append(resp.Plan.Get(context.Background(), &result)...)
			if resp.Diagnostics.HasError() {
				t.Fatalf("decode plan: %v", resp.Diagnostics)
			}
			if got := result.Definition.ValueString(); got != test.want {
				t.Fatalf("plan definition = %q, want %q", got, test.want)
			}
		})
	}
}

func rawModel(t *testing.T, schema resourceschema.Schema, value any) tftypes.Value {
	t.Helper()
	objectType, ok := schema.Type().(types.ObjectType)
	if !ok {
		t.Fatalf("schema type = %T, want types.ObjectType", schema.Type())
	}
	encoded, diags := types.ObjectValueFrom(context.Background(), objectType.AttributeTypes(), value)
	if diags.HasError() {
		t.Fatalf("encode model: %v", diags)
	}
	raw, err := encoded.ToTerraformValue(context.Background())
	if err != nil {
		t.Fatalf("encode terraform value: %v", err)
	}
	return raw
}

func TestFunctionValidateConfigRejectsInvalidDefinition(t *testing.T) {
	r := New()
	schema := trinotest.SchemaOf(t, r)
	configRaw := rawModel(t, schema, testFunctionModel("(x bigint) RETURN x"))

	resp := resource.ValidateConfigResponse{}
	r.(resource.ResourceWithValidateConfig).ValidateConfig(context.Background(),
		resource.ValidateConfigRequest{Config: tfsdk.Config{Raw: configRaw, Schema: schema}}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected a definition error")
	}
	if !strings.Contains(resp.Diagnostics[0].Summary(), "Invalid function definition") {
		t.Fatalf("summary = %q", resp.Diagnostics[0].Summary())
	}
}

func TestFunctionDeleteUsesServerSignatureWhenStateHasNone(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{{"create function": "CREATE FUNCTION hive.sales.my_add(x bigint)\nRETURNS bigint\nRETURN x"}}},
		{},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	stateModel := testFunctionModel("")
	stateModel.Definition = types.StringNull()
	trinotest.Delete(t, r, stateModel)

	want := `DROP FUNCTION IF EXISTS "hive"."sales"."my_add"(bigint)`
	if len(fake.Calls) != 2 || fake.Calls[1].Statement != want {
		t.Fatalf("delete calls = %#v, want %q", fake.Calls, want)
	}
}

func TestFunctionDeleteOverloadedWithoutStateDefinition(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{
			{"create function": "CREATE FUNCTION hive.sales.dup(x bigint)\nRETURNS bigint\nRETURN x"},
			{"create function": "CREATE FUNCTION hive.sales.dup(x varchar)\nRETURNS varchar\nRETURN x"},
		}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	stateModel := testFunctionModel("")
	stateModel.Name = types.StringValue("dup")
	stateModel.Definition = types.StringNull()
	diags := trinotest.DeleteExpectError(t, r, stateModel)
	if !diags.HasError() || !strings.Contains(diags[0].Summary(), "overloaded") {
		t.Fatalf("diagnostics = %v, want an overloaded-name error", diags)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("expected only the read, got %d calls", len(fake.Calls))
	}
}

func TestFunctionReadAdoptsSingleOverloadOnImport(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{{"function": "my_add", "argument types": "bigint"}}},
		{Rows: []trino.Row{{"create function": "CREATE FUNCTION hive.sales.my_add(x bigint)\nRETURNS bigint\nRETURN x"}}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	stateModel := testFunctionModel("")
	stateModel.Definition = types.StringNull()
	state, diags := trinotest.Read(t, r, stateModel)
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if got, want := state.Definition.ValueString(), "(x bigint)\nRETURNS bigint\nRETURN x"; got != want {
		t.Fatalf("state definition = %q, want %q", got, want)
	}
}

func TestFunctionUpdateOrReplaceOnlyIssuesNoDDL(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{{"function": "my_add", "argument types": "bigint"}}},
		{Rows: []trino.Row{{"create function": "CREATE FUNCTION hive.sales.my_add(x bigint)\nRETURNS bigint\nRETURN x"}}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	stateModel := testFunctionModel("(x bigint) RETURNS bigint RETURN x")
	stateModel.Definition = types.StringValue("(x bigint)\nRETURNS bigint\nRETURN x")
	plan := testFunctionModel("(x bigint) RETURNS bigint RETURN x")
	plan.OrReplace = types.BoolValue(true)
	trinotest.Update(t, r, stateModel, plan)

	for _, call := range fake.Calls {
		if call.Op == "exec" {
			t.Fatalf("or_replace-only update issued DDL: %q", call.Statement)
		}
	}
}

func TestFunctionCreateNotFoundAfterCreate(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{},
		{},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	_, diags := trinotest.CreateExpectError(t, r, testFunctionModel("(x bigint) RETURNS bigint RETURN x"))
	if !diags.HasError() || !strings.Contains(diags[0].Summary(), "Function not found after create") {
		t.Fatalf("diagnostics = %v, want a post-create check error", diags)
	}
}
