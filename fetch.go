package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ryanfowler/pagemark"
	"golang.org/x/net/html/charset"
)

type metadataField struct {
	key   string
	value string
}

func runFetch(ctx context.Context, args []string, stdout, stderr io.Writer, client *http.Client, _ bool) error {
	flags := flag.NewFlagSet("webtools fetch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprintln(flags.Output(), "Usage: webtools fetch URL") }
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return errors.New("fetch requires exactly one URL")
	}

	target, err := parseHTTPURL(flags.Arg(0))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html, application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %s", resp.Status)
	}

	contentType := resp.Header.Get("Content-Type")
	mediaType, _, parseErr := mime.ParseMediaType(contentType)
	if parseErr != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	}
	mediaType = strings.ToLower(mediaType)
	finalURL := resp.Request.URL.String()
	baseFields := []metadataField{
		{"url", finalURL},
		{"content_type", contentType},
		{"status", strconv.Itoa(resp.StatusCode)},
	}

	if mediaType != "text/html" && mediaType != "application/xhtml+xml" {
		return fmt.Errorf("response is not HTML (Content-Type: %s)", displayContentType(mediaType))
	}
	return outputHTML(resp.Body, contentType, finalURL, baseFields, stdout)
}

func outputHTML(body io.Reader, contentType, pageURL string, fields []metadataField, stdout io.Writer) error {
	decoded, err := charset.NewReader(body, contentType)
	if err != nil {
		return fmt.Errorf("decode HTML: %w", err)
	}
	doc, err := pagemark.Extract(decoded, pageURL)
	if err != nil {
		return fmt.Errorf("extract HTML: %w", err)
	}
	fields = append(fields,
		metadataField{"canonical_url", doc.CanonicalURL},
		metadataField{"title", doc.Title},
		metadataField{"description", doc.Description},
		metadataField{"author", doc.Author},
		metadataField{"site_name", doc.SiteName},
		metadataField{"language", doc.Language},
		metadataField{"date", doc.PublishedTime},
		metadataField{"page_type", string(doc.PageType)},
	)
	if err := writeFrontmatter(stdout, fields); err != nil {
		return err
	}
	if _, err := io.WriteString(stdout, doc.Markdown); err != nil {
		return fmt.Errorf("write extracted Markdown: %w", err)
	}
	if doc.Markdown != "" && !strings.HasSuffix(doc.Markdown, "\n") {
		if _, err := io.WriteString(stdout, "\n"); err != nil {
			return fmt.Errorf("write extracted Markdown: %w", err)
		}
	}
	return nil
}

func writeFrontmatter(w io.Writer, fields []metadataField) error {
	if _, err := io.WriteString(w, "---\n"); err != nil {
		return fmt.Errorf("write frontmatter: %w", err)
	}
	for _, field := range fields {
		if field.value == "" {
			continue
		}
		value := strconv.Quote(strings.ToValidUTF8(field.value, "�"))
		if _, err := fmt.Fprintf(w, "%s: %s\n", field.key, value); err != nil {
			return fmt.Errorf("write frontmatter: %w", err)
		}
	}
	if _, err := io.WriteString(w, "---\n"); err != nil {
		return fmt.Errorf("write frontmatter: %w", err)
	}
	return nil
}

func displayContentType(mediaType string) string {
	if mediaType == "" {
		return "unknown content type"
	}
	return mediaType
}

func parseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, errors.New("URL must be an absolute HTTP or HTTPS URL without credentials")
	}
	return u, nil
}
