// Package grantrole implements the trino_grant_role resource.
package grantrole

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/resourceutil"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

const (
	granteeTypeUser = "USER"
	granteeTypeRole = "ROLE"

	importFormat = "role_catalog.role:grantee_type:grantee"
)

var (
	_ resource.Resource                = (*grantRoleResource)(nil)
	_ resource.ResourceWithConfigure   = (*grantRoleResource)(nil)
	_ resource.ResourceWithImportState = (*grantRoleResource)(nil)
)

// grantRoleModel is the Terraform state and plan shape. The tags follow the
// framework's tfsdk reflection conventions.
type grantRoleModel struct {
	ID          types.String `tfsdk:"id"`
	Role        types.String `tfsdk:"role"`
	RoleCatalog types.String `tfsdk:"role_catalog"`
	Grantee     types.String `tfsdk:"grantee"`
	GranteeType types.String `tfsdk:"grantee_type"`
	AdminOption types.Bool   `tfsdk:"admin_option"`
}

// grantRoleResource manages a Trino catalog role grant.
type grantRoleResource struct {
	data *providerdata.Data
}

// New returns a new trino_grant_role resource.
func New() resource.Resource {
	return &grantRoleResource{}
}

func (r *grantRoleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_grant_role"
}

func (r *grantRoleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resourceschema.Schema{
		Description: "Grants a catalog role to a Trino user or role. " +
			"Trino cannot list another principal's role grants, so Read is state-authoritative: " +
			"grants revoked outside Terraform are not detected and are not removed from state.",
		Attributes: map[string]resourceschema.Attribute{
			"id": resourceschema.StringAttribute{
				Computed:    true,
				Description: "Stable identifier in the form role_catalog.role:grantee_type:grantee.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"role": resourceschema.StringAttribute{
				Required:    true,
				Description: "Catalog role to grant.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"role_catalog": resourceschema.StringAttribute{
				Required:    true,
				Description: "Catalog that owns the role, for example hive. Must be non-empty.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"grantee": resourceschema.StringAttribute{
				Required: true,
				Description: "Principal receiving the role. Trino accepts arbitrary principal strings, " +
					"so values such as alice@example.com and DOMAIN\\alice are valid.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"grantee_type": resourceschema.StringAttribute{
				Required:    true,
				Description: "Whether the grantee is a USER or a ROLE.",
				Validators: []validator.String{
					stringvalidator.OneOf(granteeTypeUser, granteeTypeRole),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"admin_option": resourceschema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
				Description: "Whether the grantee may grant the role to other principals. " +
					"Defaults to false. Changing this toggles WITH ADMIN OPTION in place. " +
					"Trino cannot read this back, so changes made outside Terraform are not detected.",
			},
		},
	}
}

func (r *grantRoleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *grantRoleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan grantRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if r.data == nil || r.data.Executor == nil {
		resp.Diagnostics.AddError("Provider not configured",
			"The Trino provider did not supply its client configuration. This is a provider bug; report it with the version in use.")
		return
	}

	admin := adminOption(plan.AdminOption)
	statement := grantStatement(plan.Role.ValueString(), plan.GranteeType.ValueString(), plan.Grantee.ValueString(), plan.RoleCatalog.ValueString(), admin)
	if err := r.data.Executor.Exec(ctx, statement); err != nil {
		resp.Diagnostics.Append(trino.DiagnosticFor("grant", "role grant", err)...)
		return
	}

	plan.ID = types.StringValue(composeID(plan.RoleCatalog.ValueString(), plan.Role.ValueString(), plan.GranteeType.ValueString(), plan.Grantee.ValueString()))
	plan.AdminOption = types.BoolValue(admin)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read is deliberately a no-op. Trino exposes no general read path for another
// principal's role grants (SHOW GRANTS and SHOW ROLE GRANTS are session-user
// scoped), so state is authoritative. Returning without touching state means a
// grant revoked out of band stays in Terraform state and is not silently zeroed.
func (r *grantRoleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state grantRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "role grants are state-authoritative; skipping Trino read", map[string]any{
		"role":         state.Role.ValueString(),
		"role_catalog": state.RoleCatalog.ValueString(),
		"grantee":      state.Grantee.ValueString(),
		"grantee_type": state.GranteeType.ValueString(),
	})

	resp.State = req.State
}

func (r *grantRoleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan grantRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state grantRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.ID.IsNull() || plan.ID.IsUnknown() {
		plan.ID = state.ID
	}

	if r.data == nil || r.data.Executor == nil {
		resp.Diagnostics.AddError("Provider not configured",
			"The Trino provider did not supply its client configuration. This is a provider bug; report it with the version in use.")
		return
	}

	before := adminOption(state.AdminOption)
	after := adminOption(plan.AdminOption)

	switch {
	case !before && after:
		statement := grantStatement(plan.Role.ValueString(), plan.GranteeType.ValueString(), plan.Grantee.ValueString(), plan.RoleCatalog.ValueString(), true)
		if err := r.data.Executor.Exec(ctx, statement); err != nil {
			resp.Diagnostics.Append(trino.DiagnosticFor("grant", "role grant", err)...)
			return
		}
	case before && !after:
		statement := revokeAdminStatement(plan.Role.ValueString(), plan.GranteeType.ValueString(), plan.Grantee.ValueString(), plan.RoleCatalog.ValueString())
		if err := r.data.Executor.Exec(ctx, statement); err != nil {
			resp.Diagnostics.Append(trino.DiagnosticFor("revoke", "role grant", err)...)
			return
		}
	}

	plan.AdminOption = types.BoolValue(after)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *grantRoleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state grantRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if r.data == nil || r.data.Executor == nil {
		resp.Diagnostics.AddError("Provider not configured",
			"The Trino provider did not supply its client configuration. This is a provider bug; report it with the version in use.")
		return
	}

	statement := revokeStatement(state.Role.ValueString(), state.GranteeType.ValueString(), state.Grantee.ValueString(), state.RoleCatalog.ValueString())
	err := r.data.Executor.Exec(ctx, statement)
	if err == nil {
		return
	}
	if trino.IsNotFound(err) || trino.IsErrorName(err, "ROLE_NOT_FOUND", "ROLE_GRANT_NOT_FOUND") {
		return
	}
	resp.Diagnostics.Append(trino.DiagnosticFor("revoke", "role grant", err)...)
}

func (r *grantRoleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := resourceutil.SplitImportID(req.ID, ":", 3, importFormat)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error()+
			". The ID must be <role_catalog>.<role>:<grantee_type>:<grantee>; a \":\" inside a segment is unsupported.")
		return
	}

	if strings.Contains(parts[2], ":") {
		resp.Diagnostics.AddError("Invalid import ID",
			"The ID must be <role_catalog>.<role>:<grantee_type>:<grantee>; a \":\" inside a segment is unsupported.")
		return
	}

	nameParts := strings.SplitN(parts[0], ".", 2)
	if len(nameParts) != 2 || strings.TrimSpace(nameParts[0]) == "" || strings.TrimSpace(nameParts[1]) == "" {
		resp.Diagnostics.AddError("Invalid import ID",
			"Expected <role_catalog>.<role>:<grantee_type>:<grantee>, for example hive.analyst:USER:alice. A role or catalog name containing \":\" is unsupported.")
		return
	}

	granteeType := strings.ToUpper(strings.TrimSpace(parts[1]))
	if granteeType != granteeTypeUser && granteeType != granteeTypeRole {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("grantee_type must be USER or ROLE, got %q.", parts[1]))
		return
	}

	state := grantRoleModel{
		ID:          types.StringValue(composeID(strings.TrimSpace(nameParts[0]), strings.TrimSpace(nameParts[1]), granteeType, strings.TrimSpace(parts[2]))),
		Role:        types.StringValue(strings.TrimSpace(nameParts[1])),
		RoleCatalog: types.StringValue(strings.TrimSpace(nameParts[0])),
		Grantee:     types.StringValue(strings.TrimSpace(parts[2])),
		GranteeType: types.StringValue(granteeType),
		AdminOption: types.BoolValue(false),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func grantStatement(role, granteeType, grantee, catalog string, admin bool) string {
	statement := fmt.Sprintf("GRANT %s TO %s %s",
		trino.QuoteIdentifier(role),
		strings.ToUpper(granteeType),
		trino.QuoteIdentifier(grantee))
	if admin {
		statement += " WITH ADMIN OPTION"
	}
	return statement + " IN " + trino.QuoteIdentifier(catalog)
}

func revokeAdminStatement(role, granteeType, grantee, catalog string) string {
	return fmt.Sprintf("REVOKE ADMIN OPTION FOR %s FROM %s %s IN %s",
		trino.QuoteIdentifier(role),
		strings.ToUpper(granteeType),
		trino.QuoteIdentifier(grantee),
		trino.QuoteIdentifier(catalog))
}

func revokeStatement(role, granteeType, grantee, catalog string) string {
	return fmt.Sprintf("REVOKE %s FROM %s %s IN %s",
		trino.QuoteIdentifier(role),
		strings.ToUpper(granteeType),
		trino.QuoteIdentifier(grantee),
		trino.QuoteIdentifier(catalog))
}

func adminOption(v types.Bool) bool {
	if v.IsNull() || v.IsUnknown() {
		return false
	}
	return v.ValueBool()
}

func composeID(catalog, role, granteeType, grantee string) string {
	return fmt.Sprintf("%s.%s:%s:%s", catalog, role, strings.ToUpper(granteeType), grantee)
}
