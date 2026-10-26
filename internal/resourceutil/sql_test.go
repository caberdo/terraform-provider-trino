package resourceutil

import (
	"strings"
	"testing"
)

func TestTokenizeSQL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []SQLToken
	}{
		{
			name:  "words and symbols",
			input: `SELECT a FROM t WHERE b = 1`,
			want: []SQLToken{
				{Kind: SQLWord, Text: "SELECT"},
				{Kind: SQLWord, Text: "a"},
				{Kind: SQLWord, Text: "FROM"},
				{Kind: SQLWord, Text: "t"},
				{Kind: SQLWord, Text: "WHERE"},
				{Kind: SQLWord, Text: "b"},
				{Kind: SQLSymbol, Text: "="},
				{Kind: SQLWord, Text: "1"},
			},
		},
		{
			name:  "keyword case is preserved in text",
			input: `select From`,
			want:  []SQLToken{{Kind: SQLWord, Text: "select"}, {Kind: SQLWord, Text: "From"}},
		},
		{
			name:  "quoted identifier with doubled quote",
			input: `"a""b"`,
			want:  []SQLToken{{Kind: SQLQuotedIdentifier, Text: `a"b`}},
		},
		{
			name:  "string with doubled quote",
			input: `'it''s'`,
			want:  []SQLToken{{Kind: SQLString, Text: "it's"}},
		},
		{
			name:  "line and block comments",
			input: "a -- b\nc /* d\ne */ f",
			want: []SQLToken{
				{Kind: SQLWord, Text: "a"},
				{Kind: SQLWord, Text: "c"},
				{Kind: SQLWord, Text: "f"},
			},
		},
		{
			name:  "offsets recover the original text",
			input: `x  "y"  'z'`,
			want: []SQLToken{
				{Kind: SQLWord, Text: "x", Start: 0, End: 1},
				{Kind: SQLQuotedIdentifier, Text: "y", Start: 3, End: 6},
				{Kind: SQLString, Text: "z", Start: 8, End: 11},
			},
		},
		{name: "empty", input: "", want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TokenizeSQL(tc.input)
			if err != nil {
				t.Fatalf("TokenizeSQL(%q): %v", tc.input, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("TokenizeSQL(%q) = %+v, want %d tokens %+v", tc.input, got, len(tc.want), tc.want)
			}
			for i := range got {
				if tc.want[i].Start != 0 || tc.want[i].End != 0 {
					if got[i].Start != tc.want[i].Start || got[i].End != tc.want[i].End {
						t.Fatalf("token %d offsets = %d-%d, want %d-%d", i, got[i].Start, got[i].End, tc.want[i].Start, tc.want[i].End)
					}
				}
				if got[i].Kind != tc.want[i].Kind || got[i].Text != tc.want[i].Text {
					t.Fatalf("token %d = %v %q, want %v %q", i, got[i].Kind, got[i].Text, tc.want[i].Kind, tc.want[i].Text)
				}
			}
		})
	}
}

func TestTokenizeSQLErrors(t *testing.T) {
	for _, input := range []string{`'unterminated`, `"unterminated`, `/* unterminated`} {
		if _, err := TokenizeSQL(input); err == nil {
			t.Fatalf("TokenizeSQL(%q) returned no error", input)
		}
	}
}

func TestSQLTokenPredicates(t *testing.T) {
	word := SQLToken{Kind: SQLWord, Text: "Select"}
	if !word.IsWord("select") || word.IsWord("from") {
		t.Fatalf("IsWord on %+v did not match case-insensitively", word)
	}
	if word.IsSymbol("(") {
		t.Fatalf("word %+v matched a symbol", word)
	}

	symbol := SQLToken{Kind: SQLSymbol, Text: "("}
	if !symbol.IsSymbol("(") || symbol.IsSymbol(")") {
		t.Fatalf("IsSymbol on %+v did not match exactly", symbol)
	}
	if symbol.IsWord("(") {
		t.Fatalf("symbol %+v matched a word", symbol)
	}
}

func TestMatchingParen(t *testing.T) {
	tokens, err := TokenizeSQL(`f(a, g(b), (c))`)
	if err != nil {
		t.Fatalf("TokenizeSQL: %v", err)
	}
	close, err := MatchingParen(tokens, 1)
	if err != nil {
		t.Fatalf("MatchingParen: %v", err)
	}
	if tokens[close].Text != ")" || close != len(tokens)-1 {
		t.Fatalf("MatchingParen closed at %d (%q), want the last token", close, tokens[close].Text)
	}

	inner, err := MatchingParen(tokens, 5)
	if err != nil {
		t.Fatalf("MatchingParen inner: %v", err)
	}
	if inner != 7 {
		t.Fatalf("MatchingParen inner = %d, want 7", inner)
	}

	if _, err := MatchingParen(tokens, 0); err == nil {
		t.Fatal("MatchingParen accepted a non-parenthesis")
	}
	if _, err := MatchingParen(tokens, len(tokens)); err == nil {
		t.Fatal("MatchingParen accepted an out-of-range index")
	}
	if _, err := MatchingParen([]SQLToken{{Kind: SQLSymbol, Text: "("}}, 0); err == nil {
		t.Fatal("MatchingParen accepted unbalanced parentheses")
	}
}

func TestTrimStatement(t *testing.T) {
	tests := map[string]string{
		"":                     "",
		"  select 1  ":         "select 1",
		"select 1;":            "select 1",
		" select 1 ; ; ":       "select 1",
		"select ';'":           "select ';'",
		"select 1 -- x\n;":     "select 1 -- x",
		strings.Repeat(" ", 3): "",
	}
	for input, want := range tests {
		if got := TrimStatement(input); got != want {
			t.Fatalf("TrimStatement(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEquivalentSQL(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
		want  bool
	}{
		{name: "case and whitespace", left: "ARRAY['a', 'b']", right: "array[\n  'a'\n, 'b' ]", want: true},
		{name: "string case matters", left: "'ORC'", right: "'orc'", want: false},
		{name: "quoted identifier case matters", left: `"A"`, right: `"a"`, want: false},
		{name: "grouping matters", left: "(a + b) * c", right: "a + b * c", want: false},
		{name: "comments", left: "2 -- two\n", right: "2", want: true},
		{name: "trailing semicolon", left: "true;", right: "true", want: true},
		{name: "unterminated", left: "'a", right: "'a", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := EquivalentSQL(tc.left, tc.right); got != tc.want {
				t.Fatalf("EquivalentSQL(%q, %q) = %v, want %v", tc.left, tc.right, got, tc.want)
			}
		})
	}
}
