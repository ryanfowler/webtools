package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
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

func TestFetchPDFExtractsMarkdownAndMetadata(t *testing.T) {
	pdfData := testPDF(t, "PDF Guide", "Ada Example", "PDF Guide", "Useful PDF content.")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept"), "application/pdf") {
			t.Errorf("Accept header does not include application/pdf: %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdfData)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := runFetch(context.Background(), []string{server.URL}, &stdout, &stderr, server.Client(), false)
	if err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, want := range []string{`content_type: "application/pdf"`, `title: "PDF Guide"`, `author: "Ada Example"`, `page_count: "1"`, "PDF Guide", "Useful PDF content."} {
		if !strings.Contains(output, want) {
			t.Errorf("output does not contain %q:\n%s", want, output)
		}
	}
}

func TestFetchRejectsNonHTMLOrPDF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Heading"))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := runFetch(context.Background(), []string{server.URL}, &stdout, &stderr, server.Client(), false)
	if err == nil || !strings.Contains(err.Error(), "response is not HTML or PDF") {
		t.Fatalf("expected unsupported content error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected output: %q", stdout.String())
	}
}

func TestFetchRejectsOversizedPDF(t *testing.T) {
	var stdout bytes.Buffer
	err := outputPDFWithLimits(bytes.NewReader(make([]byte, 1025)), nil, &stdout, 1024, maxPDFPages)
	if err == nil || !strings.Contains(err.Error(), "PDF response exceeds") {
		t.Fatalf("expected PDF size error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected output: %q", stdout.String())
	}
}

func TestFetchRejectsInvalidPDFPageCount(t *testing.T) {
	for _, pageCount := range []int{-1, maxPDFPages + 1} {
		t.Run(fmt.Sprintf("count_%d", pageCount), func(t *testing.T) {
			pdfData := testPDFWithPageCount(t, "Invalid", "", "text", "", pageCount)
			var stdout bytes.Buffer
			err := outputPDF(bytes.NewReader(pdfData), nil, &stdout)
			want := fmt.Sprintf("invalid page count %d", pageCount)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("expected %q error, got %v", want, err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("unexpected output: %q", stdout.String())
			}
		})
	}
}

func TestFetchRejectsPDFDecompressionBomb(t *testing.T) {
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	chunk := make([]byte, 32<<10)
	for written := int64(0); written <= maxPDFDecodedSize; written += int64(len(chunk)) {
		if _, err := writer.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	pdfData := testPDFWithContent(t, "Compressed", "", compressed.Bytes(), 1, "FlateDecode")
	var stdout bytes.Buffer
	err := outputPDF(bytes.NewReader(pdfData), nil, &stdout)
	if err == nil || !strings.Contains(err.Error(), "decoded content exceeds") {
		t.Fatalf("expected decoded content size error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected output: %q", stdout.String())
	}
}

func TestFetchRejectsExpandingToUnicodeMapping(t *testing.T) {
	cmap := []byte("begincmap\n1 begincodespacerange <00> <ff> endcodespacerange\n1 beginbfchar <41> <004100410041> endbfchar\nendcmap")
	content := []byte("BT /F1 12 Tf 72 720 Td (AAAAAAAAAA) Tj ET")
	pdfData := testPDFWithContentAndCMap(t, "CMap", "", content, 1, "", cmap)
	var stdout bytes.Buffer
	err := outputPDF(bytes.NewReader(pdfData), nil, &stdout)
	if err == nil || !strings.Contains(err.Error(), "ToUnicode mapping exceeds") {
		t.Fatalf("expected ToUnicode mapping error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected output: %q", stdout.String())
	}
}

func TestFetchRejectsPDFWithUnsupportedFilter(t *testing.T) {
	pdfData := testPDFDocument(t, "Filtered", "", "text", "", 1, "UnsupportedFilter")
	var stdout bytes.Buffer
	err := outputPDF(bytes.NewReader(pdfData), nil, &stdout)
	if err == nil || !strings.Contains(err.Error(), "extract PDF") {
		t.Fatalf("expected PDF extraction error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected output: %q", stdout.String())
	}
}

func TestFetchRejectsPDFWithoutText(t *testing.T) {
	pdfData := testPDF(t, "Empty", "", "", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdfData)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := runFetch(context.Background(), []string{server.URL}, &stdout, &stderr, server.Client(), false)
	if err == nil || !strings.Contains(err.Error(), "no extractable text") {
		t.Fatalf("expected extraction error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected output: %q", stdout.String())
	}
}

func testPDF(t *testing.T, title, author, heading, body string) []byte {
	t.Helper()
	return testPDFWithPageCount(t, title, author, heading, body, 1)
}

func testPDFWithPageCount(t *testing.T, title, author, heading, body string, pageCount int) []byte {
	t.Helper()
	return testPDFDocument(t, title, author, heading, body, pageCount, "")
}

func testPDFDocument(t *testing.T, title, author, heading, body string, pageCount int, filter string) []byte {
	t.Helper()
	content := ""
	if heading != "" || body != "" {
		content = fmt.Sprintf("BT /F1 18 Tf 72 720 Td (%s) Tj ET\nBT /F1 12 Tf 72 690 Td (%s) Tj ET", heading, body)
	}
	return testPDFWithContent(t, title, author, []byte(content), pageCount, filter)
}

func testPDFWithContent(t *testing.T, title, author string, content []byte, pageCount int, filter string) []byte {
	t.Helper()
	return testPDFWithContentAndCMap(t, title, author, content, pageCount, filter, nil)
}

func testPDFWithContentAndCMap(t *testing.T, title, author string, content []byte, pageCount int, filter string, cmap []byte) []byte {
	t.Helper()
	filterEntry := ""
	if filter != "" {
		filterEntry = " /Filter /" + filter
	}
	font := []byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	if cmap != nil {
		font = []byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /ToUnicode 7 0 R >>")
	}
	objects := [][]byte{
		[]byte("<< /Type /Catalog /Pages 2 0 R >>"),
		fmt.Appendf(nil, "<< /Type /Pages /Kids [3 0 R] /Count %d >>", pageCount),
		[]byte("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>"),
		font,
		fmt.Appendf(nil, "<< /Length %d%s >>\nstream\n%s\nendstream", len(content), filterEntry, content),
		fmt.Appendf(nil, "<< /Title (%s) /Author (%s) >>", title, author),
	}
	if cmap != nil {
		objects = append(objects, fmt.Appendf(nil, "<< /Length %d >>\nstream\n%s\nendstream", len(cmap), cmap))
	}

	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for i, object := range objects {
		offsets[i+1] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n", i+1)
		output.Write(object)
		output.WriteString("\nendobj\n")
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&output, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R /Info 6 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return output.Bytes()
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
