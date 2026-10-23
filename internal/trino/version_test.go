package trino

import "testing"

func TestAtLeast(t *testing.T) {
	cases := []struct {
		version string
		minimum string
		want    bool
	}{
		{"476", "476", true},
		{"484", "476", true},
		{"475", "476", false},
		{"", "476", true},
		{"unknown", "476", true},
		{"476.1", "476", true},
		{"484", "484", true},
		{"476", "484", false},
	}
	for _, tc := range cases {
		if got := AtLeast(tc.version, tc.minimum); got != tc.want {
			t.Errorf("AtLeast(%q, %q) = %v, want %v", tc.version, tc.minimum, got, tc.want)
		}
	}
}
