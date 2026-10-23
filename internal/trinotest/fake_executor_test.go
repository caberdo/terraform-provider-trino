package trinotest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

func TestFakeExecutorRecordsCalls(t *testing.T) {
	boom := errors.New("boom")
	fake := &FakeExecutor{
		ServerVersion: "484",
		Steps: []Step{
			{Rows: []trino.Row{{"catalog_name": "hive"}}},
			{Rows: []trino.Row{{"count": int64(1)}}},
			{Err: boom},
		},
	}

	rows, err := fake.Query(context.Background(), "SELECT catalog_name FROM system.metadata.catalogs")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	name, ok := rows[0].String("catalog_name")
	if len(rows) != 1 || !ok || name != "hive" {
		t.Fatalf("rows = %#v", rows)
	}

	row, ok, err := fake.QueryOne(context.Background(), "SELECT count(*) FROM t", int64(7))
	if err != nil || !ok {
		t.Fatalf("QueryOne = %v, %v, %v", row, ok, err)
	}

	if err := fake.Exec(context.Background(), "DROP SCHEMA sales"); !errors.Is(err, boom) {
		t.Fatalf("Exec error = %v, want boom", err)
	}

	if len(fake.Calls) != 3 {
		t.Fatalf("recorded %d calls, want 3", len(fake.Calls))
	}
	first := fake.Calls[0]
	if first.Op != "query" || first.Statement != "SELECT catalog_name FROM system.metadata.catalogs" {
		t.Errorf("first call = %#v", first)
	}
	second := fake.Calls[1]
	if second.Op != "query" || len(second.Args) != 1 || second.Args[0] != int64(7) {
		t.Errorf("second call = %#v", second)
	}
	third := fake.Calls[2]
	if third.Op != "exec" || third.Statement != "DROP SCHEMA sales" {
		t.Errorf("third call = %#v", third)
	}

	version, err := fake.Version(context.Background())
	if err != nil || version != "484" {
		t.Fatalf("Version = %q, %v", version, err)
	}
}

func TestFakeExecutorExhaustedQueueNamesStatement(t *testing.T) {
	fake := &FakeExecutor{}
	_, err := fake.Query(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("expected an error when the queue is empty")
	}
	if !strings.Contains(err.Error(), "SELECT 1") {
		t.Errorf("error %q should name the unexpected statement", err)
	}
}
