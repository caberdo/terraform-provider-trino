package resourceutil

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestStringSetNullAndUnknown(t *testing.T) {
	var diags diag.Diagnostics

	null := StringSet(context.Background(), types.SetNull(types.StringType), &diags)
	if len(null) != 0 {
		t.Fatalf("null set returned %v", null)
	}
	unknown := StringSet(context.Background(), types.SetUnknown(types.StringType), &diags)
	if len(unknown) != 0 {
		t.Fatalf("unknown set returned %v", unknown)
	}
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	values, valueDiags := types.SetValue(types.StringType, []attr.Value{
		types.StringValue("analyst"),
		types.StringValue("admin"),
	})
	if valueDiags.HasError() {
		t.Fatal(valueDiags)
	}
	got := StringSet(context.Background(), values, &diags)
	if len(got) != 2 {
		t.Fatalf("got %v, want two values", got)
	}
	seen := map[string]bool{got[0]: true, got[1]: true}
	if !seen["analyst"] || !seen["admin"] {
		t.Fatalf("got %v", got)
	}
}

func TestStringMapNull(t *testing.T) {
	var diags diag.Diagnostics

	null := StringMap(context.Background(), types.MapNull(types.StringType), &diags)
	if len(null) != 0 {
		t.Fatalf("null map returned %v", null)
	}
	unknown := StringMap(context.Background(), types.MapUnknown(types.StringType), &diags)
	if len(unknown) != 0 {
		t.Fatalf("unknown map returned %v", unknown)
	}
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	values, valueDiags := types.MapValue(types.StringType, map[string]attr.Value{
		"hive": types.StringValue("admin"),
	})
	if valueDiags.HasError() {
		t.Fatal(valueDiags)
	}
	got := StringMap(context.Background(), values, &diags)
	if got["hive"] != "admin" {
		t.Fatalf("got %v", got)
	}
}
