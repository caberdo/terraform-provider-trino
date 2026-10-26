package view

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/caberdo/terraform-provider-trino/internal/trino"
	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

const (
	storedView = "CREATE VIEW hive.sales.orders_by_region SECURITY DEFINER AS\nSELECT\n  region\n, count(*)\nFROM\n  hive.sales.orders\nGROUP BY region"

	findStatement = `SELECT table_schema, table_name, table_type FROM "hive"."information_schema"."tables" ` +
		`WHERE lower(table_schema) = lower(?) AND lower(table_name) = lower(?)`
)

func newTestResource(t *testing.T, steps ...trinotest.Step) (resource.Resource, *trinotest.FakeExecutor) {
	t.Helper()
	fake := &trinotest.FakeExecutor{Steps: steps}
	r := New()
	trinotest.Configure(t, r, fake)
	return r, fake
}

func planModel() model {
	value := testModel()
	value.ID = types.StringUnknown()
	return value
}

func stateModel() model {
	value := testModel()
	value.ID = types.StringValue("hive.sales.orders_by_region")
	return value
}

func infoRow(schema, name, tableType string) trino.Row {
	return trino.Row{"table_schema": schema, "table_name": name, "table_type": tableType}
}

func viewRow() trinotest.Step {
	return trinotest.Step{Rows: []trino.Row{infoRow("sales", "orders_by_region", "VIEW")}}
}

func showCreateRow(ddl string) trinotest.Step {
	return trinotest.Step{Rows: []trino.Row{{"Create View": ddl}}}
}

func TestCreateIssuesCreateViewAndPreservesDefinition(t *testing.T) {
	r, fake := newTestResource(t,
		trinotest.Step{},
		viewRow(),
		showCreateRow(storedView),
	)

	plan := planModel()
	state, diags := trinotest.Create(t, r, plan)
	if diags.HasError() {
		t.Fatalf("create diagnostics: %v", diags)
	}

	if len(fake.Calls) != 3 || fake.Calls[0].Op != "exec" {
		t.Fatalf("calls = %+v, want an exec followed by the two reads", fake.Calls)
	}
	want := "CREATE VIEW \"hive\".\"sales\".\"orders_by_region\"\n" +
		"SECURITY DEFINER\n" +
		"AS\n" +
		"SELECT region, count(*) AS orders FROM hive.sales.orders GROUP BY region"
	if got := fake.Calls[0].Statement; got != want {
		t.Fatalf("create statement =\n%s\nwant\n%s", got, want)
	}
	if got := fake.Calls[1]; got.Statement != findStatement || len(got.Args) != 2 ||
		got.Args[0] != "sales" || got.Args[1] != "orders_by_region" {
		t.Fatalf("lookup call = %#v, want %q with the schema and name bound", got, findStatement)
	}
	if got, want := fake.Calls[2].Statement, `SHOW CREATE VIEW "hive"."sales"."orders_by_region"`; got != want {
		t.Fatalf("read statement = %q, want %q", got, want)
	}
	if got, want := state.ID.ValueString(), "hive.sales.orders_by_region"; got != want {
		t.Fatalf("state id = %q, want %q", got, want)
	}
	// The configured text is preserved even though the server normalises it.
	if got, want := state.Definition.ValueString(), plan.Definition.ValueString(); got != want {
		t.Fatalf("state definition = %q, want %q", got, want)
	}
	if got, want := state.Security.ValueString(), securityDefiner; got != want {
		t.Fatalf("state security = %q, want %q", got, want)
	}
}

func TestCreateSecurityInvoker(t *testing.T) {
	r, fake := newTestResource(t,
		trinotest.Step{},
		viewRow(),
		showCreateRow(storedView),
	)

	plan := planModel()
	plan.Security = types.StringValue("INVOKER")
	trinotest.Create(t, r, plan)

	if !strings.Contains(fake.Calls[0].Statement, "\nSECURITY INVOKER\n") {
		t.Fatalf("create statement = %q, want the INVOKER clause", fake.Calls[0].Statement)
	}
}

