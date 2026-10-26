package materializedview

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/resourceutil"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

const (
	typeName            = "trino_materialized_view"
	invalidImportID     = "Invalid import ID"
	importIDExpectation = "catalog.schema.name"

	// findQuery locates the view in the system metadata table. The column
	// filters are pushed down, so Trino only materialises this view's row.
	findQuery = `SELECT name FROM system.metadata.materialized_views ` +
		`WHERE catalog_name = ? AND schema_name = ? AND name = ?`
)

var (
	_ resource.Resource                   = (*Resource)(nil)
	_ resource.ResourceWithConfigure      = (*Resource)(nil)
	_ resource.ResourceWithImportState    = (*Resource)(nil)
	_ resource.ResourceWithValidateConfig = (*Resource)(nil)
)

// Resource manages a connector-backed materialized view. Read never refreshes:
// the stored data is only rebuilt by CREATE (when refresh_on_create is set) or
// when refresh_token changes.
type Resource struct {
	data *providerdata.Data
}

// New returns the trino_materialized_view resource.
func New() resource.Resource {
	return &Resource{}
}

type model struct {
	ID              types.String `tfsdk:"id"`
	Catalog         types.String `tfsdk:"catalog"`
	Schema          types.String `tfsdk:"schema"`
	Name            types.String `tfsdk:"name"`
	Query           types.String `tfsdk:"query"`
	Properties      types.Map    `tfsdk:"properties"`
	GracePeriod     types.String `tfsdk:"grace_period"`
	RefreshOnCreate types.Bool   `tfsdk:"refresh_on_create"`
	RefreshToken    types.String `tfsdk:"refresh_token"`
}

