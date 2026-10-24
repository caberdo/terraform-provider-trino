package grantprivileges

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/resourceutil"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

const (
	importIDFormat = "<catalog>.<schema>.<table>:<grantee_type>:<grantee>"
	driftNotice    = "Drift detection reads information_schema.table_privileges and therefore depends on Trino authorization. Use an admin identity for reliable results."
)

var allowedPrivileges = []string{"SELECT", "INSERT", "UPDATE", "DELETE"}

var _ resource.Resource = (*grantPrivilegesResource)(nil)
var _ resource.ResourceWithConfigure = (*grantPrivilegesResource)(nil)
var _ resource.ResourceWithImportState = (*grantPrivilegesResource)(nil)

// New returns the trino_grant_privileges resource.
func New() resource.Resource {
	return &grantPrivilegesResource{}
}

type grantPrivilegesResource struct {
	data *providerdata.Data
}

// OnModel is the table a grant applies to.
type OnModel struct {
	Catalog types.String `tfsdk:"catalog"`
	Schema  types.String `tfsdk:"schema"`
	Table   types.String `tfsdk:"table"`
}

type grantPrivilegesModel struct {
	ID          types.String `tfsdk:"id"`
	Privileges  types.Set    `tfsdk:"privileges"`
	On          OnModel      `tfsdk:"on"`
	Grantee     types.String `tfsdk:"grantee"`
	GranteeType types.String `tfsdk:"grantee_type"`
	GrantOption types.Bool   `tfsdk:"grant_option"`
}

func (r *grantPrivilegesResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_grant_privileges"
}

