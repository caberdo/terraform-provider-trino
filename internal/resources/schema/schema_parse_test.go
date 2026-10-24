package schema

import (
	"reflect"
	"testing"
)

func TestParseShowCreateSchema(t *testing.T) {
	cases := []struct {
		name  string
		ddl   string
		owner *Owner
		props map[string]string
	}{
		{
			name:  "with properties",
			ddl:   `CREATE SCHEMA hive.sales WITH ("location" = 's3://bucket/sales')`,
			props: map[string]string{"location": "s3://bucket/sales"},
		},
		{
			name:  "with role owner",
			ddl:   `CREATE SCHEMA hive.sales AUTHORIZATION ROLE analyst WITH ("location" = '/tmp/x')`,
			owner: &Owner{Type: "ROLE", Name: "analyst"},
			props: map[string]string{"location": "/tmp/x"},
		},
		{
			name:  "quoted owner with at sign",
			ddl:   `CREATE SCHEMA "hive"."sales" AUTHORIZATION USER "alice@example.com"`,
			owner: &Owner{Type: "USER", Name: "alice@example.com"},
		},
		{
			name:  "multi-line value with escaped comma",
			ddl:   "CREATE SCHEMA hive.sales\nWITH (\"custom.key\" = 'v''al, with space')",
			props: map[string]string{"custom.key": "v'al, with space"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, props, err := parseShowCreateSchema(tc.ddl)
			if err != nil {
				t.Fatalf("parseShowCreateSchema() error = %v", err)
			}
			if !reflect.DeepEqual(owner, tc.owner) {
				t.Fatalf("owner = %#v, want %#v", owner, tc.owner)
			}
			if !reflect.DeepEqual(props, tc.props) {
				t.Fatalf("properties = %#v, want %#v", props, tc.props)
			}
		})
	}
}

func TestParseShowCreateSchemaMalformed(t *testing.T) {
	cases := []string{
		"",
		"CREATE",
		"CREATE SCHEMA",
		"CREATE TABLE hive.sales",
		`CREATE SCHEMA hive.sales AUTHORIZATION`,
		`CREATE SCHEMA hive.sales AUTHORIZATION TEAM analyst`,
		`CREATE SCHEMA hive.sales WITH ("location" = 's3://bucket/sales'`,
		`CREATE SCHEMA hive.sales WITH ("location" = unquoted)`,
		`CREATE SCHEMA hive.sales WITH ("location" = 's3://bucket/sales',)`,
		`CREATE SCHEMA hive.sales WITH "location" = 'x'`,
		`CREATE SCHEMA hive.sales garbage`,
		`CREATE SCHEMA hive.sales WITH ("location" = 'unterminated)`,
		`CREATE SCHEMA "hive"."sales`,
		`CREATE SCHEMA hive..sales`,
	}

	for _, ddl := range cases {
		t.Run(ddl, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("parseShowCreateSchema(%q) panicked: %v", ddl, recovered)
				}
			}()

			if _, _, err := parseShowCreateSchema(ddl); err == nil {
				t.Fatalf("parseShowCreateSchema(%q) returned no error", ddl)
			}
		})
	}
}
