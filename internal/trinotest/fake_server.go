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

// FakeServer is a minimal Trino statement endpoint. The initial POST returns
// only an id and a nextUri; every Page is served by a follow-up GET, which is
// how the real coordinator hands out rows.
type FakeServer struct {
	t       *testing.T
	server  *httptest.Server
	pages   []Page
	mu      sync.Mutex
	calls   []Request
	queryID int
}

// NewFakeServer starts a server that serves pages in order. It closes itself
// when the test finishes.
func NewFakeServer(t *testing.T, pages ...Page) *FakeServer {
	t.Helper()
	fake := &FakeServer{t: t, pages: pages}
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

func (s *FakeServer) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	s.mu.Lock()
	s.calls = append(s.calls, Request{
		Method: r.Method,
		Path:   r.URL.RequestURI(),
		Header: r.Header.Clone(),
		Body:   string(body),
	})
	s.mu.Unlock()

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/statement":
		s.mu.Lock()
		s.queryID++
		id := fmt.Sprintf("query-%d", s.queryID)
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

func (s *FakeServer) servePage(w http.ResponseWriter, r *http.Request) {
	index, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || index < 0 || index >= len(s.pages) {
		http.Error(w, "no such page", http.StatusNotFound)
		return
	}
	page := s.pages[index]
	if page.Delay > 0 {
		select {
		case <-time.After(page.Delay):
		case <-r.Context().Done():
			return
		}
	}

	id := strings.TrimPrefix(r.URL.Path, "/v1/statement/")
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
	if index+1 < len(s.pages) {
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
