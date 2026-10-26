package function

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/resourceutil"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

const (
	typeName            = "trino_function"
	invalidImportID     = "Invalid import ID"
	importIDExpectation = "catalog.schema.name"
)

var (
	_ resource.Resource                   = (*Resource)(nil)
	_ resource.ResourceWithConfigure      = (*Resource)(nil)
	_ resource.ResourceWithImportState    = (*Resource)(nil)
	_ resource.ResourceWithValidateConfig = (*Resource)(nil)
	_ resource.ResourceWithModifyPlan     = (*Resource)(nil)
)

// Resource manages a catalog user-defined function stored by the connector.
type Resource struct {
	data *providerdata.Data
}

// New returns the trino_function resource.
func New() resource.Resource {
	return &Resource{}
}

type model struct {
	ID         types.String `tfsdk:"id"`
	Catalog    types.String `tfsdk:"catalog"`
	Schema     types.String `tfsdk:"schema"`
	Name       types.String `tfsdk:"name"`
	Definition types.String `tfsdk:"definition"`
	OrReplace  types.Bool   `tfsdk:"or_replace"`
}

func (r *Resource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = typeName
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resourceschema.Schema{
		Description: "Manages a catalog user-defined function. The connector backing the catalog must support UDF storage, as the Hive and Memory connectors do. Changing the definition replaces the function in place with CREATE OR REPLACE FUNCTION.",
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
				Description: "Catalog that stores the function.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"schema": resourceschema.StringAttribute{
				Required:    true,
				Description: "Schema that stores the function.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": resourceschema.StringAttribute{
				Required:    true,
				Description: "Function name. Hive lowercases it; the configured spelling is kept in state.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"definition": resourceschema.StringAttribute{
				Required: true,
				Description: "SQL routine definition after the function name, starting with the parameter list, for example `(x bigint) RETURNS bigint RETURN x + 1`. " +
					"This is raw SQL, not a value: identifiers inside it are not escaped by the provider. " +
					"SHOW CREATE FUNCTION returns a normalised form; the provider compares definitions token by token so whitespace, keyword case and the parentheses Trino adds around RETURN expressions do not create diffs.",
			},
			"or_replace": resourceschema.BoolAttribute{
				Optional:    true,
				Description: "Add OR REPLACE when creating the function. Defaults to false, so creating an existing function fails. Definition updates always use CREATE OR REPLACE because Trino has no ALTER FUNCTION.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
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
	if config.Definition.IsNull() || config.Definition.IsUnknown() {
		return
	}
	if err := validateDefinition(config.Definition.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("definition"),
			"Invalid function definition",
			err.Error()+" The definition must start with the parameter list and include RETURNS.",
		)
	}
}

// ModifyPlan keeps the configured definition when it is equivalent to the one
// in state. Without this, the normalised form returned by SHOW CREATE FUNCTION
// would show a cosmetic diff on every plan after an import or an out-of-band
// read.
func (r *Resource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var plan, state model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Definition.IsNull() || plan.Definition.IsUnknown() ||
		state.Definition.IsNull() || state.Definition.IsUnknown() {
		return
	}
	if !definitionsEquivalent(plan.Definition.ValueString(), state.Definition.ValueString()) {
		return
	}
	plan.Definition = state.Definition
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *Resource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateDefinition(plan.Definition.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("definition"), "Invalid function definition",
			err.Error()+" The definition must start with the parameter list and include RETURNS.")
		return
	}

	statement := createStatement(plan, isTrue(plan.OrReplace))
	if err := r.data.Executor.Exec(ctx, statement); err != nil {
		resp.Diagnostics.Append(r.diagnosticForError(ctx, "create", plan.Catalog.ValueString(), err)...)
		return
	}

	state, exists, readDiags := r.refresh(ctx, plan)
	resp.Diagnostics.Append(readDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !exists {
		resp.Diagnostics.AddError(
			"Function not found after create",
			fmt.Sprintf("Trino accepted CREATE FUNCTION for %s but did not list it afterwards.", functionID(plan)),
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
	if err := validateDefinition(plan.Definition.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("definition"), "Invalid function definition",
			err.Error()+" The definition must start with the parameter list and include RETURNS.")
		return
	}

	if !plan.Definition.Equal(state.Definition) &&
		!definitionsEquivalent(plan.Definition.ValueString(), state.Definition.ValueString()) {
		// Trino has no ALTER FUNCTION, so any definition change is a replace.
		statement := createStatement(plan, true)
		if err := r.data.Executor.Exec(ctx, statement); err != nil {
			resp.Diagnostics.Append(r.diagnosticForError(ctx, "replace", plan.Catalog.ValueString(), err)...)
			return
		}
	}

	updated, exists, readDiags := r.refresh(ctx, plan)
	resp.Diagnostics.Append(readDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !exists {
		resp.Diagnostics.AddError(
			"Function not found after update",
			fmt.Sprintf("Trino accepted CREATE OR REPLACE FUNCTION for %s but did not list it afterwards.", functionID(plan)),
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

	parameterTypes, found, diags := r.parameterTypesForDelete(ctx, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		return
	}

	statement := "DROP FUNCTION IF EXISTS " + trino.QuoteQualified(
		state.Catalog.ValueString(), state.Schema.ValueString(), state.Name.ValueString(),
	) + "(" + strings.Join(parameterTypes, ", ") + ")"
	if err := r.data.Executor.Exec(ctx, statement); err != nil {
		if trino.IsNotFound(err) || strings.Contains(strings.ToLower(err.Error()), "function not found") {
			return
		}
		resp.Diagnostics.Append(trino.DiagnosticFor("drop", "function", err)...)
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
		OrReplace:  types.BoolNull(),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// refresh checks existence through SHOW FUNCTIONS and reads the definition
// from SHOW CREATE FUNCTION. A same-named function with a different parameter
// signature is a different overload and does not count as this resource.
func (r *Resource) refresh(ctx context.Context, current model) (model, bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	catalog := current.Catalog.ValueString()
	schema := current.Schema.ValueString()
	name := current.Name.ValueString()
	current.ID = types.StringValue(catalog + "." + schema + "." + name)

	parameterTypes, signatureKnown, err := configuredSignature(current.Definition)
	if err != nil {
		diags.AddAttributeError(path.Root("definition"), "Invalid function definition", err.Error())
		return current, false, diags
	}

	rows, err := r.data.Executor.Query(ctx, "SHOW FUNCTIONS FROM "+trino.QuoteQualified(catalog, schema))
	if err != nil {
		if trino.IsNotFound(err) || trino.IsErrorName(err, "CATALOG_NOT_FOUND", "SCHEMA_NOT_FOUND") {
			return current, false, diags
		}
		diags.Append(trino.DiagnosticFor("read", "function", err)...)
		return current, false, diags
	}

	wantSignature := canonicalTypeList(parameterTypes)
	matches := make([]trino.Row, 0, 1)
	for _, row := range rows {
		functionName, ok := row.String("function")
		if !ok {
			var nameOK bool
			functionName, nameOK = row.String("function name")
			if !nameOK {
				diags.AddError("Could not read function name",
					"Trino returned a function row without a function column.")
				return current, false, diags
			}
		}
		if !strings.EqualFold(functionName, name) {
			continue
		}
		if signatureKnown {
			argumentTypes, ok := row.String("argument types")
			if !ok {
				diags.AddError("Could not read function argument types",
					"Trino returned a function row without an argument types column.")
				return current, false, diags
			}
			if canonicalArgumentTypes(argumentTypes) != wantSignature {
				continue
			}
		}
		matches = append(matches, row)
	}

	if len(matches) == 0 {
		return current, false, diags
	}
	if !signatureKnown && len(matches) > 1 {
		diags.AddError("Function name is overloaded",
			fmt.Sprintf("Function %s has more than one signature and the import ID %q does not identify one. "+
				"Remove the other overloads or import the function before adding them.", trino.QuoteQualified(catalog, schema, name), current.ID.ValueString()))
		return current, true, diags
	}

	ddlRows, err := r.data.Executor.Query(ctx, "SHOW CREATE FUNCTION "+trino.QuoteQualified(catalog, schema, name))
	if err != nil {
		if trino.IsNotFound(err) {
			return current, false, diags
		}
		diags.Append(trino.DiagnosticFor("read", "function", err)...)
		return current, true, diags
	}
	if len(ddlRows) == 0 {
		tflog.Warn(ctx, "SHOW CREATE FUNCTION returned no rows; preserving prior state", map[string]any{
			"function": catalog + "." + schema + "." + name,
		})
		return current, true, diags
	}

	var serverDefinition string
	for _, row := range ddlRows {
		ddl, ok := row.SoleString()
		if !ok {
			tflog.Warn(ctx, "SHOW CREATE FUNCTION returned an unreadable row; preserving prior state")
			continue
		}
		definition, err := extractDefinition(ddl)
		if err != nil {
			tflog.Warn(ctx, "Could not parse SHOW CREATE FUNCTION output; preserving prior state", map[string]any{
				"function": catalog + "." + schema + "." + name,
				"error":    err.Error(),
			})
			continue
		}
		if !signatureKnown {
			serverDefinition = definition
			break
		}
		serverTypes, err := parseSignature(definition)
		if err != nil {
			continue
		}
		if canonicalTypeList(serverTypes) == wantSignature {
			serverDefinition = definition
			break
		}
	}
	if serverDefinition == "" {
		tflog.Warn(ctx, "SHOW CREATE FUNCTION did not return the configured signature; preserving prior state", map[string]any{
			"function": catalog + "." + schema + "." + name,
		})
		return current, true, diags
	}

	if !signatureKnown || !definitionsEquivalent(current.Definition.ValueString(), serverDefinition) {
		current.Definition = types.StringValue(serverDefinition)
	}
	return current, true, diags
}

// configuredSignature parses the parameter types of the state definition. A
// null definition is an import before the first refresh, which is allowed.
func configuredSignature(definition types.String) ([]string, bool, error) {
	if definition.IsNull() || definition.IsUnknown() {
		return nil, false, nil
	}
	parameterTypes, err := parseSignature(definition.ValueString())
	if err != nil {
		return nil, false, err
	}
	return parameterTypes, true, nil
}

// parameterTypesForDelete uses the state definition when it is available and
// falls back to the server when an import has not been refreshed yet.
func (r *Resource) parameterTypesForDelete(ctx context.Context, state model) ([]string, bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	if !state.Definition.IsNull() && !state.Definition.IsUnknown() {
		parameterTypes, err := parseSignature(state.Definition.ValueString())
		if err == nil {
			return parameterTypes, true, diags
		}
	}

	// No usable state definition, for example an import that has not been
	// refreshed yet: ask the server for the signature before dropping.
	rows, queryErr := r.data.Executor.Query(ctx, "SHOW CREATE FUNCTION "+trino.QuoteQualified(
		state.Catalog.ValueString(), state.Schema.ValueString(), state.Name.ValueString(),
	))
	if queryErr != nil {
		if trino.IsNotFound(queryErr) {
			return nil, false, diags
		}
		diags.Append(trino.DiagnosticFor("read", "function", queryErr)...)
		return nil, false, diags
	}
	if len(rows) == 0 {
		return nil, false, diags
	}
	if len(rows) > 1 {
		diags.AddError("Function name is overloaded",
			fmt.Sprintf("Function %s has more than one signature and state has no definition to choose from. "+
				"Remove the other overloads before destroying it.", trino.QuoteQualified(state.Catalog.ValueString(), state.Schema.ValueString(), state.Name.ValueString())))
		return nil, false, diags
	}
	if ddl, ok := rows[0].SoleString(); ok {
		if definition, extractErr := extractDefinition(ddl); extractErr == nil {
			if types, parseErr := parseSignature(definition); parseErr == nil {
				return types, true, diags
			}
		}
	}

	diags.AddAttributeError(path.Root("definition"), "Invalid function definition",
		"Could not parse the stored definition to determine the parameter types DROP FUNCTION requires.")
	return nil, false, diags
}

func functionID(value model) string {
	return value.Catalog.ValueString() + "." + value.Schema.ValueString() + "." + value.Name.ValueString()
}

func createStatement(plan model, orReplace bool) string {
	var b strings.Builder
	b.WriteString("CREATE ")
	if orReplace {
		b.WriteString("OR REPLACE ")
	}
	b.WriteString("FUNCTION ")
	b.WriteString(trino.QuoteQualified(plan.Catalog.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString()))
	b.WriteString(" ")
	b.WriteString(trimStatement(plan.Definition.ValueString()))
	return b.String()
}

func isTrue(value types.Bool) bool {
	return !value.IsNull() && !value.IsUnknown() && value.ValueBool()
}

// diagnosticForError turns connector limitations and existing functions into
// actionable diagnostics instead of raw server errors.
func (r *Resource) diagnosticForError(ctx context.Context, op, catalog string, err error) diag.Diagnostics {
	message := strings.ToLower(err.Error())
	if trino.SourceOf(err) == trino.KindNotSupported ||
		(strings.Contains(message, "does not support") &&
			(strings.Contains(message, "function") || strings.Contains(message, "udf"))) {
		var diags diag.Diagnostics
		connector := r.connectorName(ctx, catalog)
		subject := fmt.Sprintf("Catalog %q", catalog)
		if connector != "" {
			subject = fmt.Sprintf("Catalog %q uses connector %q, which", catalog, connector)
		}
		diags.AddError("Catalog UDF storage is not supported",
			fmt.Sprintf("%s does not support storing user-defined functions. "+
				"Use the Hive or Memory connector, or another connector that implements catalog UDF storage. Trino said: %s",
				subject, err.Error()))
		return diags
	}
	if trino.IsErrorName(err, "ALREADY_EXISTS", "FUNCTION_ALREADY_EXISTS") || strings.Contains(message, "already exists") {
		var diags diag.Diagnostics
		diags.AddError("Function already exists",
			"A function with this name and parameter signature already exists. "+
				"Set or_replace = true to replace it, or choose another name.")
		return diags
	}
	return trino.DiagnosticFor(op, "function", err)
}

// connectorName looks up the connector behind a catalog for diagnostics. It is
// best effort: a lookup failure must not hide the original error.
func (r *Resource) connectorName(ctx context.Context, catalog string) string {
	row, ok, err := r.data.Executor.QueryOne(ctx,
		"SELECT connector_name FROM system.metadata.catalogs WHERE catalog_name = ?", catalog)
	if err != nil || !ok {
		return ""
	}
	connector, _ := row.String("connector_name")
	return connector
}
