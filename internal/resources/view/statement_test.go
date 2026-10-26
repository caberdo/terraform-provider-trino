package view

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func testModel() model {
	return model{
		Catalog:    types.StringValue("hive"),
		Schema:     types.StringValue("sales"),
		Name:       types.StringValue("orders_by_region"),
		Definition: types.StringValue("SELECT region, count(*) AS orders FROM hive.sales.orders GROUP BY region"),
		Security:   types.StringValue(securityDefiner),
	}
}

func TestCreateStatement(t *testing.T) {
	want := "CREATE VIEW \"hive\".\"sales\".\"orders_by_region\"\n" +
		"SECURITY DEFINER\n" +
		"AS\n" +
		"SELECT region, count(*) AS orders FROM hive.sales.orders GROUP BY region"
	if got := createStatement(testModel()); got != want {
		t.Fatalf("create statement =\n%s\nwant\n%s", got, want)
	}
}

func TestCreateStatementInvoker(t *testing.T) {
	value := testModel()
	value.Security = types.StringValue("INVOKER")
	want := "CREATE VIEW \"hive\".\"sales\".\"orders_by_region\"\n" +
		"SECURITY INVOKER\n" +
		"AS\n" +
		"SELECT region, count(*) AS orders FROM hive.sales.orders GROUP BY region"
	if got := createStatement(value); got != want {
		t.Fatalf("create statement =\n%s\nwant\n%s", got, want)
	}
}

func TestReplaceStatementTrimsDefinition(t *testing.T) {
	value := testModel()
	value.Definition = types.StringValue("  SELECT 1;  ")
	want := "CREATE OR REPLACE VIEW \"hive\".\"sales\".\"orders_by_region\"\n" +
		"SECURITY DEFINER\n" +
		"AS\n" +
		"SELECT 1"
	if got := replaceStatement(value); got != want {
		t.Fatalf("replace statement =\n%s\nwant\n%s", got, want)
	}
}

func TestDefaultSecurityWhenNull(t *testing.T) {
	value := testModel()
	value.Security = types.StringNull()
	if got := configuredSecurity(value.Security); got != securityDefiner {
		t.Fatalf("security = %q, want DEFINER", got)
	}
}

func TestDropStatement(t *testing.T) {
	want := `DROP VIEW IF EXISTS "hive"."sales"."orders_by_region"`
	if got := dropStatement(testModel()); got != want {
		t.Fatalf("drop statement = %q, want %q", got, want)
	}
}

func TestParseSecurity(t *testing.T) {
	tests := []struct {
		name string
		ddl  string
		want string
		err  string
	}{
		{
			name: "definer defaults as rendered by Trino",
			ddl:  "CREATE VIEW hive.sales.v SECURITY DEFINER AS\nSELECT\n  a\n, b\nFROM\n  hive.sales.t",
			want: securityDefiner,
		},
		{
			name: "invoker",
			ddl:  `CREATE VIEW "hive"."sales"."v" SECURITY INVOKER AS SELECT 1`,
			want: securityInvoker,
		},
		{
			name: "lowercase output",
			ddl:  "create view hive.sales.v security invoker as select 1",
			want: securityInvoker,
		},
		{
			name: "security column in the definition is ignored",
			ddl:  "CREATE VIEW hive.sales.v SECURITY DEFINER AS SELECT security FROM hive.sales.t",
			want: securityDefiner,
		},
		{
			name: "security keyword in a string literal is ignored",
			ddl:  "CREATE VIEW hive.sales.v SECURITY INVOKER AS SELECT 'security invoker' AS s",
			want: securityInvoker,
		},
		{
			name: "no security clause",
			ddl:  "CREATE VIEW hive.sales.v AS SELECT 1",
			err:  "no SECURITY clause",
		},
		{
			name: "unexpected mode",
			ddl:  "CREATE VIEW hive.sales.v SECURITY OWNER AS SELECT 1",
			err:  "unexpected security mode",
		},
		{
			name: "not a create view statement",
			ddl:  "SELECT 1",
			err:  "no SECURITY clause",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSecurity(tc.ddl)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want it to contain %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("security = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateDefinition(t *testing.T) {
	if diags := validateDefinition(types.StringValue("SELECT 1")); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if diags := validateDefinition(types.StringValue("  ;  ")); !diags.HasError() {
		t.Fatal("expected an error for an empty definition")
	}
	if diags := validateDefinition(types.StringNull()); diags.HasError() {
		t.Fatalf("a null definition during an import must not fail config validation: %v", diags)
	}
}