func TestCreateExistingIsActionable(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{Err: &trino.ErrTrino{ErrorName: "TABLE_ALREADY_EXISTS", Message: "View already exists"}},
	)
	_, diags := trinotest.CreateExpectError(t, r, planModel())
	if !diags.HasError() || diags[0].Summary() != "View already exists" {
		t.Fatalf("diagnostics = %v, want the already-exists diagnostic", diags)
	}
	if !strings.Contains(diags[0].Detail(), "Import it") {
		t.Fatalf("detail = %q, want import guidance", diags[0].Detail())
	}
}

func TestCreateRejectsEmptyDefinition(t *testing.T) {
	r, _ := newTestResource(t)
	plan := planModel()
	plan.Definition = types.StringValue("  ;  ")
	_, diags := trinotest.CreateExpectError(t, r, plan)
	if !diags.HasError() || diags[0].Summary() != "Invalid view definition" {
		t.Fatalf("diagnostics = %v, want an Invalid view definition error", diags)
	}
}

func TestCreateReportsMissingViewAfterCreate(t *testing.T) {
	r, _ := newTestResource(t, trinotest.Step{}, trinotest.Step{Rows: nil})
	_, diags := trinotest.CreateExpectError(t, r, planModel())
	if !diags.HasError() || !strings.Contains(diags[0].Summary(), "not found after create") {
		t.Fatalf("diagnostics = %v", diags)
	}
}

func TestReadAdoptsServerSecurityAndPreservesDefinition(t *testing.T) {
	r, fake := newTestResource(t,
		viewRow(),
		showCreateRow("CREATE VIEW hive.sales.orders_by_region SECURITY INVOKER AS SELECT region FROM hive.sales.orders GROUP BY region"),
	)

	state, diags := trinotest.Read(t, r, stateModel())
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if got, want := len(fake.Calls), 2; got != want {
		t.Fatalf("calls = %d, want %d", got, want)
	}
	if got, want := state.Security.ValueString(), securityInvoker; got != want {
		t.Fatalf("state security = %q, want the server mode %q", got, want)
	}
	if got, want := state.Definition.ValueString(), testModel().Definition.ValueString(); got != want {
		t.Fatalf("state definition = %q, want the configured text %q preserved", got, want)
	}
}

func TestReadUsesStoredSpellingFromInformationSchema(t *testing.T) {
	// Hive stores view and schema names lowercased and rejects a quoted
	// SHOW CREATE VIEW with a non-lowercase name, so the lookup returns the
	// stored spelling while state keeps the configured one.
	configured := stateModel()
	configured.Schema = types.StringValue("Sales")
	configured.Name = types.StringValue("Orders")
	configured.ID = types.StringValue("hive.Sales.Orders")

	r, fake := newTestResource(t,
		trinotest.Step{Rows: []trino.Row{infoRow("sales", "orders", "VIEW")}},
		showCreateRow(storedView),
	)

	state, diags := trinotest.Read(t, r, configured)
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if got, want := fake.Calls[1].Statement, `SHOW CREATE VIEW "hive"."sales"."orders"`; got != want {
		t.Fatalf("read statement = %q, want the stored spelling %q", got, want)
	}
	if got, want := state.ID.ValueString(), "hive.Sales.Orders"; got != want {
		t.Fatalf("state id = %q, want the configured identity %q", got, want)
	}
}

func TestReadMissingViewRemovesState(t *testing.T) {
	for name, step := range map[string]trinotest.Step{
		"no rows":         {Rows: nil},
		"base table":      {Rows: []trino.Row{infoRow("sales", "orders_by_region", "BASE TABLE")}},
		"typed not found": {Err: &trino.ErrTrino{ErrorName: "VIEW_NOT_FOUND", Message: "View does not exist"}},
		"message only":    {Err: errors.New(`View 'hive.sales.orders_by_region' does not exist`)},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := newTestResource(t, step)
			state, _ := trinotest.ReadExpectError(t, r, stateModel())
			if !state.ID.IsNull() && state.ID.ValueString() != "" {
				t.Fatalf("state id = %q, want null/empty after removal", state.ID.ValueString())
			}
		})
	}
}

