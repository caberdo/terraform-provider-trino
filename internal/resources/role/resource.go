package role

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/resourceutil"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

const systemRolesNonGoal = "Stock Trino disables system roles (N1b), so trino_role only manages catalog roles."

var (
	_ resource.Resource                = (*roleResource)(nil)
	_ resource.ResourceWithConfigure   = (*roleResource)(nil)
	_ resource.ResourceWithImportState = (*roleResource)(nil)
)

// roleResource manages a Trino catalog role.
type roleResource struct {
	data *providerdata.Data
}

// New returns the trino_role resource.
func New() resource.Resource {
	return &roleResource{}
}

type roleModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	Catalog types.String `tfsdk:"catalog"`
}

func (r *roleResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "trino_role"
}

func (r *roleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resourceschema.Schema{
		Description: "Manages a catalog role in Trino. System roles are a documented non-goal: " +
			systemRolesNonGoal,
		Attributes: map[string]resourceschema.Attribute{
			"id": resourceschema.StringAttribute{
				Computed:    true,
				Description: "Resource identifier in the form catalog.name.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": resourceschema.StringAttribute{
				Required:    true,
				Description: "Role name. Trino has no ALTER ROLE, so changing it replaces the role.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"catalog": resourceschema.StringAttribute{
				Required: true,
				Description: "Catalog the role belongs to. Required because stock Trino disables system roles " +
					"and only catalog roles can be created. Changing it replaces the role.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					catalogValidator{},
				},
			},
		},
	}
}

func (r *roleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, diags := providerdata.FromProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.data = data
}

func (r *roleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	ctx, cancel := context.WithTimeout(ctx, r.data.Timeout)
	defer cancel()

	var plan roleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := plan.Name.ValueString()
	catalog := plan.Catalog.ValueString()

	statement := "CREATE ROLE " + trino.QuoteIdentifier(name) + " IN " + trino.QuoteIdentifier(catalog)
	tflog.Debug(ctx, "creating role", map[string]any{
		"operation":   "create",
		"object_type": "role",
		"object_name": name,
		"catalog":     catalog,
	})

	if err := r.data.Executor.Exec(ctx, statement); err != nil {
		resp.Diagnostics.Append(trino.DiagnosticFor("create", "role", err)...)
		return
	}

	plan.ID = types.StringValue(roleID(catalog, name))

	found, err := r.roleExists(ctx, catalog, name)
	if err != nil {
		resp.Diagnostics.Append(trino.DiagnosticFor("read", "role", err)...)
		return
	}
	if !found {
		tflog.Warn(ctx, "role is not visible immediately after create; keeping planned state", map[string]any{
			"operation":   "read",
			"object_type": "role",
			"object_name": name,
			"catalog":     catalog,
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *roleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	ctx, cancel := context.WithTimeout(ctx, r.data.Timeout)
	defer cancel()

	var state roleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	catalog := state.Catalog.ValueString()

	found, err := r.roleExists(ctx, catalog, name)
	if err != nil {
		resp.Diagnostics.Append(trino.DiagnosticFor("read", "role", err)...)
		return
	}
	if !found {
		tflog.Debug(ctx, "role not found; removing from state", map[string]any{
			"operation":   "read",
			"object_type": "role",
			"object_name": name,
			"catalog":     catalog,
		})
		resp.State.RemoveResource(ctx)
		return
	}

	// Keep the configured spelling: Hive lower-cases role names on read (C16).
	state.ID = types.StringValue(roleID(catalog, name))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *roleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Every attribute is RequiresReplace, so an update should never reach Trino.
	// Copy the plan to state so a framework-driven no-op cannot corrupt state.
	var plan roleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "role attributes are immutable; copying plan to state without SQL", map[string]any{
		"operation":   "update",
		"object_type": "role",
		"object_name": plan.Name.ValueString(),
		"catalog":     plan.Catalog.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *roleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	ctx, cancel := context.WithTimeout(ctx, r.data.Timeout)
	defer cancel()

	var state roleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	catalog := state.Catalog.ValueString()

	statement := "DROP ROLE IF EXISTS " + trino.QuoteIdentifier(name) + " IN " + trino.QuoteIdentifier(catalog)
	tflog.Info(ctx, "dropping role", map[string]any{
		"operation":   "drop",
		"object_type": "role",
		"object_name": name,
		"catalog":     catalog,
	})

	if err := r.data.Executor.Exec(ctx, statement); err != nil {
		if trino.IsNotFound(err) || trino.IsErrorName(err, "ROLE_NOT_FOUND") {
			return
		}
		resp.Diagnostics.Append(trino.DiagnosticFor("drop", "role", err)...)
	}
}

func (r *roleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := resourceutil.SplitImportID(req.ID, ".", 2, "catalog.name")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected catalog.name, got %q: %s", req.ID, err))
		return
	}

	catalog, name := parts[0], parts[1]
	state := roleModel{
		ID:      types.StringValue(roleID(catalog, name)),
		Name:    types.StringValue(name),
		Catalog: types.StringValue(catalog),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// roleExists reports whether the catalog lists name, matching case-insensitively
// because Hive normalises role names (C16). A missing catalog or role is not an
// error: it means the role is gone.
func (r *roleResource) roleExists(ctx context.Context, catalog, name string) (bool, error) {
	rows, err := r.data.Executor.Query(ctx, "SHOW ROLES FROM "+trino.QuoteIdentifier(catalog))
	if err != nil {
		if trino.IsNotFound(err) || trino.IsErrorName(err, "CATALOG_NOT_FOUND") {
			return false, nil
		}
		return false, err
	}

	for _, row := range rows {
		candidate, ok := row.SoleString()
		if !ok {
			continue
		}
		if strings.EqualFold(candidate, name) {
			return true, nil
		}
	}
	return false, nil
}

func roleID(catalog, name string) string {
	return catalog + "." + name
}

// catalogValidator rejects a blank catalog. LengthAtLeast(1) catches the empty
// string; this validator catches whitespace and names the reason catalog is
// required.
type catalogValidator struct{}

func (catalogValidator) Description(context.Context) string {
	return "catalog must not be empty or whitespace; " + systemRolesNonGoal
}

func (v catalogValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (catalogValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if strings.TrimSpace(req.ConfigValue.ValueString()) != "" {
		return
	}

	resp.Diagnostics.AddAttributeError(
		req.Path,
		"Catalog is required",
		"trino_role manages catalog roles only. Stock Trino disables system roles, "+
			"so catalog must name the catalog the role belongs to; a blank catalog cannot be used.",
	)
}
