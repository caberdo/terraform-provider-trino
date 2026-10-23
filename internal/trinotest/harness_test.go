package trinotest

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
)

type harnessModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

type harnessResource struct {
	data      *providerdata.Data
	failNames bool
}

func (r *harnessResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "trino_harness"
}

func (r *harnessResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resourceschema.Schema{
		Attributes: map[string]resourceschema.Attribute{
			"id": resourceschema.StringAttribute{
				Computed: true,
			},
			"name": resourceschema.StringAttribute{
				Optional: true,
			},
		},
	}
}

func (r *harnessResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	data, diags := providerdata.FromProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.data = data
}

func (r *harnessResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan harnessModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.failNames && plan.Name.ValueString() == "fail" {
		resp.Diagnostics.AddError("create failed", "the fake resource rejected this name")
		return
	}
	plan.ID = types.StringValue("generated")
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *harnessResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state harnessModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *harnessResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan harnessModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *harnessResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state harnessModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.State.RemoveResource(ctx)
}

var (
	_ resource.Resource              = (*harnessResource)(nil)
	_ resource.ResourceWithConfigure = (*harnessResource)(nil)
)

func TestHarnessRoundTripsStates(t *testing.T) {
	r := &harnessResource{}
	fake := &FakeExecutor{ServerVersion: "484"}
	Configure(t, r, fake)
	if r.data == nil {
		t.Fatal("Configure did not deliver provider data")
	}
	if r.data.Executor != fake {
		t.Fatal("Configure delivered the wrong executor")
	}

	plan := harnessModel{ID: types.StringNull(), Name: types.StringValue("first")}
	state, diags := Create(t, r, plan)
	if diags.HasError() {
		t.Fatalf("create diagnostics: %v", diags)
	}
	if state.ID.ValueString() != "generated" {
		t.Fatalf("create id = %q", state.ID.ValueString())
	}
	if state.Name.ValueString() != "first" {
		t.Fatalf("create name = %q", state.Name.ValueString())
	}

	read, diags := Read(t, r, state)
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if read.ID.ValueString() != "generated" || read.Name.ValueString() != "first" {
		t.Fatalf("read state = %+v", read)
	}

	updated, diags := Update(t, r, read, harnessModel{ID: read.ID, Name: types.StringValue("second")})
	if diags.HasError() {
		t.Fatalf("update diagnostics: %v", diags)
	}
	if updated.Name.ValueString() != "second" {
		t.Fatalf("update name = %q", updated.Name.ValueString())
	}

	if diags := Delete(t, r, updated); diags.HasError() {
		t.Fatalf("delete diagnostics: %v", diags)
	}
}

func TestHarnessExpectErrorReturnsDiagnostics(t *testing.T) {
	r := &harnessResource{failNames: true}
	Configure(t, r, &FakeExecutor{})

	_, diags := CreateExpectError(t, r, harnessModel{Name: types.StringValue("fail")})
	if !diags.HasError() {
		t.Fatal("expected create diagnostics")
	}
}
