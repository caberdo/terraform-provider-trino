package grantrole

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

func baseModel() grantRoleModel {
	return grantRoleModel{
		ID:          types.StringNull(),
		Role:        types.StringValue("analyst"),
		RoleCatalog: types.StringValue("hive"),
		Grantee:     types.StringValue("alice"),
		GranteeType: types.StringValue("USER"),
		AdminOption: types.BoolValue(false),
	}
}

func assertSingleExec(t *testing.T, fake *trinotest.FakeExecutor, want string) {
	t.Helper()
	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(fake.Calls), fake.Calls)
	}
	call := fake.Calls[0]
	if call.Op != "exec" {
		t.Fatalf("expected exec, got %s", call.Op)
	}
	if call.Statement != want {
		t.Fatalf("statement mismatch\n got: %s\nwant: %s", call.Statement, want)
	}
	if len(call.Args) != 0 {
		t.Fatalf("expected no bound args, got %v", call.Args)
	}
}

func TestGrantRoleCreateSQL(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}}}
	resourceUnderTest := New()
	trinotest.Configure(t, resourceUnderTest, fake)

	state, _ := trinotest.Create(t, resourceUnderTest, baseModel())

	assertSingleExec(t, fake, `GRANT "analyst" TO USER "alice" IN "hive"`)
	if got, want := state.ID.ValueString(), "hive.analyst:USER:alice"; got != want {
		t.Fatalf("id = %q, want %q", got, want)
	}
}

func TestGrantRoleCreateWithAdmin(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}}}
	resourceUnderTest := New()
	trinotest.Configure(t, resourceUnderTest, fake)

	model := baseModel()
	model.AdminOption = types.BoolValue(true)
	state, _ := trinotest.Create(t, resourceUnderTest, model)

	assertSingleExec(t, fake, `GRANT "analyst" TO USER "alice" WITH ADMIN OPTION IN "hive"`)
	if !state.AdminOption.ValueBool() {
		t.Fatal("expected admin_option to be true in state")
	}
}

func TestGrantRoleUpdateAdminOn(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}}}
	resourceUnderTest := New()
	trinotest.Configure(t, resourceUnderTest, fake)

	state := baseModel()
	state.ID = types.StringValue("hive.analyst:USER:alice")
	plan := state
	plan.AdminOption = types.BoolValue(true)

	updated, _ := trinotest.Update(t, resourceUnderTest, state, plan)

	assertSingleExec(t, fake, `GRANT "analyst" TO USER "alice" WITH ADMIN OPTION IN "hive"`)
	if !updated.AdminOption.ValueBool() {
		t.Fatal("expected admin_option to be true in state")
	}
	if updated.ID.ValueString() != state.ID.ValueString() {
		t.Fatalf("id changed from %q to %q", state.ID.ValueString(), updated.ID.ValueString())
	}
}

func TestGrantRoleUpdateAdminOff(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}}}
	resourceUnderTest := New()
	trinotest.Configure(t, resourceUnderTest, fake)

	state := baseModel()
	state.ID = types.StringValue("hive.analyst:USER:alice")
	state.AdminOption = types.BoolValue(true)
	plan := state
	plan.AdminOption = types.BoolValue(false)

	updated, _ := trinotest.Update(t, resourceUnderTest, state, plan)

	assertSingleExec(t, fake, `REVOKE ADMIN OPTION FOR "analyst" FROM USER "alice" IN "hive"`)
	if updated.AdminOption.ValueBool() {
		t.Fatal("expected admin_option to be false in state")
	}
}

func TestGrantRoleDeleteSQL(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}}}
	resourceUnderTest := New()
	trinotest.Configure(t, resourceUnderTest, fake)

	state := baseModel()
	state.ID = types.StringValue("hive.analyst:USER:alice")

	trinotest.Delete(t, resourceUnderTest, state)

	assertSingleExec(t, fake, `REVOKE "analyst" FROM USER "alice" IN "hive"`)
}

func TestGrantRoleReadIsStateAuthoritative(t *testing.T) {
	fake := &trinotest.FakeExecutor{}
	resourceUnderTest := New()
	trinotest.Configure(t, resourceUnderTest, fake)

	state := baseModel()
	state.ID = types.StringValue("hive.analyst:USER:alice")

	read, _ := trinotest.Read(t, resourceUnderTest, state)

	if len(fake.Calls) != 0 {
		t.Fatalf("expected Read to issue no Trino calls, got %+v", fake.Calls)
	}
	if !reflect.DeepEqual(read, state) {
		t.Fatalf("state changed during Read\n got: %+v\nwant: %+v", read, state)
	}
}

func TestGrantRoleGranteeAllowsEmail(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}}}
	resourceUnderTest := New()
	trinotest.Configure(t, resourceUnderTest, fake)

	model := baseModel()
	model.Grantee = types.StringValue("alice@example.com")

	_, _ = trinotest.Create(t, resourceUnderTest, model)

	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.Calls))
	}
	if len(fake.Calls[0].Args) != 0 {
		t.Fatalf("expected no bound args, got %v", fake.Calls[0].Args)
	}
	if !strings.Contains(fake.Calls[0].Statement, `"alice@example.com"`) {
		t.Fatalf("statement does not quote the email grantee: %s", fake.Calls[0].Statement)
	}
}

func TestGrantRoleImportID(t *testing.T) {
	resourceUnderTest := New()

	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: trinotest.SchemaOf(t, resourceUnderTest)}}
	resourceUnderTest.(resource.ResourceWithImportState).ImportState(
		trinotest.Ctx(t),
		resource.ImportStateRequest{ID: "hive.analyst:USER:alice@example.com"},
		&resp,
	)
	if resp.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", resp.Diagnostics)
	}

	var state grantRoleModel
	if diags := resp.State.Get(trinotest.Ctx(t), &state); diags.HasError() {
		t.Fatalf("state diagnostics: %v", diags)
	}

	want := grantRoleModel{
		ID:          types.StringValue("hive.analyst:USER:alice@example.com"),
		Role:        types.StringValue("analyst"),
		RoleCatalog: types.StringValue("hive"),
		Grantee:     types.StringValue("alice@example.com"),
		GranteeType: types.StringValue("USER"),
		AdminOption: types.BoolValue(false),
	}
	if !reflect.DeepEqual(state, want) {
		t.Fatalf("imported state mismatch\n got: %+v\nwant: %+v", state, want)
	}
}

func TestGrantRoleAdminOptionInPlace(t *testing.T) {
	attr, ok := trinotest.SchemaOf(t, New()).Attributes["admin_option"].(resourceschema.BoolAttribute)
	if !ok {
		t.Fatal("admin_option is not a BoolAttribute")
	}
	if !attr.Optional || attr.Required || !attr.Computed {
		t.Fatalf("admin_option should be optional+computed, got %+v", attr)
	}
	for _, modifier := range attr.PlanModifiers {
		if strings.Contains(strings.ToLower(fmt.Sprintf("%T", modifier)), "requiresreplace") {
			t.Fatalf("admin_option must update in place but has modifier %T", modifier)
		}
	}
}
