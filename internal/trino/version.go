package trino

import (
	"strings"

	goversion "github.com/hashicorp/go-version"
)

// AtLeast reports whether version is at least minimum. An unknown or
// unparseable version is treated as satisfying the requirement, so version
// detection failure never blocks an operation; the server stays the final
// authority.
func AtLeast(version, minimum string) bool {
	if strings.TrimSpace(version) == "" {
		return true
	}
	current, err := goversion.NewVersion(strings.TrimSpace(version))
	if err != nil {
		return true
	}
	floor, err := goversion.NewVersion(strings.TrimSpace(minimum))
	if err != nil {
		return true
	}
	return !current.LessThan(floor)
}
