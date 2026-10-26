package compose

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// config is a merged configuration as `compose config --format json`
// gives it: the app service publishes 8080, a debugger port and a range;
// the workspace is mounted at /workspace.
func config(t *testing.T, workspace string) *Config {
	t.Helper()
	src := `{
	  "services": {
	    "app": {
	      "networks": {"default": null},
	      "ports": [
	        {"mode": "ingress", "target": 8080, "published": "8080", "protocol": "tcp"},
	        {"mode": "ingress", "host_ip": "127.0.0.1", "target": 9229, "published": "9229", "protocol": "tcp"},
	        {"mode": "ingress", "target": 8080, "published": "8080", "protocol": "udp"}
	      ],
	      "volumes": [
	        {"type": "volume", "source": "node_modules", "target": "/workspace/node_modules"},
	        {"type": "bind", "source": ` + jsonString(workspace) + `, "target": "/workspace", "bind": {}}
	      ]
	    },
	    "db": {"networks": {"default": null}}
	  }
	}`
	var cfg Config
	if err := json.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatal(err)
	}
	return &cfg
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func render(t *testing.T, o Override) string {
	t.Helper()
	b, err := o.Render()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestOverrideMainWorktree: the service joins bl-<folder> with that alias,
// keeps its default network, and stops publishing 8080/tcp; the other
// published ports stay, and a main worktree gets no git mount.
func TestOverrideMainWorktree(t *testing.T) {
	o, notes, err := Plan(config(t, "/home/u/api"), "app", "bl-api", "bl-api", 8080, nil)
	if err != nil || len(notes) > 0 {
		t.Fatal(err, notes)
	}
	want := `# Written by berthlight add on each start of the devcontainer, for this
# worktree only (PRD 0041). Do not edit or commit it.
services:
  "app":
    networks:
      "default": {}
      "bl-api":
        aliases: ["bl-api"]
    ports: !override
      - {"mode":"ingress","host_ip":"127.0.0.1","target":9229,"published":"9229","protocol":"tcp"}
      - {"mode":"ingress","target":8080,"published":"8080","protocol":"udp"}
networks:
  "bl-api":
    external: true
`
	if got := render(t, o); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestOverridePorts(t *testing.T) {
	cfg := config(t, "/w")
	// Only the dev server's port: nothing is left, so the list is reset.
	app := cfg.Services["app"]
	app.Ports = app.Ports[:1]
	cfg.Services["app"] = app
	o, _, err := Plan(cfg, "app", "bl-api", "bl-api", 8080, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := render(t, o); !strings.Contains(got, "    ports: !reset []\n") {
		t.Errorf("no reset:\n%s", got)
	}
	// A port the service does not publish: its ports stay untouched.
	o, _, err = Plan(cfg, "app", "bl-api", "bl-api", 3000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := render(t, o); strings.Contains(got, "ports") {
		t.Errorf("ports touched:\n%s", got)
	}
}

// TestOverrideNetworks: a service on networks of its own keeps them, and
// is not added to the default one; network_mode cannot join a network.
func TestOverrideNetworks(t *testing.T) {
	cfg := &Config{Services: map[string]Service{
		"app":  {Networks: map[string]json.RawMessage{"backend": nil}},
		"host": {NetworkMode: "host"},
	}}
	o, _, err := Plan(cfg, "app", "bl-api", "bl-api", 8080, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := render(t, o); strings.Contains(got, "default") {
		t.Errorf("default network added:\n%s", got)
	}
	if _, _, err := Plan(cfg, "host", "bl-api", "bl-api", 8080, nil); err == nil || !strings.Contains(err.Error(), "network_mode") {
		t.Errorf("network_mode: %v", err)
	}
	if _, _, err := Plan(cfg, "web", "bl-api", "bl-api", 8080, nil); err == nil || !strings.Contains(err.Error(), `no service "web"`) {
		t.Errorf("missing service: %v", err)
	}
}

// TestOverrideGitRelative: in a linked worktree with a relative gitdir,
// the repository's git directory is mounted where the gitdir resolves
// from the workspace inside the container (PRD 0041, C6).
func TestOverrideGitRelative(t *testing.T) {
	root := t.TempDir()
	api, feat := filepath.Join(root, "api"), filepath.Join(root, "api-feat")
	os.MkdirAll(filepath.Join(api, ".git", "worktrees", "api-feat"), 0o755)
	os.MkdirAll(feat, 0o755)
	os.WriteFile(filepath.Join(feat, ".git"), []byte("gitdir: ../api/.git/worktrees/api-feat\n"), 0o644)
	g, err := LinkedGit(feat, filepath.Join(api, ".git"))
	if err != nil || g == nil {
		t.Fatal(g, err)
	}
	o, notes, err := Plan(config(t, feat), "app", "bl-api-feat", "bl-api-feat", 8080, g)
	if err != nil || len(notes) > 0 {
		t.Fatal(err, notes)
	}
	want := "    volumes:\n      - {\"type\": \"bind\", \"source\": " + jsonString(filepath.Join(api, ".git")) + ", \"target\": \"/api/.git\"}\n"
	if got := render(t, o); !strings.Contains(got, want) {
		t.Errorf("got:\n%s\nwant it to hold:\n%s", got, want)
	}

	// The workspace's parent mounted: the git directory is in already.
	cfg := config(t, root)
	o, _, _ = Plan(cfg, "app", "bl-api-feat", "bl-api-feat", 8080, g)
	if len(o.Mounts) != 0 {
		t.Errorf("mounted %v, though the parent is mounted", o.Mounts)
	}

	// The worktree not mounted at all: a note, no mount.
	o, notes, _ = Plan(config(t, filepath.Join(root, "elsewhere")), "app", "bl-api-feat", "bl-api-feat", 8080, g)
	if len(o.Mounts) != 0 || len(notes) != 1 {
		t.Errorf("mounts %v, notes %q", o.Mounts, notes)
	}

	// A main worktree has a .git directory.
	if g, err := LinkedGit(api, filepath.Join(api, ".git")); g != nil || err != nil {
		t.Errorf("main worktree: %v %v", g, err)
	}
}

// TestOverrideGitAbsolute: an absolute gitdir resolves at the same path in
// the container, so the git directory is mounted there, with Compose or
// without.
func TestOverrideGitAbsolute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix paths")
	}
	g := &Git{Worktree: "/home/u/api-feat", GitDir: "/home/u/api/.git/worktrees/api-feat", CommonDir: "/home/u/api/.git"}
	want := Mount{Source: "/home/u/api/.git", Target: "/home/u/api/.git"}
	for _, cfg := range []*Config{config(t, "/home/u/api-feat"), nil} {
		o, _, err := Plan(cfg, "app", "bl-api-feat", "bl-api-feat", 8080, g)
		if err != nil {
			t.Fatal(err)
		}
		if len(o.Mounts) != 1 || o.Mounts[0] != want {
			t.Errorf("config %v: mounts %v, want %v", cfg != nil, o.Mounts, want)
		}
	}
}

// TestOverrideWithoutCompose: without the merged configuration, the
// override joins the network and keeps the default one, and leaves ports
// alone.
func TestOverrideWithoutCompose(t *testing.T) {
	o, _, err := Plan(nil, "app", "bl-api", "bl-api", 8080, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := render(t, o)
	if !strings.Contains(got, "\"default\": {}") || strings.Contains(got, "ports") || !strings.Contains(got, "external: true") {
		t.Errorf("got:\n%s", got)
	}
}

// TestOverrideHostile: names that would add YAML of their own are
// refused, and paths, which can hold anything, stay one quoted scalar,
// with $ escaped from Compose's interpolation.
func TestOverrideHostile(t *testing.T) {
	for _, o := range []Override{
		{Service: "app", Network: "bl-x\"\n    privileged: true\n#", Alias: "bl-x"},
		{Service: "app", Network: "bl-x: {}\nservices", Alias: "bl-x"},
		{Service: "app", Network: "evil", Alias: "evil"},
		{Service: "app\n  evil:", Network: "bl-x", Alias: "bl-x"},
		{Service: "app", Network: "bl-x", Alias: "bl-x]\n    privileged: true"},
		{Service: "app", Network: "bl-x", Alias: "bl-x", Mounts: []Mount{{Source: "/a", Target: "relative"}}},
	} {
		if _, err := o.Render(); err == nil {
			t.Errorf("rendered %+v", o)
		}
	}
	hostile := "/home/u/x\"}\n    privileged: true\n#${HOME}"
	o := Override{Service: "app", Network: "bl-x", Alias: "bl-x", Mounts: []Mount{{Source: hostile, Target: "/x/.git"}}}
	got := render(t, o)
	if strings.Contains(got, "privileged: true\n") || strings.Contains(strings.ReplaceAll(got, "$${", ""), "${") || !strings.Contains(got, "$${HOME}") {
		t.Errorf("got:\n%s", got)
	}
	// Each line is the template's: indentation, a key, a quoted scalar.
	for _, l := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if strings.Contains(l, "privileged") && !strings.Contains(l, `\"}\n    privileged: true\n#`) {
			t.Errorf("line %q escapes its quotes", l)
		}
	}
	// The source reads back as the path, once Compose's $$ is undone.
	line := got[strings.Index(got, `"source": `)+len(`"source": `):]
	line = line[:strings.Index(line, `, "target"`)]
	var back string
	if err := json.Unmarshal([]byte(strings.ReplaceAll(line, "$$", "$")), &back); err != nil || back != hostile {
		t.Errorf("source reads back as %q (%v)", back, err)
	}
}
