package tableprivileges

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

const tableExistsQuery = `SELECT table_name FROM information_schema.tables WHERE table_catalog = ? AND table_schema = ? AND table_name = ?`

const tablePrivilegesQuery = `SELECT grantee, grantee_type, privilege_type, is_grantable FROM information_schema.table_privileges WHERE table_catalog = ? AND table_schema = ? AND table_name = ? ORDER BY privilege_type`

const schemaPrivilegesQuery = `SELECT table_name, grantee, grantee_type, privilege_type, is_grantable FROM %s WHERE table_schema = ? ORDER BY table_name, privilege_type, grantee`

var privilegeElementType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"table_name":     types.StringType,
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
	TableName     types.String `tfsdk:"table_name"`
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
		Description: "Lists the table privileges recorded for one Trino table, or for every table in a schema when table is omitted. Visibility follows Trino authorization, so a non-admin provider user may only see its own grants.",
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
				Optional:    true,
				Description: "Table whose privileges should be listed. When omitted, every table in the schema is included and each privilege row names its table.",
			},
			"privileges": datasourceschema.ListNestedAttribute{
				Computed:    true,
				Description: "Privileges recorded for the table or schema, sorted by table name, privilege type and grantee.",
				NestedObject: datasourceschema.NestedAttributeObject{
					Attributes: map[string]datasourceschema.Attribute{
						"table_name": datasourceschema.StringAttribute{
							Computed:    true,
							Description: "Table the privilege applies to.",
						},
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
	table := optionalString(config.Table)

	var (
		entries []tablePrivilegeEntry
		diags   diag.Diagnostics
	)
	if table == "" {
		entries, diags = d.readSchema(ctx, catalog, schema)
	} else {
		entries, diags = d.readTable(ctx, catalog, schema, table)
	}
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].TableName.ValueString() != entries[j].TableName.ValueString() {
			return entries[i].TableName.ValueString() < entries[j].TableName.ValueString()
		}
		if entries[i].PrivilegeType.ValueString() != entries[j].PrivilegeType.ValueString() {
			return entries[i].PrivilegeType.ValueString() < entries[j].PrivilegeType.ValueString()
		}
		if entries[i].Grantee.ValueString() != entries[j].Grantee.ValueString() {
			return entries[i].Grantee.ValueString() < entries[j].Grantee.ValueString()
		}
		if entries[i].GranteeType.ValueString() != entries[j].GranteeType.ValueString() {
			return entries[i].GranteeType.ValueString() < entries[j].GranteeType.ValueString()
		}
		return !entries[i].IsGrantable.ValueBool() && entries[j].IsGrantable.ValueBool()
	})

	list, listDiags := types.ListValueFrom(ctx, privilegeElementType, entries)
	resp.Diagnostics.Append(listDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.Privileges = list

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

// readTable lists the privileges recorded for one table. The SQL is unchanged
// from before schema-wide listing existed, so existing configurations read the
// same rows.
func (d *tablePrivilegesDataSource) readTable(ctx context.Context, catalog, schema, table string) ([]tablePrivilegeEntry, diag.Diagnostics) {
	var diags diag.Diagnostics

	args := []any{catalog, schema, table}

	exists, err := d.data.Executor.Query(ctx, tableExistsQuery, args...)
	if err != nil {
		diags.Append(trino.DiagnosticFor("read", "table", err)...)
		return nil, diags
	}
	if len(exists) == 0 {
		diags.AddError("Table not found",
			fmt.Sprintf("Table %s was not found in Trino.", trino.QuoteQualified(catalog, schema, table)))
		return nil, diags
	}

	rows, err := d.data.Executor.Query(ctx, tablePrivilegesQuery, args...)
	if err != nil {
		diags.Append(trino.DiagnosticFor("list", "table privileges", err)...)
		return nil, diags
	}

	entries, decodeDiags := decodePrivileges(rows, table)
	diags.Append(decodeDiags...)
	return entries, diags
}

// readSchema lists every table privilege recorded in one schema. The catalog
// is used to qualify information_schema so the query does not depend on the
// provider's default catalog.
func (d *tablePrivilegesDataSource) readSchema(ctx context.Context, catalog, schema string) ([]tablePrivilegeEntry, diag.Diagnostics) {
	var diags diag.Diagnostics

	schemas, err := d.data.Executor.Query(ctx, "SHOW SCHEMAS FROM "+trino.QuoteIdentifier(catalog))
	if err != nil {
		diags.Append(trino.DiagnosticFor("read", "catalog", err)...)
		return nil, diags
	}
	found := false
	for _, row := range schemas {
		value, ok := row.SoleString()
		if ok && strings.EqualFold(value, schema) {
			found = true
			break
		}
	}
	if !found {
		diags.AddError("Schema not found",
			fmt.Sprintf("Schema %s was not found in Trino.", trino.QuoteQualified(catalog, schema)))
		return nil, diags
	}

	statement := fmt.Sprintf(schemaPrivilegesQuery, trino.QuoteQualified(catalog, "information_schema", "table_privileges"))
	rows, err := d.data.Executor.Query(ctx, statement, schema)
	if err != nil {
		diags.Append(trino.DiagnosticFor("list", "table privileges", err)...)
		return nil, diags
	}

	return decodePrivileges(rows, "")
}

// decodePrivileges converts Query rows to entries. An empty tableName means the
// row must carry table_name; a non-empty one is used for the exact-table read
// that keeps its original query shape.
func decodePrivileges(rows []trino.Row, tableName string) ([]tablePrivilegeEntry, diag.Diagnostics) {
	var diags diag.Diagnostics

	entries := make([]tablePrivilegeEntry, 0, len(rows))
	for _, row := range rows {
		name := tableName
		if name == "" {
			value, ok := row.String("table_name")
			if !ok {
				diags.AddError("Could not read table name",
					"Trino returned a privilege row without a table_name column.")
				return nil, diags
			}
			name = value
		}
		grantee, ok := row.String("grantee")
		if !ok {
			diags.AddError("Could not read grantee",
				"Trino returned a privilege row without a grantee column.")
			return nil, diags
		}
		granteeType, ok := row.String("grantee_type")
		if !ok {
			diags.AddError("Could not read grantee type",
				"Trino returned a privilege row without a grantee_type column.")
			return nil, diags
		}
		privilegeType, ok := row.String("privilege_type")
		if !ok {
			diags.AddError("Could not read privilege type",
				"Trino returned a privilege row without a privilege_type column.")
			return nil, diags
		}
		isGrantable, ok := row.Bool("is_grantable")
		if !ok {
			diags.AddError("Could not read grantable flag",
				"Trino returned a privilege row without a usable is_grantable column.")
			return nil, diags
		}
		entries = append(entries, tablePrivilegeEntry{
			TableName:     types.StringValue(name),
			Grantee:       types.StringValue(grantee),
			GranteeType:   types.StringValue(granteeType),
			PrivilegeType: types.StringValue(privilegeType),
			IsGrantable:   types.BoolValue(isGrantable),
		})
	}
	return entries, diags
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
