package catalog

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/caberdo/terraform-provider-trino/internal/trino"
	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

func testStringMap(values map[string]string) types.Map {
	elements := make(map[string]attr.Value, len(values))
	for key, value := range values {
		elements[key] = types.StringValue(value)
	}
	return types.MapValueMust(types.StringType, elements)
}

func testCatalogModel(name, connector string) catalogModel {
	return catalogModel{
		ID:        types.StringValue(name),
		Name:      types.StringValue(name),
		Connector: types.StringValue(connector),
		Properties: testStringMap(map[string]string{
			"iceberg.catalog.type": "rest",
			"iceberg.rest.uri":     "http://iceberg:8181",
		}),
		SensitiveProperties: testStringMap(map[string]string{
			"iceberg.rest.oauth2.credential": "secret",
		}),
	}
}

func TestCatalogCreateEmitsSQL(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{},
		{},
		{Rows: []trino.Row{{"catalog_name": "lakehouse", "connector_name": "iceberg"}}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	plan := catalogModel{
		Name:      types.StringValue("lakehouse"),
		Connector: types.StringValue("iceberg"),
		Properties: testStringMap(map[string]string{
			"iceberg.rest.uri":     "http://iceberg:8181",
			"iceberg.catalog.type": "rest",
			`weird"key`:            "va'lue",
		}),
		SensitiveProperties: testStringMap(map[string]string{
			"iceberg.rest.oauth2.credential": "secret",
		}),
	}

	state, diags := trinotest.Create(t, r, plan)
	if diags.HasError() {
		t.Fatalf("create diagnostics: %v", diags)
	}

	if len(fake.Calls) != 3 {
		t.Fatalf("got %d calls, want 3: %+v", len(fake.Calls), fake.Calls)
	}

	if fake.Calls[0].Op != "query" {
		t.Fatalf("first call op = %q, want query", fake.Calls[0].Op)
	}
	if got := fake.Calls[0].Statement; got != catalogLookupSQL {
		t.Fatalf("existence statement = %q", got)
	}
	if want := []any{"lakehouse"}; !reflect.DeepEqual(fake.Calls[0].Args, want) {
		t.Fatalf("existence args = %#v, want %#v", fake.Calls[0].Args, want)
	}

	// The catalog name is a quoted identifier, but the connector name must be
	// emitted bare: Trino rejects `USING "iceberg"` with
	// `Invalid connector name: "iceberg"` (see buildCreateStatement).
	wantCreate := `CREATE CATALOG "lakehouse" USING iceberg WITH (` +
		`"iceberg.catalog.type" = 'rest', ` +
		`"iceberg.rest.oauth2.credential" = 'secret', ` +
		`"iceberg.rest.uri" = 'http://iceberg:8181', ` +
		`"weird""key" = 'va''lue')`
	if fake.Calls[1].Op != "exec" {
		t.Fatalf("second call op = %q, want exec", fake.Calls[1].Op)
	}
	if got := fake.Calls[1].Statement; got != wantCreate {
		t.Fatalf("create statement =\n%q\nwant\n%q", got, wantCreate)
	}

	if fake.Calls[2].Op != "query" {
		t.Fatalf("third call op = %q, want query", fake.Calls[2].Op)
	}
	if got := fake.Calls[2].Statement; got != catalogLookupSQL {
		t.Fatalf("read-back statement = %q", got)
	}
	if want := []any{"lakehouse"}; !reflect.DeepEqual(fake.Calls[2].Args, want) {
		t.Fatalf("read-back args = %#v, want %#v", fake.Calls[2].Args, want)
	}

	if state.ID.ValueString() != "lakehouse" {
		t.Fatalf("state id = %q", state.ID.ValueString())
	}
	if state.Name.ValueString() != "lakehouse" {
		t.Fatalf("state name = %q", state.Name.ValueString())
	}
	if state.Connector.ValueString() != "iceberg" {
		t.Fatalf("state connector = %q", state.Connector.ValueString())
	}
	if !state.Properties.Equal(plan.Properties) {
		t.Fatalf("state properties = %v, want %v", state.Properties, plan.Properties)
	}
	if !state.SensitiveProperties.Equal(plan.SensitiveProperties) {
		t.Fatalf("state sensitive_properties = %v, want %v", state.SensitiveProperties, plan.SensitiveProperties)
	}
}

func TestCatalogCreateRejectsExisting(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{{"catalog_name": "lakehouse", "connector_name": "memory"}}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	_, diags := trinotest.CreateExpectError(t, r, testCatalogModel("lakehouse", "iceberg"))
	if !diags.HasError() {
		t.Fatal("expected create to reject an existing catalog")
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("got %d calls, want only the existence query: %+v", len(fake.Calls), fake.Calls)
	}
	if fake.Calls[0].Op != "query" {
		t.Fatalf("call op = %q, want query", fake.Calls[0].Op)
	}
	if fake.Calls[0].Statement != catalogLookupSQL {
		t.Fatalf("statement = %q", fake.Calls[0].Statement)
	}
	if !strings.Contains(diags[0].Summary(), `Catalog "lakehouse" already exists`) {
		t.Fatalf("summary = %q", diags[0].Summary())
	}
	if !strings.Contains(diags[0].Detail(), "does not support import") {
		t.Fatalf("detail = %q", diags[0].Detail())
	}
	if !strings.Contains(diags[0].Detail(), "choose another name") {
		t.Fatalf("detail = %q", diags[0].Detail())
	}
}

func TestCatalogReadNotFoundRemovesState(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}}}
	r := New()
	trinotest.Configure(t, r, fake)

	// The shared harness cannot deserialize a removed (null) state, so it
	// returns the zero model plus the framework's conversion diagnostic. The
	// zero model is what Terraform sees after RemoveResource: a null object.
	state, _ := trinotest.ReadExpectError(t, r, testCatalogModel("lakehouse", "iceberg"))
	if !state.ID.IsNull() && state.ID.ValueString() != "" {
		t.Fatalf("state id = %q, want null/empty after removal", state.ID.ValueString())
	}
	if !state.Name.IsNull() && state.Name.ValueString() != "" {
		t.Fatalf("state name = %q, want null/empty after removal", state.Name.ValueString())
	}
	if len(fake.Calls) != 1 || fake.Calls[0].Op != "query" {
		t.Fatalf("calls = %+v", fake.Calls)
	}
}

