package schema

import "testing"

// FuzzParseShowCreateSchema checks the parser that consumes untrusted input
// from SHOW CREATE SCHEMA: no input may panic, and every input must either
// parse to a valid result or return an error with no partial data.
//
// Seeds live in testdata/fuzz/FuzzParseShowCreateSchema and run with the
// ordinary test suite; `make fuzz` extends them.
func FuzzParseShowCreateSchema(f *testing.F) {
	for _, seed := range []string{
		`CREATE SCHEMA "hive"."sales"`,
		`CREATE SCHEMA "hive"."sales" AUTHORIZATION USER alice`,
		`CREATE SCHEMA hive AUTHORIZATION ROLE admin WITH ("location" = 'local:///tmp/hive')`,
		`CREATE SCHEMA "my.schema" WITH ("a" = 'b', "c" = 'd')`,
		`CREATE TABLE orders (id bigint)`,
		`CREATE SCHEMA "unterminated`,
		"",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, ddl string) {
		owner, properties, err := parseShowCreateSchema(ddl)
		if err != nil {
			if owner != nil || properties != nil {
				t.Fatalf("parseShowCreateSchema(%q) returned data with error %v: owner=%v properties=%v", ddl, err, owner, properties)
			}
			return
		}
		if owner != nil {
			if owner.Type != "USER" && owner.Type != "ROLE" {
				t.Fatalf("parseShowCreateSchema(%q) returned owner type %q, want USER or ROLE", ddl, owner.Type)
			}
			if owner.Name == "" {
				t.Fatalf("parseShowCreateSchema(%q) returned an empty owner name", ddl)
			}
		}
	})
}