func TestReadReportsPermissionDeniedWithoutRemovingState(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{Err: &trino.ErrTrino{ErrorName: "PERMISSION_DENIED", Message: "Access denied"}},
	)
	_, diags := trinotest.ReadExpectError(t, r, stateModel())
	if !diags.HasError() || diags[0].Summary() != "Permission denied" {
		t.Fatalf("diagnostics = %v, want a permission diagnostic", diags)
	}
}

func TestReadPreservesSecurityWhenOutputIsUnparseable(t *testing.T) {
	r, _ := newTestResource(t,
		viewRow(),
		showCreateRow("CREATE TABLE hive.sales.orders_by_region (a bigint)"),
	)
	state, diags := trinotest.Read(t, r, stateModel())
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if got, want := state.Security.ValueString(), securityDefiner; got != want {
		t.Fatalf("state security = %q, want the prior value %q preserved", got, want)
	}
}

func TestReadPreservesStateWhenShowCreateReturnsNoRows(t *testing.T) {
	r, _ := newTestResource(t, viewRow(), trinotest.Step{Rows: nil})
	state, diags := trinotest.Read(t, r, stateModel())
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if got, want := state.Security.ValueString(), securityDefiner; got != want {
		t.Fatalf("state security = %q, want the prior value %q preserved", got, want)
	}
}

func TestReadReportsUnreadableMetadata(t *testing.T) {
	r, _ := newTestResource(t, trinotest.Step{Rows: []trino.Row{{"table_schema": "sales"}}})
	_, diags := trinotest.ReadExpectError(t, r, stateModel())
	if !diags.HasError() || diags[0].Summary() != "Could not read view metadata" {
		t.Fatalf("diagnostics = %v, want a metadata diagnostic", diags)
	}
}

func TestReadReportsAmbiguousStoredSpelling(t *testing.T) {
	r, _ := newTestResource(t, trinotest.Step{Rows: []trino.Row{
		infoRow("sales", "Orders_by_region", "VIEW"),
		infoRow("sales", "ORDERS_BY_REGION", "VIEW"),
	}})
	configured := stateModel()
	configured.Name = types.StringValue("orders_by_REGION")
	_, diags := trinotest.ReadExpectError(t, r, configured)
	if !diags.HasError() || diags[0].Summary() != "View name is ambiguous" {
		t.Fatalf("diagnostics = %v, want an ambiguity diagnostic", diags)
	}
}

func TestUpdateIssuesCreateOrReplace(t *testing.T) {
	r, fake := newTestResource(t,
		trinotest.Step{},
		viewRow(),
		showCreateRow(storedView),
	)

	state := stateModel()
	plan := stateModel()
	plan.Definition = types.StringValue("SELECT region FROM hive.sales.orders GROUP BY region")
	plan.Security = types.StringValue(securityInvoker)

	result, diags := trinotest.Update(t, r, state, plan)
	if diags.HasError() {
		t.Fatalf("update diagnostics: %v", diags)
	}

	want := "CREATE OR REPLACE VIEW \"hive\".\"sales\".\"orders_by_region\"\n" +
		"SECURITY INVOKER\n" +
		"AS\n" +
		"SELECT region FROM hive.sales.orders GROUP BY region"
	if got := fake.Calls[0].Statement; got != want {
		t.Fatalf("update statement =\n%s\nwant\n%s", got, want)
	}
	if got, want := result.Definition.ValueString(), plan.Definition.ValueString(); got != want {
		t.Fatalf("state definition = %q, want %q", got, want)
	}
}

func TestUpdateReportsMissingView(t *testing.T) {
	r, _ := newTestResource(t, trinotest.Step{}, trinotest.Step{Rows: nil})
	state := stateModel()
	plan := stateModel()
	plan.Definition = types.StringValue("SELECT 1")
	_, diags := trinotest.UpdateExpectError(t, r, state, plan)
	if !diags.HasError() || !strings.Contains(diags[0].Summary(), "not found after update") {
		t.Fatalf("diagnostics = %v", diags)
	}
}