func TestCatalogReadPreservesUnreadableProperties(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{{"catalog_name": "lakehouse", "connector_name": "iceberg"}}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	initial := testCatalogModel("lakehouse", "iceberg")
	state, diags := trinotest.Read(t, r, initial)
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if !state.Properties.Equal(initial.Properties) {
		t.Fatalf("properties = %v, want %v", state.Properties, initial.Properties)
	}
	if !state.SensitiveProperties.Equal(initial.SensitiveProperties) {
		t.Fatalf("sensitive_properties = %v, want %v", state.SensitiveProperties, initial.SensitiveProperties)
	}
	if state.ID.ValueString() != "lakehouse" || state.Name.ValueString() != "lakehouse" {
		t.Fatalf("identity = %q/%q", state.ID.ValueString(), state.Name.ValueString())
	}
}

func TestCatalogReadDetectsConnectorDrift(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Rows: []trino.Row{{"catalog_name": "lakehouse", "connector_name": "tpch"}}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	state, diags := trinotest.Read(t, r, testCatalogModel("lakehouse", "memory"))
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if state.Connector.ValueString() != "tpch" {
		t.Fatalf("connector = %q, want the read-back value tpch", state.Connector.ValueString())
	}
	if state.ID.ValueString() != "lakehouse" || state.Name.ValueString() != "lakehouse" {
		t.Fatalf("identity = %q/%q", state.ID.ValueString(), state.Name.ValueString())
	}
}

func TestCatalogDeleteIsIdempotent(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "catalog not found", err: &trino.ErrTrino{ErrorName: "CATALOG_NOT_FOUND", Message: "Catalog 'lakehouse' not found"}},
		{name: "generic not found", err: &trino.ErrTrino{ErrorName: "SOMETHING_NOT_FOUND"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{Err: tt.err}}}
			r := New()
			trinotest.Configure(t, r, fake)

			diags := trinotest.Delete(t, r, testCatalogModel("lakehouse", "iceberg"))
			if diags.HasError() {
				t.Fatalf("delete diagnostics: %v", diags)
			}
			if len(fake.Calls) != 1 {
				t.Fatalf("got %d calls, want 1: %+v", len(fake.Calls), fake.Calls)
			}
			if fake.Calls[0].Op != "exec" {
				t.Fatalf("call op = %q, want exec", fake.Calls[0].Op)
			}
			if got := fake.Calls[0].Statement; got != `DROP CATALOG "lakehouse"` {
				t.Fatalf("statement = %q", got)
			}
		})
	}
}

func TestCatalogDeleteReportsOtherErrors(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{Err: &trino.ErrTrino{ErrorName: "PERMISSION_DENIED", Message: "Access denied"}},
	}}
	r := New()
	trinotest.Configure(t, r, fake)

	diags := trinotest.DeleteExpectError(t, r, testCatalogModel("lakehouse", "iceberg"))
	if !diags.HasError() {
		t.Fatal("expected a diagnostic for a non-not-found delete failure")
	}
	if diags[0].Summary() != "Permission denied" {
		t.Fatalf("summary = %q", diags[0].Summary())
	}
}

