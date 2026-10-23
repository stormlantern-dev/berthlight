package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmbeddedSkillIsValid: the shipped SKILL.md passes the Agent Skills
// specification's rules, in a directory named after it (PRD 0001, R8).
func TestEmbeddedSkillIsValid(t *testing.T) {
	b := Content("1.2.3")
	if err := Validate(b, Name); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `version: "1.2.3"`) || strings.Contains(string(b), "{{VERSION}}") {
		t.Error("version not filled in")
	}
	for _, cmd := range []string{"berthlight devcontainer", "berthlight url", "berthlight status", "0.0.0.0", "allowedHosts", "clientPort: 7480", "redirect"} {
		if !strings.Contains(string(b), cmd) {
			t.Errorf("SKILL.md does not mention %q", cmd)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	long := strings.Repeat("x", 1025)
	tests := map[string]string{
		"no frontmatter":  "# hi\n",
		"unclosed":        "---\nname: a\n",
		"uppercase name":  "---\nname: Berthlight\ndescription: d\n---\n",
		"double hyphen":   "---\nname: berth--light\ndescription: d\n---\n",
		"wrong directory": "---\nname: other\ndescription: d\n---\n",
		"no description":  "---\nname: berthlight\n---\n",
		"long desc":       "---\nname: berthlight\ndescription: " + long + "\n---\n",
	}
	for name, in := range tests {
		if err := Validate([]byte(in), Name); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestTargets: the install path per agent and scope, as each agent's
// documentation gives it (see Agents).
func TestTargets(t *testing.T) {
	want := map[string][2]string{
		"claude":   {".claude/skills/berthlight", ".claude/skills/berthlight"},
		"codex":    {".agents/skills/berthlight", ".agents/skills/berthlight"},
		"opencode": {".agents/skills/berthlight", ".agents/skills/berthlight"},
		"pi":       {".agents/skills/berthlight", ".agents/skills/berthlight"},
	}
	for id, w := range want {
		agents, err := Lookup(id)
		if err != nil {
			t.Fatal(err)
		}
		for i, project := range []bool{false, true} {
			got := Targets(agents, "/base", project)
			if len(got) != 1 || got[0].Dir != filepath.Join("/base", filepath.FromSlash(w[i])) {
				t.Errorf("%s project=%v: %+v, want %s", id, project, got, w[i])
			}
		}
	}
	// All four share two directories.
	all, _ := Lookup("all")
	got := Targets(all, "/home/u", false)
	if len(got) != 2 || len(got[1].Agents) != 3 {
		t.Errorf("targets for all agents: %+v", got)
	}
	if _, err := Lookup("claude,nope"); err == nil || !strings.Contains(err.Error(), "known: claude, codex, opencode, pi") {
		t.Errorf("unknown agent: %v", err)
	}
	if got, _ := Lookup(" Claude , pi"); len(got) != 2 {
		t.Errorf("lookup with spaces and case: %v", got)
	}
}

func TestDetect(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	if got := Detect(home); len(got) != 0 {
		t.Errorf("empty home: %v", got)
	}
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755)
	got := Detect(home)
	if len(got) != 2 || got[0].ID != "claude" || got[1].ID != "opencode" {
		t.Errorf("detected %v", got)
	}
}

func TestInstallUninstall(t *testing.T) {
	base := t.TempDir()
	agents, _ := Lookup("all")
	targets := Targets(agents, base, true)
	for _, want := range []Result{Installed, Unchanged} {
		for _, tg := range targets {
			if got, err := Install(tg, Content("1.0.0")); err != nil || got != want {
				t.Errorf("install %s: %v %v, want %v", tg.Dir, got, err, want)
			}
		}
	}
	if got, _ := Install(targets[0], Content("1.0.1")); got != Updated {
		t.Errorf("new version: %v", got)
	}
	b, err := os.ReadFile(filepath.Join(base, ".agents", "skills", "berthlight", "SKILL.md"))
	if err != nil || Validate(b, filepath.Base(filepath.Dir(filepath.Join(base, ".agents", "skills", "berthlight", "SKILL.md")))) != nil {
		t.Errorf("installed file: %v", err)
	}
	for _, tg := range targets {
		if ok, err := Uninstall(tg); !ok || err != nil {
			t.Errorf("uninstall %s: %v %v", tg.Dir, ok, err)
		}
		if ok, err := Uninstall(tg); ok || err != nil {
			t.Errorf("second uninstall %s: %v %v", tg.Dir, ok, err)
		}
	}
	// Someone else's skill in that directory is left alone.
	foreign := Target{Dir: filepath.Join(base, "x", "berthlight")}
	os.MkdirAll(foreign.Dir, 0o755)
	os.WriteFile(filepath.Join(foreign.Dir, "SKILL.md"), []byte("---\nname: other\ndescription: d\n---\n"), 0o644)
	if _, err := Uninstall(foreign); err == nil {
		t.Error("removed a foreign skill")
	}
}
