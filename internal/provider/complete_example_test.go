package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// TestCompleteExampleCoversProviderSurface keeps examples/complete/main.tf in
// step with the resource registry: a resource added to Resources without a
// block in the end-to-end example is a documentation gap a compile-time check
// would otherwise miss. Data sources are covered by their own examples under
// examples/data-sources/ and are not part of the end-to-end module, which must
// stay appliable.
func TestCompleteExampleCoversProviderSurface(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "complete", "main.tf")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the complete example: %v", err)
	}
	source := string(contents)

	ctx := context.Background()
	p := New("dev")()

	for _, factory := range p.Resources(ctx) {
		var resp resource.MetadataResponse
		factory().Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "trino"}, &resp)
		block := `resource "` + resp.TypeName + `"`
		if !strings.Contains(source, block) {
			t.Errorf("examples/complete/main.tf has no %s block", block)
		}
	}

}
