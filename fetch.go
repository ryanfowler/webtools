package main

import (
	"bytes"
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

	"github.com/ledongthuc/pdf"
	"github.com/ryanfowler/pagemark"
	"golang.org/x/net/html/charset"
)

const (
	maxPDFResponseSize int64 = 10 << 20 // 10 MiB
	maxPDFDecodedSize  int64 = 10 << 20 // 10 MiB
	maxPDFTextSize           = 10 << 20 // 10 MiB
	maxPDFPages              = 1000
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
	req.Header.Set("Accept", "text/html, application/xhtml+xml, application/pdf")

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

	switch mediaType {
	case "text/html", "application/xhtml+xml":
		return outputHTML(resp.Body, contentType, finalURL, baseFields, stdout)
	case "application/pdf":
		return outputPDF(resp.Body, baseFields, stdout)
	default:
		return fmt.Errorf("response is not HTML or PDF (Content-Type: %s)", displayContentType(mediaType))
	}
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

func outputPDF(body io.Reader, fields []metadataField, stdout io.Writer) error {
	return outputPDFWithLimits(body, fields, stdout, maxPDFResponseSize, maxPDFPages)
}

func outputPDFWithLimits(body io.Reader, fields []metadataField, stdout io.Writer, maxSize int64, maxPages int) error {
	data, err := io.ReadAll(io.LimitReader(body, maxSize+1))
	if err != nil {
		return fmt.Errorf("read PDF: %w", err)
	}
	if int64(len(data)) > maxSize {
		return fmt.Errorf("PDF response exceeds %d MiB limit", maxSize>>20)
	}

	pdfFields, markdown, err := extractPDF(data, maxPages)
	if err != nil {
		return err
	}
	fields = append(fields, pdfFields...)

	if err := writeFrontmatter(stdout, fields); err != nil {
		return err
	}
	if _, err := io.WriteString(stdout, markdown+"\n"); err != nil {
		return fmt.Errorf("write extracted Markdown: %w", err)
	}
	return nil
}

func extractPDF(data []byte, maxPages int) (fields []metadataField, markdown string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			fields = nil
			markdown = ""
			err = fmt.Errorf("extract PDF: parser panic: %v", recovered)
		}
	}()

	doc, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, "", fmt.Errorf("extract PDF: %w", err)
	}
	pageCount := doc.NumPage()
	if pageCount <= 0 || pageCount > maxPages {
		return nil, "", fmt.Errorf("extract PDF: invalid page count %d", pageCount)
	}

	info := doc.Trailer().Key("Info")
	fields = []metadataField{
		{"title", info.Key("Title").Text()},
		{"description", info.Key("Subject").Text()},
		{"author", info.Key("Author").Text()},
		{"date", info.Key("CreationDate").Text()},
		{"page_count", strconv.Itoa(pageCount)},
	}

	decodedRemaining := maxPDFDecodedSize
	textInputRemaining := int64(maxPDFTextSize / 6) // A UTF-16 mapping can emit at most 6 UTF-8 bytes per source byte.
	var output strings.Builder
	for pageNumber := 1; pageNumber <= pageCount; pageNumber++ {
		page := doc.Page(pageNumber)
		if err := checkPDFPageStreams(page, &decodedRemaining, &textInputRemaining); err != nil {
			return nil, "", fmt.Errorf("extract PDF page %d: %w", pageNumber, err)
		}

		text, err := page.GetPlainText(nil)
		if err != nil {
			return nil, "", fmt.Errorf("extract PDF page %d: %w", pageNumber, err)
		}
		additional := len(text)
		if output.Len() > 0 {
			additional += 2
		}
		if additional > maxPDFTextSize-output.Len() {
			return nil, "", fmt.Errorf("extract PDF: extracted text exceeds %d MiB limit", maxPDFTextSize>>20)
		}
		if text = normalizePDFText(text); text != "" {
			if output.Len() > 0 {
				output.WriteString("\n\n")
			}
			output.WriteString(text)
		}
	}
	markdown = output.String()
	if markdown == "" {
		return nil, "", errors.New("extract PDF: no extractable text found")
	}
	return fields, markdown, nil
}

func checkPDFPageStreams(page pdf.Page, decodedRemaining, textInputRemaining *int64) error {
	contents := page.V.Key("Contents")
	if err := checkPDFStream(contents, decodedRemaining); err != nil {
		return err
	}
	if err := checkPDFTextInput(contents, textInputRemaining); err != nil {
		return err
	}
	for _, name := range page.Fonts() {
		toUnicode := page.Font(name).V.Key("ToUnicode")
		if err := checkPDFStream(toUnicode, decodedRemaining); err != nil {
			return err
		}
		if err := checkPDFCMap(toUnicode); err != nil {
			return err
		}
	}
	return nil
}

