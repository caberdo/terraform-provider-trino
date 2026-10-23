package trino

import (
	"encoding/json"
	"testing"
)

func TestRowString(t *testing.T) {
	row := Row{
		"name":  "orders",
		"bytes": []byte("from-bytes"),
		"n":     int64(7),
	}
	if got, ok := row.String("name"); !ok || got != "orders" {
		t.Fatalf(`String("name") = %q, %v`, got, ok)
	}
	if got, ok := row.String("bytes"); !ok || got != "from-bytes" {
		t.Fatalf(`String("bytes") = %q, %v`, got, ok)
	}
	if got, ok := row.String("NAME"); !ok || got != "orders" {
		t.Fatalf(`String("NAME") = %q, %v`, got, ok)
	}
	if _, ok := row.String("n"); ok {
		t.Fatal("String on a non-string value should not be ok")
	}
}

func TestRowBool(t *testing.T) {
	row := Row{
		"true":      true,
		"str_true":  "true",
		"str_false": "false",
		"yes":       "YES",
		"no":        "NO",
		"bad":       "maybe",
		"number":    int64(1),
	}
	cases := map[string]struct {
		want   bool
		wantOK bool
	}{
		"true":      {true, true},
		"str_true":  {true, true},
		"str_false": {false, true},
		"yes":       {true, true},
		"no":        {false, true},
		"bad":       {false, false},
		"number":    {false, false},
		"missing":   {false, false},
	}
	for col, want := range cases {
		got, ok := row.Bool(col)
		if got != want.want || ok != want.wantOK {
			t.Errorf("Bool(%q) = %v, %v; want %v, %v", col, got, ok, want.want, want.wantOK)
		}
	}
}

func TestRowInt64(t *testing.T) {
	row := Row{
		"int64":  int64(42),
		"int":    int(43),
		"int32":  int32(44),
		"number": json.Number("45"),
		"string": "46",
		"float":  float64(1.5),
		"bad":    "not-a-number",
	}
	cases := map[string]struct {
		want   int64
		wantOK bool
	}{
		"int64":   {42, true},
		"int":     {43, true},
		"int32":   {44, true},
		"number":  {45, true},
		"string":  {46, true},
		"float":   {0, false},
		"bad":     {0, false},
		"missing": {0, false},
	}
	for col, want := range cases {
		got, ok := row.Int64(col)
		if got != want.want || ok != want.wantOK {
			t.Errorf("Int64(%q) = %v, %v; want %v, %v", col, got, ok, want.want, want.wantOK)
		}
	}
}

func TestRowMissing(t *testing.T) {
	row := Row{}
	if _, ok := row.String("x"); ok {
		t.Error("String on a missing column should not be ok")
	}
	if _, ok := row.Bool("x"); ok {
		t.Error("Bool on a missing column should not be ok")
	}
	if _, ok := row.Int64("x"); ok {
		t.Error("Int64 on a missing column should not be ok")
	}
	if _, ok := row.SoleString(); ok {
		t.Error("SoleString on an empty row should not be ok")
	}
}

func TestSoleString(t *testing.T) {
	if got, ok := (Row{"version": "484"}).SoleString(); !ok || got != "484" {
		t.Fatalf("SoleString single column = %q, %v", got, ok)
	}
	if got, ok := (Row{"a": "x", "b": "y"}).SoleString(); ok {
		t.Fatalf("SoleString with two columns = %q, %v; want not ok", got, ok)
	}
	if _, ok := (Row{"a": int64(1)}).SoleString(); ok {
		t.Fatal("SoleString on a non-string value should not be ok")
	}
}
