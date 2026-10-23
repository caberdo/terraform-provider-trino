package providerdata

import (
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

// Data is the configured client and its per-operation defaults, passed from
// the provider to every resource and data source.
type Data struct {
	Executor trino.Executor
	Timeout  time.Duration
	Version  string
}

// FromProviderData extracts Data from the untyped value the framework hands to
// Configure. It returns an error diagnostic when the provider did not supply
// the expected type.
func FromProviderData(v any) (*Data, diag.Diagnostics) {
	var diags diag.Diagnostics
	data, ok := v.(*Data)
	if !ok || data == nil {
		diags.AddError("Provider data is not configured",
			"The Trino provider did not supply its client configuration. This is a provider bug; report it with the version in use.")
		return nil, diags
	}
	return data, diags
}
