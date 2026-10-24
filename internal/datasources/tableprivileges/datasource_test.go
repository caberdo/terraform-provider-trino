package tableprivileges

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

func TestTablePrivilegesReadSQL(t *testing.T) {
	fake := &trinotest.FakeExecutor{
		Steps: []trinotest.Step{
			{Rows: []trino.Row{{"table_name": "acc_ds_orders"}}},
			{Rows: []trino.Row{
				{"grantee": "bob", "grantee_type": "USER", "privilege_type": "SELECT", "is_grantable": "NO"},
				{"grantee": "alice", "grantee_type": "USER", "privilege_type": "SELECT", "is_grantable": "YES"},
				{"grantee": "alice", "grantee_type": "USER", "privilege_type": "INSERT", "is_grantable": "NO"},
			}},
		},
	}
	ds := newTestDataSource(t, fake)

	result, diags := readTablePrivileges(t, ds, tablePrivilegesDataSourceModel{
		Catalog:    types.StringValue("hive"),
		Schema:     types.StringValue("acc_ds_sales"),
		Table:      types.StringValue("acc_ds_orders"),
		Privileges: types.ListNull(privilegeElementType),
	})
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}

	if len(fake.Calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(fake.Calls))
	}
	if fake.Calls[0].Statement != tableExistsQuery {
		t.Fatalf("existence statement = %q, want %q", fake.Calls[0].Statement, tableExistsQuery)
	}
	wantArgs := []any{"hive", "acc_ds_sales", "acc_ds_orders"}
	if !reflect.DeepEqual(fake.Calls[0].Args, wantArgs) {
		t.Fatalf("existence args = %v, want %v", fake.Calls[0].Args, wantArgs)
	}
	if fake.Calls[1].Statement != tablePrivilegesQuery {
		t.Fatalf("privileges statement = %q, want %q", fake.Calls[1].Statement, tablePrivilegesQuery)
	}
	if !strings.Contains(fake.Calls[1].Statement, "ORDER BY privilege_type") {
		t.Fatalf("privileges statement must order by privilege_type: %q", fake.Calls[1].Statement)
	}
	if !reflect.DeepEqual(fake.Calls[1].Args, wantArgs) {
		t.Fatalf("privileges args = %v, want %v", fake.Calls[1].Args, wantArgs)
	}

	var entries []tablePrivilegeEntry
	if diags := result.Privileges.ElementsAs(context.Background(), &entries, false); diags.HasError() {
		t.Fatalf("decode privileges: %v", diags)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 privileges, got %d", len(entries))
	}
	type row struct {
		privilegeType string
		grantee       string
		grantable     bool
	}
	got := make([]row, len(entries))
	for i, entry := range entries {
		got[i] = row{
			privilegeType: entry.PrivilegeType.ValueString(),
			grantee:       entry.Grantee.ValueString(),
			grantable:     entry.IsGrantable.ValueBool(),
		}
	}
	want := []row{
		{privilegeType: "INSERT", grantee: "alice", grantable: false},
		{privilegeType: "SELECT", grantee: "alice", grantable: true},
		{privilegeType: "SELECT", grantee: "bob", grantable: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("privileges = %v, want %v", got, want)
	}
}

func TestTablePrivilegesEmptyResult(t *testing.T) {
	fake := &trinotest.FakeExecutor{
		Steps: []trinotest.Step{
			{Rows: []trino.Row{{"table_name": "acc_ds_orders"}}},
			{},
		},
	}
	ds := newTestDataSource(t, fake)

	result, diags := readTablePrivileges(t, ds, tablePrivilegesDataSourceModel{
		Catalog:    types.StringValue("hive"),
		Schema:     types.StringValue("acc_ds_sales"),
		Table:      types.StringValue("acc_ds_orders"),
		Privileges: types.ListNull(privilegeElementType),
	})
	if diags.HasError() {
		t.Fatalf("read diagnostics: %v", diags)
	}
	if len(fake.Calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(fake.Calls))
	}
	if result.Privileges.IsNull() {
		t.Fatal("privileges is null, want an empty list")
	}
	if got := len(result.Privileges.Elements()); got != 0 {
		t.Fatalf("expected 0 privileges, got %d", got)
	}
}

func TestTablePrivilegesMissingTable(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}}}
	ds := newTestDataSource(t, fake)

	_, diags := readTablePrivileges(t, ds, tablePrivilegesDataSourceModel{
		Catalog:    types.StringValue("hive"),
		Schema:     types.StringValue("acc_ds_sales"),
		Table:      types.StringValue("missing"),
		Privileges: types.ListNull(privilegeElementType),
	})
	if !diags.HasError() {
		t.Fatal("expected an error when the table does not exist")
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("expected only the existence check, got %d calls", len(fake.Calls))
	}
	if !strings.Contains(diags[0].Summary(), "not found") {
		t.Fatalf("diagnostic summary = %q, want it to mention not found", diags[0].Summary())
	}
}

func newTestDataSource(t *testing.T, fake *trinotest.FakeExecutor) datasource.DataSource {
	t.Helper()
	ds := New()
	configurable, ok := ds.(datasource.DataSourceWithConfigure)
	if !ok {
		t.Fatalf("%T does not implement datasource.DataSourceWithConfigure", ds)
	}
	var resp datasource.ConfigureResponse
	configurable.Configure(context.Background(), datasource.ConfigureRequest{
		ProviderData: &providerdata.Data{Executor: fake, Timeout: time.Minute},
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", resp.Diagnostics)
	}
	return ds
}

func testSchema(t *testing.T, ds datasource.DataSource) datasourceschema.Schema {
	t.Helper()
	var resp datasource.SchemaResponse
	ds.Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return resp.Schema
}

func readTablePrivileges(t *testing.T, ds datasource.DataSource, config tablePrivilegesDataSourceModel) (tablePrivilegesDataSourceModel, diag.Diagnostics) {
	t.Helper()
	ctx := context.Background()
	schema := testSchema(t, ds)
	objectType, ok := schema.Type().(types.ObjectType)
	if !ok {
		t.Fatalf("schema type = %T, want types.ObjectType", schema.Type())
	}
	value, diags := types.ObjectValueFrom(ctx, objectType.AttributeTypes(), config)
	if diags.HasError() {
		t.Fatalf("encode config: %v", diags)
	}
	raw, err := value.ToTerraformValue(ctx)
	if err != nil {
		t.Fatalf("encode config value: %v", err)
	}

	resp := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
	ds.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Raw: raw, Schema: schema}}, &resp)

	var result tablePrivilegesDataSourceModel
	if !resp.Diagnostics.HasError() {
		if diags := resp.State.Get(ctx, &result); diags.HasError() {
			t.Fatalf("decode state: %v", diags)
		}
	}
	return result, resp.Diagnostics
}
