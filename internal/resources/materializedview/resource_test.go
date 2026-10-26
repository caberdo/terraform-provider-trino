package materializedview

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
	value.ID = types.StringValue("iceberg.sales.orders_by_region")
	return value
}

func mustMap(t *testing.T, values map[string]string) types.Map {
	t.Helper()
	value, diags := types.MapValueFrom(trinotest.Ctx(t), types.StringType, values)
	if diags.HasError() {
		t.Fatalf("map value diagnostics: %v", diags)
	}
	return value
}

func rawValue(t *testing.T, r resource.Resource, value model) (tfsdk.State, diag.Diagnostics) {
	t.Helper()
	schema := trinotest.SchemaOf(t, r)
	objectType, ok := schema.Type().(types.ObjectType)
	if !ok {
		t.Fatalf("schema type = %T, want types.ObjectType", schema.Type())
	}
	object, diags := types.ObjectValueFrom(trinotest.Ctx(t), objectType.AttributeTypes(), value)
	if diags.HasError() {
		return tfsdk.State{}, diags
	}
	raw, err := object.ToTerraformValue(trinotest.Ctx(t))
	if err != nil {
		t.Fatalf("encode value: %v", err)
	}
	return tfsdk.State{Raw: raw, Schema: schema}, diags
}

func readState(t *testing.T, r resource.Resource, state model) (tfsdk.State, diag.Diagnostics) {
	t.Helper()
	raw, diags := rawValue(t, r, state)
	if diags.HasError() {
		return tfsdk.State{}, diags
	}
	resp := resource.ReadResponse{State: tfsdk.State{Schema: raw.Schema}}
	r.Read(trinotest.Ctx(t), resource.ReadRequest{State: raw}, &resp)
	return resp.State, resp.Diagnostics
}

func TestCreateRunsRefreshAndReadsBack(t *testing.T) {
	r, fake := newTestResource(t,
		trinotest.Step{},
		trinotest.Step{},
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
	)

	state, _ := trinotest.Create(t, r, planModel())

	wantCreate := "CREATE MATERIALIZED VIEW \"iceberg\".\"sales\".\"orders_by_region\"\nAS\n" +
		"SELECT region, count(*) AS orders FROM iceberg.sales.orders GROUP BY region"
	if got := fake.Calls[0].Statement; got != wantCreate {
		t.Fatalf("create SQL = %q, want %q", got, wantCreate)
	}
	if got, want := fake.Calls[1].Statement, `REFRESH MATERIALIZED VIEW "iceberg"."sales"."orders_by_region"`; got != want {
		t.Fatalf("refresh SQL = %q, want %q", got, want)
	}
	if got := fake.Calls[2].Args; len(got) != 3 || got[0] != "iceberg" || got[1] != "sales" || got[2] != "orders_by_region" {
		t.Fatalf("find args = %v", got)
	}
	if got := state.ID.ValueString(); got != "iceberg.sales.orders_by_region" {
		t.Fatalf("id = %q", got)
	}
	if got := state.Query.ValueString(); !strings.Contains(got, "SELECT region") {
		t.Fatalf("query = %q, want the configured text", got)
	}
}

func TestCreateCanSkipRefresh(t *testing.T) {
	r, fake := newTestResource(t,
		trinotest.Step{},
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
	)

	plan := planModel()
	plan.RefreshOnCreate = types.BoolValue(false)
	state, _ := trinotest.Create(t, r, plan)

	if len(fake.Calls) != 2 {
		t.Fatalf("calls = %d, want create and read: %+v", len(fake.Calls), fake.Calls)
	}
	if fake.Calls[0].Op != "exec" || fake.Calls[1].Op != "query" {
		t.Fatalf("calls = %+v, want an exec followed by a query", fake.Calls)
	}
	if state.RefreshOnCreate.ValueBool() {
		t.Fatalf("refresh_on_create = true, want false")
	}
}

func TestCreateWithGracePeriodAndProperties(t *testing.T) {
	r, fake := newTestResource(t,
		trinotest.Step{},
		trinotest.Step{},
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
	)

	plan := planModel()
	plan.GracePeriod = types.StringValue("1h")
	plan.Properties = mustMap(t, map[string]string{"format": "'ORC'"})
	trinotest.Create(t, r, plan)

	want := "CREATE MATERIALIZED VIEW \"iceberg\".\"sales\".\"orders_by_region\"\n" +
		"GRACE PERIOD INTERVAL '3600' SECOND\n" +
		"WITH (\n  \"format\" = 'ORC'\n)\nAS\n" +
		"SELECT region, count(*) AS orders FROM iceberg.sales.orders GROUP BY region"
	if got := fake.Calls[0].Statement; got != want {
		t.Fatalf("create SQL =\n%s\nwant\n%s", got, want)
	}
}

