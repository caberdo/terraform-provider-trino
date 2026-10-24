package grantprivileges

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/caberdo/terraform-provider-trino/internal/resourceutil"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

func TestGrantPrivilegesCreateSQL(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{},
		{Rows: privilegeRows(false, "SELECT", "INSERT")},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	state, _ := trinotest.Create(t, r, testModel([]string{"INSERT", "SELECT"}, false))

	want := `GRANT INSERT, SELECT ON TABLE "hive"."sales"."orders" TO ROLE "analyst"`
	if len(fake.Calls) == 0 || fake.Calls[0].Op != "exec" || fake.Calls[0].Statement != want {
		t.Fatalf("create call = %#v, want exec %q", fake.Calls, want)
	}
	assertPrivileges(t, state.Privileges, "INSERT", "SELECT")
}

func TestGrantPrivilegesCreateWithGrantOption(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{},
		{Rows: privilegeRows(true, "SELECT")},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	state, _ := trinotest.Create(t, r, testModel([]string{"SELECT"}, true))

	want := `GRANT SELECT ON TABLE "hive"."sales"."orders" TO ROLE "analyst" WITH GRANT OPTION`
	if fake.Calls[0].Statement != want {
		t.Fatalf("create statement = %q, want %q", fake.Calls[0].Statement, want)
	}
	if !state.GrantOption.ValueBool() {
		t.Fatal("grant_option = false, want true")
	}
}

func TestGrantPrivilegesReadUpdatesDrift(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{
			{"grantee": "analyst", "grantee_type": "ROLE", "privilege_type": "SELECT", "is_grantable": "YES"},
			{"grantee": "analyst", "grantee_type": "ROLE", "privilege_type": "DELETE", "is_grantable": "NO"},
		}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	state, _ := trinotest.Read(t, r, testState([]string{"SELECT"}, false))

	assertPrivileges(t, state.Privileges, "SELECT", "DELETE")
	if !state.GrantOption.ValueBool() {
		t.Fatal("grant_option = false, want true (any YES)")
	}
	if state.Grantee.ValueString() != "analyst" || state.GranteeType.ValueString() != "ROLE" {
		t.Fatalf("configured grantee changed: %q/%q", state.Grantee.ValueString(), state.GranteeType.ValueString())
	}
}

func TestGrantPrivilegesReadRemovesWhenNoRows(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{Rows: nil}}}
	r := New()
	trinotest.Configure(t, r, fake)

	ctx := trinotest.Ctx(t)
	schema := trinotest.SchemaOf(t, r)
	state := testState([]string{"SELECT"}, false)
	raw := rawState(t, schema, state)

	resp := resource.ReadResponse{State: tfsdk.State{Raw: raw, Schema: schema}}
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Raw: raw, Schema: schema}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Fatal("state was not removed when the grant returned no rows")
	}
}

func TestGrantPrivilegesReadCaseInsensitiveGrantee(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{
			{"grantee": "alice@example.com", "grantee_type": "USER", "privilege_type": "SELECT", "is_grantable": "NO"},
		}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	state := testState([]string{"SELECT"}, false)
	state.Grantee = types.StringValue("Alice@Example.com")
	state.GranteeType = types.StringValue("USER")

	result, _ := trinotest.Read(t, r, state)

	call := fake.Calls[0]
	if len(call.Args) != 5 {
		t.Fatalf("query args = %#v, want 5", call.Args)
	}
	if call.Args[3] != "Alice@Example.com" {
		t.Fatalf("grantee arg = %#v, want configured spelling Alice@Example.com", call.Args[3])
	}
	if !strings.Contains(call.Statement, "lower(grantee) = lower(?)") {
		t.Fatalf("query %q does not match grantees case-insensitively", call.Statement)
	}
	if result.Grantee.ValueString() != "Alice@Example.com" {
		t.Fatalf("state grantee = %q, want configured spelling", result.Grantee.ValueString())
	}
}

func TestGrantPrivilegesUpdateAddsAndRemoves(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{},
		{Rows: privilegeRows(false, "SELECT", "INSERT")},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	state := testState([]string{"SELECT"}, false)
	plan := testState([]string{"SELECT", "INSERT"}, false)

	updated, _ := trinotest.Update(t, r, state, plan)

	// The full desired set is granted, not just the addition, so the operation
	// is idempotent on metastores that replace a grantee's privileges per
	// statement (for example Trino's file metastore).
	want := `GRANT INSERT, SELECT ON TABLE "hive"."sales"."orders" TO ROLE "analyst"`
	if fake.Calls[0].Op != "exec" || fake.Calls[0].Statement != want {
		t.Fatalf("update call = %#v, want exec %q", fake.Calls[0], want)
	}
	execs := execStatements(fake)
	if len(execs) != 1 {
		t.Fatalf("update emitted %d statements, want 1: %v", len(execs), execs)
	}

	fake.Calls = nil
	fake.Steps = nil
	again, _ := trinotest.Update(t, r, updated, plan)

	if len(fake.Calls) != 0 {
		t.Fatalf("second apply emitted %d calls, want none: %#v", len(fake.Calls), fake.Calls)
	}
	assertPrivileges(t, again.Privileges, "INSERT", "SELECT")
}

