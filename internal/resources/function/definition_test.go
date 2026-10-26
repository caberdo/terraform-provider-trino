package function

import (
	"reflect"
	"testing"
)

func TestDefinitionsEquivalent(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{
			name: "case and whitespace",
			a:    "(x bigint) RETURNS bigint RETURN x + 1",
			b:    "(X BIGINT)\nRETURNS BIGINT\nRETURN (x + 1)",
			want: true,
		},
		{
			name: "comments",
			a:    "(x bigint) RETURNS bigint -- adds one\nRETURN x + 1",
			b:    "(x bigint) RETURNS bigint RETURN (x + 1)",
			want: true,
		},
		{
			name: "trailing semicolon",
			a:    "(x bigint) RETURNS bigint RETURN x;",
			b:    "(x bigint) RETURNS bigint RETURN x",
			want: true,
		},
		{
			name: "different body",
			a:    "(x bigint) RETURNS bigint RETURN x + 1",
			b:    "(x bigint) RETURNS bigint RETURN (x * 1)",
			want: false,
		},
		{
			name: "meaningful grouping is preserved",
			a:    "(x bigint) RETURNS bigint RETURN (x + 1) * 2",
			b:    "(x bigint) RETURNS bigint RETURN (x + 1 * 2)",
			want: false,
		},
		{
			name: "string literal case",
			a:    "() RETURNS varchar RETURN 'A'",
			b:    "() RETURNS varchar RETURN 'a'",
			want: false,
		},
		{
			name: "quoted identifier case",
			a:    `("X" bigint) RETURNS bigint RETURN "X"`,
			b:    `(x bigint) RETURNS bigint RETURN x`,
			want: false,
		},
		{
			name: "compound body",
			a:    "(x bigint) RETURNS bigint BEGIN RETURN x + 1; END",
			b:    "(x bigint) RETURNS bigint BEGIN RETURN (x + 1); END",
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := definitionsEquivalent(test.a, test.b); got != test.want {
				t.Fatalf("definitionsEquivalent(%q, %q) = %t, want %t", test.a, test.b, got, test.want)
			}
		})
	}
}

func TestParseSignature(t *testing.T) {
	tests := []struct {
		name       string
		definition string
		want       []string
		wantErr    bool
	}{
		{
			name:       "no parameters",
			definition: "() RETURNS bigint RETURN 42",
			want:       []string{},
		},
		{
			name:       "simple",
			definition: "(x bigint, y varchar) RETURNS bigint RETURN x",
			want:       []string{"bigint", "varchar"},
		},
		{
			name:       "nested type commas",
			definition: "(x decimal(10, 2), y array(varchar)) RETURNS decimal(10, 2) RETURN x",
			want:       []string{"decimal(10, 2)", "array(varchar)"},
		},
		{
			name:       "quoted parameter name",
			definition: `("X" bigint) RETURNS bigint RETURN "X"`,
			want:       []string{"bigint"},
		},
		{
			name:       "trailing semicolon",
			definition: "(x bigint) RETURNS bigint RETURN x;",
			want:       []string{"bigint"},
		},
		{
			name:       "missing parameter type",
			definition: "(x) RETURNS bigint RETURN x",
			wantErr:    true,
		},
		{
			name:       "missing RETURNS",
			definition: "(x bigint) RETURN x",
			wantErr:    true,
		},
		{
			name:       "trailing comma",
			definition: "(x bigint,) RETURNS bigint RETURN x",
			wantErr:    true,
		},
		{
			name:       "does not start with a parameter list",
			definition: "x bigint RETURNS bigint RETURN x",
			wantErr:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseSignature(test.definition)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseSignature(%q) succeeded, want an error", test.definition)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSignature(%q): %v", test.definition, err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseSignature(%q) = %v, want %v", test.definition, got, test.want)
			}
		})
	}
}

func TestExtractDefinition(t *testing.T) {
	tests := []struct {
		name       string
		showCreate string
		want       string
		wantErr    bool
	}{
		{
			name:       "unquoted name",
			showCreate: "CREATE FUNCTION memory.spike.my_add(x bigint, y bigint)\nRETURNS bigint\nRETURN (x + y)",
			want:       "(x bigint, y bigint)\nRETURNS bigint\nRETURN (x + y)",
		},
		{
			name:       "quoted name",
			showCreate: `CREATE FUNCTION memory.spike."MyFunc"(x bigint)` + "\nRETURNS bigint\nRETURN (x + 1)",
			want:       "(x bigint)\nRETURNS bigint\nRETURN (x + 1)",
		},
		{
			name:       "lower case keywords",
			showCreate: "create function hive.sales.f() returns bigint return 1",
			want:       "() returns bigint return 1",
		},
		{
			name:       "not a create function statement",
			showCreate: "CREATE TABLE hive.sales.t (id bigint)",
			wantErr:    true,
		},
		{
			name:       "missing parameter list",
			showCreate: "CREATE FUNCTION hive.sales.f RETURNS bigint",
			wantErr:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := extractDefinition(test.showCreate)
			if test.wantErr {
				if err == nil {
					t.Fatalf("extractDefinition(%q) succeeded, want an error", test.showCreate)
				}
				return
			}
			if err != nil {
				t.Fatalf("extractDefinition(%q): %v", test.showCreate, err)
			}
			if got != test.want {
				t.Fatalf("extractDefinition(%q) = %q, want %q", test.showCreate, got, test.want)
			}
		})
	}
}

func TestCanonicalArgumentTypes(t *testing.T) {
	want := canonicalTypeList([]string{"decimal(10, 2)", "array(varchar)"})
	if got := canonicalArgumentTypes("decimal(10,2), array(varchar)"); got != want {
		t.Fatalf("canonicalArgumentTypes = %q, want %q", got, want)
	}
	if got := canonicalArgumentTypes(""); got != "" {
		t.Fatalf("canonicalArgumentTypes(\"\") = %q, want empty", got)
	}
}
