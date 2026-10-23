package resourceutil

import (
	"fmt"
	"strings"
)

// SplitImportID splits an import ID on sep and requires exactly n non-empty
// segments. The error names the expected format so callers can attach it to an
// "Invalid import ID" diagnostic.
func SplitImportID(id, sep string, n int, expected string) ([]string, error) {
	if n < 1 {
		return nil, fmt.Errorf("expected %s", expected)
	}
	parts := strings.SplitN(id, sep, n)
	if len(parts) != n {
		return nil, fmt.Errorf("expected %s", expected)
	}
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
		if parts[i] == "" {
			return nil, fmt.Errorf("import ID segment %d is empty; expected %s", i+1, expected)
		}
	}
	return parts, nil
}
