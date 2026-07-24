package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const userAgent = "webtools/0.1 (+https://github.com/ryanfowler/webtools)"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	terminal, _ := isTerminal(os.Stdout)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, &http.Client{Timeout: 30 * time.Second}, terminal)
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
	case "install":
		return runInstall(args[1:], stdout, stderr)
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
  webtools fetch [--max-response-bytes N] [--max-chars N] [--allow-private] URL
  webtools install [--force] [agents|pi]

Commands:
  search   Search DuckDuckGo and output results as JSON
  fetch    Fetch a URL and output its content with YAML frontmatter
  install  Install portable Agent Skills, or native pi tools with the pi target`)
}

func isTerminal(f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	return info.Mode()&os.ModeCharDevice != 0, nil
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, errors.New("response limit must not be negative")
	}
	data, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) < limit {
		return data, nil
	}

	var probe [1]byte
	_, err = io.ReadFull(r, probe[:])
	if err == nil {
		return nil, fmt.Errorf("response exceeds %d-byte limit", limit)
	}
	if !errors.Is(err, io.EOF) {
		return nil, err
	}
	return data, nil
}

// parseInterspersed allows flags before or after positional arguments. A bare --
// still marks all following arguments as positional.
func parseInterspersed(flags *flag.FlagSet, args []string) error {
	options := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			positionals = append(positionals, arg)
			continue
		}

		name := strings.TrimLeft(arg, "-")
		hasValue := strings.Contains(name, "=")
		if hasValue {
			name = strings.SplitN(name, "=", 2)[0]
		}
		option := flags.Lookup(name)
		options = append(options, arg)
		if option == nil || hasValue {
			continue
		}
		boolFlag, isBool := option.Value.(interface{ IsBoolFlag() bool })
		if isBool && boolFlag.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			options = append(options, args[i])
		}
	}
	return flags.Parse(append(options, positionals...))
}
