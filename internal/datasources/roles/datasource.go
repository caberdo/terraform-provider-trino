package roles

import (
	"context"
	"sort"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

var _ datasource.DataSource = (*rolesDataSource)(nil)
var _ datasource.DataSourceWithConfigure = (*rolesDataSource)(nil)

// rolesDataSource lists the roles in one catalog.
type rolesDataSource struct {
	data *providerdata.Data
}

// New returns the trino_roles data source.
func New() datasource.DataSource {
	return &rolesDataSource{}
}

type rolesDataSourceModel struct {
	Catalog types.String `tfsdk:"catalog"`
	Roles   types.List   `tfsdk:"roles"`
}

func (d *rolesDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "trino_roles"
}

func (d *rolesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = datasourceschema.Schema{
		Description: "Lists the catalog roles in a Trino catalog.",
		Attributes: map[string]datasourceschema.Attribute{
			"catalog": datasourceschema.StringAttribute{
				Required:    true,
				Description: "Catalog whose roles should be listed.",
			},
			"roles": datasourceschema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "Roles in the catalog, sorted by name.",
			},
		},
	}
}

func (d *rolesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *rolesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config rolesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, operationTimeout(d.data.Timeout))
	defer cancel()

	catalog := config.Catalog.ValueString()
	statement := "SHOW ROLES FROM " + trino.QuoteIdentifier(catalog)
	rows, err := d.data.Executor.Query(ctx, statement)
	if err != nil {
		resp.Diagnostics.Append(trino.DiagnosticFor("list", "roles", err)...)
		return
	}

	names := make([]string, 0, len(rows))
	for _, row := range rows {
		name, ok := row.SoleString()
		if !ok {
			resp.Diagnostics.AddError("Could not read role name",
				"Trino returned a role row with an unexpected shape.")
			return
		}
		names = append(names, name)
	}
	sort.Strings(names)

	list, diags := types.ListValueFrom(ctx, types.StringType, names)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.Roles = list

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func operationTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return time.Minute
	}
	return timeout
}