func TestCreateRejectsEmptyQuery(t *testing.T) {
	r, _ := newTestResource(t)
	plan := planModel()
	plan.Query = types.StringValue("  ;  ")
	_, diags := trinotest.CreateExpectError(t, r, plan)
	if !diags.HasError() || !strings.Contains(diags[0].Summary(), "Invalid query") {
		t.Fatalf("diagnostics = %v, want an Invalid query error", diags)
	}
}

func TestCreateAlreadyExistsDiagnostic(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{Err: &trino.ErrTrino{ErrorName: "ALREADY_EXISTS", Message: "Materialized view already exists"}},
	)
	_, diags := trinotest.CreateExpectError(t, r, planModel())
	if !diags.HasError() || diags[0].Summary() != "Materialized view already exists" {
		t.Fatalf("diagnostics = %v, want the already-exists diagnostic", diags)
	}
}

func TestCreateUnsupportedConnectorDiagnostic(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{Err: &trino.ErrTrino{
			ErrorName: "NOT_SUPPORTED",
			Message:   "This connector does not support materialized views",
		}},
		trinotest.Step{Rows: []trino.Row{{"connector_name": "tpch"}}},
	)
	_, diags := trinotest.CreateExpectError(t, r, planModel())
	if !diags.HasError() || diags[0].Summary() != "Materialized views are not supported" {
		t.Fatalf("diagnostics = %v, want the unsupported-connector diagnostic", diags)
	}
	if !strings.Contains(diags[0].Detail(), `"tpch"`) {
		t.Fatalf("detail = %q, want the connector name", diags[0].Detail())
	}
}

func TestCreateGracePeriodNotSupportedIsNotMislabelled(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{Err: &trino.ErrTrino{
			ErrorName: "NOT_SUPPORTED",
			Message:   "Catalog 'iceberg' does not support GRACE PERIOD",
		}},
	)
	_, diags := trinotest.CreateExpectError(t, r, planModel())
	if !diags.HasError() {
		t.Fatalf("expected a diagnostic")
	}
	if strings.Contains(diags[0].Summary(), "Materialized views") {
		t.Fatalf("diagnostics = %v, want the generic not-supported diagnostic", diags)
	}
}

func TestCreateReportsMissingViewAfterCreate(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{},
		trinotest.Step{},
		trinotest.Step{Rows: nil},
	)
	_, diags := trinotest.CreateExpectError(t, r, planModel())
	if !diags.HasError() || !strings.Contains(diags[0].Summary(), "not found after create") {
		t.Fatalf("diagnostics = %v", diags)
	}
}

func TestCreateRefreshFailureReportsAndKeepsState(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{},
		trinotest.Step{Err: errors.New("refresh timed out")},
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
	)

	schema := trinotest.SchemaOf(t, r)
	raw, diags := rawValue(t, r, planModel())
	if diags.HasError() {
		t.Fatal(diags)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(trinotest.Ctx(t), resource.CreateRequest{Plan: tfsdk.Plan{Raw: raw.Raw, Schema: schema}}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected the refresh failure to surface")
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state is null; the created view would be lost and the retry would fail with ALREADY_EXISTS")
	}
}

func TestReadPreservesConfiguredQuery(t *testing.T) {
	r, fake := newTestResource(t,
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
	)
	state, _ := trinotest.Read(t, r, stateModel())

	if len(fake.Calls) != 1 {
		t.Fatalf("calls = %+v, want only the existence query", fake.Calls)
	}
	if got := state.Query.ValueString(); got != testModel().Query.ValueString() {
		t.Fatalf("query = %q, want the configured text", got)
	}
	if got := state.ID.ValueString(); got != "iceberg.sales.orders_by_region" {
		t.Fatalf("id = %q", got)
	}
}

func TestReadImportsDefinitionFromShowCreate(t *testing.T) {
	const definition = "CREATE MATERIALIZED VIEW iceberg.sales.orders_by_region\nAS\nSELECT 1"
	r, fake := newTestResource(t,
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
		trinotest.Step{Rows: []trino.Row{{"create_materialized_view": definition}}},
	)

	state := stateModel()
	state.Query = types.StringNull()
	result, _ := trinotest.Read(t, r, state)

	if got := fake.Calls[1].Statement; got != `SHOW CREATE MATERIALIZED VIEW "iceberg"."sales"."orders_by_region"` {
		t.Fatalf("SHOW CREATE statement = %q", got)
	}
	if got := result.Query.ValueString(); got != "SELECT 1" {
		t.Fatalf("query = %q, want SELECT 1", got)
	}
}

