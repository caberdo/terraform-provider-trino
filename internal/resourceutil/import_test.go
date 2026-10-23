package resourceutil

import (
	"strings"
	"testing"
)

func TestSplitImportID(t *testing.T) {
	cases := []struct {
		name     string
		id       string
		sep      string
		n        int
		expected []string
		wantErr  string
	}{
		{name: "two segments", id: "hive.sales", sep: ".", n: 2, expected: []string{"hive", "sales"}},
		{name: "rest in last segment", id: "hive.a.b", sep: ".", n: 2, expected: []string{"hive", "a.b"}},
		{name: "too few", id: "hive", sep: ".", n: 2, wantErr: "catalog.schema"},
		{name: "empty segment", id: ":USER:alice", sep: ":", n: 3, wantErr: "import ID"},
		{name: "principal with domain", id: "hive.analyst:USER:alice@example.com", sep: ":", n: 3, expected: []string{"hive.analyst", "USER", "alice@example.com"}},
		{name: "whitespace segment", id: "hive.   ", sep: ".", n: 2, wantErr: "import ID"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SplitImportID(tc.id, tc.sep, tc.n, "catalog.schema")
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.expected) {
				t.Fatalf("got %v, want %v", got, tc.expected)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Fatalf("got %v, want %v", got, tc.expected)
				}
			}
		})
	}
}
