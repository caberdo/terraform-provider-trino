package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestStringOrEmpty(t *testing.T) {
	if got := stringOrEmpty(types.StringNull()); got != "" {
		t.Fatalf("null = %q, want empty", got)
	}
	if got := stringOrEmpty(types.StringUnknown()); got != "" {
		t.Fatalf("unknown = %q, want empty", got)
	}
	if got := stringOrEmpty(types.StringValue("value")); got != "value" {
		t.Fatalf("value = %q, want %q", got, "value")
	}
}

func TestBoolOrFalse(t *testing.T) {
	if boolOrFalse(types.BoolNull()) {
		t.Fatal("null should be false")
	}
	if boolOrFalse(types.BoolUnknown()) {
		t.Fatal("unknown should be false")
	}
	if !boolOrFalse(types.BoolValue(true)) {
		t.Fatal("true should be true")
	}
}

func TestBoolPointer(t *testing.T) {
	if got := boolPointer(types.BoolNull()); got != nil {
		t.Fatalf("null should map to nil, got %v", got)
	}
	if got := boolPointer(types.BoolUnknown()); got != nil {
		t.Fatalf("unknown should map to nil, got %v", got)
	}
	got := boolPointer(types.BoolValue(false))
	if got == nil || *got {
		t.Fatalf("false should map to a pointer to false, got %v", got)
	}
}

func TestStringMap(t *testing.T) {
	ctx := context.Background()

	if got := stringMapValue(t, ctx, types.MapNull(types.StringType)); got != nil {
		t.Fatalf("null map should map to nil, got %v", got)
	}
	if got := stringMapValue(t, ctx, types.MapUnknown(types.StringType)); got != nil {
		t.Fatalf("unknown map should map to nil, got %v", got)
	}

	value := types.MapValueMust(types.StringType, map[string]attr.Value{
		"hive": types.StringValue("admin"),
	})
	if got := stringMapValue(t, ctx, value); got["hive"] != "admin" {
		t.Fatalf("map value not converted: %v", got)
	}

	wrong := types.MapValueMust(types.Int64Type, map[string]attr.Value{"hive": types.Int64Value(1)})
	var diags diag.Diagnostics
	stringMap(ctx, wrong, &diags)
	if !diags.HasError() {
		t.Fatal("a non-string map should produce a diagnostic")
	}
}

func stringMapValue(t *testing.T, ctx context.Context, value types.Map) map[string]string {
	t.Helper()
	var diags diag.Diagnostics
	got := stringMap(ctx, value, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	return got
}
