package trinotest

import (
	"context"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
)

const harnessTimeout = 5 * time.Minute

// Ctx returns the context used by harness calls.
func Ctx(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}

// SchemaOf returns the resource's schema, failing the test on diagnostics.
func SchemaOf(t *testing.T, r resource.Resource) resourceschema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	r.Schema(Ctx(t), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// Configure hands the fake executor to the resource through its Configure
// method.
func Configure(t *testing.T, r resource.Resource, fake *FakeExecutor) {
	t.Helper()
	configurable, ok := r.(resource.ResourceWithConfigure)
	if !ok {
		t.Fatalf("%T does not implement resource.ResourceWithConfigure", r)
	}
	version := ""
	if fake != nil {
		version = fake.ServerVersion
	}
	data := &providerdata.Data{Executor: fake, Timeout: harnessTimeout, Version: version}
	var resp resource.ConfigureResponse
	configurable.Configure(Ctx(t), resource.ConfigureRequest{ProviderData: data}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", resp.Diagnostics)
	}
}

// Create calls the resource's Create method and fails the test on diagnostics.
func Create[Model any](t *testing.T, r resource.Resource, plan Model) (Model, diag.Diagnostics) {
	t.Helper()
	state, diags := CreateExpectError(t, r, plan)
	if diags.HasError() {
		t.Fatalf("create diagnostics: %v", diags)
	}
	return state, diags
}

// CreateExpectError calls Create and returns diagnostics for inspection.
func CreateExpectError[Model any](t *testing.T, r resource.Resource, plan Model) (Model, diag.Diagnostics) {
	t.Helper()
	var zero Model
	schema := SchemaOf(t, r)
	raw, diags := rawFromModel(t, schema, plan)
	if diags.HasError() {
		return zero, diags
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(Ctx(t), resource.CreateRequest{Plan: tfsdk.Plan{Raw: raw, Schema: schema}}, &resp)
	if resp.Diagnostics.HasError() {
		return zero, resp.Diagnostics
	}
	var state Model
	stateDiags := resp.State.Get(Ctx(t), &state)
	return state, stateDiags
}

// Read calls the resource's Read method and fails the test on diagnostics.
func Read[Model any](t *testing.T, r resource.Resource, state Model) (Model, diag.Diagnostics) {
	t.Helper()
	result, diags := ReadExpectError(t, r, state)
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	return result, diags
}

// ReadExpectError calls Read and returns diagnostics for inspection.
func ReadExpectError[Model any](t *testing.T, r resource.Resource, state Model) (Model, diag.Diagnostics) {
	t.Helper()
	var zero Model
	schema := SchemaOf(t, r)
	raw, diags := rawFromModel(t, schema, state)
	if diags.HasError() {
		return zero, diags
	}
	resp := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	r.Read(Ctx(t), resource.ReadRequest{State: tfsdk.State{Raw: raw, Schema: schema}}, &resp)
	if resp.Diagnostics.HasError() {
		return zero, resp.Diagnostics
	}
	var result Model
	stateDiags := resp.State.Get(Ctx(t), &result)
	return result, stateDiags
}

// Update calls the resource's Update method and fails the test on diagnostics.
func Update[Model any](t *testing.T, r resource.Resource, state, plan Model) (Model, diag.Diagnostics) {
	t.Helper()
	result, diags := UpdateExpectError(t, r, state, plan)
	if diags.HasError() {
		t.Fatalf("update diagnostics: %v", diags)
	}
	return result, diags
}

// UpdateExpectError calls Update and returns diagnostics for inspection.
func UpdateExpectError[Model any](t *testing.T, r resource.Resource, state, plan Model) (Model, diag.Diagnostics) {
	t.Helper()
	var zero Model
	schema := SchemaOf(t, r)
	stateRaw, diags := rawFromModel(t, schema, state)
	if diags.HasError() {
		return zero, diags
	}
	planRaw, planDiags := rawFromModel(t, schema, plan)
	if planDiags.HasError() {
		return zero, planDiags
	}
	resp := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
	r.Update(Ctx(t), resource.UpdateRequest{
		State: tfsdk.State{Raw: stateRaw, Schema: schema},
		Plan:  tfsdk.Plan{Raw: planRaw, Schema: schema},
	}, &resp)
	if resp.Diagnostics.HasError() {
		return zero, resp.Diagnostics
	}
	var result Model
	stateDiags := resp.State.Get(Ctx(t), &result)
	return result, stateDiags
}

// Delete calls the resource's Delete method and fails the test on diagnostics.
func Delete[Model any](t *testing.T, r resource.Resource, state Model) diag.Diagnostics {
	t.Helper()
	diags := DeleteExpectError(t, r, state)
	if diags.HasError() {
		t.Fatalf("delete diagnostics: %v", diags)
	}
	return diags
}

// DeleteExpectError calls Delete and returns diagnostics for inspection.
func DeleteExpectError[Model any](t *testing.T, r resource.Resource, state Model) diag.Diagnostics {
	t.Helper()
	schema := SchemaOf(t, r)
	raw, diags := rawFromModel(t, schema, state)
	if diags.HasError() {
		return diags
	}
	resp := resource.DeleteResponse{State: tfsdk.State{Schema: schema}}
	r.Delete(Ctx(t), resource.DeleteRequest{State: tfsdk.State{Raw: raw, Schema: schema}}, &resp)
	return resp.Diagnostics
}

func rawFromModel(t *testing.T, schema resourceschema.Schema, model any) (tftypes.Value, diag.Diagnostics) {
	t.Helper()
	var diags diag.Diagnostics
	objectType, ok := schema.Type().(types.ObjectType)
	if !ok {
		diags.AddError("Unsupported schema", "The harness requires a top-level object schema.")
		return tftypes.Value{}, diags
	}
	value, valueDiags := types.ObjectValueFrom(Ctx(t), objectType.AttributeTypes(), model)
	diags.Append(valueDiags...)
	if diags.HasError() {
		return tftypes.Value{}, diags
	}
	raw, err := value.ToTerraformValue(Ctx(t))
	if err != nil {
		diags.AddError("Could not encode model", err.Error())
		return tftypes.Value{}, diags
	}
	return raw, diags
}
