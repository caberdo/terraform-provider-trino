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
