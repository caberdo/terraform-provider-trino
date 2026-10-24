package schema

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
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
	typeName            = "trino_schema"
	ownerTypeUser       = "USER"
	ownerTypeRole       = "ROLE"
	invalidImportID     = "Invalid import ID"
	importIDExpectation = "catalog.schema"
)

var (
	_ resource.Resource                   = (*Resource)(nil)
	_ resource.ResourceWithConfigure      = (*Resource)(nil)
	_ resource.ResourceWithImportState    = (*Resource)(nil)
	_ resource.ResourceWithValidateConfig = (*Resource)(nil)
)

// Resource manages a schema in a Trino catalog.
type Resource struct {
	data *providerdata.Data
}

// New returns the trino_schema resource.
func New() resource.Resource {
	return &Resource{}
}

type model struct {
	ID           types.String `tfsdk:"id"`
	Catalog      types.String `tfsdk:"catalog"`
	Name         types.String `tfsdk:"name"`
	Owner        types.String `tfsdk:"owner"`
	OwnerType    types.String `tfsdk:"owner_type"`
	Properties   types.Map    `tfsdk:"properties"`
	ForceDestroy types.Bool   `tfsdk:"force_destroy"`
}

func (r *Resource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = typeName
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resourceschema.Schema{
		Description: "Manages a schema in a Trino catalog. The owner can be changed in place; properties and the catalog or schema name force replacement.",
		Attributes: map[string]resourceschema.Attribute{
			"id": resourceschema.StringAttribute{
				Computed:    true,
				Description: "Identifier in the form catalog.name.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"catalog": resourceschema.StringAttribute{
				Required:    true,
				Description: "Catalog that contains the schema.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": resourceschema.StringAttribute{
				Required:    true,
				Description: "Schema name. Hive lowercases it; the configured spelling is kept in state.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"owner": resourceschema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Schema owner. Defaults to the creating user when unset.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"owner_type": resourceschema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Owner principal type, USER or ROLE. Defaults to USER when owner is set.",
				Validators: []validator.String{
					stringvalidator.OneOf(ownerTypeUser, ownerTypeRole),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"properties": resourceschema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Schema properties passed as WITH (key = 'value'). Changing them forces replacement.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
			},
			"force_destroy": resourceschema.BoolAttribute{
				Optional:    true,
				Description: "Drop the schema with CASCADE, including its contents. Defaults to false.",
			},
		},
	}
}

func (r *Resource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, diags := providerdata.FromProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || data == nil {
		return
	}
	r.data = data
}

// ValidateConfig rejects owner_type without owner at plan time. The reverse is
// allowed because owner defaults owner_type to USER.
func (r *Resource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config model
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ownerType := ""
	if !config.OwnerType.IsNull() && !config.OwnerType.IsUnknown() {
		ownerType = strings.TrimSpace(config.OwnerType.ValueString())
	}
	ownerSet := !config.Owner.IsNull() && !config.Owner.IsUnknown()
	if ownerType != "" && !ownerSet {
		resp.Diagnostics.AddAttributeError(
			path.Root("owner_type"),
			"owner_type requires owner",
			"Set owner when owner_type is configured, or omit owner_type to use the default USER.",
		)
	}
}

func (r *Resource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.OwnerType.IsNull() || plan.OwnerType.IsUnknown() || strings.TrimSpace(plan.OwnerType.ValueString()) == "" {
		plan.OwnerType = types.StringValue(ownerTypeUser)
	}

	statement, diags := createStatement(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.data.Executor.Exec(ctx, statement); err != nil {
		resp.Diagnostics.Append(trino.DiagnosticFor("create", "schema", err)...)
		return
	}

	state, exists, readDiags := r.refresh(ctx, plan)
	resp.Diagnostics.Append(readDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !exists {
		resp.Diagnostics.AddError(
			"Schema not found after create",
			fmt.Sprintf("Trino accepted CREATE SCHEMA for %s but did not list it afterwards.", plan.ID.ValueString()),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, exists, diags := r.refresh(ctx, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

func (r *Resource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var state, plan model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ownerChanged := !plan.Owner.Equal(state.Owner) || !plan.OwnerType.Equal(state.OwnerType)
	if ownerChanged && isKnownString(plan.Owner) {
		statement := "ALTER SCHEMA " + trino.QuoteQualified(plan.Catalog.ValueString(), plan.Name.ValueString()) +
			" SET AUTHORIZATION " + normaliseOwnerType(plan.OwnerType) + " " + trino.QuoteIdentifier(plan.Owner.ValueString())
		if err := r.data.Executor.Exec(ctx, statement); err != nil {
			resp.Diagnostics.Append(trino.DiagnosticFor("update", "schema", err)...)
			return
		}
	}

	if !plan.Catalog.Equal(state.Catalog) || !plan.Name.Equal(state.Name) || !plan.Properties.Equal(state.Properties) {
		tflog.Warn(ctx, "trino_schema update changed an attribute that should force replacement", map[string]any{
			"catalog_changed":    !plan.Catalog.Equal(state.Catalog),
			"name_changed":       !plan.Name.Equal(state.Name),
			"properties_changed": !plan.Properties.Equal(state.Properties),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	statement := "DROP SCHEMA " + trino.QuoteQualified(state.Catalog.ValueString(), state.Name.ValueString())
	if !state.ForceDestroy.IsNull() && !state.ForceDestroy.IsUnknown() && state.ForceDestroy.ValueBool() {
		statement += " CASCADE"
	}

	if err := r.data.Executor.Exec(ctx, statement); err != nil {
		if trino.IsNotFound(err) {
			return
		}
		resp.Diagnostics.Append(trino.DiagnosticFor("drop", "schema", err)...)
		if isSchemaNotEmpty(err) {
			resp.Diagnostics.AddError(
				"Schema is not empty",
				"Dropping the schema failed because it still contains tables, views or other objects. Set force_destroy = true to drop it with CASCADE.",
			)
		}
	}
}

func (r *Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := resourceutil.SplitImportID(req.ID, ".", 2, importIDExpectation)
	if err != nil {
		resp.Diagnostics.AddError(
			invalidImportID,
			fmt.Sprintf("Expected %s, got %q.", importIDExpectation, req.ID),
		)
		return
	}

	state := model{
		ID:           types.StringValue(parts[0] + "." + parts[1]),
		Catalog:      types.StringValue(parts[0]),
		Name:         types.StringValue(parts[1]),
		Owner:        types.StringNull(),
		OwnerType:    types.StringNull(),
		Properties:   types.MapNull(types.StringType),
		ForceDestroy: types.BoolNull(),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// refresh queries existence and best-effort DDL for the schema. The second
// return value is false when the schema or its catalog no longer exists.
func (r *Resource) refresh(ctx context.Context, current model) (model, bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	catalog := current.Catalog.ValueString()
	name := current.Name.ValueString()
	current.ID = types.StringValue(catalog + "." + name)

	rows, err := r.data.Executor.Query(ctx, "SHOW SCHEMAS FROM "+trino.QuoteIdentifier(catalog))
	if err != nil {
		if schemaMissing(err) {
			return current, false, diags
		}
		diags.Append(trino.DiagnosticFor("read", "schema", err)...)
		return current, false, diags
	}

	found := false
	for _, row := range rows {
		value, ok := row.SoleString()
		if ok && strings.EqualFold(value, name) {
			found = true
			break
		}
	}
	if !found {
		return current, false, diags
	}

	ddlRows, err := r.data.Executor.Query(ctx, "SHOW CREATE SCHEMA "+trino.QuoteQualified(catalog, name))
	if err != nil {
		if schemaMissing(err) {
			return current, false, diags
		}
		diags.Append(trino.DiagnosticFor("read", "schema", err)...)
		return current, false, diags
	}
	if len(ddlRows) == 0 {
		tflog.Warn(ctx, "SHOW CREATE SCHEMA returned no rows; preserving prior state")
		return current, true, diags
	}
	ddl, ok := ddlRows[0].SoleString()
	if !ok {
		tflog.Warn(ctx, "SHOW CREATE SCHEMA returned an unreadable row; preserving prior state")
		return current, true, diags
	}

	owner, properties, err := parseShowCreateSchema(ddl)
	if err != nil {
		tflog.Warn(ctx, "Could not parse SHOW CREATE SCHEMA; preserving prior state", map[string]any{
			"schema": catalog + "." + name,
			"error":  err.Error(),
		})
		return current, true, diags
	}

	if owner != nil {
		current.Owner = types.StringValue(owner.Name)
		current.OwnerType = types.StringValue(strings.ToUpper(owner.Type))
	} else {
		if current.Owner.IsUnknown() {
			current.Owner = types.StringNull()
		}
		if current.OwnerType.IsUnknown() {
			current.OwnerType = types.StringNull()
		}
	}

	if !current.Properties.IsNull() && !current.Properties.IsUnknown() {
		existing := map[string]string{}
		diags.Append(current.Properties.ElementsAs(ctx, &existing, false)...)
		if diags.HasError() {
			return current, true, diags
		}
		updated := make(map[string]string, len(existing))
		for key, value := range existing {
			if parsed, ok := properties[key]; ok {
				updated[key] = parsed
			} else {
				updated[key] = value
			}
		}
		value, valueDiags := types.MapValueFrom(ctx, types.StringType, updated)
		diags.Append(valueDiags...)
		if diags.HasError() {
			return current, true, diags
		}
		current.Properties = value
	}

	return current, true, diags
}

func createStatement(ctx context.Context, plan model) (string, diag.Diagnostics) {
	var diags diag.Diagnostics

	var b strings.Builder
	b.WriteString("CREATE SCHEMA ")
	b.WriteString(trino.QuoteQualified(plan.Catalog.ValueString(), plan.Name.ValueString()))

	if isKnownString(plan.Owner) {
		b.WriteString(" AUTHORIZATION ")
		b.WriteString(normaliseOwnerType(plan.OwnerType))
		b.WriteString(" ")
		b.WriteString(trino.QuoteIdentifier(plan.Owner.ValueString()))
	}

	properties := resourceutil.StringMap(ctx, plan.Properties, &diags)
	if len(properties) > 0 {
		keys := make([]string, 0, len(properties))
		for key := range properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		b.WriteString(" WITH (")
		for i, key := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(trino.QuoteIdentifier(key))
			b.WriteString(" = ")
			b.WriteString(trino.QuoteLiteral(properties[key]))
		}
		b.WriteString(")")
	}

	return b.String(), diags
}

func isKnownString(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown() && strings.TrimSpace(value.ValueString()) != ""
}

func normaliseOwnerType(value types.String) string {
	if value.IsNull() || value.IsUnknown() {
		return ownerTypeUser
	}
	switch strings.ToUpper(strings.TrimSpace(value.ValueString())) {
	case ownerTypeRole:
		return ownerTypeRole
	case ownerTypeUser:
		return ownerTypeUser
	default:
		return ownerTypeUser
	}
}

func schemaMissing(err error) bool {
	return trino.IsNotFound(err) || trino.IsErrorName(err, "CATALOG_NOT_FOUND")
}

func isSchemaNotEmpty(err error) bool {
	if trino.IsErrorName(err, "SCHEMA_NOT_EMPTY") {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "not empty")
}
