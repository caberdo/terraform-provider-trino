package trinotest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Column is one result column; Type is a Trino type name such as "varchar".
type Column struct {
	Name string
	Type string
}

// ProtoError is the error payload Trino puts in a statement response.
type ProtoError struct {
	Message   string
	ErrorName string
	ErrorType string
	SqlState  string
}

// Page is one /v1/statement response body. The first page is fetched with the
// request's nextUri; a page with an Error fails the statement.
type Page struct {
	Columns []Column
	Rows    [][]any
	Error   *ProtoError
	Delay   time.Duration
}

// Request is one HTTP request the fake served, for header and body assertions.
type Request struct {
	Method string
	Path   string
	Header http.Header
	Body   string
}

// Failure is one injected response for the next request. A zero Status closes
// the connection without a response, which the client sees as a transport
// error. Delay holds the request before the response is produced.
type Failure struct {
	Status int
	Delay  time.Duration
}

// FakeServer is a minimal Trino statement endpoint. The initial POST returns
// only an id and a nextUri; every Page is served by a follow-up GET, which is
// how the real coordinator hands out rows.
type FakeServer struct {
	t            *testing.T
	server       *httptest.Server
	pages        []Page
	pagesFor     func(statement string) []Page
	pagesByQuery map[string][]Page
	mu           sync.Mutex
	cond         *sync.Cond
	calls        []Request
	failures     []Failure
	block        chan struct{}
	queryID      int
}

// NewFakeServer starts a server that serves pages in order. It closes itself
// when the test finishes.
func NewFakeServer(t *testing.T, pages ...Page) *FakeServer {
	t.Helper()
	return newFakeServer(t, nil, pages)
}

// NewStatementServer starts a server that chooses the pages for each statement
// from the SQL text in its POST body. Use it when a test drives more than one
// statement, such as a resource that checks existence and then creates.
// Return []Page{{}} for a statement that succeeds without rows; returning nil
// pages serves no page and the client sees an HTTP 404.
func NewStatementServer(t *testing.T, pagesFor func(statement string) []Page) *FakeServer {
	t.Helper()
	return newFakeServer(t, pagesFor, nil)
}

func newFakeServer(t *testing.T, pagesFor func(statement string) []Page, pages []Page) *FakeServer {
	t.Helper()
	fake := &FakeServer{
		t:            t,
		pages:        pages,
		pagesFor:     pagesFor,
		pagesByQuery: make(map[string][]Page),
	}
	fake.cond = sync.NewCond(&fake.mu)
	fake.server = httptest.NewServer(http.HandlerFunc(fake.handle))
	t.Cleanup(fake.server.Close)
	return fake
}

// URL is the base endpoint to pass to trino.Open.
func (s *FakeServer) URL() string {
	return s.server.URL
}

// Requests returns a copy of the requests served so far.
func (s *FakeServer) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.calls...)
}

// FailNext queues responses for the next requests, in order. Once the queue is
// empty the server serves pages normally.
func (s *FakeServer) FailNext(failures ...Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, failures...)
}

// BlockNext parks the next request until the returned function is called. It
// lets a test hold a request in flight while other callers arrive.
func (s *FakeServer) BlockNext() (release func()) {
	blocked := make(chan struct{})
	s.mu.Lock()
	s.block = blocked
	s.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(blocked) }) }
}

// WaitForRequests blocks until the server has recorded at least n requests.
func (s *FakeServer) WaitForRequests(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.calls) < n {
		s.cond.Wait()
	}
}

func (s *FakeServer) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	s.mu.Lock()
	s.calls = append(s.calls, Request{
		Method: r.Method,
		Path:   r.URL.RequestURI(),
		Header: r.Header.Clone(),
		Body:   string(body),
	})
	block := s.block
	s.block = nil
	var failure Failure
	hasFailure := false
	if len(s.failures) > 0 {
		failure = s.failures[0]
		s.failures = s.failures[1:]
		hasFailure = true
	}
	s.cond.Broadcast()
	s.mu.Unlock()

	if block != nil {
		select {
		case <-block:
		case <-r.Context().Done():
			return
		}
	}
	if hasFailure {
		s.injectFailure(w, r, failure)
		return
	}

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/statement":
		pages := s.pages
		if s.pagesFor != nil {
			pages = s.pagesFor(string(body))
		}
		s.mu.Lock()
		s.queryID++
		id := fmt.Sprintf("query-%d", s.queryID)
		s.pagesByQuery[id] = pages
		s.mu.Unlock()
		s.writeJSON(w, map[string]any{
			"id":      id,
			"stats":   map[string]any{"state": "QUEUED"},
			"nextUri": s.URL() + "/v1/statement/" + id + "?page=0",
		})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/statement/"):
		s.servePage(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *FakeServer) injectFailure(w http.ResponseWriter, r *http.Request, failure Failure) {
	if failure.Delay > 0 {
		select {
		case <-time.After(failure.Delay):
		case <-r.Context().Done():
			return
		}
	}
	if failure.Status != 0 {
		http.Error(w, http.StatusText(failure.Status), failure.Status)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		s.t.Errorf("fake server response does not support hijacking")
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		s.t.Errorf("hijack fake connection: %v", err)
		return
	}
	_ = conn.Close()
}

func (s *FakeServer) servePage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/statement/")
	s.mu.Lock()
	pages := s.pages
	if s.pagesFor != nil {
		pages = s.pagesByQuery[id]
	}
	s.mu.Unlock()

	index, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || index < 0 || index >= len(pages) {
		http.Error(w, "no such page", http.StatusNotFound)
		return
	}
	page := pages[index]
	if page.Delay > 0 {
		select {
		case <-time.After(page.Delay):
		case <-r.Context().Done():
			return
		}
	}

	response := map[string]any{
		"id":    id,
		"stats": map[string]any{"state": "RUNNING"},
	}
	if len(page.Columns) > 0 {
		columns := make([]map[string]any, len(page.Columns))
		for i, column := range page.Columns {
			columns[i] = map[string]any{
				"name":          column.Name,
				"type":          column.Type,
				"typeSignature": map[string]any{"rawType": column.Type},
			}
		}
		response["columns"] = columns
	}
	if page.Rows != nil {
		response["data"] = page.Rows
	}
	if page.Error != nil {
		response["error"] = map[string]any{
			"message":   page.Error.Message,
			"errorName": page.Error.ErrorName,
			"errorType": page.Error.ErrorType,
			"sqlState":  page.Error.SqlState,
		}
	}
	if index+1 < len(pages) {
		response["nextUri"] = fmt.Sprintf("%s/v1/statement/%s?page=%d", s.URL(), id, index+1)
	}
	s.writeJSON(w, response)
}

func (s *FakeServer) writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.t.Errorf("write fake response: %v", err)
	}
}
