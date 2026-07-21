package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const userAgent = "webtools/0.1 (+https://github.com/ryanfowler/webtools)"

func main() {
	terminal, _ := isTerminal(os.Stdout)
	err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, &http.Client{Timeout: 30 * time.Second}, terminal)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return
	}
	fmt.Fprintln(os.Stderr, "webtools:", err)
	os.Exit(1)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, client *http.Client, stdoutIsTerminal bool) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("a command is required")
	}

	switch args[0] {
	case "search":
		return runSearch(ctx, args[1:], stdout, stderr, client)
	case "fetch":
		return runFetch(ctx, args[1:], stdout, stderr, client, stdoutIsTerminal)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  webtools search [--limit N] QUERY
  webtools fetch URL

Commands:
  search  Search DuckDuckGo and output results as JSON
  fetch   Fetch a URL and output its content with YAML frontmatter`)
}

func isTerminal(f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	return info.Mode()&os.ModeCharDevice != 0, nil
}
