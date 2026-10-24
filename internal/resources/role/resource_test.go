package role

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/caberdo/terraform-provider-trino/internal/trino"
	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

func roleState(name, catalog string) roleModel {
	return roleModel{
		ID:      types.StringValue(roleID(catalog, name)),
		Name:    types.StringValue(name),
		Catalog: types.StringValue(catalog),
	}
}

func TestRoleCreateSQL(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{},
		{Rows: []trino.Row{{"role_name": "analyst"}}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	plan := roleModel{
		ID:      types.StringNull(),
		Name:    types.StringValue("analyst"),
		Catalog: types.StringValue("hive"),
	}

	state, _ := trinotest.Create(t, r, plan)

	if len(fake.Calls) == 0 {
		t.Fatal("create issued no statement")
	}
	call := fake.Calls[0]
	if call.Op != "exec" {
		t.Fatalf("create op = %q, want exec", call.Op)
	}
	if want := `CREATE ROLE "analyst" IN "hive"`; call.Statement != want {
		t.Fatalf("create statement = %q, want %q", call.Statement, want)
	}
	if got, want := state.ID.ValueString(), "hive.analyst"; got != want {
		t.Fatalf("state id = %q, want %q", got, want)
	}
	if got, want := state.Name.ValueString(), "analyst"; got != want {
		t.Fatalf("state name = %q, want %q", got, want)
	}
	if got, want := state.Catalog.ValueString(), "hive"; got != want {
		t.Fatalf("state catalog = %q, want %q", got, want)
	}
}

func TestRoleReadIgnoresOtherRoles(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{
			{"role_name": "admin"},
			{"role_name": "public"},
			{"role_name": "analyst"},
		}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	state, _ := trinotest.Read(t, r, roleState("analyst", "hive"))

	if len(fake.Calls) != 1 {
		t.Fatalf("read issued %d statements, want 1", len(fake.Calls))
	}
	if want := `SHOW ROLES FROM "hive"`; fake.Calls[0].Statement != want {
		t.Fatalf("read statement = %q, want %q", fake.Calls[0].Statement, want)
	}
	if got, want := state.ID.ValueString(), "hive.analyst"; got != want {
		t.Fatalf("state id = %q, want %q", got, want)
	}
	if got, want := state.Name.ValueString(), "analyst"; got != want {
		t.Fatalf("state name = %q, want %q", got, want)
	}
}

func TestRoleReadCaseInsensitive(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{{"role_name": "analyst"}}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	state, _ := trinotest.Read(t, r, roleState("Analyst", "hive"))

	if got, want := state.Name.ValueString(), "Analyst"; got != want {
		t.Fatalf("state name = %q, want the configured spelling %q", got, want)
	}
	if got, want := state.ID.ValueString(), "hive.Analyst"; got != want {
		t.Fatalf("state id = %q, want %q", got, want)
	}
}

func TestRoleReadNotFound(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	result, diags := readDirect(t, r, roleState("analyst", "hive"))

	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if !result.Raw.IsNull() {
		t.Fatalf("read state = %s, want the resource removed from state", result.Raw)
	}
}

func TestRoleDeleteSQL(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}}}
	r := New()
	trinotest.Configure(t, r, fake)

	trinotest.Delete(t, r, roleState("analyst", "hive"))

	if len(fake.Calls) != 1 {
		t.Fatalf("delete issued %d statements, want 1", len(fake.Calls))
	}
	call := fake.Calls[0]
	if call.Op != "exec" {
		t.Fatalf("delete op = %q, want exec", call.Op)
	}
	if want := `DROP ROLE IF EXISTS "analyst" IN "hive"`; call.Statement != want {
		t.Fatalf("delete statement = %q, want %q", call.Statement, want)
	}
}

func TestRoleDeleteNotFoundIsSuccess(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Err: &trino.ErrTrino{ErrorName: "ROLE_NOT_FOUND", Message: "Role 'analyst' not found"}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	diags := trinotest.Delete(t, r, roleState("analyst", "hive"))
	if diags.HasError() {
		t.Fatalf("delete of a missing role returned an error: %v", diags)
	}
}

func TestRoleImportID(t *testing.T) {
	r := New()
	importable, ok := r.(resource.ResourceWithImportState)
	if !ok {
		t.Fatal("resource does not implement ResourceWithImportState")
	}

	resp := resource.ImportStateResponse{
		State: tfsdk.State{Schema: trinotest.SchemaOf(t, r)},
	}
	importable.ImportState(context.Background(), resource.ImportStateRequest{ID: "hive.analyst"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", resp.Diagnostics)
	}

	var state roleModel
	resp.Diagnostics.Append(resp.State.Get(context.Background(), &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("decode imported state: %v", resp.Diagnostics)
	}
	if got, want := state.Catalog.ValueString(), "hive"; got != want {
		t.Fatalf("import catalog = %q, want %q", got, want)
	}
	if got, want := state.Name.ValueString(), "analyst"; got != want {
		t.Fatalf("import name = %q, want %q", got, want)
	}
	if got, want := state.ID.ValueString(), "hive.analyst"; got != want {
		t.Fatalf("import id = %q, want %q", got, want)
	}
}

func TestRoleCatalogRequired(t *testing.T) {
	r := New()
	schema := trinotest.SchemaOf(t, r)

	attribute, ok := schema.Attributes["catalog"].(resourceschema.StringAttribute)
	if !ok {
		t.Fatal("catalog attribute is not a string attribute")
	}
	if len(attribute.Validators) == 0 {
		t.Fatal("catalog has no validators")
	}

	for _, value := range []string{"", "   "} {
		req := validator.StringRequest{
			Path:        path.Root("catalog"),
			ConfigValue: types.StringValue(value),
		}
		var resp validator.StringResponse
		for _, v := range attribute.Validators {
			v.ValidateString(context.Background(), req, &resp)
		}

		if !resp.Diagnostics.HasError() {
			t.Fatalf("catalog %q was accepted, want a validation error", value)
		}

		found := false
		for _, d := range resp.Diagnostics {
			withPath, ok := d.(diag.DiagnosticWithPath)
			if !ok || !withPath.Path().Equal(path.Root("catalog")) {
				continue
			}
			text := strings.ToLower(d.Summary() + " " + d.Detail())
			if strings.Contains(text, "system roles") {
				found = true
			}
		}
		if !found {
			t.Fatalf("catalog %q diagnostic does not name the system-roles limitation: %v", value, resp.Diagnostics)
		}
	}
}

// readDirect invokes Read without decoding the response state, so a test can
// assert that not-found removed the resource instead of fighting the harness's
// struct decoder on a null state.
func readDirect(t *testing.T, r resource.Resource, state roleModel) (tfsdk.State, diag.Diagnostics) {
	t.Helper()

	schema := trinotest.SchemaOf(t, r)
	objectType, ok := schema.Type().(types.ObjectType)
	if !ok {
		t.Fatal("schema is not an object type")
	}
	value, valueDiags := types.ObjectValueFrom(context.Background(), objectType.AttributeTypes(), state)
	if valueDiags.HasError() {
		t.Fatalf("encode state: %v", valueDiags)
	}
	raw, err := value.ToTerraformValue(context.Background())
	if err != nil {
		t.Fatalf("encode terraform value: %v", err)
	}

	resp := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	r.Read(context.Background(), resource.ReadRequest{State: tfsdk.State{Raw: raw, Schema: schema}}, &resp)
	return resp.State, resp.Diagnostics
}
