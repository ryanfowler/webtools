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

Fetches an HTTP or HTTPS URL and writes YAML response metadata followed by the body:

```sh
webtools fetch https://example.com/
webtools fetch https://example.com/file.pdf > file-with-metadata
```

HTML is decoded using its declared character set and converted to Markdown with [pagemark](https://github.com/ryanfowler/pagemark). The extracted title, author, date, canonical URL, and other available page metadata are included in the YAML frontmatter. Markdown and other response bodies are otherwise copied unchanged.

Binary bodies are written when stdout is redirected. To avoid corrupting a terminal, `webtools fetch` prints a warning and suppresses a binary body when stdout is a terminal.

## Development

```sh
gofmt -w .
go test ./...
go vet ./...
```
