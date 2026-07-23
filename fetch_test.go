package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchHTMLExtractsMarkdownAndMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head>
<title>Example Guide</title><meta name="author" content="Ada Example">
<meta property="article:published_time" content="2025-01-02">
</head><body><main><h1>Example Guide</h1><p>This is useful guide content with enough detail to extract.</p></main></body></html>`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := runFetch(context.Background(), []string{server.URL}, &stdout, &stderr, server.Client(), false)
	if err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, want := range []string{"---\n", `content_type: "text/html; charset=utf-8"`, `title: "Example Guide"`, `author: "Ada Example"`, "---\n\n# Example Guide"} {
		if !strings.Contains(output, want) {
			t.Errorf("output does not contain %q:\n%s", want, output)
		}
	}
}

func TestFetchRejectsNonHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Heading"))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := runFetch(context.Background(), []string{server.URL}, &stdout, &stderr, server.Client(), false)
	if err == nil || !strings.Contains(err.Error(), "response is not HTML") {
		t.Fatalf("expected non-HTML error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected output: %q", stdout.String())
	}
}

func TestFetchRejectsHTMLPagemarkCannotExtract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><head><title>Empty</title></head><body></body></html>"))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := runFetch(context.Background(), []string{server.URL}, &stdout, &stderr, server.Client(), false)
	if err == nil || !strings.Contains(err.Error(), "extract HTML") {
		t.Fatalf("expected extraction error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected output: %q", stdout.String())
	}
}
