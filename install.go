package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const defaultSkillTarget = "agents"

var skillNames = []string{"web-search", "web-fetch"}

// embeddedSkills contains the Agent Skills shipped with webtools.
//
//go:embed skills/*/SKILL.md
var embeddedSkills embed.FS

type skillInstall struct {
	name    string
	source  []byte
	path    string
	status  string
	pending bool
}

func runInstall(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("webtools install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprintln(flags.Output(), "Usage: webtools install [--force] [agents|pi]") }
	force := flags.Bool("force", false, "replace modified installed skills")
	if err := parseInterspersed(flags, args); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		flags.Usage()
		return errors.New("install accepts at most one agent")
	}

	agent := defaultSkillTarget
	if flags.NArg() == 1 {
		agent = flags.Arg(0)
	}
	target, err := skillTarget(agent)
	if err != nil {
		return err
	}
	return installSkills(target, *force, stdout)
}

func skillTarget(agent string) (string, error) {
	if agent != "agents" && agent != "pi" {
		return "", fmt.Errorf("unsupported agent %q (supported: agents, pi)", agent)
	}
	if agent == "pi" {
		if configDir := os.Getenv("PI_CODING_AGENT_DIR"); configDir != "" {
			return filepath.Join(configDir, "skills"), nil
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	if agent == "agents" {
		return filepath.Join(home, ".agents", "skills"), nil
	}
	return filepath.Join(home, ".pi", "agent", "skills"), nil
}

func installSkills(target string, force bool, stdout io.Writer) error {
	installs := make([]skillInstall, 0, len(skillNames))
	for _, name := range skillNames {
		source, err := fs.ReadFile(embeddedSkills, filepath.ToSlash(filepath.Join("skills", name, "SKILL.md")))
		if err != nil {
			return fmt.Errorf("read embedded %s skill: %w", name, err)
		}
		path := filepath.Join(target, name, "SKILL.md")
		installed, err := os.ReadFile(path)
		switch {
		case err == nil && string(installed) == string(source):
			installs = append(installs, skillInstall{name: name, source: source, path: path, status: "unchanged"})
		case err == nil && !force:
			return fmt.Errorf("%s already exists with different content; use --force to replace it", path)
		case err == nil:
			installs = append(installs, skillInstall{name: name, source: source, path: path, status: "replaced", pending: true})
		case errors.Is(err, os.ErrNotExist):
			installs = append(installs, skillInstall{name: name, source: source, path: path, status: "installed", pending: true})
		default:
			return fmt.Errorf("inspect %s: %w", path, err)
		}
	}

	for _, install := range installs {
		if install.pending {
			if err := writeFileAtomic(install.path, install.source, 0o644); err != nil {
				return fmt.Errorf("install %s skill: %w", install.name, err)
			}
		}
		fmt.Fprintf(stdout, "%s %s\n", install.status, install.path)
	}
	return nil
}

func writeFileAtomic(path string, content []byte, mode fs.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".SKILL.md-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if _, err := temp.Write(content); err != nil {
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