func TestReadRemovesMissingView(t *testing.T) {
	r, _ := newTestResource(t, trinotest.Step{Rows: nil})
	state, diags := readState(t, r, stateModel())
	if diags.HasError() {
		t.Fatalf("diagnostics: %v", diags)
	}
	if !state.Raw.IsNull() {
		t.Fatal("state was not removed for a missing materialized view")
	}
}

func TestReadReportsQueryFailure(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{Err: errors.New("coordinator unavailable")},
	)
	_, diags := trinotest.ReadExpectError(t, r, stateModel())
	if !diags.HasError() {
		t.Fatal("expected a read diagnostic")
	}
}

func TestReadTreatsMissingCatalogAsRemoved(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{Err: &trino.ErrTrino{ErrorName: "CATALOG_NOT_FOUND", Message: "Catalog not found"}},
	)
	state, diags := readState(t, r, stateModel())
	if diags.HasError() {
		t.Fatalf("diagnostics: %v", diags)
	}
	if !state.Raw.IsNull() {
		t.Fatal("state was not removed for a missing catalog")
	}
}

func TestUpdateRefreshesOnlyWhenTokenChanges(t *testing.T) {
	r, fake := newTestResource(t,
		trinotest.Step{},
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
	)

	state := stateModel()
	state.RefreshToken = types.StringValue("1")
	plan := stateModel()
	plan.RefreshToken = types.StringValue("2")
	result, _ := trinotest.Update(t, r, state, plan)

	if len(fake.Calls) != 2 || fake.Calls[0].Op != "exec" || !strings.HasPrefix(fake.Calls[0].Statement, "REFRESH MATERIALIZED VIEW ") {
		t.Fatalf("calls = %+v, want a refresh followed by a read", fake.Calls)
	}
	if got := result.RefreshToken.ValueString(); got != "2" {
		t.Fatalf("refresh_token = %q, want 2", got)
	}
}

func TestUpdateWithoutTokenChangeEmitsNoSQL(t *testing.T) {
	r, fake := newTestResource(t,
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
	)

	state := stateModel()
	plan := stateModel()
	plan.RefreshOnCreate = types.BoolValue(true)
	trinotest.Update(t, r, state, plan)

	if len(fake.Calls) != 1 || fake.Calls[0].Op != "query" {
		t.Fatalf("calls = %+v, want one read", fake.Calls)
	}
}

func TestUpdateReportsMissingView(t *testing.T) {
	r, _ := newTestResource(t, trinotest.Step{Rows: nil})
	state := stateModel()
	plan := stateModel()
	_, diags := trinotest.UpdateExpectError(t, r, state, plan)
	if !diags.HasError() || !strings.Contains(diags[0].Summary(), "not found after update") {
		t.Fatalf("diagnostics = %v", diags)
	}
}

func TestDeleteIssuesDropAndIgnoresNotFound(t *testing.T) {
	r, fake := newTestResource(t, trinotest.Step{})
	trinotest.Delete(t, r, stateModel())
	if got, want := fake.Calls[0].Statement, `DROP MATERIALIZED VIEW IF EXISTS "iceberg"."sales"."orders_by_region"`; got != want {
		t.Fatalf("drop SQL = %q, want %q", got, want)
	}

	r, _ = newTestResource(t, trinotest.Step{Err: &trino.ErrTrino{ErrorName: "TABLE_NOT_FOUND", Message: "not found"}})
	if diags := trinotest.DeleteExpectError(t, r, stateModel()); diags.HasError() {
		t.Fatalf("diagnostics = %v, want not-found to be ignored", diags)
	}

	r, _ = newTestResource(t, trinotest.Step{Err: errors.New("permission denied")})
	if diags := trinotest.DeleteExpectError(t, r, stateModel()); !diags.HasError() {
		t.Fatal("expected a drop diagnostic")
	}
}

