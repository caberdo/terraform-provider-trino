package acctest

import (
	"testing"

	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

func TestVersionMatches(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		actual   string
		want     bool
	}{
		{name: "exact", expected: "484", actual: "484", want: true},
		{name: "snapshot suffix", expected: "484", actual: "484-SNAPSHOT", want: true},
		{name: "patch suffix", expected: "484", actual: "484.1", want: true},
		{name: "whitespace", expected: "484", actual: " 484 ", want: true},
		{name: "different release", expected: "484", actual: "476", want: false},
		{name: "longer number", expected: "484", actual: "4840", want: false},
		{name: "empty actual", expected: "484", actual: "", want: false},
		{name: "empty expected", expected: "", actual: "484", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionMatches(tc.expected, tc.actual); got != tc.want {
				t.Fatalf("versionMatches(%q, %q) = %v, want %v", tc.expected, tc.actual, got, tc.want)
			}
		})
	}
}

func TestPreCheckVerifiesExpectedVersion(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{
		Columns: []trinotest.Column{{Name: "version", Type: "varchar"}},
		Rows:    [][]any{{"484"}},
	})

	t.Setenv("TF_ACC", "1")
	t.Setenv("TRINO_ENDPOINT", server.URL())
	t.Setenv("TRINO_USER", "admin")
	t.Setenv("TRINO_AUTH", "none")
	t.Setenv("TRINO_EXPECT_VERSION", "484")
	PreCheck(t)
}
