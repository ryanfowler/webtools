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

// skillNames are the portable, CLI-backed skills installed for Agent Skills clients.
var skillNames = []string{"web-search", "web-fetch"}

// embeddedResources contains the Agent Skills and pi extension shipped with webtools.
//
//go:embed skills/*/SKILL.md extensions/webtools/index.ts
var embeddedResources embed.FS

type resourceSpec struct {
	name       string
	sourcePath string
	targetPath string
}

type resourceInstall struct {
	spec    resourceSpec
	source  []byte
	status  string
	pending bool
}

func runInstall(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("webtools install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprintln(flags.Output(), "Usage: webtools install [--force] [agents|pi]") }
	force := flags.Bool("force", false, "replace modified installed resources")
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
	if agent == "pi" {
		return installPiResources(filepath.Dir(target), *force, stdout)
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
	specs := make([]resourceSpec, 0, len(skillNames))
	for _, name := range skillNames {
		specs = append(specs, resourceSpec{
			name:       name + " skill",
			sourcePath: filepath.ToSlash(filepath.Join("skills", name, "SKILL.md")),
			targetPath: filepath.Join(target, name, "SKILL.md"),
		})
	}
	return installResources(specs, force, stdout)
}

func installPiResources(configDir string, force bool, stdout io.Writer) error {
	return installResources([]resourceSpec{
		{
			name:       "webtools pi extension",
			sourcePath: "extensions/webtools/index.ts",
			targetPath: filepath.Join(configDir, "extensions", "webtools", "index.ts"),
		},
	}, force, stdout)
}

func installResources(specs []resourceSpec, force bool, stdout io.Writer) error {
	installs := make([]resourceInstall, 0, len(specs))
	for _, spec := range specs {
		source, err := fs.ReadFile(embeddedResources, spec.sourcePath)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", spec.name, err)
		}
		installed, err := os.ReadFile(spec.targetPath)
		switch {
		case err == nil && string(installed) == string(source):
			installs = append(installs, resourceInstall{spec: spec, source: source, status: "unchanged"})
		case err == nil && !force:
			return fmt.Errorf("%s already exists with different content; use --force to replace it", spec.targetPath)
		case err == nil:
			installs = append(installs, resourceInstall{spec: spec, source: source, status: "replaced", pending: true})
		case errors.Is(err, os.ErrNotExist):
			installs = append(installs, resourceInstall{spec: spec, source: source, status: "installed", pending: true})
		default:
			return fmt.Errorf("inspect %s: %w", spec.targetPath, err)
		}
	}

	for _, install := range installs {
		if install.pending {
			if err := writeFileAtomic(install.spec.targetPath, install.source, 0o644); err != nil {
				return fmt.Errorf("install %s: %w", install.spec.name, err)
			}
		}
		fmt.Fprintf(stdout, "%s %s\n", install.status, install.spec.targetPath)
	}
	return nil
}

func writeFileAtomic(path string, content []byte, mode fs.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".webtools-*")
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
