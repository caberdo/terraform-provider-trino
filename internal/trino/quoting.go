package trino

import "strings"

// QuoteIdentifier quotes a SQL identifier with double quotes, doubling any
// embedded double quote. Trino identifiers are case-insensitive unless quoted,
// and quoting is the only way to preserve dots, spaces and mixed case.
func QuoteIdentifier(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// QuoteLiteral quotes a SQL string literal with single quotes, doubling any
// embedded single quote. Backslashes are not escapes in Trino string literals.
func QuoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// QuoteQualified quotes each part of a qualified name and joins the parts with
// dots, so every segment stays a separate identifier.
func QuoteQualified(parts ...string) string {
	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = QuoteIdentifier(part)
	}
	return strings.Join(quoted, ".")
}
