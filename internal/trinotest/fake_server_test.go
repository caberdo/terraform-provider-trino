package trinotest_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-trino/internal/trino"
	"github.com/caberdo/terraform-provider-trino/internal/trinotest"
)

func openFake(t *testing.T, server *trinotest.FakeServer, opts trino.Options) trino.Executor {
	t.Helper()
	opts.ServerURI = server.URL()
	if opts.User == "" {
		opts.User = "alice"
	}
	executor, err := trino.Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = executor.Close() })
	return executor
}

func TestExecutorQueryReturnsRows(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{
		Columns: []trinotest.Column{
			{Name: "catalog_name", Type: "varchar"},
			{Name: "count", Type: "bigint"},
		},
		Rows: [][]any{{"hive", int64(3)}},
	})
	executor := openFake(t, server, trino.Options{})

	rows, err := executor.Query(context.Background(), "SELECT catalog_name, count FROM t")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	name, ok := rows[0].String("catalog_name")
	if !ok || name != "hive" {
		t.Errorf("catalog_name = %q, %v", name, ok)
	}
	count, ok := rows[0].Int64("count")
	if !ok || count != 3 {
		t.Errorf("count = %d, %v", count, ok)
	}

	post := server.Requests()[0]
	if post.Body != "SELECT catalog_name, count FROM t" {
		t.Errorf("posted statement = %q", post.Body)
	}
}

func TestExecutorExecSendsUserAndSourceHeaders(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{})
	executor := openFake(t, server, trino.Options{
		User:   "alice",
		Source: "terraform-provider-trino",
	})

	if err := executor.Exec(context.Background(), "CREATE SCHEMA sales"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	requests := server.Requests()
	if len(requests) != 2 {
		t.Fatalf("got %d requests, want a POST and one poll", len(requests))
	}
	post := requests[0]
	if post.Method != "POST" || post.Body != "CREATE SCHEMA sales" {
		t.Errorf("request = %s %q", post.Method, post.Body)
	}
	if got := post.Header.Get("X-Trino-User"); got != "alice" {
		t.Errorf("X-Trino-User = %q, want alice", got)
	}
	if got := post.Header.Get("X-Trino-Source"); got != "terraform-provider-trino" {
		t.Errorf("X-Trino-Source = %q", got)
	}
}

func TestOpenAppliesSessionAndExtraCredentialHeaders(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{})
	executor := openFake(t, server, trino.Options{
		SessionProperties: map[string]string{"query_max_run_time": "10m"},
		ExtraCredentials:  map[string]string{"secret_key": "abc"},
	})

	if _, err := executor.Query(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("Query: %v", err)
	}
	header := server.Requests()[0].Header
	if got := header.Get("X-Trino-Session"); got != "query_max_run_time=10m" {
		t.Errorf("X-Trino-Session = %q", got)
	}
	if got := header.Get("X-Trino-Extra-Credential"); got != "secret_key=abc" {
		t.Errorf("X-Trino-Extra-Credential = %q", got)
	}
}

func TestOpenAppliesRolesHeader(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{})
	executor := openFake(t, server, trino.Options{
		Roles: map[string]string{"hive": "admin"},
	})

	if _, err := executor.Query(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if got := server.Requests()[0].Header.Get("X-Trino-Role"); got != "hive=ROLE%7Badmin%7D" {
		t.Errorf("X-Trino-Role = %q", got)
	}
}

func TestExecutorPollsUntilDone(t *testing.T) {
	columns := []trinotest.Column{{Name: "n", Type: "bigint"}}
	server := trinotest.NewFakeServer(t,
		trinotest.Page{Columns: columns, Rows: [][]any{{int64(1)}, {int64(2)}}},
		trinotest.Page{Columns: columns, Rows: [][]any{{int64(3)}}},
		trinotest.Page{Columns: columns, Rows: [][]any{{int64(4)}}},
	)
	executor := openFake(t, server, trino.Options{})

	rows, err := executor.Query(context.Background(), "SELECT n FROM t")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var got []int64
	for _, row := range rows {
		n, _ := row.Int64("n")
		got = append(got, n)
	}
	if len(got) != 4 || got[0] != 1 || got[3] != 4 {
		t.Fatalf("rows = %v", got)
	}

	requests := server.Requests()
	if len(requests) != 4 {
		t.Fatalf("got %d requests, want 1 POST and 3 polls", len(requests))
	}
	if last := requests[len(requests)-1]; last.Method != "GET" || !strings.Contains(last.Path, "page=2") {
		t.Errorf("last request = %s %s, want the third poll", last.Method, last.Path)
	}
}

