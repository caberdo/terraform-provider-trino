package trino_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestExecutorVersionRetriesAfterProbeFailure(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{
		Columns: []trinotest.Column{{Name: "_col0", Type: "varchar"}},
		Rows:    [][]any{{"484"}},
	})
	// A zero Status closes the connection: the first probe fails in transport.
	server.FailNext(trinotest.Failure{})
	executor := openExecutor(t, server)

	if _, err := executor.Version(context.Background()); err == nil {
		t.Fatal("first Version succeeded, want the transport error")
	}
	version, err := executor.Version(context.Background())
	if err != nil {
		t.Fatalf("second Version: %v", err)
	}
	if version != "484" {
		t.Errorf("version = %q, want 484", version)
	}
	version, err = executor.Version(context.Background())
	if err != nil || version != "484" {
		t.Errorf("cached Version = %q, %v, want 484", version, err)
	}
	// One closed probe, then a POST and one poll; the third call is cached.
	if got := len(server.Requests()); got != 3 {
		t.Errorf("got %d requests, want 3", got)
	}
}

func TestExecutorVersionSingleFlight(t *testing.T) {
	server := trinotest.NewFakeServer(t, trinotest.Page{})
	release := server.BlockNext()
	server.FailNext(trinotest.Failure{Status: http.StatusInternalServerError})
	executor := openExecutor(t, server)

	const callers = 8
	entered := make(chan struct{}, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entered <- struct{}{}
			_, err := executor.Version(context.Background())
			errs <- err
		}()
	}
	for range callers {
		<-entered
	}
	server.WaitForRequests(1)
	// The first probe is parked in the server; let the remaining callers join
	// it instead of starting probes of their own.
	time.Sleep(50 * time.Millisecond)
	if got := len(server.Requests()); got != 1 {
		t.Errorf("in-flight probes = %d, want 1", got)
	}

	release()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err == nil {
			t.Error("concurrent Version succeeded, want the shared probe failure")
		}
	}
	if got := len(server.Requests()); got != 1 {
		t.Errorf("got %d requests, want one shared probe", got)
	}
}

// TestExecutorRefusesCoordinatorRedirectDowngrade pins the property the
// provider relies on: the HTTPClient it injects (TLS material, mTLS, static
// headers) is handed back to the caller without following redirects, so a
// coordinator cannot bounce a credentialed request to another server. The
// driver applies that wrapper itself; the test fails if a future client setup
// opts out of it.
func TestExecutorRefusesCoordinatorRedirectDowngrade(t *testing.T) {
	var followed atomic.Int64
	plaintext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		followed.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"redirected","stats":{"state":"FINISHED"}}`))
	}))
	defer plaintext.Close()

	coordinator := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plaintext.URL+"/v1/statement", http.StatusTemporaryRedirect)
	}))
	defer coordinator.Close()

	client, err := trino.NewHTTPClient(trino.TLSOptions{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	executor, err := trino.Open(trino.Options{ServerURI: coordinator.URL, HTTPClient: client})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = executor.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := executor.QueryOne(ctx, "SELECT 1"); err == nil {
		t.Fatal("expected the redirect to be refused")
	} else if !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("error %q does not name the refused redirect", err)
	}
	if n := followed.Load(); n != 0 {
		t.Fatalf("plaintext server received %d request(s) after the redirect", n)
	}
}