func TestCatalogImportUnsupported(t *testing.T) {
	r := New()
	importer, ok := r.(resource.ResourceWithImportState)
	if !ok {
		t.Fatalf("%T does not implement resource.ResourceWithImportState", r)
	}

	var resp resource.ImportStateResponse
	importer.ImportState(context.Background(), resource.ImportStateRequest{ID: "lakehouse"}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected import to fail")
	}
	if got := resp.Diagnostics[0].Summary(); got != "Import is not supported for trino_catalog" {
		t.Fatalf("summary = %q", got)
	}
	detail := resp.Diagnostics[0].Detail()
	for _, fragment := range []string{"properties cannot be read back", "drop and recreate", "remove it from state"} {
		if !strings.Contains(detail, fragment) {
			t.Fatalf("detail = %q, want fragment %q", detail, fragment)
		}
	}
}

func TestCatalogAllAttributesRequireReplace(t *testing.T) {
	r := New()
	resourceSchema := trinotest.SchemaOf(t, r)

	for _, name := range []string{"name", "connector"} {
		attribute, ok := resourceSchema.Attributes[name].(schema.StringAttribute)
		if !ok {
			t.Fatalf("%s is %T, want schema.StringAttribute", name, resourceSchema.Attributes[name])
		}
		if !hasStringRequiresReplace(attribute.PlanModifiers) {
			t.Fatalf("%s has no RequiresReplace plan modifier", name)
		}
	}

	for _, name := range []string{"properties", "sensitive_properties"} {
		attribute, ok := resourceSchema.Attributes[name].(schema.MapAttribute)
		if !ok {
			t.Fatalf("%s is %T, want schema.MapAttribute", name, resourceSchema.Attributes[name])
		}
		if !hasMapRequiresReplace(attribute.PlanModifiers) {
			t.Fatalf("%s has no RequiresReplace plan modifier", name)
		}
	}
}

// TestCatalogConnectorNameValidated checks the connector name is constrained to
// a safe unquoted identifier, because it is emitted bare in CREATE CATALOG.
func TestCatalogConnectorNameValidated(t *testing.T) {
	r := New()
	resourceSchema := trinotest.SchemaOf(t, r)
	attribute, ok := resourceSchema.Attributes["connector"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("connector is %T, want schema.StringAttribute", resourceSchema.Attributes["connector"])
	}
	if len(attribute.Validators) == 0 {
		t.Fatal("connector has no validators")
	}

	tests := []struct {
		name    string
		wantErr bool
	}{
		{name: "memory"},
		{name: "delta_lake"},
		{name: "tpch1"},
		{name: "Iceberg", wantErr: true},
		{name: "ice berg", wantErr: true},
		{name: `memory";DROP CATALOG "x`, wantErr: true},
		{name: "1memory", wantErr: true},
		{name: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := validator.StringRequest{
				ConfigValue: types.StringValue(tt.name),
				Path:        path.Root("connector"),
			}
			var response validator.StringResponse
			for _, v := range attribute.Validators {
				v.ValidateString(context.Background(), request, &response)
			}
			if got := response.Diagnostics.HasError(); got != tt.wantErr {
				t.Fatalf("connector %q: HasError = %v, want %v (%v)", tt.name, got, tt.wantErr, response.Diagnostics)
			}
		})
	}
}

func TestCatalogSensitivePropertiesSensitive(t *testing.T) {
	r := New()
	resourceSchema := trinotest.SchemaOf(t, r)

	sensitive, ok := resourceSchema.Attributes["sensitive_properties"].(schema.MapAttribute)
	if !ok {
		t.Fatalf("sensitive_properties is %T, want schema.MapAttribute", resourceSchema.Attributes["sensitive_properties"])
	}
	if !sensitive.Sensitive {
		t.Fatal("sensitive_properties is not marked Sensitive")
	}

	properties, ok := resourceSchema.Attributes["properties"].(schema.MapAttribute)
	if !ok {
		t.Fatalf("properties is %T, want schema.MapAttribute", resourceSchema.Attributes["properties"])
	}
	if properties.Sensitive {
		t.Fatal("properties must not be marked Sensitive")
	}
}

func hasStringRequiresReplace(modifiers []planmodifier.String) bool {
	for _, modifier := range modifiers {
		if strings.Contains(reflect.TypeOf(modifier).String(), "requiresReplace") {
			return true
		}
	}
	return false
}

func hasMapRequiresReplace(modifiers []planmodifier.Map) bool {
	for _, modifier := range modifiers {
		if strings.Contains(reflect.TypeOf(modifier).String(), "requiresReplace") {
			return true
		}
	}
	return false
}