func TestGrantPrivilegesUpdateGrantOption(t *testing.T) {
	t.Run("on", func(t *testing.T) {
		fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
			{},
			{Rows: privilegeRows(true, "SELECT")},
		}}
		r := New()
		trinotest.Configure(t, r, fake)

		_, _ = trinotest.Update(t, r, testState([]string{"SELECT"}, false), testState([]string{"SELECT"}, true))

		want := `GRANT SELECT ON TABLE "hive"."sales"."orders" TO ROLE "analyst" WITH GRANT OPTION`
		if fake.Calls[0].Statement != want {
			t.Fatalf("statement = %q, want %q", fake.Calls[0].Statement, want)
		}
	})

	t.Run("off", func(t *testing.T) {
		fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
			{},
			{},
			{Rows: privilegeRows(false, "SELECT")},
		}}
		r := New()
		trinotest.Configure(t, r, fake)

		_, _ = trinotest.Update(t, r, testState([]string{"SELECT"}, true), testState([]string{"SELECT"}, false))

		// Clearing the grant option is a two-step operation. Trino's file
		// metastore drops the privilege entirely on REVOKE GRANT OPTION FOR,
		// so the retained set is re-granted without the option; on a merge
		// metastore the re-grant is a harmless no-op.
		wantRevoke := `REVOKE GRANT OPTION FOR SELECT ON TABLE "hive"."sales"."orders" FROM ROLE "analyst"`
		if fake.Calls[0].Statement != wantRevoke {
			t.Fatalf("first statement = %q, want %q", fake.Calls[0].Statement, wantRevoke)
		}
		wantGrant := `GRANT SELECT ON TABLE "hive"."sales"."orders" TO ROLE "analyst"`
		if fake.Calls[1].Statement != wantGrant {
			t.Fatalf("second statement = %q, want %q", fake.Calls[1].Statement, wantGrant)
		}
		execs := execStatements(fake)
		if len(execs) != 2 {
			t.Fatalf("update emitted %d statements, want 2: %v", len(execs), execs)
		}
	})
}

func TestGrantPrivilegesRejectsAllPrivileges(t *testing.T) {
	ctx := trinotest.Ctx(t)
	r := New()
	schema := trinotest.SchemaOf(t, r)

	attribute, ok := schema.Attributes["privileges"].(resourceschema.SetAttribute)
	if !ok {
		t.Fatalf("privileges attribute is %T", schema.Attributes["privileges"])
	}

	value := stringSet("ALL PRIVILEGES")
	var diags diag.Diagnostics
	for _, v := range attribute.Validators {
		response := &validator.SetResponse{}
		v.ValidateSet(ctx, validator.SetRequest{Path: path.Root("privileges"), ConfigValue: value}, response)
		diags.Append(response.Diagnostics...)
	}

	if !diags.HasError() {
		t.Fatal("ALL PRIVILEGES was accepted")
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Detail(), "List SELECT, INSERT, UPDATE, DELETE explicitly") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics do not name the four privileges: %v", diags)
	}
}

func TestGrantPrivilegesDeleteSQL(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}}}
	r := New()
	trinotest.Configure(t, r, fake)

	trinotest.Delete(t, r, testState([]string{"INSERT", "SELECT"}, false))

	want := `REVOKE INSERT, SELECT ON TABLE "hive"."sales"."orders" FROM ROLE "analyst"`
	if len(fake.Calls) != 1 || fake.Calls[0].Statement != want {
		t.Fatalf("delete call = %#v, want %q", fake.Calls, want)
	}
}

// TestGrantPrivilegesDeleteClearsGrantOptionFirst covers the file metastore,
// which keys a table privilege by its grant-option flag: a plain REVOKE of a
// grant made WITH GRANT OPTION is a silent no-op there, so the option has to be
// cleared first. On a real Hive metastore the first statement only drops the
// option and the second removes the privilege.
func TestGrantPrivilegesDeleteClearsGrantOptionFirst(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}, {}}}
	r := New()
	trinotest.Configure(t, r, fake)

	trinotest.Delete(t, r, testState([]string{"INSERT", "SELECT"}, true))

	want := []string{
		`REVOKE GRANT OPTION FOR INSERT, SELECT ON TABLE "hive"."sales"."orders" FROM ROLE "analyst"`,
		`REVOKE INSERT, SELECT ON TABLE "hive"."sales"."orders" FROM ROLE "analyst"`,
	}
	execs := execStatements(fake)
	if len(execs) != len(want) {
		t.Fatalf("delete emitted %d statements, want %d: %v", len(execs), len(want), execs)
	}
	for i := range want {
		if execs[i] != want[i] {
			t.Fatalf("delete statement %d = %q, want %q", i, execs[i], want[i])
		}
	}
}