func TestExecutorMapsTrinoError(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{
		Error: &trinotest.ProtoError{
			Message:   "Catalog 'nope' not found",
			ErrorName: "CATALOG_NOT_FOUND",
			ErrorType: "USER_ERROR",
			SqlState:  "42P01",
		},
	})
	executor := openFake(t, server, trino.Options{})

	_, err := executor.Query(context.Background(), "SELECT * FROM nope.t")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !trino.IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false", err)
	}
	if trino.SourceOf(err) != trino.KindNotFound {
		t.Errorf("SourceOf = %q, want %q", trino.SourceOf(err), trino.KindNotFound)
	}
	if !strings.Contains(err.Error(), "Catalog 'nope' not found") {
		t.Errorf("error %q lost the server message", err)
	}
}

func TestExecutorContextCancellation(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{
		Columns: []trinotest.Column{{Name: "n", Type: "bigint"}},
		Rows:    [][]any{{int64(1)}},
		Delay:   2 * time.Second,
	})
	executor := openFake(t, server, trino.Options{})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := executor.Query(ctx, "SELECT n FROM t")
	if err == nil {
		t.Fatal("expected a cancellation error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want a deadline error", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Query took %s, cancellation did not interrupt the poll", elapsed)
	}
}

func TestExecutorExecDoesNotRetryMutatingStatement(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{})
	server.FailNext(trinotest.Failure{Status: http.StatusServiceUnavailable})
	executor := openFake(t, server, trino.Options{})

	err := executor.Exec(context.Background(), "CREATE SCHEMA sales")
	if err == nil {
		t.Fatal("Exec succeeded, want the 503 surfaced")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error %q does not report the 503", err)
	}
	requests := server.Requests()
	if len(requests) != 1 {
		t.Fatalf("got %d requests, want the mutating statement sent once", len(requests))
	}
	if post := requests[0]; post.Method != http.MethodPost || post.Body != "CREATE SCHEMA sales" {
		t.Errorf("request = %s %q", post.Method, post.Body)
	}
}

func TestExecutorQueryRetriesTransientFailure(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{
		Columns: []trinotest.Column{{Name: "n", Type: "bigint"}},
		Rows:    [][]any{{int64(1)}},
	})
	server.FailNext(trinotest.Failure{Status: http.StatusServiceUnavailable})
	executor := openFake(t, server, trino.Options{})

	rows, err := executor.Query(context.Background(), "SELECT n FROM t")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	// The failed POST, the retried POST and one poll.
	if got := len(server.Requests()); got != 3 {
		t.Errorf("got %d requests, want 3", got)
	}
}

func TestFakeServerClosesConnectionOnTransportFailure(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{})
	server.FailNext(trinotest.Failure{})
	executor := openFake(t, server, trino.Options{})

	if _, err := executor.Query(context.Background(), "SELECT 1"); err == nil {
		t.Fatal("Query succeeded, want the injected transport error")
	}
	if got := len(server.Requests()); got != 1 {
		t.Errorf("got %d requests, want 1", got)
	}
}

func TestFakeServerBlocksUntilReleased(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{})
	executor := openFake(t, server, trino.Options{})
	release := server.BlockNext()

	done := make(chan error, 1)
	go func() {
		_, err := executor.Query(context.Background(), "SELECT 1")
		done <- err
	}()

	server.WaitForRequests(1)
	select {
	case err := <-done:
		t.Fatalf("Query completed while blocked: %v", err)
	default:
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("Query after release: %v", err)
	}
}

func TestExecutorVersionIsCached(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{
		Columns: []trinotest.Column{{Name: "_col0", Type: "varchar"}},
		Rows:    [][]any{{"484"}},
	})
	executor := openFake(t, server, trino.Options{})

	for range 2 {
		version, err := executor.Version(context.Background())
		if err != nil {
			t.Fatalf("Version: %v", err)
		}
		if version != "484" {
			t.Errorf("version = %q, want 484", version)
		}
	}
	if requests := server.Requests(); len(requests) != 2 {
		t.Errorf("got %d requests, want the probe to run once", len(requests))
	}
}
