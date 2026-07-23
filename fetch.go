package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ryanfowler/pagemark"
	"golang.org/x/net/html/charset"
)

const (
	defaultFetchResponseBytes int64 = 5 << 20
	defaultFetchChars               = 50_000
)

type metadataField struct {
	key   string
	value string
}

type ipResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

func runFetch(ctx context.Context, args []string, stdout, stderr io.Writer, client *http.Client, _ bool) error {
	flags := flag.NewFlagSet("webtools fetch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: webtools fetch [--max-response-bytes N] [--max-chars N] [--allow-private] URL")
	}
	maxResponseBytes := flags.Int64("max-response-bytes", defaultFetchResponseBytes, "maximum downloaded response size")
	maxChars := flags.Int("max-chars", defaultFetchChars, "maximum extracted Markdown characters")
	allowPrivate := flags.Bool("allow-private", false, "allow private, loopback, and link-local destinations")
	if err := parseInterspersed(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return errors.New("fetch requires exactly one URL")
	}
	if *maxResponseBytes < 1 {
		return errors.New("max-response-bytes must be at least 1")
	}
	if *maxChars < 1 {
		return errors.New("max-chars must be at least 1")
	}

	target, err := parseHTTPURL(flags.Arg(0))
	if err != nil {
		return err
	}
	resolver := ipResolver(net.DefaultResolver)
	if err := validateFetchDestination(ctx, target, *allowPrivate, resolver); err != nil {
		return err
	}

	fetchClient := *client
	if !*allowPrivate {
		transport, err := restrictedTransport(client.Transport, resolver)
		if err != nil {
			return err
		}
		fetchClient.Transport = transport
	}
	originalCheckRedirect := client.CheckRedirect
	fetchClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if err := validateFetchDestination(req.Context(), req.URL, *allowPrivate, resolver); err != nil {
			return err
		}
		if originalCheckRedirect != nil {
			return originalCheckRedirect(req, via)
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html, application/xhtml+xml")

	resp, err := fetchClient.Do(req)
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
	body, err := readLimited(resp.Body, *maxResponseBytes)
	if err != nil {
		return fmt.Errorf("read %s: %w", target, err)
	}
	return outputHTML(bytes.NewReader(body), contentType, finalURL, baseFields, *maxChars, stdout)
}

func outputHTML(body io.Reader, contentType, pageURL string, fields []metadataField, maxChars int, stdout io.Writer) error {
	decoded, err := charset.NewReader(body, contentType)
	if err != nil {
		return fmt.Errorf("decode HTML: %w", err)
	}
	doc, err := pagemark.Extract(decoded, pageURL)
	if err != nil {
		return fmt.Errorf("extract HTML: %w", err)
	}
	markdown, truncated, extractedChars := truncateMarkdown(doc.Markdown, maxChars)
	fields = append(fields,
		metadataField{"canonical_url", doc.CanonicalURL},
		metadataField{"title", doc.Title},
		metadataField{"description", doc.Description},
		metadataField{"author", doc.Author},
		metadataField{"site_name", doc.SiteName},
		metadataField{"language", doc.Language},
		metadataField{"date", doc.PublishedTime},
		metadataField{"page_type", string(doc.PageType)},
		metadataField{"extracted_chars", strconv.Itoa(extractedChars)},
		metadataField{"output_chars", strconv.Itoa(utf8.RuneCountInString(markdown))},
		metadataField{"truncated", strconv.FormatBool(truncated)},
	)
	if err := writeFrontmatter(stdout, fields); err != nil {
		return err
	}
	if _, err := io.WriteString(stdout, markdown); err != nil {
		return fmt.Errorf("write extracted Markdown: %w", err)
	}
	if markdown != "" && !strings.HasSuffix(markdown, "\n") {
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
	if _, err := io.WriteString(w, "---\n\n"); err != nil {
		return fmt.Errorf("write frontmatter: %w", err)
	}
	return nil
}

func truncateMarkdown(markdown string, maxChars int) (string, bool, int) {
	total := utf8.RuneCountInString(markdown)
	if total <= maxChars {
		return markdown, false, total
	}
	end := len(markdown)
	count := 0
	for index := range markdown {
		if count == maxChars {
			end = index
			break
		}
		count++
	}
	return markdown[:end], true, total
}

func restrictedTransport(base http.RoundTripper, resolver ipResolver) (*http.Transport, error) {
	var transport *http.Transport
	switch typed := base.(type) {
	case nil:
		transport = http.DefaultTransport.(*http.Transport).Clone()
	case *http.Transport:
		transport = typed.Clone()
	default:
		return nil, fmt.Errorf("cannot enforce private-network protection with transport %T", base)
	}

	underlyingDial := transport.DialContext
	if underlyingDial == nil {
		underlyingDial = (&net.Dialer{}).DialContext
	}
	transport.Proxy = nil // A proxy could resolve the unchecked hostname itself.
	transport.DialContext = restrictedDialContext(resolver, underlyingDial)
	transport.DialTLSContext = nil
	transport.DialTLS = nil
	return transport, nil
}

func restrictedDialContext(resolver ipResolver, dial dialContextFunc) dialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("parse dial address %q: %w", address, err)
		}
		addresses, err := resolvePublicAddresses(ctx, resolver, host)
		if err != nil {
			return nil, err
		}

		var dialErrors []error
		for _, resolved := range addresses {
			conn, dialErr := dial(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			dialErrors = append(dialErrors, dialErr)
		}
		return nil, fmt.Errorf("dial %s: %w", host, errors.Join(dialErrors...))
	}
}

func resolvePublicAddresses(ctx context.Context, resolver ipResolver, host string) ([]net.IPAddr, error) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return nil, fmt.Errorf("refusing private destination %q; use --allow-private to override", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return nil, fmt.Errorf("refusing private destination %q; use --allow-private to override", host)
		}
		return []net.IPAddr{{IP: ip}}, nil
	}
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, address := range addresses {
		if !isPublicIP(address.IP) {
			return nil, fmt.Errorf("refusing %q because it resolves to private address %s; use --allow-private to override", host, address.IP)
		}
	}
	return addresses, nil
}

func validateFetchDestination(ctx context.Context, target *url.URL, allowPrivate bool, resolver ipResolver) error {
	if target == nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil {
		return errors.New("URL must be an absolute HTTP or HTTPS URL without credentials")
	}
	if allowPrivate {
		return nil
	}

	_, err := resolvePublicAddresses(ctx, resolver, target.Hostname())
	return err
}

func isPublicIP(ip net.IP) bool {
	return ip != nil && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !ip.IsMulticast()
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
