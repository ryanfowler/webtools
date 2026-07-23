package main

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net"
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
	err := runFetch(context.Background(), []string{server.URL, "--allow-private"}, &stdout, &stderr, server.Client(), false)
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

func TestFetchLimitsExtractedMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Long</title></head><body><main><h1>Long page</h1><p>This content is deliberately long enough to truncate safely.</p></main></body></html>`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := runFetch(context.Background(), []string{server.URL, "--max-chars", "10", "--allow-private"}, &stdout, &stderr, server.Client(), false)
	if err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if !strings.Contains(output, `output_chars: "10"`) || !strings.Contains(output, `truncated: "true"`) {
		t.Fatalf("missing truncation metadata:\n%s", output)
	}
}

func TestFetchRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><main>This response is too large.</main>"))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := runFetch(context.Background(), []string{"--allow-private", server.URL, "--max-response-bytes", "10"}, &stdout, &stderr, server.Client(), false)
	if err == nil || !strings.Contains(err.Error(), "exceeds 10-byte limit") {
		t.Fatalf("expected response limit error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected output: %q", stdout.String())
	}
}

func TestFetchRejectsPrivateDestinationByDefault(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runFetch(context.Background(), []string{"http://127.0.0.1/"}, &stdout, &stderr, http.DefaultClient, false)
	if err == nil || !strings.Contains(err.Error(), "refusing private destination") {
		t.Fatalf("expected private destination error, got %v", err)
	}
}

func TestRestrictedDialRejectsDNSRebinding(t *testing.T) {
	resolver := &sequenceResolver{answers: [][]net.IPAddr{
		{{IP: net.ParseIP("93.184.216.34")}},
		{{IP: net.ParseIP("127.0.0.1")}},
	}}
	target, err := parseHTTPURL("http://rebind.example/")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFetchDestination(context.Background(), target, false, resolver); err != nil {
		t.Fatalf("initial validation failed: %v", err)
	}

	dialed := false
	dial := restrictedDialContext(resolver, func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("unexpected dial")
	})
	_, err = dial(context.Background(), "tcp", "rebind.example:80")
	if err == nil || !strings.Contains(err.Error(), "private address 127.0.0.1") {
		t.Fatalf("expected rebinding rejection, got %v", err)
	}
	if dialed {
		t.Fatal("underlying dialer was called with a rebinding address")
	}
}

func TestFetchRejectsPrivateRedirectBeforeDial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, serverURL(r)+"/private", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><main>private content</main>"))
	}))
	defer server.Close()

	serverAddress := strings.TrimPrefix(server.URL, "http://")
	dials := 0
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		dials++
		return (&net.Dialer{}).DialContext(ctx, network, serverAddress)
	}}}
	publicTarget := "http://93.184.216.34:" + strings.Split(serverAddress, ":")[1] + "/start"
	var stdout, stderr bytes.Buffer
	err := runFetch(context.Background(), []string{publicTarget}, &stdout, &stderr, client, false)
	if err == nil || !strings.Contains(err.Error(), "refusing private destination") {
		t.Fatalf("expected private redirect rejection, got %v", err)
	}
	if dials != 1 {
		t.Fatalf("underlying dialer called %d times, want 1", dials)
	}
}

func TestReadLimitedHandlesMaxInt64(t *testing.T) {
	got, err := readLimited(strings.NewReader("abc"), math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abc" {
		t.Fatalf("got %q, want abc", got)
	}
}

type sequenceResolver struct {
	answers [][]net.IPAddr
	calls   int
}

func (r *sequenceResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	if r.calls >= len(r.answers) {
		return nil, errors.New("unexpected lookup")
	}
	answer := r.answers[r.calls]
	r.calls++
	return answer, nil
}

func serverURL(r *http.Request) string {
	return "http://127.0.0.1:" + strings.Split(r.Host, ":")[1]
}

func TestFetchRejectsNonHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Heading"))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := runFetch(context.Background(), []string{"--allow-private", server.URL}, &stdout, &stderr, server.Client(), false)
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
	err := runFetch(context.Background(), []string{"--allow-private", server.URL}, &stdout, &stderr, server.Client(), false)
	if err == nil || !strings.Contains(err.Error(), "extract HTML") {
		t.Fatalf("expected extraction error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected output: %q", stdout.String())
	}
}
