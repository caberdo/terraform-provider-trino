package view

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/resourceutil"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

const (
	typeName            = "trino_view"
	invalidImportID     = "Invalid import ID"
	importIDExpectation = "catalog.schema.name"
)

var (
	_ resource.Resource                   = (*Resource)(nil)
	_ resource.ResourceWithConfigure      = (*Resource)(nil)
	_ resource.ResourceWithImportState    = (*Resource)(nil)
	_ resource.ResourceWithValidateConfig = (*Resource)(nil)
)

// Resource manages a view. Trino stores a normalised definition and Hive keeps
// a stale one after an underlying column is renamed, so the definition is
// write-only: Read never rewrites the configured text, and only the security
// mode drifts.
type Resource struct {
	data *providerdata.Data
}

// New returns the trino_view resource.
func New() resource.Resource {
	return &Resource{}
}

type model struct {
	ID         types.String `tfsdk:"id"`
	Catalog    types.String `tfsdk:"catalog"`
	Schema     types.String `tfsdk:"schema"`
	Name       types.String `tfsdk:"name"`
	Definition types.String `tfsdk:"definition"`
	Security   types.String `tfsdk:"security"`
}

func (r *Resource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = typeName
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resourceschema.Schema{
		Description: "Manages a view. Trino stores the query in a normalised form, and Hive does not rewrite a stored view when an underlying column is renamed, " +
			"so `definition` is write-only: state keeps the configured text and Read never overwrites it. Only `security` is read back and drifted. " +
			"Changing the definition or the security mode runs CREATE OR REPLACE VIEW in place.",
		Attributes: map[string]resourceschema.Attribute{
			"id": resourceschema.StringAttribute{
				Computed:    true,
				Description: "Identifier in the form catalog.schema.name.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"catalog": resourceschema.StringAttribute{
				Required:    true,
				Description: "Catalog that stores the view.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"schema": resourceschema.StringAttribute{
				Required:    true,
				Description: "Schema that stores the view. Hive lowercases it; the configured spelling is kept in state and the stored spelling is resolved case-insensitively.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": resourceschema.StringAttribute{
				Required:    true,
				Description: "View name. The provider always quotes it. Hive lowercases it; the configured spelling is kept in state and the stored spelling is resolved case-insensitively, because a quoted SHOW CREATE VIEW rejects a non-lowercase name.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"definition": resourceschema.StringAttribute{
				Required: true,
				Description: "SELECT statement placed after AS. This is raw SQL, not a value: identifiers inside it are not escaped, " +
					"and table references are resolved in the provider's session catalog and schema, so qualify them fully. " +
					"Write-only: SHOW CREATE VIEW returns a normalised form, and Hive keeps a stale definition after an underlying column rename, " +
					"so state keeps the configured text instead of pretending to detect definition drift. Changing it runs CREATE OR REPLACE VIEW.",
			},
			"security": resourceschema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(securityDefiner),
				Description: "View security mode: DEFINER runs the view with the definer's privileges, INVOKER with the querying user's. Defaults to DEFINER. " +
					"Read parses the SECURITY clause from SHOW CREATE VIEW, so an out-of-band change is detected. Changing it runs CREATE OR REPLACE VIEW.",
				Validators: []validator.String{
					stringvalidator.OneOf(securityDefiner, securityInvoker),
				},
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

func (r *Resource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config model
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateDefinition(config.Definition)...)
}

func (r *Resource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateDefinition(plan.Definition)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.data.Executor.Exec(ctx, createStatement(plan)); err != nil {
		resp.Diagnostics.Append(r.diagnosticForError("create", err)...)
		return
	}

	state, exists, readDiags := r.refresh(ctx, plan)
	resp.Diagnostics.Append(readDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !exists {
		resp.Diagnostics.AddError(
			"View not found after create",
			fmt.Sprintf("Trino accepted CREATE VIEW for %s but SHOW CREATE VIEW does not list it afterwards.", qualifiedName(plan)),
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
	var plan model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateDefinition(plan.Definition)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Every update is a definition or security change. Trino has no ALTER VIEW
	// for either, so both are applied with CREATE OR REPLACE VIEW.
	if err := r.data.Executor.Exec(ctx, replaceStatement(plan)); err != nil {
		resp.Diagnostics.Append(r.diagnosticForError("replace", err)...)
		return
	}

	updated, exists, readDiags := r.refresh(ctx, plan)
	resp.Diagnostics.Append(readDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !exists {
		resp.Diagnostics.AddError(
			"View not found after update",
			fmt.Sprintf("Trino accepted CREATE OR REPLACE VIEW for %s but SHOW CREATE VIEW does not list it afterwards.", qualifiedName(plan)),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.data.Executor.Exec(ctx, dropStatement(state)); err != nil {
		if isViewMissing(err) {
			return
		}
		resp.Diagnostics.Append(trino.DiagnosticFor("drop", "view", err)...)
	}
}

func (r *Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := resourceutil.SplitImportID(req.ID, ".", 3, importIDExpectation)
	if err != nil {
		resp.Diagnostics.AddError(
			invalidImportID,
			fmt.Sprintf("Expected %s, got %q.", importIDExpectation, req.ID),
		)
		return
	}

	state := model{
		ID:         types.StringValue(strings.Join(parts, ".")),
		Catalog:    types.StringValue(parts[0]),
		Schema:     types.StringValue(parts[1]),
		Name:       types.StringValue(parts[2]),
		Definition: types.StringNull(),
		Security:   types.StringNull(),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// findQuery resolves the view and the spelling Trino stored. Hive lowercases
// schema and view names, and a quoted SHOW CREATE VIEW rejects a non-lowercase
// name ("objectName is not lowercase"), so existence is read from the
// information schema where the comparison can be case-insensitive.
const findQuery = `SELECT table_schema, table_name, table_type FROM %s ` +
	`WHERE lower(table_schema) = lower(?) AND lower(table_name) = lower(?)`

// refresh checks existence through information_schema.tables and reads the
// SECURITY clause from SHOW CREATE VIEW, quoted with the stored spelling. The
// definition is never written back: Trino normalises it and Hive can leave it
// stale, so a text comparison would either produce a permanent diff or report
// "no drift" for a broken view.
func (r *Resource) refresh(ctx context.Context, current model) (model, bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	catalog := current.Catalog.ValueString()
	current.ID = types.StringValue(qualifiedID(current))

	statement := fmt.Sprintf(findQuery, trino.QuoteQualified(catalog, "information_schema", "tables"))
	rows, err := r.data.Executor.Query(ctx, statement, current.Schema.ValueString(), current.Name.ValueString())
	if err != nil {
		if isViewMissing(err) {
			return current, false, diags
		}
		diags.Append(trino.DiagnosticFor("read", "view", err)...)
		return current, false, diags
	}

	view, found, resolveDiags := resolveView(rows, current)
	diags.Append(resolveDiags...)
	if diags.HasError() {
		return current, true, diags
	}
	if !found {
		return current, false, diags
	}

	rows, err = r.data.Executor.Query(ctx, "SHOW CREATE VIEW "+trino.QuoteQualified(catalog, view.schema, view.name))
	if err != nil {
		if isViewMissing(err) {
			return current, false, diags
		}
		diags.Append(trino.DiagnosticFor("read", "view", err)...)
		return current, true, diags
	}
	if len(rows) == 0 {
		tflog.Warn(ctx, "SHOW CREATE VIEW returned no rows; preserving prior state", map[string]any{
			"view": current.ID.ValueString(),
		})
		return current, true, diags
	}

	for _, row := range rows {
		ddl, ok := row.SoleString()
		if !ok {
			tflog.Warn(ctx, "SHOW CREATE VIEW returned an unreadable row; preserving prior state", map[string]any{
				"view": current.ID.ValueString(),
			})
			continue
		}
		mode, err := parseSecurity(ddl)
		if err != nil {
			tflog.Warn(ctx, "Could not parse SHOW CREATE VIEW output; preserving prior state", map[string]any{
				"view":  current.ID.ValueString(),
				"error": err.Error(),
			})
			continue
		}
		current.Security = types.StringValue(mode)
		return current, true, diags
	}

	return current, true, diags
}

// viewName is the spelling Trino stored for the view.
type viewName struct {
	schema string
	name   string
}

// resolveView picks the stored view from information_schema rows. An exact
// case match wins; otherwise a single case-insensitive match is the object the
// connector lowercased. Anything else is either absent or ambiguous.
func resolveView(rows []trino.Row, current model) (viewName, bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	schema := current.Schema.ValueString()
	name := current.Name.ValueString()
	var matches []viewName

	for _, row := range rows {
		storedSchema, schemaOK := row.String("table_schema")
		storedName, nameOK := row.String("table_name")
		tableType, typeOK := row.String("table_type")
		if !schemaOK || !nameOK || !typeOK {
			diags.AddError("Could not read view metadata",
				"information_schema.tables returned a row without table_schema, table_name or table_type.")
			return viewName{}, true, diags
		}
		if !strings.EqualFold(tableType, "VIEW") {
			continue
		}
		if !strings.EqualFold(storedSchema, schema) || !strings.EqualFold(storedName, name) {
			continue
		}
		if storedSchema == schema && storedName == name {
			return viewName{schema: storedSchema, name: storedName}, true, diags
		}
		matches = append(matches, viewName{schema: storedSchema, name: storedName})
	}

	switch len(matches) {
	case 0:
		return viewName{}, false, diags
	case 1:
		return matches[0], true, diags
	default:
		diags.AddError("View name is ambiguous",
			fmt.Sprintf("More than one view matches %s case-insensitively. Rename one of them or use the exact stored spelling.",
				trino.QuoteQualified(current.Catalog.ValueString(), schema, name)))
		return viewName{}, true, diags
	}
}

// isViewMissing reports whether an error means the view is absent. Treating a
// name that is not a view as absent is honest, because a table is not this
// resource.
func isViewMissing(err error) bool {
	if trino.IsNotFound(err) || trino.IsErrorName(err, "CATALOG_NOT_FOUND", "SCHEMA_NOT_FOUND") {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "does not exist") || strings.Contains(message, "not a view")
}

func qualifiedID(value model) string {
	return value.Catalog.ValueString() + "." + value.Schema.ValueString() + "." + value.Name.ValueString()
}

// diagnosticForError turns an existing object into an actionable diagnostic
// instead of a raw server error.
func (r *Resource) diagnosticForError(op string, err error) diag.Diagnostics {
	message := strings.ToLower(err.Error())
	if trino.IsErrorName(err, "ALREADY_EXISTS", "TABLE_ALREADY_EXISTS", "VIEW_ALREADY_EXISTS") ||
		strings.Contains(message, "already exists") {
		var diags diag.Diagnostics
		diags.AddError("View already exists",
			"A view or table with this name already exists. Import it if Terraform should manage it, or choose another name.")
		return diags
	}
	return trino.DiagnosticFor(op, "view", err)
}
