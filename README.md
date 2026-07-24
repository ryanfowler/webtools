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
webtools search "Go HTTP client" --limit 5  # flags may appear after the query
```

Each result contains `title`, `url`, and, when available, `snippet`.

## Fetch

Fetches an HTTP or HTTPS HTML page and writes YAML response metadata followed by extracted Markdown:

```sh
webtools fetch https://example.com/
webtools fetch --max-chars 20000 --max-response-bytes 2097152 https://example.com/
```

Downloads are limited to 5 MiB and extracted Markdown to 50,000 characters by default. The limits can be changed with `--max-response-bytes` and `--max-chars`; frontmatter reports character counts and whether output was truncated.

HTML is decoded using its declared character set and extracted with [pagemark](https://github.com/ryanfowler/pagemark). The title, author, date, canonical URL, and other available page metadata are included in the YAML frontmatter. Non-HTML responses and pages from which pagemark cannot extract useful content return an error.

For safety, fetch rejects loopback, private, link-local, unspecified, and multicast destinations, including redirect targets. Use `--allow-private` when intentionally fetching a development server or other trusted private endpoint.

## Agent integration

### Portable Agent Skills

Install the bundled CLI-backed `web-search` and `web-fetch` [Agent Skills](https://agentskills.io) for any compatible agent:

```sh
webtools install
```

This installs them under `~/.agents/skills/`. The skills instruct an agent to invoke the CLI through its shell tool.

### Native pi tools

For [pi](https://pi.dev), install the native `web_search` and `web_fetch` extension tools:

```sh
webtools install pi
```

The extension provides typed arguments, research guidance, cancellation, output truncation, structured tool results, and compact TUI rendering without routing calls through the model's shell tool. It is installed as `extensions/webtools/index.ts`. The pi target uses `$PI_CODING_AGENT_DIR` when set and `~/.pi/agent` otherwise.

The repository can also be loaded directly as a pi package after the `webtools` executable has been installed:

```sh
pi install git:github.com/ryanfowler/webtools
```

Installation is idempotent and preserves modified resources; use `webtools install --force [agents|pi]` to replace modified or older copies. Restart pi or run `/reload` after installation.

## Development

```sh
gofmt -w .
go test ./...
go vet ./...
pi -e ./extensions/webtools/index.test.ts --list-models
```
