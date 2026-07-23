# webtools

A small Go CLI for searching and fetching the web.

## Install

```sh
go install github.com/ryanfowler/webtools@latest
```

## Search

Searches DuckDuckGo's HTML endpoint and prints organic results as JSON. The default limit is 10 and the maximum is 100.

```sh
webtools search "Go HTTP client"
webtools search --limit 5 "Go HTTP client"
```

Each result contains `title`, `url`, and, when available, `snippet`.

## Fetch

Fetches an HTTP or HTTPS HTML page or PDF and writes YAML response metadata followed by extracted Markdown:

```sh
webtools fetch https://example.com/
```

HTML is decoded using its declared character set and extracted with [pagemark](https://github.com/ryanfowler/pagemark). Text-based PDFs are extracted page by page, with available document metadata and the page count included in the YAML frontmatter. PDF responses, decoded text-related streams, and extracted text are each limited to 10 MiB; PDFs are also limited to 1,000 pages. To bound text expansion before allocation, individual `ToUnicode` mappings are limited to four bytes. Scanned PDFs without a text layer are not OCRed. Unsupported responses and documents without extractable content return an error.

## Development

```sh
gofmt -w .
go test ./...
go vet ./...
```
