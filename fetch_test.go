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
	for _, want := range []string{"---\n", `content_type: "text/html; charset=utf-8"`, `title: "Example Guide"`, `author: "Ada Example"`, "# Example Guide"} {
		if !strings.Contains(output, want) {
			t.Errorf("output does not contain %q:\n%s", want, output)
		}
	}
}

func TestFetchMarkdownPreservesBody(t *testing.T) {
	const body = "# Heading\n\nText without a final newline"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	if err := runFetch(context.Background(), []string{server.URL}, &stdout, &stderr, server.Client(), false); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(stdout.String(), body) {
		t.Fatalf("body was changed:\n%q", stdout.String())
	}
}

func TestFetchSuppressesBinaryOnTerminal(t *testing.T) {
	payload := []byte{0x00, 0x01, 0x02, 0xff}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	if err := runFetch(context.Background(), []string{server.URL}, &stdout, &stderr, server.Client(), true); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stdout.Bytes(), payload) {
		t.Fatal("binary payload was written to terminal output")
	}
	if !strings.Contains(stderr.String(), "refusing to write binary content") {
		t.Fatalf("missing warning: %q", stderr.String())
	}
}

func TestFetchSuppressesMislabeledBinaryOnTerminal(t *testing.T) {
	payload := []byte("apparently text\x00\x01\x02")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	if err := runFetch(context.Background(), []string{server.URL}, &stdout, &stderr, server.Client(), true); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stdout.Bytes(), payload) {
		t.Fatal("mislabeled binary payload was written to terminal output")
	}
	if !strings.Contains(stderr.String(), "refusing to write binary content") {
		t.Fatalf("missing warning: %q", stderr.String())
	}
}

func TestFetchWritesBinaryWhenPiped(t *testing.T) {
	payload := []byte{0x00, 0x01, 0xff}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	if err := runFetch(context.Background(), []string{server.URL}, &stdout, &stderr, server.Client(), false); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(stdout.Bytes(), payload) {
		t.Fatalf("binary payload was not preserved: %q", stdout.Bytes())
	}
}