func checkPDFTextInput(contents pdf.Value, remaining *int64) error {
	var limitErr error
	pdf.Interpret(contents, func(stack *pdf.Stack, operator string) {
		if limitErr != nil {
			return
		}
		var size int64
		switch operator {
		case "BT", "T*":
			size = 1
		case "Tj":
			size = int64(len(stack.Pop().RawString()))
		case "'", "\"":
			size = int64(len(stack.Pop().RawString())) + 1
		case "TJ":
			values := stack.Pop()
			for i := 0; i < values.Len(); i++ {
				size += int64(len(values.Index(i).RawString()))
			}
		}
		for stack.Len() > 0 {
			stack.Pop()
		}
		if size > *remaining {
			limitErr = errors.New("extracted text exceeds 10 MiB limit")
			return
		}
		*remaining -= size
	})
	return limitErr
}

func checkPDFCMap(value pdf.Value) error {
	if value.Kind() != pdf.Stream {
		return nil
	}
	stream := value.Reader()
	data, err := io.ReadAll(io.LimitReader(stream, maxPDFDecodedSize+1))
	closeErr := stream.Close()
	if err != nil {
		return fmt.Errorf("decode ToUnicode CMap: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("decode ToUnicode CMap: %w", closeErr)
	}
	if int64(len(data)) > maxPDFDecodedSize {
		return errors.New("decoded content exceeds 10 MiB limit")
	}

	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '%':
			for i < len(data) && data[i] != '\n' && data[i] != '\r' {
				i++
			}
		case '<':
			if i+1 >= len(data) || data[i+1] == '<' || i > 0 && data[i-1] == '<' {
				continue
			}
			digits := 0
			closed := false
			for i++; i < len(data); i++ {
				if data[i] == '>' {
					closed = true
					break
				}
				if !isPDFWhitespace(data[i]) {
					digits++
				}
			}
			if !closed {
				return errors.New("invalid ToUnicode CMap string")
			}
			if (digits+1)/2 > 4 {
				return errors.New("ToUnicode mapping exceeds 4-byte limit")
			}
		case '(':
			length, end, ok := pdfLiteralStringLength(data, i)
			if !ok {
				return errors.New("invalid ToUnicode CMap string")
			}
			if length > 4 {
				return errors.New("ToUnicode mapping exceeds 4-byte limit")
			}
			i = end
		}
	}
	return nil
}

func pdfLiteralStringLength(data []byte, start int) (length, end int, ok bool) {
	depth := 1
	for i := start + 1; i < len(data); i++ {
		switch data[i] {
		case '\\':
			if i+1 >= len(data) {
				return 0, 0, false
			}
			i++
			if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
				i++
				continue
			}
			if data[i] != '\n' && data[i] != '\r' {
				length++
				if data[i] >= '0' && data[i] <= '7' {
					for consumed := 1; consumed < 3 && i+1 < len(data) && data[i+1] >= '0' && data[i+1] <= '7'; consumed++ {
						i++
					}
				}
			}
		case '(':
			depth++
			length++
		case ')':
			depth--
			if depth == 0 {
				return length, i, true
			}
			length++
		default:
			length++
		}
	}
	return 0, 0, false
}

func isPDFWhitespace(b byte) bool {
	return b == 0 || b == '\t' || b == '\n' || b == '\f' || b == '\r' || b == ' '
}

func checkPDFStream(value pdf.Value, remaining *int64) error {
	if value.Kind() == pdf.Array {
		for i := 0; i < value.Len(); i++ {
			if err := checkPDFStream(value.Index(i), remaining); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Kind() != pdf.Stream {
		return nil
	}
	if params := value.Key("DecodeParms"); params.Kind() != pdf.Array {
		if columns := params.Key("Columns").Int64(); columns > *remaining {
			return fmt.Errorf("decoded content exceeds %d MiB limit", maxPDFDecodedSize>>20)
		}
	} else {
		for i := 0; i < params.Len(); i++ {
			if columns := params.Index(i).Key("Columns").Int64(); columns > *remaining {
				return fmt.Errorf("decoded content exceeds %d MiB limit", maxPDFDecodedSize>>20)
			}
		}
	}

	stream := value.Reader()
	n, err := io.Copy(io.Discard, io.LimitReader(stream, *remaining+1))
	closeErr := stream.Close()
	if err != nil {
		return fmt.Errorf("decode content stream: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("decode content stream: %w", closeErr)
	}
	if n > *remaining {
		return fmt.Errorf("decoded content exceeds %d MiB limit", maxPDFDecodedSize>>20)
	}
	*remaining -= n
	return nil
}

func normalizePDFText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(out) > 0 && !blank {
				out = append(out, "")
				blank = true
			}
			continue
		}
		out = append(out, line)
		blank = false
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
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
