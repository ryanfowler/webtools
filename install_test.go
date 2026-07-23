package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")

	tests := []struct {
		agent string
		want  string
	}{
		{"agents", filepath.Join(home, ".agents", "skills")},
		{"pi", filepath.Join(home, ".pi", "agent", "skills")},
	}
	for _, test := range tests {
		t.Run(test.agent, func(t *testing.T) {
			got, err := skillTarget(test.agent)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("target = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSkillTargetUsesPiConfigDirWithoutHome(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "custom-pi")
	t.Setenv("HOME", "")
	t.Setenv("PI_CODING_AGENT_DIR", configDir)
	got, err := skillTarget("pi")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(configDir, "skills")
	if got != want {
		t.Fatalf("target = %q, want %q", got, want)
	}
}

func TestInstallSkillsInstallsBothSkillsAndIsIdempotent(t *testing.T) {
	target := t.TempDir()
	var output bytes.Buffer
	if err := installSkills(target, false, &output); err != nil {
		t.Fatal(err)
	}
	for _, name := range skillNames {
		content, err := os.ReadFile(filepath.Join(target, name, "SKILL.md"))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(content), "name: "+name) {
			t.Errorf("%s frontmatter does not contain matching name", name)
		}
		if !strings.Contains(output.String(), "installed "+filepath.Join(target, name, "SKILL.md")) {
			t.Errorf("missing installation output for %s: %s", name, output.String())
		}
	}

	output.Reset()
	if err := installSkills(target, false, &output); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(output.String(), "unchanged "); got != len(skillNames) {
		t.Fatalf("got %d unchanged skills, want %d: %s", got, len(skillNames), output.String())
	}
}

func TestInstallSkillsRequiresForceToReplaceChanges(t *testing.T) {
	target := t.TempDir()
	if err := installSkills(target, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(target, "web-search", "SKILL.md")
	if err := os.WriteFile(path, []byte("custom content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := installSkills(target, false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "use --force") {
		t.Fatalf("error = %v", err)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "custom content\n" {
		t.Fatalf("modified skill was overwritten: %q", content)
	}

	var output bytes.Buffer
	if err := installSkills(target, true, &output); err != nil {
		t.Fatal(err)
	}
	content, readErr = os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(content), "name: web-search") {
		t.Fatalf("skill was not replaced: %q", content)
	}
	if !strings.Contains(output.String(), "replaced "+path) {
		t.Fatalf("missing replacement output: %s", output.String())
	}
}

func TestRunInstallRejectsUnknownAgentWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	err := runInstall([]string{"unknown"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "supported: agents, pi") {
		t.Fatalf("error = %v", err)
	}
}