func (r *grantPrivilegesResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Grants table privileges to a Trino user or role. " + driftNotice,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Stable resource ID: <catalog>.<schema>.<table>:<grantee_type>:<grantee>.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"privileges": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				Description: "Table privileges to grant. List SELECT, INSERT, UPDATE and/or DELETE explicitly. " + driftNotice,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(
						stringvalidator.OneOf(allowedPrivileges...),
						allPrivilegesValidator{},
					),
				},
			},
			"on": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Table the privileges are granted on. Changing it replaces the resource.",
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
				Attributes: map[string]schema.Attribute{
					"catalog": schema.StringAttribute{
						Required:    true,
						Description: "Catalog name.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
					"schema": schema.StringAttribute{
						Required:    true,
						Description: "Schema name.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
					"table": schema.StringAttribute{
						Required:    true,
						Description: "Table name.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
				},
			},
			"grantee": schema.StringAttribute{
				Required:    true,
				Description: "Principal receiving the privileges. This is an arbitrary Trino principal such as an LDAP DN or alice@example.com, not necessarily a SQL identifier.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"grantee_type": schema.StringAttribute{
				Required:    true,
				Description: "Grantee type: USER or ROLE.",
				Validators: []validator.String{
					stringvalidator.OneOf("USER", "ROLE"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"grant_option": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Allow the grantee to grant these privileges to others (WITH GRANT OPTION). Updated in place.",
			},
		},
	}
}

func (r *grantPrivilegesResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *grantPrivilegesResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan grantPrivilegesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	privileges := resourceutil.StringSet(ctx, plan.Privileges, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	statement := grantStatement(privileges, plan.On, plan.GranteeType.ValueString(), plan.Grantee.ValueString(), plan.GrantOption.ValueBool())
	if err := r.data.Executor.Exec(ctx, statement); err != nil {
		resp.Diagnostics.Append(trino.DiagnosticFor("grant privileges on", objectName(plan.On), err)...)
		return
	}

	plan.ID = types.StringValue(resourceID(plan.On, plan.GranteeType.ValueString(), plan.Grantee.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state := plan
	r.refresh(ctx, &state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *grantPrivilegesResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state grantPrivilegesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.refresh(ctx, &state, &resp.Diagnostics) {
		if resp.Diagnostics.HasError() {
			return
		}
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *grantPrivilegesResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var state, plan grantPrivilegesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.ID.IsNull() || plan.ID.IsUnknown() {
		plan.ID = state.ID
	}

	statePrivileges := resourceutil.StringSet(ctx, state.Privileges, &resp.Diagnostics)
	planPrivileges := resourceutil.StringSet(ctx, plan.Privileges, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	additions := difference(planPrivileges, statePrivileges)
	removals := difference(statePrivileges, planPrivileges)
	turningOn := plan.GrantOption.ValueBool() && !state.GrantOption.ValueBool()
	turningOff := !plan.GrantOption.ValueBool() && state.GrantOption.ValueBool()

	if len(additions) == 0 && len(removals) == 0 && !turningOn && !turningOff {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	on := plan.On
	grantee := plan.Grantee.ValueString()
	granteeType := plan.GranteeType.ValueString()

	if turningOff {
		// Clear the grant option before the re-grant below. On Trino's file
		// metastore REVOKE GRANT OPTION FOR also drops the privilege, so the
		// re-grant is what restores the retained set; on a real Hive metastore
		// it only clears the option and the re-grant is a no-op. Only
		// privileges that remain need this; removed ones are revoked outright.
		keep := intersection(statePrivileges, planPrivileges)
		if len(keep) > 0 {
			statement := revokeGrantOptionStatement(keep, on, granteeType, grantee)
			if err := r.data.Executor.Exec(ctx, statement); err != nil {
				resp.Diagnostics.Append(trino.DiagnosticFor("revoke grant option on", objectName(on), err)...)
				return
			}
		}
	}

	if turningOn || turningOff || len(additions) > 0 {
		// Grant the full configured set, not just the additions. Granting an
		// already-held privilege is a no-op on a merge metastore (Hive) and the
		// only correct move on a metastore that replaces a grantee's privileges
		// per statement (Trino's file metastore), where granting just the
		// additions would silently drop the privileges already held. It also
		// restores the retained set after the file metastore dropped it while
		// clearing the grant option above.
		statement := grantStatement(planPrivileges, on, granteeType, grantee, plan.GrantOption.ValueBool())
		if err := r.data.Executor.Exec(ctx, statement); err != nil {
			resp.Diagnostics.Append(trino.DiagnosticFor("grant privileges on", objectName(on), err)...)
			return
		}
	}

	if len(removals) > 0 {
		statement := revokeStatement(removals, on, granteeType, grantee)
		if err := r.data.Executor.Exec(ctx, statement); err != nil {
			resp.Diagnostics.Append(trino.DiagnosticFor("revoke privileges on", objectName(on), err)...)
			return
		}
	}

	state = plan
	r.refresh(ctx, &state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *grantPrivilegesResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state grantPrivilegesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	privileges := resourceutil.StringSet(ctx, state.Privileges, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(privileges) == 0 {
		return
	}

	grantee := state.Grantee.ValueString()
	granteeType := state.GranteeType.ValueString()

	if state.GrantOption.ValueBool() {
		// Trino's file metastore keys a table privilege by its grant-option
		// flag, so a plain REVOKE of a grant made WITH GRANT OPTION is a silent
		// no-op there (FileHiveMetastore.revokeTablePrivileges matches the full
		// HivePrivilegeInfo). Clear the option first: on the file metastore that
		// also drops the privilege, so the plain REVOKE below is then a no-op;
		// on a real Hive metastore it only clears the option and the plain
		// REVOKE removes the privilege. A grant that is already gone is
		// tolerated.
		statement := revokeGrantOptionStatement(privileges, state.On, granteeType, grantee)
		if err := r.data.Executor.Exec(ctx, statement); err != nil && !revokeIsAbsent(err) {
			resp.Diagnostics.Append(trino.DiagnosticFor("revoke grant option on", objectName(state.On), err)...)
			return
		}
	}

	statement := revokeStatement(privileges, state.On, granteeType, grantee)
	if err := r.data.Executor.Exec(ctx, statement); err != nil && !revokeIsAbsent(err) {
		resp.Diagnostics.Append(trino.DiagnosticFor("revoke privileges on", objectName(state.On), err)...)
	}
}

// revokeIsAbsent reports whether a REVOKE failed only because there was nothing
// to revoke: the privilege, grantee or table is already gone. Deleting a grant
// that no longer exists is success.
func revokeIsAbsent(err error) bool {
	return trino.IsNotFound(err) || trino.IsErrorName(err, "NOT_FOUND", "USER_NOT_FOUND", "ROLE_NOT_FOUND", "TABLE_NOT_FOUND", "GRANT_NOT_FOUND")
}

func (r *grantPrivilegesResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := resourceutil.SplitImportID(req.ID, ":", 3, importIDFormat)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error()+" for "+importIDFormat)
		return
	}

	tableParts := strings.SplitN(parts[0], ".", 3)
	if len(tableParts) != 3 {
		resp.Diagnostics.AddError("Invalid import ID", "Expected "+importIDFormat)
		return
	}
	for i, part := range tableParts {
		tableParts[i] = strings.TrimSpace(part)
		if tableParts[i] == "" {
			resp.Diagnostics.AddError("Invalid import ID", "Expected "+importIDFormat)
			return
		}
	}

	granteeType := strings.ToUpper(strings.TrimSpace(parts[1]))
	if granteeType != "USER" && granteeType != "ROLE" {
		resp.Diagnostics.AddError("Invalid import ID", "grantee_type must be USER or ROLE, got "+parts[1])
		return
	}

	model := grantPrivilegesModel{
		ID:         types.StringValue(strings.Join(tableParts, ".") + ":" + granteeType + ":" + strings.TrimSpace(parts[2])),
		Privileges: types.SetValueMust(types.StringType, []attr.Value{}),
		On: OnModel{
			Catalog: types.StringValue(tableParts[0]),
			Schema:  types.StringValue(tableParts[1]),
			Table:   types.StringValue(tableParts[2]),
		},
		Grantee:     types.StringValue(strings.TrimSpace(parts[2])),
		GranteeType: types.StringValue(granteeType),
		GrantOption: types.BoolValue(false),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

// refresh reads the grant from information_schema.table_privileges and updates
// privileges, grant_option and id. It returns false when the grant no longer
// exists. Configured identifiers and the grantee are preserved.
func (r *grantPrivilegesResource) refresh(ctx context.Context, model *grantPrivilegesModel, diags *diag.Diagnostics) bool {
	rows, err := r.data.Executor.Query(ctx, tablePrivilegesQuery,
		model.On.Catalog.ValueString(),
		model.On.Schema.ValueString(),
		model.On.Table.ValueString(),
		model.Grantee.ValueString(),
		model.GranteeType.ValueString(),
	)
	if err != nil {
		if trino.IsNotFound(err) {
			return false
		}
		diags.Append(trino.DiagnosticFor("read privileges for", objectName(model.On), err)...)
		return false
	}
	if len(rows) == 0 {
		return false
	}

	privileges := make([]string, 0, len(rows))
	grantOption := false
	for _, row := range rows {
		if privilege, ok := row.String("privilege_type"); ok {
			privileges = append(privileges, privilege)
		}
		if grantable, ok := row.Bool("is_grantable"); ok && grantable {
			grantOption = true
		}
	}

	value, valueDiags := types.SetValueFrom(ctx, types.StringType, uniqueSorted(privileges))
	diags.Append(valueDiags...)
	if diags.HasError() {
		return false
	}
	model.Privileges = value
	model.GrantOption = types.BoolValue(grantOption)
	if model.ID.IsNull() || model.ID.IsUnknown() {
		model.ID = types.StringValue(resourceID(model.On, model.GranteeType.ValueString(), model.Grantee.ValueString()))
	}
	return true
}

const tablePrivilegesQuery = `SELECT grantee, grantee_type, privilege_type, is_grantable FROM information_schema.table_privileges WHERE table_catalog = ? AND table_schema = ? AND table_name = ? AND lower(grantee) = lower(?) AND grantee_type = ?`

type allPrivilegesValidator struct{}

var _ validator.String = allPrivilegesValidator{}

func (v allPrivilegesValidator) Description(context.Context) string {
	return "ALL PRIVILEGES is not supported; list SELECT, INSERT, UPDATE, DELETE explicitly"
}

func (v allPrivilegesValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v allPrivilegesValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := strings.ToUpper(strings.TrimSpace(req.ConfigValue.ValueString()))
	if value != "ALL" && value != "ALL PRIVILEGES" {
		return
	}
	resp.Diagnostics.AddAttributeError(
		req.Path,
		"ALL PRIVILEGES is not supported",
		"List SELECT, INSERT, UPDATE, DELETE explicitly. Trino expands ALL PRIVILEGES before storing it, so a read-back returns the expanded set and would produce a permanent diff.",
	)
}

func grantStatement(privileges []string, on OnModel, granteeType, grantee string, grantOption bool) string {
	statement := "GRANT " + strings.Join(sortedStrings(privileges), ", ") +
		" ON TABLE " + tableName(on) +
		" TO " + granteeType + " " + trino.QuoteIdentifier(grantee)
	if grantOption {
		statement += " WITH GRANT OPTION"
	}
	return statement
}

func revokeStatement(privileges []string, on OnModel, granteeType, grantee string) string {
	return "REVOKE " + strings.Join(sortedStrings(privileges), ", ") +
		" ON TABLE " + tableName(on) +
		" FROM " + granteeType + " " + trino.QuoteIdentifier(grantee)
}

func revokeGrantOptionStatement(privileges []string, on OnModel, granteeType, grantee string) string {
	return "REVOKE GRANT OPTION FOR " + strings.Join(sortedStrings(privileges), ", ") +
		" ON TABLE " + tableName(on) +
		" FROM " + granteeType + " " + trino.QuoteIdentifier(grantee)
}

func tableName(on OnModel) string {
	return trino.QuoteQualified(on.Catalog.ValueString(), on.Schema.ValueString(), on.Table.ValueString())
}

func objectName(on OnModel) string {
	return fmt.Sprintf("%s.%s.%s", on.Catalog.ValueString(), on.Schema.ValueString(), on.Table.ValueString())
}

func resourceID(on OnModel, granteeType, grantee string) string {
	return objectName(on) + ":" + granteeType + ":" + grantee
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func difference(a, b []string) []string {
	excluded := make(map[string]struct{}, len(b))
	for _, value := range b {
		excluded[value] = struct{}{}
	}
	result := make([]string, 0, len(a))
	for _, value := range a {
		if _, ok := excluded[value]; ok {
			continue
		}
		result = append(result, value)
	}
	return uniqueSorted(result)
}

func intersection(a, b []string) []string {
	included := make(map[string]struct{}, len(b))
	for _, value := range b {
		included[value] = struct{}{}
	}
	result := make([]string, 0, len(a))
	for _, value := range a {
		if _, ok := included[value]; ok {
			result = append(result, value)
		}
	}
	return uniqueSorted(result)
}