func TestDeleteIssuesDropAndIgnoresMissing(t *testing.T) {
	r, fake := newTestResource(t, trinotest.Step{})
	trinotest.Delete(t, r, stateModel())
	want := `DROP VIEW IF EXISTS "hive"."sales"."orders_by_region"`
	if got := fake.Calls[0].Statement; got != want {
		t.Fatalf("drop statement = %q, want %q", got, want)
	}

	r, _ = newTestResource(t, trinotest.Step{Err: &trino.ErrTrino{ErrorName: "VIEW_NOT_FOUND", Message: "View does not exist"}})
	if diags := trinotest.DeleteExpectError(t, r, stateModel()); diags.HasError() {
		t.Fatalf("diagnostics = %v, want not-found to be ignored", diags)
	}

	r, _ = newTestResource(t, trinotest.Step{Err: &trino.ErrTrino{ErrorName: "PERMISSION_DENIED", Message: "Access denied"}})
	if diags := trinotest.DeleteExpectError(t, r, stateModel()); !diags.HasError() {
		t.Fatal("expected a drop diagnostic")
	}
}

func TestImportState(t *testing.T) {
	r, _ := newTestResource(t)

	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: trinotest.SchemaOf(t, r)}}
	r.(resource.ResourceWithImportState).ImportState(trinotest.Ctx(t), resource.ImportStateRequest{ID: "hive.sales.orders_by_region"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("diagnostics: %v", resp.Diagnostics)
	}
	var state model
	if diags := resp.State.Get(trinotest.Ctx(t), &state); diags.HasError() {
		t.Fatalf("state diagnostics: %v", diags)
	}
	if got, want := state.ID.ValueString(), "hive.sales.orders_by_region"; got != want {
		t.Fatalf("id = %q, want %q", got, want)
	}
	// The definition cannot be recovered from SHOW CREATE VIEW, so the import
	// leaves it for configuration to supply.
	if !state.Definition.IsNull() {
		t.Fatalf("definition = %q, want null after import", state.Definition.ValueString())
	}
	if !state.Security.IsNull() {
		t.Fatalf("security = %q, want null until the first read", state.Security.ValueString())
	}

	resp = resource.ImportStateResponse{State: tfsdk.State{Schema: trinotest.SchemaOf(t, r)}}
	r.(resource.ResourceWithImportState).ImportState(trinotest.Ctx(t), resource.ImportStateRequest{ID: "hive.sales"}, &resp)
	if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != invalidImportID {
		t.Fatalf("diagnostics = %v, want an invalid import ID error", resp.Diagnostics)
	}
}

func TestValidateConfig(t *testing.T) {
	r, _ := newTestResource(t)

	valid := planModel()
	if diags := validateConfig(t, r, valid); diags.HasError() {
		t.Fatalf("valid model diagnostics: %v", diags)
	}

	empty := planModel()
	empty.Definition = types.StringValue(" ")
	diags := validateConfig(t, r, empty)
	if !diags.HasError() || diags[0].Summary() != "Invalid view definition" {
		t.Fatalf("diagnostics = %v, want an Invalid view definition error", diags)
	}
}

func validateConfig(t *testing.T, r resource.Resource, value model) diag.Diagnostics {
	t.Helper()
	schema := trinotest.SchemaOf(t, r)
	objectType, ok := schema.Type().(types.ObjectType)
	if !ok {
		t.Fatal("the schema is not a top-level object")
	}
	object, objectDiags := types.ObjectValueFrom(trinotest.Ctx(t), objectType.AttributeTypes(), value)
	if objectDiags.HasError() {
		t.Fatalf("encode config: %v", objectDiags)
	}
	raw, err := object.ToTerraformValue(trinotest.Ctx(t))
	if err != nil {
		t.Fatalf("encode raw config: %v", err)
	}

	resp := resource.ValidateConfigResponse{}
	r.(resource.ResourceWithValidateConfig).ValidateConfig(context.Background(), resource.ValidateConfigRequest{
		Config: tfsdk.Config{Raw: raw, Schema: schema},
	}, &resp)
	return resp.Diagnostics
}
