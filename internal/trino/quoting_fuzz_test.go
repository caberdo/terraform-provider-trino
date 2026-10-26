package trino

import (
	"strings"
	"testing"
)

// unquoteIdentifier decodes the output of QuoteIdentifier and reports whether
// the input was one well-formed double-quoted identifier. Nothing in the
// provider unquotes identifiers (Trino does), so the fuzz target uses this
// minimal inverse to prove the escaper is lossless.
func unquoteIdentifier(s string) (string, bool) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", false
	}
	var b strings.Builder
	b.Grow(len(s) - 2)
	for i := 1; i < len(s)-1; i++ {
		if s[i] != '"' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s)-1 || s[i+1] != '"' {
			return "", false
		}
		b.WriteByte('"')
		i++
	}
	return b.String(), true
}

// unquoteLiteral decodes the output of QuoteLiteral and reports whether the
// input was one well-formed single-quoted string literal.
func unquoteLiteral(s string) (string, bool) {
	if len(s) < 2 || s[0] != '\'' || s[len(s)-1] != '\'' {
		return "", false
	}
	var b strings.Builder
	b.Grow(len(s) - 2)
	for i := 1; i < len(s)-1; i++ {
		if s[i] != '\'' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s)-1 || s[i+1] != '\'' {
			return "", false
		}
		b.WriteByte('\'')
		i++
	}
	return b.String(), true
}

// FuzzQuoteIdentifier checks the last line of defence against SQL injection in
// identifiers: no panic, deterministic output, every interior double quote
// doubled, and a lossless round trip through the matching unquote helper.
//
// Seeds live in testdata/fuzz/FuzzQuoteIdentifier and run with the ordinary
// test suite; `make fuzz` extends them.
func FuzzQuoteIdentifier(f *testing.F) {
	for _, seed := range []string{
		"",
		"orders",
		`weird "name"`,
		"dots.and spaces",
		"ünïcode",
		"a\x00b",
		`"`,
		`""`,
		`x"; DROP TABLE users; --`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		quoted := QuoteIdentifier(input)
		if again := QuoteIdentifier(input); again != quoted {
			t.Fatalf("QuoteIdentifier(%q) is not deterministic: %q then %q", input, quoted, again)
		}
		if len(quoted) < 2 || quoted[0] != '"' || quoted[len(quoted)-1] != '"' {
			t.Fatalf("QuoteIdentifier(%q) = %q, want one double-quoted identifier", input, quoted)
		}

		body := quoted[1 : len(quoted)-1]
		if strings.Contains(strings.ReplaceAll(body, `""`, ""), `"`) {
			t.Fatalf("QuoteIdentifier(%q) = %q leaves an undoubled double quote", input, quoted)
		}

		decoded, ok := unquoteIdentifier(quoted)
		if !ok {
			t.Fatalf("QuoteIdentifier(%q) = %q is not a well-formed quoted identifier", input, quoted)
		}
		if decoded != input {
			t.Fatalf("round trip lost data: %q -> %q -> %q", input, quoted, decoded)
		}
	})
}

// FuzzQuoteLiteral checks the last line of defence against SQL injection in
// string literals: no panic, deterministic output, every interior single quote
// doubled, and no unescaped quote that could terminate the literal early.
//
// Seeds live in testdata/fuzz/FuzzQuoteLiteral and run with the ordinary test
// suite; `make fuzz` extends them.
func FuzzQuoteLiteral(f *testing.F) {
	for _, seed := range []string{
		"",
		"SELECT",
		"O'Brien",
		`'; DROP TABLE users; --`,
		`C:\temp\x`,
		"line1\nline2",
		"'",
		"''",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		quoted := QuoteLiteral(input)
		if again := QuoteLiteral(input); again != quoted {
			t.Fatalf("QuoteLiteral(%q) is not deterministic: %q then %q", input, quoted, again)
		}
		if len(quoted) < 2 || quoted[0] != '\'' || quoted[len(quoted)-1] != '\'' {
			t.Fatalf("QuoteLiteral(%q) = %q, want one single-quoted literal", input, quoted)
		}

		// Every quote in the body must be doubled: a lone one would terminate
		// the literal and let the rest of the input escape into the statement.
		body := quoted[1 : len(quoted)-1]
		for i := 0; i < len(body); {
			if body[i] != '\'' {
				i++
				continue
			}
			if i+1 >= len(body) || body[i+1] != '\'' {
				t.Fatalf("QuoteLiteral(%q) = %q leaves an undoubled single quote", input, quoted)
			}
			i += 2
		}

		decoded, ok := unquoteLiteral(quoted)
		if !ok {
			t.Fatalf("QuoteLiteral(%q) = %q is not a well-formed literal", input, quoted)
		}
		if decoded != input {
			t.Fatalf("round trip lost data: %q -> %q -> %q", input, quoted, decoded)
		}
	})
}
