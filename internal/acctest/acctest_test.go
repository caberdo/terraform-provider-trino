package acctest

import (
	"errors"
	"testing"

	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

func TestRunFixtureDropsCreatedObjectsInReverse(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{{}, {}, {}, {}, {}}}

	cleanup, err := fixture(t, fake,
		`CREATE SCHEMA IF NOT EXISTS hive.sales`,
		`CREATE TABLE "my table" (id int)`,
		`SELECT 1`,
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()

	if len(fake.Calls) != 5 {
		t.Fatalf("got %d calls, want 5: %+v", len(fake.Calls), fake.Calls)
	}
	if got := fake.Calls[3].Statement; got != `DROP TABLE IF EXISTS "my table"` {
		t.Fatalf("first drop = %q", got)
	}
	if got := fake.Calls[4].Statement; got != `DROP SCHEMA IF EXISTS hive.sales` {
		t.Fatalf("second drop = %q", got)
	}
}

func TestRunFixtureCleansUpAfterPartialFailure(t *testing.T) {
	fake := &trinotest.FakeExecutor{Steps: []trinotest.Step{
		{},
		{Err: errors.New("boom")},
		{},
	}}

	cleanup, err := fixture(t, fake, `CREATE SCHEMA hive.one`, `CREATE SCHEMA hive.two`)
	if err == nil {
		t.Fatal("expected the fixture to fail")
	}
	cleanup()
	if len(fake.Calls) != 3 {
		t.Fatalf("got %d calls, want 3: %+v", len(fake.Calls), fake.Calls)
	}
	if got := fake.Calls[2].Statement; got != `DROP SCHEMA IF EXISTS hive.one` {
		t.Fatalf("cleanup = %q", got)
	}
}
