// Package catalog implements the trino_catalog resource.
package catalog

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
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

const catalogLookupSQL = `SELECT catalog_name, connector_name FROM system.metadata.catalogs WHERE catalog_name = ?`

// connectorNamePattern is the shape of a Trino connector plugin name. The
// connector is emitted bare in CREATE CATALOG (Trino rejects a quoted
// connector), so the value is constrained to a safe unquoted identifier
// instead of being passed through trino.QuoteIdentifier.
var connectorNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// catalogModel is the Terraform state for a Trino catalog. Properties and
// sensitive_properties are write-only: Trino exposes no read path for them, so
// they are preserved from configuration/state rather than read back.
type catalogModel struct {
	ID                  types.String `tfsdk:"id"`
	Name                types.String `tfsdk:"name"`
	Connector           types.String `tfsdk:"connector"`
	Properties          types.Map    `tfsdk:"properties"`
	SensitiveProperties types.Map    `tfsdk:"sensitive_properties"`
}

type catalogResource struct {
	executor trino.Executor
}

var (
	_ resource.Resource                = (*catalogResource)(nil)
	_ resource.ResourceWithConfigure   = (*catalogResource)(nil)
	_ resource.ResourceWithImportState = (*catalogResource)(nil)
)

// New returns the trino_catalog resource.
func New() resource.Resource {
	return &catalogResource{}
}

func (r *catalogResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "trino_catalog"
}

func (r *catalogResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Trino catalog through dynamic catalog management (`catalog.management=dynamic`). " +
			"Catalog properties are write-only: Trino has no read path for them, so Terraform keeps the configured values in state and cannot detect property drift. " +
			"Import is not supported for the same reason.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The catalog name. This is the same value as `name`.",
			},
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Name of the catalog. Trino catalog names are case-insensitive; the configured spelling is kept in state. " +
					"Changing this forces a new catalog to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"connector": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Trino connector that backs the catalog, for example `memory`, `tpch`, `hive` or `iceberg`. " +
					"Must be a lowercase connector name (letters, digits and underscores, starting with a letter). " +
					"Changing this forces a new catalog to be created.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(connectorNamePattern,
						"must be a lowercase Trino connector name: letters, digits and underscores, starting with a letter"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"properties": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Catalog properties passed to the connector. Every value is sent to Trino as a string literal. " +
					"**Write-only:** Trino cannot read catalog properties back, so drift in this map is not detected and Terraform keeps the configured values in state. " +
					"Changing this forces a new catalog to be created.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
			},
			"sensitive_properties": schema.MapAttribute{
				Optional:    true,
				Sensitive:   true,
				ElementType: types.StringType,
				MarkdownDescription: "Catalog properties whose values are secret, merged with `properties` when the catalog is created. " +
					"**Sensitive and write-only:** values are never read back from Trino and are stored in Terraform state as sensitive. " +
					"Changing this forces a new catalog to be created.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *catalogResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, diags := providerdata.FromProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || data == nil {
		return
	}
	r.executor = data.Executor
}

func (r *catalogResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan catalogModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := plan.Name.ValueString()

	existing, diags := readCatalog(ctx, r.executor, name)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if existing.found {
		resp.Diagnostics.AddError(
			fmt.Sprintf("Catalog %q already exists", name),
			fmt.Sprintf("trino_catalog does not support import because catalog properties cannot be read back. "+
				"The catalog %q already exists in Trino. Drop the existing catalog or choose another name.", name),
		)
		return
	}

	properties := resourceutil.StringMap(ctx, plan.Properties, &resp.Diagnostics)
	sensitive := resourceutil.StringMap(ctx, plan.SensitiveProperties, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	for key, value := range sensitive {
		properties[key] = value
	}

	statement := buildCreateStatement(name, plan.Connector.ValueString(), properties)
	if err := r.executor.Exec(ctx, statement); err != nil {
		resp.Diagnostics.Append(trino.DiagnosticFor("create", "catalog", err)...)
		return
	}

	created, diags := readCatalog(ctx, r.executor, name)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !created.found {
		resp.Diagnostics.AddError(
			"Catalog not found after create",
			fmt.Sprintf("Trino accepted the request to create catalog %q, but it does not appear in system.metadata.catalogs. It may have been removed out of band.", name),
		)
		return
	}

	plan.ID = types.StringValue(name)
	plan.Name = types.StringValue(name)
	plan.Connector = types.StringValue(created.connector)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *catalogResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state catalogModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	if name == "" {
		name = state.ID.ValueString()
	}
	if name == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	current, diags := readCatalog(ctx, r.executor, name)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !current.found {
		resp.State.RemoveResource(ctx)
		return
	}

	// Catalog properties cannot be read back; keep the state values exactly as
	// they are. Only the identity and the connector are refreshed.
	state.ID = types.StringValue(name)
	state.Name = types.StringValue(name)
	if current.connector != "" {
		state.Connector = types.StringValue(current.connector)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *catalogResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan catalogModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Every configurable attribute requires replacement, so Update is
	// unreachable in practice. Copy the plan to state and do not emit SQL, so a
	// framework-driven no-op update cannot corrupt state.
	tflog.Info(ctx, "trino_catalog update is a state-only no-op", map[string]any{
		"name": plan.Name.ValueString(),
	})
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *catalogResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state catalogModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	if name == "" {
		name = state.ID.ValueString()
	}
	if name == "" {
		return
	}

	err := r.executor.Exec(ctx, "DROP CATALOG "+trino.QuoteIdentifier(name))
	if err == nil {
		return
	}
	if trino.IsNotFound(err) || trino.IsErrorName(err, "CATALOG_NOT_FOUND") {
		return
	}
	resp.Diagnostics.Append(trino.DiagnosticFor("drop", "catalog", err)...)
}

func (r *catalogResource) ImportState(_ context.Context, _ resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.AddError(
		"Import is not supported for trino_catalog",
		"Catalog properties cannot be read back from Trino, so an import would start with empty properties. "+
			"Because every catalog attribute requires replacement, the first plan would drop and recreate the live catalog. "+
			"Define the resource in configuration and let Terraform manage an existing catalog by name only if appropriate, or remove it from state.",
	)
}

type catalogLookup struct {
	found     bool
	connector string
}

func readCatalog(ctx context.Context, executor trino.Executor, name string) (catalogLookup, diag.Diagnostics) {
	var diags diag.Diagnostics
	rows, err := executor.Query(ctx, catalogLookupSQL, name)
	if err != nil {
		diags.Append(trino.DiagnosticFor("read", "catalog", err)...)
		return catalogLookup{}, diags
	}
	if len(rows) == 0 {
		return catalogLookup{}, diags
	}

	result := catalogLookup{found: true}
	if connector, ok := rows[0].String("connector_name"); ok {
		result.connector = connector
	}
	return result, diags
}

func buildCreateStatement(name, connector string, properties map[string]string) string {
	// The connector name is emitted bare, not as a quoted identifier: Trino
	// parses `USING "memory"` as a quoted identifier and rejects it with
	// `Invalid connector name: "memory"`. The schema validator constrains the
	// value to a safe unquoted identifier.
	statement := "CREATE CATALOG " + trino.QuoteIdentifier(name) + " USING " + connector
	if len(properties) == 0 {
		return statement
	}

	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, trino.QuoteIdentifier(key)+" = "+trino.QuoteLiteral(properties[key]))
	}
	return statement + " WITH (" + strings.Join(parts, ", ") + ")"
}
