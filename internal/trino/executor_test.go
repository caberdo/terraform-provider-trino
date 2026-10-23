package trino_test

import (
	"context"
	"testing"

	"github.com/caberdo/terraform-provider-trino/internal/trino"
	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

func openExecutor(t *testing.T, server *trinotest.FakeServer) trino.Executor {
	t.Helper()
	executor, err := trino.Open(trino.Options{ServerURI: server.URL(), User: "alice"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = executor.Close() })
	return executor
}

func TestExecutorQueryOne(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{
		Columns: []trinotest.Column{{Name: "n", Type: "bigint"}},
		Rows:    [][]any{{int64(1)}, {int64(2)}},
	})
	row, ok, err := openExecutor(t, server).QueryOne(context.Background(), "SELECT n FROM t")
	if err != nil || !ok {
		t.Fatalf("QueryOne = %v, %v, %v", row, ok, err)
	}
	if n, _ := row.Int64("n"); n != 1 {
		t.Errorf("first row n = %d, want 1", n)
	}

	empty := trinotest.NewFakeServer(t, trinotest.Page{})
	_, ok, err = openExecutor(t, empty).QueryOne(context.Background(), "SELECT n FROM empty")
	if err != nil {
		t.Fatalf("QueryOne on empty result: %v", err)
	}
	if ok {
		t.Error("QueryOne reported a row for an empty result")
	}
}

func TestExecutorExecSurfacesError(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{
		Error: &trinotest.ProtoError{
			Message:   "Access denied",
			ErrorName: "PERMISSION_DENIED",
			ErrorType: "USER_ERROR",
		},
	})
	err := openExecutor(t, server).Exec(context.Background(), "DROP SCHEMA sales")
	if err == nil {
		t.Fatal("expected an error")
	}
	if trino.SourceOf(err) != trino.KindPermissionDenied {
		t.Errorf("SourceOf = %q", trino.SourceOf(err))
	}
}

func TestExecutorVersionCachesFailure(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{
		Error: &trinotest.ProtoError{
			Message:   "No access to system.runtime",
			ErrorName: "PERMISSION_DENIED",
			ErrorType: "USER_ERROR",
		},
	})
	executor := openExecutor(t, server)

	for range 2 {
		version, err := executor.Version(context.Background())
		if err == nil {
			t.Fatalf("Version = %q, want the cached probe failure", version)
		}
		if trino.SourceOf(err) != trino.KindPermissionDenied {
			t.Errorf("SourceOf = %q", trino.SourceOf(err))
		}
	}
	if requests := server.Requests(); len(requests) != 2 {
		t.Errorf("got %d requests, want the probe to be attempted once", len(requests))
	}
}
