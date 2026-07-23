package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

const (
	duckDuckGoURL  = "https://html.duckduckgo.com/html/"
	maxSearchLimit = 100
)

// SearchResult is one organic DuckDuckGo search result.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

func runSearch(ctx context.Context, args []string, stdout, stderr io.Writer, client *http.Client) error {
	flags := flag.NewFlagSet("webtools search", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprintln(flags.Output(), "Usage: webtools search [--limit N] QUERY") }
	limit := flags.Int("limit", 10, "maximum number of results")
	flags.IntVar(limit, "n", 10, "maximum number of results (shorthand)")
	if err := parseInterspersed(flags, args); err != nil {
		return err
	}
	if *limit < 1 || *limit > maxSearchLimit {
		return fmt.Errorf("search limit must be between 1 and %d", maxSearchLimit)
	}
	query := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if query == "" {
		flags.Usage()
		return errors.New("search query is required")
	}

	results, err := search(ctx, client, duckDuckGoURL, query, *limit)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(results); err != nil {
		return fmt.Errorf("write search results: %w", err)
	}
	return nil
}

func search(ctx context.Context, client *http.Client, endpoint, query string, limit int) ([]SearchResult, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse search endpoint: %w", err)
	}
	values := u.Query()
	values.Set("q", query)
	u.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create search request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search DuckDuckGo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("DuckDuckGo returned %s", resp.Status)
	}

	body, err := readLimited(resp.Body, 5<<20)
	if err != nil {
		return nil, fmt.Errorf("read DuckDuckGo response: %w", err)
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse DuckDuckGo response: %w", err)
	}
	results := parseSearchResults(doc, limit)
	if len(results) == 0 && findClass(doc, "anomaly-modal") != nil {
		return nil, errors.New("DuckDuckGo requested human verification; try again later")
	}
	return results, nil
}

func parseSearchResults(root *html.Node, limit int) []SearchResult {
	results := make([]SearchResult, 0, limit)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(results) >= limit {
			return
		}
		if n.Type == html.ElementNode && hasClass(n, "result") && !hasClass(n, "result--ad") {
			link := findClass(n, "result__a")
			if link != nil {
				title := nodeText(link)
				href := attr(link, "href")
				if title != "" && href != "" {
					snippetNode := findClass(n, "result__snippet")
					results = append(results, SearchResult{Title: title, URL: resultURL(href), Snippet: nodeText(snippetNode)})
					return
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
			if len(results) >= limit {
				return
			}
		}
	}
	walk(root)
	return results
}

func resultURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if target := u.Query().Get("uddg"); target != "" {
		return target
	}
	if u.IsAbs() {
		return u.String()
	}
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	return raw
}

func hasClass(n *html.Node, class string) bool {
	for _, field := range strings.Fields(attr(n, "class")) {
		if field == class {
			return true
		}
	}
	return false
}

func findClass(n *html.Node, class string) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && hasClass(n, class) {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findClass(child, class); found != nil {
			return found
		}
	}
	return nil
}

func attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func nodeText(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
			b.WriteByte(' ')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}