func TestImportState(t *testing.T) {
	r, _ := newTestResource(t)
	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: trinotest.SchemaOf(t, r)}}
	r.(resource.ResourceWithImportState).ImportState(trinotest.Ctx(t), resource.ImportStateRequest{ID: "iceberg.sales.orders_by_region"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("diagnostics: %v", resp.Diagnostics)
	}
	var state model
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("state diagnostics: %v", diags)
	}
	if state.ID.ValueString() != "iceberg.sales.orders_by_region" {
		t.Fatalf("id = %q", state.ID.ValueString())
	}
	if !state.Query.IsNull() || !state.Properties.IsNull() || !state.GracePeriod.IsNull() ||
		!state.RefreshOnCreate.IsNull() || !state.RefreshToken.IsNull() {
		t.Fatalf("imported state should leave unreadable attributes null: %+v", state)
	}

	resp = resource.ImportStateResponse{State: tfsdk.State{Schema: trinotest.SchemaOf(t, r)}}
	r.(resource.ResourceWithImportState).ImportState(trinotest.Ctx(t), resource.ImportStateRequest{ID: "iceberg"}, &resp)
	if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != invalidImportID {
		t.Fatalf("diagnostics = %v, want an invalid import ID error", resp.Diagnostics)
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model)
		want   string
	}{
		{name: "valid", mutate: func(*model) {}},
		{
			name: "empty query",
			mutate: func(value *model) {
				value.Query = types.StringValue(";")
			},
			want: "Invalid query",
		},
		{
			name: "bad grace period",
			mutate: func(value *model) {
				value.GracePeriod = types.StringValue("soon")
			},
			want: "Invalid grace period",
		},
		{
			name: "empty property value",
			mutate: func(value *model) {
				value.Properties = mustMap(t, map[string]string{"format": "  "})
			},
			want: "Invalid property",
		},
		{
			name: "empty property name",
			mutate: func(value *model) {
				value.Properties = mustMap(t, map[string]string{"": "'ORC'"})
			},
			want: "Invalid property",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newTestResource(t)
			value := planModel()
			tc.mutate(&value)

			raw, diags := rawValue(t, r, value)
			if diags.HasError() {
				t.Fatal(diags)
			}
			resp := resource.ValidateConfigResponse{}
			r.(resource.ResourceWithValidateConfig).ValidateConfig(trinotest.Ctx(t), resource.ValidateConfigRequest{
				Config: tfsdk.Config(raw),
			}, &resp)

			if tc.want == "" {
				if resp.Diagnostics.HasError() {
					t.Fatalf("diagnostics: %v", resp.Diagnostics)
				}
				return
			}
			if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != tc.want {
				t.Fatalf("diagnostics = %v, want %q", resp.Diagnostics, tc.want)
			}
		})
	}
}

func TestReadPreservesNullQueryWhenShowCreateIsUnreadable(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
		trinotest.Step{Rows: []trino.Row{{"other": "value"}}},
	)
	state := stateModel()
	state.Query = types.StringNull()
	result, diags := trinotest.Read(t, r, state)
	if diags.HasError() {
		t.Fatalf("diagnostics: %v", diags)
	}
	if !result.Query.IsNull() {
		t.Fatalf("query = %q, want it preserved as null", result.Query.ValueString())
	}

	r, _ = newTestResource(t,
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
		trinotest.Step{Rows: []trino.Row{{"definition": "not a create statement"}}},
	)
	state.Query = types.StringNull()
	result, diags = trinotest.Read(t, r, state)
	if diags.HasError() {
		t.Fatalf("diagnostics: %v", diags)
	}
	if !result.Query.IsNull() {
		t.Fatalf("query = %q, want it preserved as null", result.Query.ValueString())
	}
}

func TestReadRejectsRowWithoutName(t *testing.T) {
	r, _ := newTestResource(t, trinotest.Step{Rows: []trino.Row{{"freshness": "FRESH"}}})
	_, diags := trinotest.ReadExpectError(t, r, stateModel())
	if !diags.HasError() || !strings.Contains(diags[0].Summary(), "Could not read materialized view name") {
		t.Fatalf("diagnostics = %v", diags)
	}
}

func TestConfigureIgnoresMissingProviderData(t *testing.T) {
	r := New()
	var resp resource.ConfigureResponse
	r.(resource.ResourceWithConfigure).Configure(trinotest.Ctx(t), resource.ConfigureRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("diagnostics: %v", resp.Diagnostics)
	}
}

