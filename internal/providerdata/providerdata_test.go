package providerdata_test

import (
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-trino/internal/providerdata"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

func TestFromProviderDataReturnsData(t *testing.T) {
	want := &providerdata.Data{
		Executor: &trinotest.FakeExecutor{ServerVersion: "484"},
		Timeout:  5 * time.Minute,
		Version:  "484",
	}

	got, diags := providerdata.FromProviderData(want)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if got != want {
		t.Fatalf("FromProviderData returned %p, want %p", got, want)
	}
}

func TestFromProviderDataRejectsUnexpectedValues(t *testing.T) {
	cases := []any{
		nil,
		providerdata.Data{},
		&struct{}{},
		trino.Row{},
	}
	for _, value := range cases {
		if _, diags := providerdata.FromProviderData(value); !diags.HasError() {
			t.Fatalf("%T: expected an error diagnostic", value)
		}
	}
}
