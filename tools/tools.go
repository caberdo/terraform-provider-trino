//go:build tools

// Package tools pins the build- and test-time dependencies that the provider
// does not import yet. Keeping them here means `go mod tidy` retains the
// versions the design pins before Tasks 2–10 add the real imports.
package tools

import (
	_ "github.com/hashicorp/go-version"
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
	_ "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	_ "github.com/hashicorp/terraform-plugin-go/tfprotov6"
	_ "github.com/hashicorp/terraform-plugin-log/tflog"
	_ "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	_ "github.com/trinodb/trino-go-client/trino"
)
