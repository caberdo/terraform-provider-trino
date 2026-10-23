package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
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

func TestNewHasNoResourcesOrDataSources(t *testing.T) {
	p := New("dev")()

	if got := p.Resources(context.Background()); len(got) != 0 {
		t.Fatalf("Resources() returned %d entries, want 0", len(got))
	}
	if got := p.DataSources(context.Background()); len(got) != 0 {
		t.Fatalf("DataSources() returned %d entries, want 0", len(got))
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
