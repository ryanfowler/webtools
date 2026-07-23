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

Fetches an HTTP or HTTPS HTML page and writes YAML response metadata followed by extracted Markdown:

```sh
webtools fetch https://example.com/
```

HTML is decoded using its declared character set and extracted with [pagemark](https://github.com/ryanfowler/pagemark). The title, author, date, canonical URL, and other available page metadata are included in the YAML frontmatter. Non-HTML responses and pages from which pagemark cannot extract useful content return an error.

## Agent skills

Install the bundled `web-search` and `web-fetch` [Agent Skills](https://agentskills.io) into the generic global skills directory:

```sh
webtools install
```

This installs the skills under `~/.agents/skills/`, which is supported by pi and other compatible agents. To install them into pi's agent-specific directory instead, run:

```sh
webtools install pi
```

The pi target uses `$PI_CODING_AGENT_DIR/skills` when that environment variable is set and `~/.pi/agent/skills/` otherwise. Installation is idempotent and preserves modified skill files; use `webtools install --force [agents|pi]` to replace modified or older copies.

## Development

```sh
gofmt -w .
go test ./...
go vet ./...
```
