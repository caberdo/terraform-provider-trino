package tables

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

const tablesQuery = `SELECT table_name, table_type FROM %s WHERE table_schema = ? ORDER BY table_name`

var tableElementType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"name":       types.StringType,
		"table_type": types.StringType,
	},
}

var _ datasource.DataSource = (*tablesDataSource)(nil)
var _ datasource.DataSourceWithConfigure = (*tablesDataSource)(nil)

// tablesDataSource lists the tables and views in one schema.
type tablesDataSource struct {
	data *providerdata.Data
}

// New returns the trino_tables data source.
func New() datasource.DataSource {
	return &tablesDataSource{}
}

type tablesDataSourceModel struct {
	Catalog    types.String `tfsdk:"catalog"`
	Schema     types.String `tfsdk:"schema"`
	NamePrefix types.String `tfsdk:"name_prefix"`
	TableType  types.String `tfsdk:"table_type"`
	Tables     types.List   `tfsdk:"tables"`
}

type tableEntry struct {
	Name      types.String `tfsdk:"name"`
	TableType types.String `tfsdk:"table_type"`
}

func (d *tablesDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "trino_tables"
}

func (d *tablesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = datasourceschema.Schema{
		Description: "Lists the tables and views in one schema of a Trino catalog.",
		Attributes: map[string]datasourceschema.Attribute{
			"catalog": datasourceschema.StringAttribute{
				Required:    true,
				Description: "Catalog containing the schema.",
			},
			"schema": datasourceschema.StringAttribute{
				Required:    true,
				Description: "Schema whose tables should be listed.",
			},
			"name_prefix": datasourceschema.StringAttribute{
				Optional:    true,
				Description: "Only return tables whose name starts with this prefix. Matching is case-insensitive.",
			},
			"table_type": datasourceschema.StringAttribute{
				Optional:    true,
				Description: "Only return tables of this type, for example BASE TABLE or VIEW. Matching is case-insensitive.",
			},
			"tables": datasourceschema.ListNestedAttribute{
				Computed:    true,
				Description: "Tables in the schema, sorted by name.",
				NestedObject: datasourceschema.NestedAttributeObject{
					Attributes: map[string]datasourceschema.Attribute{
						"name": datasourceschema.StringAttribute{
							Computed:    true,
							Description: "Table name.",
						},
						"table_type": datasourceschema.StringAttribute{
							Computed:    true,
							Description: "Table type as reported by information_schema, for example BASE TABLE or VIEW.",
						},
					},
				},
			},
		},
	}
}

func (d *tablesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, diags := providerdata.FromProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || data == nil {
		return
	}
	d.data = data
}

func (d *tablesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config tablesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, operationTimeout(d.data.Timeout))
	defer cancel()

	catalog := config.Catalog.ValueString()
	schema := config.Schema.ValueString()

	schemas, err := d.data.Executor.Query(ctx, "SHOW SCHEMAS FROM "+trino.QuoteIdentifier(catalog))
	if err != nil {
		resp.Diagnostics.Append(trino.DiagnosticFor("read", "catalog", err)...)
		return
	}
	if !containsFold(schemas, schema) {
		resp.Diagnostics.AddError("Schema not found",
			fmt.Sprintf("Schema %s was not found in Trino.", trino.QuoteQualified(catalog, schema)))
		return
	}

	statement := fmt.Sprintf(tablesQuery, trino.QuoteQualified(catalog, "information_schema", "tables"))
	rows, err := d.data.Executor.Query(ctx, statement, schema)
	if err != nil {
		resp.Diagnostics.Append(trino.DiagnosticFor("list", "tables", err)...)
		return
	}

	prefix := strings.ToLower(optionalString(config.NamePrefix))
	tableType := optionalString(config.TableType)

	entries := make([]tableEntry, 0, len(rows))
	for _, row := range rows {
		name, ok := row.String("table_name")
		if !ok {
			resp.Diagnostics.AddError("Could not read table name",
				"Trino returned a table row without a table_name column.")
			return
		}
		kind, ok := row.String("table_type")
		if !ok {
			resp.Diagnostics.AddError("Could not read table type",
				"Trino returned a table row without a table_type column.")
			return
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(name), prefix) {
			continue
		}
		if tableType != "" && !strings.EqualFold(kind, tableType) {
			continue
		}
		entries = append(entries, tableEntry{
			Name:      types.StringValue(name),
			TableType: types.StringValue(kind),
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Name.ValueString() != entries[j].Name.ValueString() {
			return entries[i].Name.ValueString() < entries[j].Name.ValueString()
		}
		return entries[i].TableType.ValueString() < entries[j].TableType.ValueString()
	})

	list, diags := types.ListValueFrom(ctx, tableElementType, entries)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.Tables = list

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

// containsFold reports whether rows contain a single-column value matching
// name case-insensitively. Hive lowercases schema names; the configured
// spelling is kept in state and compared the same way as trino_schema.
func containsFold(rows []trino.Row, name string) bool {
	for _, row := range rows {
		value, ok := row.SoleString()
		if ok && strings.EqualFold(value, name) {
			return true
		}
	}
	return false
}

func optionalString(value types.String) string {
	if value.IsNull() || value.IsUnknown() {
		return ""
	}
	return strings.TrimSpace(value.ValueString())
}

func operationTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return time.Minute
	}
	return timeout
}
