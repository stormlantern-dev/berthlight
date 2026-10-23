// Package skill embeds berthlight's agent skill (PRD 0001, R8) and installs
// it where coding agents look for skills. The skill is in the Agent Skills
// format (https://agentskills.io/specification): a directory named after
// the skill, holding SKILL.md.
package skill

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Name is the skill's name and the directory it is installed in.
const Name = "berthlight"

//go:embed berthlight/SKILL.md
var source []byte

// Content returns SKILL.md for this version of berthlight.
func Content(version string) []byte {
	return bytes.ReplaceAll(source, []byte("{{VERSION}}"), []byte(version))
}

// Agent is a coding agent that loads skills.
type Agent struct {
	// ID is what --agent takes.
	ID string
	// Name is the product name.
	Name string
	// User and Project are the skills directories, relative to the home
	// directory and to the project. Each comes from the agent's own
	// documentation, in Docs, read 2026-10-23.
	User, Project string
	Docs          string
	// Markers are paths under the home directory, and Commands executables
	// on PATH, whose presence means the agent is installed.
	Markers, Commands []string
}

// Agents are the agents berthlight installs the skill for. Codex, OpenCode
// and pi all read the shared Agent Skills location .agents/skills, so one
// copy there serves the three; Claude Code reads only .claude/skills.
// OpenCode reads .claude/skills too; with both copies present it keeps one,
// and they are the same file.
var Agents = []Agent{
	{
		ID: "claude", Name: "Claude Code",
		User: ".claude/skills", Project: ".claude/skills",
		Docs:    "https://code.claude.com/docs/en/skills",
		Markers: []string{".claude"}, Commands: []string{"claude"},
	},
	{
		ID: "codex", Name: "Codex",
		User: ".agents/skills", Project: ".agents/skills",
		Docs:    "https://learn.chatgpt.com/docs/build-skills",
		Markers: []string{".codex"}, Commands: []string{"codex"},
	},
	{
		ID: "opencode", Name: "OpenCode",
		User: ".agents/skills", Project: ".agents/skills",
		Docs:    "https://opencode.ai/docs/skills/",
		Markers: []string{".config/opencode"}, Commands: []string{"opencode"},
	},
	{
		ID: "pi", Name: "pi",
		User: ".agents/skills", Project: ".agents/skills",
		Docs:    "https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/skills.md",
		Markers: []string{".pi"}, Commands: []string{"pi"},
	},
}

// IDs lists the agent IDs.
func IDs() []string {
	var ids []string
	for _, a := range Agents {
		ids = append(ids, a.ID)
	}
	return ids
}

// Lookup returns the agents for comma-separated IDs, or all of them for
// "all".
func Lookup(list string) ([]Agent, error) {
	if list == "all" {
		return Agents, nil
	}
	var out []Agent
	for _, id := range strings.Split(list, ",") {
		id = strings.TrimSpace(strings.ToLower(id))
		i := slices.IndexFunc(Agents, func(a Agent) bool { return a.ID == id })
		if i < 0 {
			return nil, fmt.Errorf("unknown agent %q; known: %s, or all", id, strings.Join(IDs(), ", "))
		}
		out = append(out, Agents[i])
	}
	return out, nil
}

// Detect returns the agents that appear to be installed: a marker
// directory in home, or the command on PATH.
func Detect(home string) []Agent {
	var out []Agent
	for _, a := range Agents {
		found := false
		for _, m := range a.Markers {
			if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(m))); err == nil {
				found = true
			}
		}
		for _, c := range a.Commands {
			if _, err := exec.LookPath(c); err == nil {
				found = true
			}
		}
		if found {
			out = append(out, a)
		}
	}
	return out
}

// Target is one skills directory and the agents that read it.
type Target struct {
	Dir    string // the skill's own directory: <skills dir>/berthlight
	Agents []string
}

