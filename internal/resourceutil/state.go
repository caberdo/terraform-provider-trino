package resourceutil

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// StringSet converts a framework set to a slice, treating null and unknown
// values as an empty set.
func StringSet(ctx context.Context, v types.Set, diags *diag.Diagnostics) []string {
	if v.IsNull() || v.IsUnknown() {
		return []string{}
	}
	result := make([]string, 0, len(v.Elements()))
	diags.Append(v.ElementsAs(ctx, &result, false)...)
	return result
}

// StringMap converts a framework map to a Go map, treating null and unknown
// values as an empty map.
func StringMap(ctx context.Context, v types.Map, diags *diag.Diagnostics) map[string]string {
	if v.IsNull() || v.IsUnknown() {
		return map[string]string{}
	}
	result := make(map[string]string, len(v.Elements()))
	diags.Append(v.ElementsAs(ctx, &result, false)...)
	return result
}
