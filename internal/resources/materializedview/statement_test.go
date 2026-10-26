package materializedview

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func testModel() model {
	return model{
		Catalog:         types.StringValue("iceberg"),
		Schema:          types.StringValue("sales"),
		Name:            types.StringValue("orders_by_region"),
		Query:           types.StringValue("SELECT region, count(*) AS orders FROM iceberg.sales.orders GROUP BY region"),
		Properties:      types.MapNull(types.StringType),
		GracePeriod:     types.StringNull(),
		RefreshOnCreate: types.BoolNull(),
		RefreshToken:    types.StringNull(),
	}
}

func TestCreateStatement(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*model)
		properties map[string]string
		grace      string
		want       string
	}{
		{
			name: "minimal",
			want: "CREATE MATERIALIZED VIEW \"iceberg\".\"sales\".\"orders_by_region\"\nAS\n" +
				"SELECT region, count(*) AS orders FROM iceberg.sales.orders GROUP BY region",
		},
		{
			name:  "grace period",
			grace: "INTERVAL '3600' SECOND",
			want: "CREATE MATERIALIZED VIEW \"iceberg\".\"sales\".\"orders_by_region\"\nGRACE PERIOD INTERVAL '3600' SECOND\nAS\n" +
				"SELECT region, count(*) AS orders FROM iceberg.sales.orders GROUP BY region",
		},
		{
			name:       "sorted raw properties",
			properties: map[string]string{"partitioning": "ARRAY['region']", "format": " 'ORC' "},
			want: "CREATE MATERIALIZED VIEW \"iceberg\".\"sales\".\"orders_by_region\"\nWITH (\n" +
				"  \"format\" = 'ORC',\n" +
				"  \"partitioning\" = ARRAY['region']\n" +
				")\nAS\n" +
				"SELECT region, count(*) AS orders FROM iceberg.sales.orders GROUP BY region",
		},
		{
			name: "trailing semicolon is trimmed",
			mutate: func(value *model) {
				value.Query = types.StringValue("SELECT 1;  ")
			},
			want: "CREATE MATERIALIZED VIEW \"iceberg\".\"sales\".\"orders_by_region\"\nAS\nSELECT 1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			value := testModel()
			if tc.mutate != nil {
				tc.mutate(&value)
			}
			if got := createStatement(value, tc.properties, tc.grace); got != tc.want {
				t.Fatalf("createStatement() =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestRefreshAndDropStatements(t *testing.T) {
	value := testModel()
	if got, want := refreshStatement(value), `REFRESH MATERIALIZED VIEW "iceberg"."sales"."orders_by_region"`; got != want {
		t.Fatalf("refreshStatement() = %q, want %q", got, want)
	}
	if got, want := dropStatement(value), `DROP MATERIALIZED VIEW IF EXISTS "iceberg"."sales"."orders_by_region"`; got != want {
		t.Fatalf("dropStatement() = %q, want %q", got, want)
	}
}

func TestRenderGracePeriod(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr string
	}{
		{input: "1h", want: "INTERVAL '3600' SECOND"},
		{input: "30m", want: "INTERVAL '1800' SECOND"},
		{input: " 90s ", want: "INTERVAL '90' SECOND"},
		{input: "1.5h", want: "INTERVAL '5400' SECOND"},
		{input: "", wantErr: "must not be empty"},
		{input: "soon", wantErr: "not a duration"},
		{input: "0s", wantErr: "must be positive"},
		{input: "-1h", wantErr: "must be positive"},
		{input: "500ms", wantErr: "whole number of seconds"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := renderGracePeriod(tc.input)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("renderGracePeriod(%q) error = %v, want one containing %q", tc.input, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("renderGracePeriod(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("renderGracePeriod(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestShowCreateQuery(t *testing.T) {
	tests := []struct {
		name       string
		showCreate string
		want       string
		wantErr    string
	}{
		{
			name:       "trino 476 output",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders_by_region\nAS\nSELECT\n  region\n, count(*) AS orders\nFROM\n  iceberg.sales.orders\nGROUP BY region",
			want:       "SELECT\n  region\n, count(*) AS orders\nFROM\n  iceberg.sales.orders\nGROUP BY region",
		},
		{
			name:       "quoted qualified name",
			showCreate: "CREATE MATERIALIZED VIEW \"iceberg\".\"sales\".\"orders\" AS SELECT 1",
			want:       "SELECT 1",
		},
		{
			name:       "quoted name only",
			showCreate: "CREATE MATERIALIZED VIEW \"Orders\" AS SELECT 1",
			want:       "SELECT 1",
		},
		{
			name:       "grace period from trino 484",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders\nGRACE PERIOD INTERVAL '1' HOUR\nAS\nSELECT 1",
			want:       "SELECT 1",
		},
		{
			name:       "grace period with day to second interval",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders\nGRACE PERIOD INTERVAL '1 02:03:04.000' DAY TO SECOND\nAS\nSELECT 1",
			want:       "SELECT 1",
		},
		{
			name:       "when stale",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders\nGRACE PERIOD INTERVAL '1' HOUR\nWHEN STALE FAIL\nAS\nSELECT 1",
			want:       "SELECT 1",
		},
		{
			name:       "comment",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders\nCOMMENT 'as AS with (\n'\nAS\nSELECT 1",
			want:       "SELECT 1",
		},
		{
			name:       "properties",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders\nWITH (\n  \"format\" = 'ORC',\n  \"partitioning\" = ARRAY['region']\n)\nAS\nSELECT 1",
			want:       "SELECT 1",
		},
		{
			name:       "all clauses",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders\nGRACE PERIOD INTERVAL '3600' SECOND\nCOMMENT 'nightly'\nWITH (\n  \"format\" = 'PARQUET'\n)\nAS\nSELECT region FROM iceberg.sales.orders;",
			want:       "SELECT region FROM iceberg.sales.orders",
		},
		{
			name:       "missing AS",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders",
			wantErr:    "no AS clause",
		},
		{
			name:       "empty query after AS",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders AS",
			wantErr:    "no query after AS",
		},
		{
			name:       "not a materialized view",
			showCreate: "CREATE VIEW iceberg.sales.orders AS SELECT 1",
			wantErr:    "did not return a CREATE MATERIALIZED VIEW",
		},
		{
			name:       "unexpected token",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders OWNER admin AS SELECT 1",
			wantErr:    "unexpected token",
		},
		{
			name:       "malformed comment",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders COMMENT admin AS SELECT 1",
			wantErr:    "COMMENT clause",
		},
		{
			name:       "malformed with",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders WITH format AS SELECT 1",
			wantErr:    "WITH clause",
		},
		{
			name:       "unbalanced with",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders WITH (format = 'ORC' AS SELECT 1",
			wantErr:    "unbalanced",
		},
		{
			name:       "unterminated quote",
			showCreate: "CREATE MATERIALIZED VIEW o COMMENT 'x AS SELECT 1",
			wantErr:    "unterminated",
		},
		{
			name:       "unexpected qualified name",
			showCreate: "CREATE MATERIALIZED VIEW iceberg..orders AS SELECT 1",
			wantErr:    "unexpected qualified view name",
		},
		{
			name:       "unexpected view name",
			showCreate: "CREATE MATERIALIZED VIEW ( AS SELECT 1",
			wantErr:    "unexpected view name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseShowCreate(tc.showCreate)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("parseShowCreate() error = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseShowCreate(): %v", err)
			}
			if got.query != tc.want {
				t.Fatalf("parseShowCreate() query = %q, want %q", got.query, tc.want)
			}
		})
	}
}

func TestParseShowCreateProperties(t *testing.T) {
	tests := []struct {
		name       string
		showCreate string
		want       map[string]string
		wantErr    string
	}{
		{
			name:       "no properties",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders AS SELECT 1",
			want:       nil,
		},
		{
			name:       "string, integer and array values",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders\nWITH (\n  \"format\" = 'ORC',\n  format_version = 2,\n  partitioning = ARRAY['region', 'day']\n)\nAS\nSELECT 1",
			want: map[string]string{
				"format":         "'ORC'",
				"format_version": "2",
				"partitioning":   "ARRAY['region', 'day']",
			},
		},
		{
			name:       "array commas do not split properties",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders WITH (sorted_by = ARRAY['a', 'b'], location = 's3://bucket/a,b') AS SELECT 1",
			want: map[string]string{
				"sorted_by": "ARRAY['a', 'b']",
				"location":  "'s3://bucket/a,b'",
			},
		},
		{
			name:       "quoted property name is lowercased",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders WITH (\"FORMAT\" = 'ORC') AS SELECT 1",
			want:       map[string]string{"format": "'ORC'"},
		},
		{
			name:       "nested parentheses",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders WITH (transform = transform(a, b)) AS SELECT 1",
			want:       map[string]string{"transform": "transform(a, b)"},
		},
		{
			name:       "missing equals",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders WITH (format 'ORC') AS SELECT 1",
			wantErr:    "malformed value",
		},
		{
			name:       "missing value",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders WITH (format =) AS SELECT 1",
			wantErr:    "malformed value",
		},
		{
			name:       "unexpected name",
			showCreate: "CREATE MATERIALIZED VIEW iceberg.sales.orders WITH ('format' = 'ORC') AS SELECT 1",
			wantErr:    "unexpected property name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseShowCreate(tc.showCreate)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("parseShowCreate() error = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseShowCreate(): %v", err)
			}
			if len(got.properties) != len(tc.want) {
				t.Fatalf("properties = %v, want %v", got.properties, tc.want)
			}
			for key, want := range tc.want {
				if got.properties[key] != want {
					t.Fatalf("properties[%q] = %q, want %q", key, got.properties[key], want)
				}
			}
		})
	}
}
