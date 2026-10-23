package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

var _ provider.Provider = (*TrinoProvider)(nil)

// TrinoProvider is the Terraform provider entry point. The v1 implementation
// adds the provider schema and resource/datasource registrations; this stub
// exists so the module, tooling and release pipeline can be built and tested
// before that logic lands.
type TrinoProvider struct {
	version string
}

// New returns a provider factory carrying the build version reported to
// Terraform in Metadata.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &TrinoProvider{version: version}
	}
}

func (p *TrinoProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "trino"
	resp.Version = p.version
}

func (p *TrinoProvider) Schema(context.Context, provider.SchemaRequest, *provider.SchemaResponse) {}

func (p *TrinoProvider) Configure(context.Context, provider.ConfigureRequest, *provider.ConfigureResponse) {
}

func (p *TrinoProvider) Resources(context.Context) []func() resource.Resource { return nil }

func (p *TrinoProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}