func (r *Resource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = typeName
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resourceschema.Schema{
		Description: "Manages a materialized view. Trino only supports materialized views on connectors that implement them; the Iceberg connector needs a Hive metastore, because the REST catalog rejects CREATE MATERIALIZED VIEW. Read never refreshes the view.",
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
				Description: "Catalog that stores the materialized view.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"schema": resourceschema.StringAttribute{
				Required:    true,
				Description: "Schema that stores the materialized view.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": resourceschema.StringAttribute{
				Required:    true,
				Description: "Materialized view name. The provider always quotes it, so the configured spelling is kept exactly.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"query": resourceschema.StringAttribute{
				Required: true,
				Description: "SQL query that defines the view, placed after AS. This is raw SQL, not a value: identifiers inside it are not escaped, " +
					"and table references are resolved in the provider's session catalog and schema, so qualify them fully. " +
					"Trino stores a normalised form of the query; the provider keeps the configured text in state instead of rewriting it from read-back, " +
					"so a cosmetic normalisation never causes a diff. Changing the query replaces the view.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"properties": resourceschema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Storage-table properties as raw SQL expressions, for example `{ format = \"'ORC'\", partitioning = \"ARRAY['region']\" }`. " +
					"The provider quotes the property names and emits the values verbatim. Property names are matched case-insensitively. " +
					"Read compares only the keys configured here against SHOW CREATE MATERIALIZED VIEW: Iceberg's derived values (location, format_version, storage_schema, ...) are ignored, " +
					"and a configured property the server does not report is dropped from state so the difference surfaces as a plan. " +
					"Changing the map replaces the view because Iceberg does not implement ALTER MATERIALIZED VIEW SET PROPERTIES.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
			},
			"grace_period": resourceschema.StringAttribute{
				Optional: true,
				Description: "Grace period as a Go duration, for example 1h or 30m. Trino renders it as INTERVAL '<seconds>' SECOND. " +
					"Write-only: Trino 476's SHOW CREATE MATERIALIZED VIEW does not return it, so the provider keeps the configured value and cannot detect an out-of-band change. " +
					"Changing it replaces the view.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"refresh_on_create": resourceschema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Run REFRESH MATERIALIZED VIEW after CREATE so the view is populated. Defaults to true and is only used when the view is created.",
			},
			"refresh_token": resourceschema.StringAttribute{
				Optional: true,
				Description: "Change this value to run REFRESH MATERIALIZED VIEW on the next apply. Any string works, for example a timestamp. " +
					"The provider never refreshes during a read, so a stale view is left stale until this changes, the view is replaced, or a scheduler refreshes it.",
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
	resp.Diagnostics.Append(validateQuery(config.Query)...)
	resp.Diagnostics.Append(validateGracePeriod(config.GracePeriod)...)
	resp.Diagnostics.Append(validateProperties(config.Properties)...)
}

func (r *Resource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	properties := resourceutil.StringMap(ctx, plan.Properties, &resp.Diagnostics)
	gracePeriod, err := parsedGracePeriod(plan.GracePeriod)
	if err != nil {
		resp.Diagnostics.Append(validateGracePeriod(plan.GracePeriod)...)
		return
	}
	resp.Diagnostics.Append(validateQuery(plan.Query)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.data.Executor.Exec(ctx, createStatement(plan, properties, gracePeriod)); err != nil {
		resp.Diagnostics.Append(r.diagnosticForError(ctx, "create", plan.Catalog.ValueString(), err)...)
		return
	}

	if refreshOnCreate(plan.RefreshOnCreate) {
		if err := r.data.Executor.Exec(ctx, refreshStatement(plan)); err != nil {
			// The view exists, so record it before reporting the failed
			// refresh; otherwise the retry would fail with ALREADY_EXISTS.
			if created, exists, readDiags := r.refresh(ctx, plan); exists {
				resp.Diagnostics.Append(resp.State.Set(ctx, &created)...)
			} else {
				resp.Diagnostics.Append(readDiags...)
			}
			resp.Diagnostics.Append(trino.DiagnosticFor("refresh", "materialized view", err)...)
			return
		}
	}

	state, exists, readDiags := r.refresh(ctx, plan)
	resp.Diagnostics.Append(readDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !exists {
		resp.Diagnostics.AddError(
			"Materialized view not found after create",
			fmt.Sprintf("Trino accepted CREATE MATERIALIZED VIEW for %s but did not list it afterwards.", qualifiedName(plan)),
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

	updated, exists, diags = r.readDefinition(ctx, updated)
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
	resp.Diagnostics.Append(validateQuery(plan.Query)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Every configurable attribute except refresh_on_create and refresh_token
	// requires replacement, so a real update is a refresh request. A token
	// change is explicit: the provider never refreshes from a read.
	if !plan.RefreshToken.Equal(state.RefreshToken) {
		if err := r.data.Executor.Exec(ctx, refreshStatement(plan)); err != nil {
			resp.Diagnostics.Append(trino.DiagnosticFor("refresh", "materialized view", err)...)
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
			"Materialized view not found after update",
			fmt.Sprintf("The materialized view %s disappeared during the update. Run terraform apply again to recreate it.", qualifiedName(plan)),
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
		if trino.IsNotFound(err) {
			return
		}
		resp.Diagnostics.Append(trino.DiagnosticFor("drop", "materialized view", err)...)
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
		ID:              types.StringValue(strings.Join(parts, ".")),
		Catalog:         types.StringValue(parts[0]),
		Schema:          types.StringValue(parts[1]),
		Name:            types.StringValue(parts[2]),
		Query:           types.StringNull(),
		Properties:      types.MapNull(types.StringType),
		GracePeriod:     types.StringNull(),
		RefreshOnCreate: types.BoolNull(),
		RefreshToken:    types.StringNull(),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// refresh checks existence through system.metadata.materialized_views and
// refreshes the identifier. It deliberately does not read the definition: a
// configured query is never overwritten, because Trino stores a normalised form
// and replacing the authored text would create a permanent diff.
func (r *Resource) refresh(ctx context.Context, current model) (model, bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	catalog := current.Catalog.ValueString()
	schema := current.Schema.ValueString()
	name := current.Name.ValueString()
	current.ID = types.StringValue(catalog + "." + schema + "." + name)

	row, found, err := r.data.Executor.QueryOne(ctx, findQuery, catalog, schema, name)
	if err != nil {
		if trino.IsNotFound(err) || trino.IsErrorName(err, "CATALOG_NOT_FOUND", "SCHEMA_NOT_FOUND") {
			return current, false, diags
		}
		diags.Append(trino.DiagnosticFor("read", "materialized view", err)...)
		return current, false, diags
	}
	if !found {
		return current, false, diags
	}
	if _, ok := row.String("name"); !ok {
		diags.AddError("Could not read materialized view name",
			"Trino returned a materialized view row without a name column.")
		return current, false, diags
	}
	return current, true, diags
}

// readDefinition fills the definition-dependent attributes of state from SHOW
// CREATE MATERIALIZED VIEW. It runs when an import has no query yet, or when
// properties are configured and their server value must be compared. Trino
// normalises the query, so a configured query is preserved rather than
// replaced; properties are compared only for the configured keys, because
// Iceberg also reports values derived from the storage table.
func (r *Resource) readDefinition(ctx context.Context, current model) (model, bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	configured := resourceutil.StringMap(ctx, current.Properties, &diags)
	if diags.HasError() {
		return current, true, diags
	}

	if !current.Query.IsNull() && !current.Query.IsUnknown() && len(configured) == 0 {
		return current, true, diags
	}

	rows, err := r.data.Executor.Query(ctx, "SHOW CREATE MATERIALIZED VIEW "+qualifiedName(current))
	if err != nil {
		if trino.IsNotFound(err) {
			return current, false, diags
		}
		diags.Append(trino.DiagnosticFor("read", "materialized view", err)...)
		return current, true, diags
	}

	var definition showCreateStatement
	parsed := false
	for _, definitionRow := range rows {
		statement, ok := definitionRow.SoleString()
		if !ok {
			continue
		}
		parsedDefinition, err := parseShowCreate(statement)
		if err != nil {
			tflog.Warn(ctx, "Could not parse SHOW CREATE MATERIALIZED VIEW output; preserving prior state", map[string]any{
				"materialized_view": current.ID.ValueString(),
				"error":             err.Error(),
			})
			continue
		}
		definition = parsedDefinition
		parsed = true
		break
	}
	if !parsed {
		return current, true, diags
	}

	if current.Query.IsNull() || current.Query.IsUnknown() {
		current.Query = types.StringValue(definition.query)
	}

	if len(configured) > 0 {
		reconciled := make(map[string]string, len(configured))
		for key, value := range configured {
			serverValue, ok := definition.properties[strings.ToLower(key)]
			if !ok {
				// The server does not report the property. Dropping it from
				// state surfaces the difference as a plan instead of hiding it.
				continue
			}
			if resourceutil.EquivalentSQL(value, serverValue) {
				reconciled[key] = value
				continue
			}
			reconciled[key] = serverValue
		}
		properties, valueDiags := types.MapValueFrom(ctx, types.StringType, reconciled)
		diags.Append(valueDiags...)
		if diags.HasError() {
			return current, true, diags
		}
		current.Properties = properties
	}

	return current, true, diags
}

func validateQuery(value types.String) diag.Diagnostics {
	var diags diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return diags
	}
	if resourceutil.TrimStatement(value.ValueString()) == "" {
		diags.AddAttributeError(path.Root("query"), "Invalid query",
			"query must not be empty; it must be the SELECT statement placed after AS.")
	}
	return diags
}

func validateGracePeriod(value types.String) diag.Diagnostics {
	var diags diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return diags
	}
	if _, err := renderGracePeriod(value.ValueString()); err != nil {
		diags.AddAttributeError(path.Root("grace_period"), "Invalid grace period", err.Error())
	}
	return diags
}

func validateProperties(value types.Map) diag.Diagnostics {
	var diags diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return diags
	}
	for key, element := range value.Elements() {
		if strings.TrimSpace(key) == "" {
			diags.AddAttributeError(path.Root("properties"), "Invalid property",
				"properties must not contain an empty property name.")
			continue
		}
		raw, ok := element.(types.String)
		if !ok || raw.IsNull() || raw.IsUnknown() {
			continue
		}
		if strings.TrimSpace(raw.ValueString()) == "" {
			diags.AddAttributeError(path.Root("properties"), "Invalid property",
				fmt.Sprintf("Property %q has an empty value.", key))
		}
	}
	return diags
}

// parsedGracePeriod renders grace_period, returning an empty string when it is
// not configured.
func parsedGracePeriod(value types.String) (string, error) {
	if value.IsNull() || value.IsUnknown() {
		return "", nil
	}
	return renderGracePeriod(value.ValueString())
}

func refreshOnCreate(value types.Bool) bool {
	if value.IsNull() || value.IsUnknown() {
		return true
	}
	return value.ValueBool()
}

// diagnosticForError turns connector limitations and existing views into
// actionable diagnostics instead of raw server errors.
func (r *Resource) diagnosticForError(ctx context.Context, op, catalog string, err error) diag.Diagnostics {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "materialized view") &&
		(trino.SourceOf(err) == trino.KindNotSupported || strings.Contains(message, "does not support")) {
		var diags diag.Diagnostics
		connector := r.connectorName(ctx, catalog)
		subject := fmt.Sprintf("Catalog %q", catalog)
		if connector != "" {
			subject = fmt.Sprintf("Catalog %q uses connector %q, which", catalog, connector)
		}
		diags.AddError("Materialized views are not supported",
			fmt.Sprintf("%s does not support materialized views. Use the Iceberg connector with a Hive metastore, or another connector that implements materialized-view storage. Trino said: %s",
				subject, err.Error()))
		return diags
	}
	if trino.IsErrorName(err, "ALREADY_EXISTS", "TABLE_ALREADY_EXISTS") || strings.Contains(message, "already exists") {
		var diags diag.Diagnostics
		diags.AddError("Materialized view already exists",
			"A materialized view with this name already exists. Import it if Terraform should manage it, or choose another name.")
		return diags
	}
	return trino.DiagnosticFor(op, "materialized view", err)
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
