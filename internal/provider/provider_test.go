package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestNewSetsMetadata(t *testing.T) {
	p := New("1.2.3")()

	var resp provider.MetadataResponse
	p.Metadata(context.Background(), provider.MetadataRequest{}, &resp)

	if resp.TypeName != "trino" {
		t.Fatalf("TypeName = %q, want %q", resp.TypeName, "trino")
	}
	if resp.Version != "1.2.3" {
		t.Fatalf("Version = %q, want %q", resp.Version, "1.2.3")
	}
}

func TestProviderRegistersAllTypes(t *testing.T) {
	ctx := context.Background()
	p := New("dev")()

	resourceNames := make(map[string]bool)
	for _, factory := range p.Resources(ctx) {
		var resp resource.MetadataResponse
		factory().Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "trino"}, &resp)
		resourceNames[resp.TypeName] = true
	}
	wantResources := []string{
		"trino_catalog",
		"trino_schema",
		"trino_role",
		"trino_grant_role",
		"trino_grant_privileges",
	}
	if len(resourceNames) != len(wantResources) {
		t.Fatalf("Resources() returned %d types, want %d: %v", len(resourceNames), len(wantResources), resourceNames)
	}
	for _, name := range wantResources {
		if !resourceNames[name] {
			t.Fatalf("Resources() is missing %q; got %v", name, resourceNames)
		}
	}

	dataSourceNames := make(map[string]bool)
	for _, factory := range p.DataSources(ctx) {
		var resp datasource.MetadataResponse
		factory().Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "trino"}, &resp)
		dataSourceNames[resp.TypeName] = true
	}
	wantDataSources := []string{
		"trino_catalogs",
		"trino_schemas",
		"trino_roles",
		"trino_table_privileges",
	}
	if len(dataSourceNames) != len(wantDataSources) {
		t.Fatalf("DataSources() returned %d types, want %d: %v", len(dataSourceNames), len(wantDataSources), dataSourceNames)
	}
	for _, name := range wantDataSources {
		if !dataSourceNames[name] {
			t.Fatalf("DataSources() is missing %q; got %v", name, dataSourceNames)
		}
	}
}

func TestProviderSchemaSensitiveAttributes(t *testing.T) {
	var resp provider.SchemaResponse
	New("dev")().Schema(context.Background(), provider.SchemaRequest{}, &resp)

	sensitive := []string{
		"password",
		"access_token",
		"oauth2_client_secret",
		"kerberos_keytab",
		"kerberos_credential_cache",
		"extra_credentials",
		"http_headers",
		"client_cert",
		"client_key",
	}
	for _, name := range sensitive {
		attr, ok := resp.Schema.Attributes[name]
		if !ok {
			t.Fatalf("schema is missing attribute %q", name)
		}
		if !attr.IsSensitive() {
			t.Fatalf("attribute %q is not sensitive", name)
		}
	}

	for _, name := range []string{"endpoint", "user", "ca_cert_file", "client_cert_file"} {
		attr, ok := resp.Schema.Attributes[name]
		if !ok {
			t.Fatalf("schema is missing attribute %q", name)
		}
		if attr.IsSensitive() {
			t.Fatalf("attribute %q should not be sensitive", name)
		}
	}
}

func TestProviderSchemaCoversConfiguration(t *testing.T) {
	var resp provider.SchemaResponse
	New("dev")().Schema(context.Background(), provider.SchemaRequest{}, &resp)

	want := []string{
		"endpoint", "auth", "user", "source", "catalog", "schema",
		"session_properties", "extra_credentials", "roles", "http_headers",
		"query_timeout", "password", "access_token",
		"oauth2_client_id", "oauth2_client_secret", "oauth2_token_endpoint", "oauth2_scope",
		"kerberos_principal", "kerberos_realm", "kerberos_keytab",
		"kerberos_credential_cache", "kerberos_config_file", "kerberos_service_name",
		"kerberos_service_principal_pattern", "kerberos_use_canonical_hostname",
		"insecure_skip_verify", "ca_cert", "ca_cert_file",
		"client_cert", "client_cert_file", "client_key", "client_key_file",
	}
	for _, name := range want {
		if _, ok := resp.Schema.Attributes[name]; !ok {
			t.Fatalf("schema is missing attribute %q", name)
		}
	}
}
