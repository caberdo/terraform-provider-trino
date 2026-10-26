package provider

import (
	"context"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

// TestConfigureBuildsResourceData drives Configure end to end against a fake
// /v1/statement server: config mapping, client construction, the version probe
// and the typed data handed to resources.
func TestConfigureBuildsResourceData(t *testing.T) {
	clearTrinoEnv(t)
	server := trinotest.NewFakeServer(t, trinotest.Page{
		Columns: []trinotest.Column{{Name: "version", Type: "varchar"}},
		Rows:    [][]any{{"484"}},
	})

	p := New("test")()
	ctx := context.Background()

	var schemaResp provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &schemaResp)

	config := tfsdk.Config{
		Raw: providerConfigValue(ctx, t, schemaResp.Schema, map[string]any{
			"endpoint":      server.URL(),
			"auth":          "none",
			"user":          "admin",
			"query_timeout": "90s",
		}),
		Schema: schemaResp.Schema,
	}

	var resp provider.ConfigureResponse
	p.Configure(ctx, provider.ConfigureRequest{Config: config}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	data, ok := resp.ResourceData.(*providerdata.Data)
	if !ok {
		t.Fatalf("ResourceData has type %T, want *providerdata.Data", resp.ResourceData)
	}
	if data.Executor == nil {
		t.Fatal("executor was not handed to resources")
	}
	if data.Timeout != 90*time.Second {
		t.Fatalf("Timeout = %s, want 90s", data.Timeout)
	}
	version, err := data.Executor.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version != "484" {
		t.Fatalf("Version = %q, want %q", version, "484")
	}
	if resp.DataSourceData != data {
		t.Fatal("data sources did not receive the same provider data")
	}
}

// providerConfigValue builds a Terraform value with every provider attribute
// null except the overrides, which is exactly what an omitted attribute looks
// like to the framework.
func providerConfigValue(ctx context.Context, t *testing.T, schema providerschema.Schema, overrides map[string]any) tftypes.Value {
	t.Helper()

	attrs := make(map[string]tftypes.Value, len(schema.Attributes))
	for name, attr := range schema.Attributes {
		typ := attr.GetType().TerraformType(ctx)
		raw, ok := overrides[name]
		if !ok {
			attrs[name] = tftypes.NewValue(typ, nil)
			continue
		}
		attrs[name] = tftypes.NewValue(typ, raw)
	}
	return tftypes.NewValue(schema.Type().TerraformType(ctx), attrs)
}
