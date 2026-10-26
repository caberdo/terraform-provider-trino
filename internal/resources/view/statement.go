package view

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/caberdo/terraform-provider-trino/internal/resourceutil"
	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

const (
	// securityDefiner is Trino's default: the view runs with the definer's
	// privileges.
	securityDefiner = "DEFINER"
	// securityInvoker runs the view with the querying user's privileges.
	securityInvoker = "INVOKER"
)

// createStatement builds CREATE VIEW for the configured model. The view name is
// quoted; the definition is authored SQL placed after AS.
func createStatement(value model) string {
	return viewStatement("CREATE VIEW ", value)
}

// replaceStatement builds CREATE OR REPLACE VIEW, which is how both definition
// and security changes are applied: Trino has no ALTER VIEW for either.
func replaceStatement(value model) string {
	return viewStatement("CREATE OR REPLACE VIEW ", value)
}

func viewStatement(prefix string, value model) string {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(qualifiedName(value))
	b.WriteString("\nSECURITY ")
	b.WriteString(configuredSecurity(value.Security))
	b.WriteString("\nAS\n")
	b.WriteString(resourceutil.TrimStatement(value.Definition.ValueString()))
	return b.String()
}

func dropStatement(value model) string {
	return "DROP VIEW IF EXISTS " + qualifiedName(value)
}

func qualifiedName(value model) string {
	return trino.QuoteQualified(
		value.Catalog.ValueString(),
		value.Schema.ValueString(),
		value.Name.ValueString(),
	)
}

// configuredSecurity renders the SECURITY clause value. A null or unknown
// attribute is the schema default, DEFINER.
func configuredSecurity(value types.String) string {
	if value.IsNull() || value.IsUnknown() {
		return securityDefiner
	}
	return value.ValueString()
}

// parseSecurity extracts the SECURITY clause from SHOW CREATE VIEW. Trino
// renders it as `CREATE VIEW <name> SECURITY {DEFINER|INVOKER} AS ...` on 476
// and 484, on both the Hive and Memory connectors. Only tokens before the AS
// that starts the query are inspected, so a column or literal named "security"
// in the definition cannot be mistaken for the clause.
func parseSecurity(ddl string) (string, error) {
	tokens, err := resourceutil.TokenizeSQL(ddl)
	if err != nil {
		return "", err
	}

	inView := false
	for i, token := range tokens {
		switch {
		case !inView && token.IsWord("view"):
			inView = true
		case inView && token.IsWord("as"):
			return "", errors.New("SHOW CREATE VIEW returned no SECURITY clause")
		case inView && token.IsWord("security"):
			if i+1 >= len(tokens) {
				break
			}
			switch {
			case tokens[i+1].IsWord("definer"):
				return securityDefiner, nil
			case tokens[i+1].IsWord("invoker"):
				return securityInvoker, nil
			}
			return "", fmt.Errorf("SHOW CREATE VIEW returned an unexpected security mode %q", tokens[i+1].Text)
		}
	}
	return "", errors.New("SHOW CREATE VIEW returned no SECURITY clause")
}

// validateDefinition checks the part the provider itself needs: a non-empty
// statement to place after AS. Trino remains the authority on the SQL.
func validateDefinition(value types.String) diag.Diagnostics {
	var diags diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return diags
	}
	if resourceutil.TrimStatement(value.ValueString()) == "" {
		diags.AddAttributeError(path.Root("definition"), "Invalid view definition",
			"definition must not be empty; it must be the SELECT statement placed after AS.")
	}
	return diags
}
