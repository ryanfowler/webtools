package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

const searchFixture = `<!doctype html><html><body>
<div class="result results_links">
  <h2><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F">The Go Documentation</a></h2>
  <a class="result__snippet">Build simple, secure, scalable systems.</a>
</div>
<div class="result result--ad"><a class="result__a" href="https://ad.example/">An ad</a></div>
<div class="result results_links">
  <a class="result__a" href="https://go.dev/blog/">The Go Blog</a>
  <div class="result__snippet">News from the Go team.</div>
</div>
</body></html>`

func TestSearchParsesOrganicResultsAndLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "go language" {
			t.Errorf("query = %q", got)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(searchFixture))
	}))
	defer server.Close()

	results, err := search(context.Background(), server.Client(), server.URL, "go language", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	want := SearchResult{Title: "The Go Documentation", URL: "https://go.dev/doc/", Snippet: "Build simple, secure, scalable systems."}
	if results[0] != want {
		t.Errorf("result = %#v, want %#v", results[0], want)
	}
}

func TestRunSearchOutputsJSON(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.Query().Get("q"); got != "go language" {
			t.Errorf("query = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(searchFixture)),
			Request:    req,
		}, nil
	})}
	var stdout, stderr bytes.Buffer
	if err := runSearch(context.Background(), []string{"--limit", "1", "go language"}, &stdout, &stderr, client); err != nil {
		t.Fatal(err)
	}

	var results []SearchResult
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, stdout.String())
	}
	if len(results) != 1 || results[0].URL != "https://go.dev/doc/" {
		t.Fatalf("unexpected results: %#v", results)
	}
}

func TestRunSearchRejectsExcessiveLimit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runSearch(context.Background(), []string{"--limit", strconv.Itoa(maxSearchLimit + 1), "query"}, &stdout, &stderr, http.DefaultClient)
	if err == nil || !strings.Contains(err.Error(), "between 1 and") {
		t.Fatalf("error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