func TestGrantPrivilegesImportID(t *testing.T) {
	ctx := trinotest.Ctx(t)
	r := New()
	schema := trinotest.SchemaOf(t, r)
	importable := r.(resource.ResourceWithImportState)

	t.Run("valid", func(t *testing.T) {
		resp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
		importable.ImportState(ctx, resource.ImportStateRequest{ID: "hive.sales.orders:USER:Alice@Example.com"}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("import diagnostics: %v", resp.Diagnostics)
		}

		var state grantPrivilegesModel
		resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			t.Fatalf("state diagnostics: %v", resp.Diagnostics)
		}
		if state.On.Catalog.ValueString() != "hive" || state.On.Schema.ValueString() != "sales" || state.On.Table.ValueString() != "orders" {
			t.Fatalf("on = %+v", state.On)
		}
		if state.GranteeType.ValueString() != "USER" || state.Grantee.ValueString() != "Alice@Example.com" {
			t.Fatalf("grantee = %q/%q", state.GranteeType.ValueString(), state.Grantee.ValueString())
		}
		if state.ID.ValueString() != "hive.sales.orders:USER:Alice@Example.com" {
			t.Fatalf("id = %q", state.ID.ValueString())
		}
	})

	t.Run("invalid", func(t *testing.T) {
		resp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
		importable.ImportState(ctx, resource.ImportStateRequest{ID: "hive.sales.orders:BOGUS:alice"}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("invalid grantee_type was accepted")
		}
		if !strings.Contains(resp.Diagnostics[0].Summary(), "Invalid import ID") {
			t.Fatalf("summary = %q", resp.Diagnostics[0].Summary())
		}
	})
}

func TestGrantPrivilegesOnRequiresReplace(t *testing.T) {
	schema := trinotest.SchemaOf(t, New())

	on, ok := schema.Attributes["on"].(resourceschema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("on attribute is %T", schema.Attributes["on"])
	}
	assertReplaces(t, "on", on.PlanModifiers)

	for _, name := range []string{"catalog", "schema", "table"} {
		attribute, ok := on.Attributes[name].(resourceschema.StringAttribute)
		if !ok {
			t.Fatalf("on.%s attribute is %T", name, on.Attributes[name])
		}
		assertReplaces(t, "on."+name, attribute.PlanModifiers)
	}
}

func assertReplaces[T any](t *testing.T, name string, modifiers []T) {
	t.Helper()
	if len(modifiers) == 0 {
		t.Fatalf("%s has no plan modifiers", name)
	}
	for _, modifier := range modifiers {
		if strings.Contains(fmt.Sprintf("%T", modifier), "requiresReplace") {
			return
		}
	}
	t.Fatalf("%s plan modifiers do not require replacement: %v", name, modifiers)
}

func testModel(privileges []string, grantOption bool) grantPrivilegesModel {
	return grantPrivilegesModel{
		ID:          types.StringNull(),
		Privileges:  stringSet(privileges...),
		On:          testOn(),
		Grantee:     types.StringValue("analyst"),
		GranteeType: types.StringValue("ROLE"),
		GrantOption: types.BoolValue(grantOption),
	}
}

func testState(privileges []string, grantOption bool) grantPrivilegesModel {
	model := testModel(privileges, grantOption)
	model.ID = types.StringValue("hive.sales.orders:ROLE:analyst")
	return model
}

func testOn() OnModel {
	return OnModel{
		Catalog: types.StringValue("hive"),
		Schema:  types.StringValue("sales"),
		Table:   types.StringValue("orders"),
	}
}

func stringSet(values ...string) types.Set {
	elements := make([]attr.Value, len(values))
	for i, value := range values {
		elements[i] = types.StringValue(value)
	}
	return types.SetValueMust(types.StringType, elements)
}

func privilegeRows(grantOption bool, privileges ...string) []trino.Row {
	rows := make([]trino.Row, 0, len(privileges))
	for _, privilege := range privileges {
		grantable := "NO"
		if grantOption {
			grantable = "YES"
		}
		rows = append(rows, trino.Row{
			"grantee":        "analyst",
			"grantee_type":   "ROLE",
			"privilege_type": privilege,
			"is_grantable":   grantable,
		})
	}
	return rows
}

func assertPrivileges(t *testing.T, value types.Set, want ...string) {
	t.Helper()
	var diags diag.Diagnostics
	got := resourceutil.StringSet(context.Background(), value, &diags)
	if diags.HasError() {
		t.Fatalf("privileges diagnostics: %v", diags)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("privileges = %v, want %v", got, want)
	}
}

func execStatements(fake *trinotest.FakeExecutor) []string {
	var statements []string
	for _, call := range fake.Calls {
		if call.Op == "exec" {
			statements = append(statements, call.Statement)
		}
	}
	return statements
}

func rawState(t *testing.T, schema resourceschema.Schema, model grantPrivilegesModel) tftypes.Value {
	t.Helper()
	objectType, ok := schema.Type().(types.ObjectType)
	if !ok {
		t.Fatalf("schema type is %T", schema.Type())
	}
	value, diags := types.ObjectValueFrom(context.Background(), objectType.AttributeTypes(), model)
	if diags.HasError() {
		t.Fatalf("object value diagnostics: %v", diags)
	}
	raw, err := value.ToTerraformValue(context.Background())
	if err != nil {
		t.Fatalf("terraform value: %v", err)
	}
	return raw
}
