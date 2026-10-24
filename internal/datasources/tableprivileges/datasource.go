package tableprivileges

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

const tableExistsQuery = `SELECT table_name FROM information_schema.tables WHERE table_catalog = ? AND table_schema = ? AND table_name = ?`

const tablePrivilegesQuery = `SELECT grantee, grantee_type, privilege_type, is_grantable FROM information_schema.table_privileges WHERE table_catalog = ? AND table_schema = ? AND table_name = ? ORDER BY privilege_type`

var privilegeElementType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"grantee":        types.StringType,
		"grantee_type":   types.StringType,
		"privilege_type": types.StringType,
		"is_grantable":   types.BoolType,
	},
}

var _ datasource.DataSource = (*tablePrivilegesDataSource)(nil)
var _ datasource.DataSourceWithConfigure = (*tablePrivilegesDataSource)(nil)

// tablePrivilegesDataSource lists the table privileges recorded for a table.
type tablePrivilegesDataSource struct {
	data *providerdata.Data
}

// New returns the trino_table_privileges data source.
func New() datasource.DataSource {
	return &tablePrivilegesDataSource{}
}

type tablePrivilegesDataSourceModel struct {
	Catalog    types.String `tfsdk:"catalog"`
	Schema     types.String `tfsdk:"schema"`
	Table      types.String `tfsdk:"table"`
	Privileges types.List   `tfsdk:"privileges"`
}

type tablePrivilegeEntry struct {
	Grantee       types.String `tfsdk:"grantee"`
	GranteeType   types.String `tfsdk:"grantee_type"`
	PrivilegeType types.String `tfsdk:"privilege_type"`
	IsGrantable   types.Bool   `tfsdk:"is_grantable"`
}

func (d *tablePrivilegesDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "trino_table_privileges"
}

func (d *tablePrivilegesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = datasourceschema.Schema{
		Description: "Lists the table privileges recorded for one Trino table.",
		Attributes: map[string]datasourceschema.Attribute{
			"catalog": datasourceschema.StringAttribute{
				Required:    true,
				Description: "Catalog containing the table.",
			},
			"schema": datasourceschema.StringAttribute{
				Required:    true,
				Description: "Schema containing the table.",
			},
			"table": datasourceschema.StringAttribute{
				Required:    true,
				Description: "Table whose privileges should be listed.",
			},
			"privileges": datasourceschema.ListNestedAttribute{
				Computed:    true,
				Description: "Privileges recorded for the table, sorted by privilege type and grantee.",
				NestedObject: datasourceschema.NestedAttributeObject{
					Attributes: map[string]datasourceschema.Attribute{
						"grantee": datasourceschema.StringAttribute{
							Computed:    true,
							Description: "Principal the privilege is granted to.",
						},
						"grantee_type": datasourceschema.StringAttribute{
							Computed:    true,
							Description: "Type of the grantee, for example USER or ROLE.",
						},
						"privilege_type": datasourceschema.StringAttribute{
							Computed:    true,
							Description: "Privilege granted on the table.",
						},
						"is_grantable": datasourceschema.BoolAttribute{
							Computed:    true,
							Description: "Whether the grantee may grant the privilege to others.",
						},
					},
				},
			},
		},
	}
}

func (d *tablePrivilegesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *tablePrivilegesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config tablePrivilegesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, operationTimeout(d.data.Timeout))
	defer cancel()

	catalog := config.Catalog.ValueString()
	schema := config.Schema.ValueString()
	table := config.Table.ValueString()
	args := []any{catalog, schema, table}

	exists, err := d.data.Executor.Query(ctx, tableExistsQuery, args...)
	if err != nil {
		resp.Diagnostics.Append(trino.DiagnosticFor("read", "table", err)...)
		return
	}
	if len(exists) == 0 {
		resp.Diagnostics.AddError("Table not found",
			fmt.Sprintf("Table %s was not found in Trino.", trino.QuoteQualified(catalog, schema, table)))
		return
	}

	rows, err := d.data.Executor.Query(ctx, tablePrivilegesQuery, args...)
	if err != nil {
		resp.Diagnostics.Append(trino.DiagnosticFor("list", "table privileges", err)...)
		return
	}

	entries := make([]tablePrivilegeEntry, 0, len(rows))
	for _, row := range rows {
		grantee, ok := row.String("grantee")
		if !ok {
			resp.Diagnostics.AddError("Could not read grantee",
				"Trino returned a privilege row without a grantee column.")
			return
		}
		granteeType, ok := row.String("grantee_type")
		if !ok {
			resp.Diagnostics.AddError("Could not read grantee type",
				"Trino returned a privilege row without a grantee_type column.")
			return
		}
		privilegeType, ok := row.String("privilege_type")
		if !ok {
			resp.Diagnostics.AddError("Could not read privilege type",
				"Trino returned a privilege row without a privilege_type column.")
			return
		}
		isGrantable, ok := row.Bool("is_grantable")
		if !ok {
			resp.Diagnostics.AddError("Could not read grantable flag",
				"Trino returned a privilege row without a usable is_grantable column.")
			return
		}
		entries = append(entries, tablePrivilegeEntry{
			Grantee:       types.StringValue(grantee),
			GranteeType:   types.StringValue(granteeType),
			PrivilegeType: types.StringValue(privilegeType),
			IsGrantable:   types.BoolValue(isGrantable),
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].PrivilegeType.ValueString() != entries[j].PrivilegeType.ValueString() {
			return entries[i].PrivilegeType.ValueString() < entries[j].PrivilegeType.ValueString()
		}
		return entries[i].Grantee.ValueString() < entries[j].Grantee.ValueString()
	})

	list, diags := types.ListValueFrom(ctx, privilegeElementType, entries)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.Privileges = list

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func operationTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return time.Minute
	}
	return timeout
}
