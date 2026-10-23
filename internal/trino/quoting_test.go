package trino

import "testing"

func TestQuoteIdentifier(t *testing.T) {
	cases := map[string]string{
		`simple`:            `"simple"`,
		`has"quote`:         `"has""quote"`,
		`with space`:        `"with space"`,
		`a.b`:               `"a.b"`,
		`alice@example.com`: `"alice@example.com"`,
		`DOMAIN\alice`:      `"DOMAIN\alice"`,
		``:                  `""`,
	}
	for in, want := range cases {
		if got := QuoteIdentifier(in); got != want {
			t.Errorf("QuoteIdentifier(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuoteLiteral(t *testing.T) {
	cases := map[string]string{
		`plain`:       `'plain'`,
		`O'Reilly`:    `'O''Reilly'`,
		`back\slash`:  `'back\slash'`,
		`two''quotes`: `'two''''quotes'`,
		"new\nline":   "'new\nline'",
		``:            `''`,
	}
	for in, want := range cases {
		if got := QuoteLiteral(in); got != want {
			t.Errorf("QuoteLiteral(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuoteQualified(t *testing.T) {
	if got := QuoteQualified("hive", "sales", "orders"); got != `"hive"."sales"."orders"` {
		t.Fatalf("QuoteQualified three parts = %q", got)
	}
	if got := QuoteQualified(`a"b`); got != `"a""b"` {
		t.Fatalf("QuoteQualified one quoted part = %q", got)
	}
	if got := QuoteQualified(); got != "" {
		t.Fatalf("QuoteQualified no parts = %q, want empty", got)
	}
}