func TestReadReconcilesConfiguredProperties(t *testing.T) {
	const definition = "CREATE MATERIALIZED VIEW iceberg.sales.orders_by_region\nWITH (\n" +
		"  \"format\" = 'ORC',\n  \"location\" = 's3://bucket/other',\n  \"format_version\" = 2\n)\nAS\nSELECT 1"
	r, fake := newTestResource(t,
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
		trinotest.Step{Rows: []trino.Row{{"create_materialized_view": definition}}},
	)

	state := stateModel()
	state.Properties = mustMap(t, map[string]string{"format": "'ORC'", "location": "'s3://bucket/orders'"})
	result, diags := trinotest.Read(t, r, state)
	if diags.HasError() {
		t.Fatalf("diagnostics: %v", diags)
	}
	if len(fake.Calls) != 2 {
		t.Fatalf("calls = %+v, want the existence query and SHOW CREATE", fake.Calls)
	}

	properties := map[string]string{}
	if diags := result.Properties.ElementsAs(trinotest.Ctx(t), &properties, false); diags.HasError() {
		t.Fatal(diags)
	}
	if properties["format"] != "'ORC'" {
		t.Fatalf("format = %q, want the configured equivalent", properties["format"])
	}
	if properties["location"] != "'s3://bucket/other'" {
		t.Fatalf("location = %q, want the server value to surface as drift", properties["location"])
	}
	if _, ok := properties["format_version"]; ok {
		t.Fatalf("derived property leaked into state: %v", properties)
	}
}

func TestReadDropsConfiguredPropertyMissingOnServer(t *testing.T) {
	const definition = "CREATE MATERIALIZED VIEW iceberg.sales.orders_by_region\nWITH (\"format\" = 'ORC')\nAS\nSELECT 1"
	r, _ := newTestResource(t,
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
		trinotest.Step{Rows: []trino.Row{{"create_materialized_view": definition}}},
	)

	state := stateModel()
	state.Properties = mustMap(t, map[string]string{"format": "'ORC'", "location": "'s3://bucket/orders'"})
	result, diags := trinotest.Read(t, r, state)
	if diags.HasError() {
		t.Fatalf("diagnostics: %v", diags)
	}
	properties := map[string]string{}
	if diags := result.Properties.ElementsAs(trinotest.Ctx(t), &properties, false); diags.HasError() {
		t.Fatal(diags)
	}
	if len(properties) != 1 || properties["format"] != "'ORC'" {
		t.Fatalf("properties = %v, want only the property the server reports", properties)
	}
}

func TestReadKeepsConfiguredPropertyWhenEquivalent(t *testing.T) {
	const definition = "CREATE MATERIALIZED VIEW iceberg.sales.orders_by_region\nWITH (partitioning = ARRAY['region', 'day'])\nAS\nSELECT 1"
	r, _ := newTestResource(t,
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
		trinotest.Step{Rows: []trino.Row{{"create_materialized_view": definition}}},
	)

	state := stateModel()
	state.Properties = mustMap(t, map[string]string{"partitioning": "array[ 'region' ,'day' ]"})
	result, diags := trinotest.Read(t, r, state)
	if diags.HasError() {
		t.Fatalf("diagnostics: %v", diags)
	}
	properties := map[string]string{}
	if diags := result.Properties.ElementsAs(trinotest.Ctx(t), &properties, false); diags.HasError() {
		t.Fatal(diags)
	}
	if properties["partitioning"] != "array[ 'region' ,'day' ]" {
		t.Fatalf("partitioning = %q, want the configured spelling", properties["partitioning"])
	}
}

func TestReadPreservesPropertiesWhenDefinitionIsUnreadable(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
		trinotest.Step{Rows: []trino.Row{{"definition": "not a create statement"}}},
	)
	state := stateModel()
	state.Properties = mustMap(t, map[string]string{"format": "'ORC'"})
	result, diags := trinotest.Read(t, r, state)
	if diags.HasError() {
		t.Fatalf("diagnostics: %v", diags)
	}
	properties := map[string]string{}
	if diags := result.Properties.ElementsAs(trinotest.Ctx(t), &properties, false); diags.HasError() {
		t.Fatal(diags)
	}
	if properties["format"] != "'ORC'" {
		t.Fatalf("properties = %v, want the prior state", properties)
	}
}

func TestReadRemovesViewDroppedBeforeShowCreate(t *testing.T) {
	r, _ := newTestResource(t,
		trinotest.Step{Rows: []trino.Row{{"name": "orders_by_region"}}},
		trinotest.Step{Err: &trino.ErrTrino{ErrorName: "TABLE_NOT_FOUND", Message: "Materialized view does not exist"}},
	)
	state := stateModel()
	state.Properties = mustMap(t, map[string]string{"format": "'ORC'"})
	result, diags := readState(t, r, state)
	if diags.HasError() {
		t.Fatalf("diagnostics: %v", diags)
	}
	if !result.Raw.IsNull() {
		t.Fatal("state was not removed after SHOW CREATE reported not found")
	}
}