// Targets returns where to install for the agents: under base, which is the
// home directory for a user-wide install or the project directory. Agents
// sharing a directory share one target.
func Targets(agents []Agent, base string, project bool) []Target {
	var out []Target
	for _, a := range agents {
		rel := a.User
		if project {
			rel = a.Project
		}
		dir := filepath.Join(base, filepath.FromSlash(rel), Name)
		i := slices.IndexFunc(out, func(t Target) bool { return t.Dir == dir })
		if i < 0 {
			out = append(out, Target{Dir: dir})
			i = len(out) - 1
		}
		out[i].Agents = append(out[i].Agents, a.Name)
	}
	return out
}

// Result says what Install did in one target.
type Result string

// Results of Install.
const (
	Installed Result = "installed"
	Updated   Result = "updated"
	Unchanged Result = "unchanged"
)

// Install writes SKILL.md into the target directory.
func Install(t Target, content []byte) (Result, error) {
	path := filepath.Join(t.Dir, "SKILL.md")
	old, err := os.ReadFile(path)
	switch {
	case err == nil && bytes.Equal(old, content):
		return Unchanged, nil
	case err == nil:
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return "", err
		}
		return Updated, nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", err
	}
	if err := os.MkdirAll(t.Dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", err
	}
	return Installed, nil
}

// Uninstall removes the skill from the target, if it is berthlight's: a
// directory holding a SKILL.md named berthlight and nothing else. It
// reports whether there was anything to remove.
func Uninstall(t Target) (bool, error) {
	b, err := os.ReadFile(filepath.Join(t.Dir, "SKILL.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if fm, err := Parse(b); err != nil || fm.Name != Name {
		return false, fmt.Errorf("%s holds a SKILL.md that is not berthlight's; leaving it", t.Dir)
	}
	entries, err := os.ReadDir(t.Dir)
	if err != nil {
		return false, err
	}
	if len(entries) != 1 {
		return false, fmt.Errorf("%s holds other files besides SKILL.md; leaving it", t.Dir)
	}
	return true, os.RemoveAll(t.Dir)
}

// Frontmatter is the part of a SKILL.md header the spec constrains.
type Frontmatter struct {
	Name, Description, Compatibility string
}

// Parse reads the frontmatter's top-level single-line fields. It is not a
// YAML parser: it is enough for berthlight's own SKILL.md, which keeps each
// of these fields on one line.
func Parse(b []byte) (Frontmatter, error) {
	var fm Frontmatter
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	rest, ok := strings.CutPrefix(s, "---\n")
	if !ok {
		return fm, errors.New("SKILL.md does not start with --- frontmatter")
	}
	head, _, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return fm, errors.New("SKILL.md frontmatter is not closed with ---")
	}
	for _, line := range strings.Split(head, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '#' {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			return fm, fmt.Errorf("frontmatter line %q is not key: value", line)
		}
		val = strings.TrimSpace(val)
		switch key {
		case "name":
			fm.Name = val
		case "description":
			fm.Description = val
		case "compatibility":
			fm.Compatibility = val
		}
	}
	return fm, nil
}

var validName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Validate checks SKILL.md, installed in a directory named dir, against the
// Agent Skills specification.
func Validate(b []byte, dir string) error {
	fm, err := Parse(b)
	if err != nil {
		return err
	}
	var errs []error
	if n := len([]rune(fm.Name)); n < 1 || n > 64 || !validName.MatchString(fm.Name) {
		errs = append(errs, fmt.Errorf("name %q: 1-64 lowercase letters, digits and single hyphens", fm.Name))
	}
	if fm.Name != dir {
		errs = append(errs, fmt.Errorf("name %q does not match directory %q", fm.Name, dir))
	}
	if n := len([]rune(fm.Description)); n < 1 || n > 1024 {
		errs = append(errs, fmt.Errorf("description is %d characters, want 1-1024", n))
	}
	if n := len([]rune(fm.Compatibility)); n > 500 {
		errs = append(errs, fmt.Errorf("compatibility is %d characters, want at most 500", n))
	}
	if n := bytes.Count(b, []byte("\n")); n > 500 {
		errs = append(errs, fmt.Errorf("SKILL.md is %d lines; the specification recommends under 500", n))
	}
	return errors.Join(errs...)
}
