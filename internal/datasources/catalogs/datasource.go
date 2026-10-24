package catalogs

import (
	"context"
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

const catalogsQuery = `SELECT catalog_name, connector_name FROM system.metadata.catalogs ORDER BY catalog_name`

var catalogElementType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"name":      types.StringType,
		"connector": types.StringType,
	},
}

var _ datasource.DataSource = (*catalogsDataSource)(nil)
var _ datasource.DataSourceWithConfigure = (*catalogsDataSource)(nil)

// catalogsDataSource lists the catalogs the Trino coordinator exposes.
type catalogsDataSource struct {
	data *providerdata.Data
}

// New returns the trino_catalogs data source.
func New() datasource.DataSource {
	return &catalogsDataSource{}
}

type catalogsDataSourceModel struct {
	NamePrefix types.String `tfsdk:"name_prefix"`
	Catalogs   types.List   `tfsdk:"catalogs"`
}

type catalogEntry struct {
	Name      types.String `tfsdk:"name"`
	Connector types.String `tfsdk:"connector"`
}

func (d *catalogsDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "trino_catalogs"
}

func (d *catalogsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = datasourceschema.Schema{
		Description: "Lists the catalogs registered with Trino.",
		Attributes: map[string]datasourceschema.Attribute{
			"name_prefix": datasourceschema.StringAttribute{
				Optional:    true,
				Description: "Only return catalogs whose name starts with this prefix.",
			},
			"catalogs": datasourceschema.ListNestedAttribute{
				Computed:    true,
				Description: "Catalogs exposed by the coordinator, sorted by name.",
				NestedObject: datasourceschema.NestedAttributeObject{
					Attributes: map[string]datasourceschema.Attribute{
						"name": datasourceschema.StringAttribute{
							Computed:    true,
							Description: "Catalog name.",
						},
						"connector": datasourceschema.StringAttribute{
							Computed:    true,
							Description: "Connector backing the catalog.",
						},
					},
				},
			},
		},
	}
}

func (d *catalogsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *catalogsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config catalogsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, operationTimeout(d.data.Timeout))
	defer cancel()

	rows, err := d.data.Executor.Query(ctx, catalogsQuery)
	if err != nil {
		resp.Diagnostics.Append(trino.DiagnosticFor("list", "catalogs", err)...)
		return
	}

	prefix := ""
	if !config.NamePrefix.IsNull() && !config.NamePrefix.IsUnknown() {
		prefix = config.NamePrefix.ValueString()
	}

	entries := make([]catalogEntry, 0, len(rows))
	for _, row := range rows {
		name, ok := row.String("catalog_name")
		if !ok {
			resp.Diagnostics.AddError("Could not read catalog name",
				"Trino returned a catalog row without a catalog_name column.")
			return
		}
		connector, ok := row.String("connector_name")
		if !ok {
			resp.Diagnostics.AddError("Could not read connector name",
				"Trino returned a catalog row without a connector_name column.")
			return
		}
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		entries = append(entries, catalogEntry{
			Name:      types.StringValue(name),
			Connector: types.StringValue(connector),
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name.ValueString() < entries[j].Name.ValueString()
	})

	list, diags := types.ListValueFrom(ctx, catalogElementType, entries)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.Catalogs = list

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func operationTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return time.Minute
	}
	return timeout
}
