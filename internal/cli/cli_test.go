package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"stormlantern.nl/berthlight/internal/route"
	"stormlantern.nl/berthlight/internal/status"
	"stormlantern.nl/berthlight/internal/tui"
)

func TestRun(t *testing.T) {
	t.Setenv("BERTHLIGHT_STATE_DIR", t.TempDir())
	tests := []struct {
		args     []string
		code     int
		inStdout string
		inStderr string
	}{
		{[]string{"version"}, 0, "berthlight ", ""},
		{[]string{"help"}, 0, "Usage:", ""},
		{nil, 2, "", "Usage:"},
		{[]string{"nope"}, 2, "", `unknown command "nope"`},
		{[]string{"url", "-C", t.TempDir()}, 1, "", "git rev-parse"},
		{[]string{"add"}, 2, "", "usage: berthlight add"},
		{[]string{"add", "a", "b", "c"}, 2, "", "usage: berthlight add"},
		{[]string{"add", "shop"}, 1, "", `port "shop" is not a number`},
		{[]string{"add", "shop", "8080", "--network", "shared"}, 1, "", "not a valid route network"},
		{[]string{"add", "-C", t.TempDir(), "8080"}, 1, "", "deriving the name from the worktree"},
		{[]string{"devcontainer", "shop"}, 2, "", "usage: berthlight devcontainer"},
		{[]string{"skill"}, 2, "", "usage: berthlight skill install"},
		{[]string{"skill", "nope"}, 2, "", "usage: berthlight skill install"},
		{[]string{"skill", "install", "--agent", "vim"}, 1, "", `unknown agent "vim"`},
		{[]string{"skill", "show"}, 0, "name: berthlight", ""},
		{[]string{"devcontainer", "shop", "web"}, 1, "", "not a number"},
		{[]string{"devcontainer", "-C", t.TempDir(), "shop", "8080"}, 1, "", "no devcontainer.json"},
		{[]string{"add", "Shop!", "8080"}, 1, "", "not a valid label"},
		{[]string{"add", "shop", "http"}, 1, "", "not a number"},
		{[]string{"add", "shop", "70000"}, 1, "", "out of range"},
		{[]string{"rm"}, 2, "", "usage: berthlight rm"},
		{[]string{"rm", "nothing"}, 1, "", `no route named "nothing"`},
		{[]string{"ls"}, 0, "No routes", ""},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		code := Run(tt.args, &out, &errOut)
		if code != tt.code {
			t.Errorf("Run(%q) = %d, want %d (stderr: %s)", tt.args, code, tt.code, errOut.String())
		}
		if !strings.Contains(out.String(), tt.inStdout) {
			t.Errorf("Run(%q) stdout %q, want it to contain %q", tt.args, out.String(), tt.inStdout)
		}
		if !strings.Contains(errOut.String(), tt.inStderr) {
			t.Errorf("Run(%q) stderr %q, want it to contain %q", tt.args, errOut.String(), tt.inStderr)
		}
	}
}

func TestLsShowsURLs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BERTHLIGHT_STATE_DIR", dir)
	t.Setenv("BERTHLIGHT_PORT", "9999")
	t.Setenv("BERTHLIGHT_BASE", "dev.example.com,localhost")
	tab := &route.Table{}
	tab.Put(route.Route{Name: "shop", Target: "web", Port: 8080})
	if err := route.Save(dir, tab); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := Run([]string{"ls"}, &out, &out); code != 0 {
		t.Fatalf("ls: %d %s", code, out.String())
	}
	for _, want := range []string{"http://shop.dev.example.com:9999", "web:8080", "bl-shop"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("ls output %q lacks %q", out.String(), want)
		}
	}
}

// TestAddOwnership: add refuses a name or network another route holds
// (PRD 0001, R4). Both checks come before the runtime is touched.
func TestAddOwnership(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BERTHLIGHT_STATE_DIR", dir)
	tab := &route.Table{}
	tab.Put(route.Route{Name: "shop", Target: "bl-webshop", Port: 8080, NetworkName: "bl-webshop"})
	if err := route.Save(dir, tab); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"add", "shop", "8080"}, `route "shop" already goes to bl-webshop:8080 on network bl-webshop; to take the name over, add --replace`},
		{[]string{"add", "shop", "8080", "--target", "web", "--network", "bl-webshop"}, "already goes to"},
		{[]string{"add", "gallery", "8089", "--network", "bl-webshop"}, `network bl-webshop already belongs to route "shop"`},
		{[]string{"add", "gallery", "8089", "--target", "bl-webshop"}, `target "bl-webshop" is already route "shop"'s`},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		if code := Run(tt.args, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), tt.want) {
			t.Errorf("Run(%q) = %d, stderr %q, want %q", tt.args, code, errOut.String(), tt.want)
		}
	}
}

func TestDevcontainer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".devcontainer", "devcontainer.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "{\n  // dev image\n  \"image\": \"node:22\",\n  \"appPort\": 8080\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"devcontainer", "-C", dir, "--dry-run", "shop", "8080"}, &out, &errOut); code != 0 {
		t.Fatalf("dry run: %d %s", code, errOut.String())
	}
	for _, want := range []string{"--- a/", "+++ b/", `-  "appPort": 8080`, `+  "runArgs": [`, `"initializeCommand"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry-run diff lacks %q:\n%s", want, out.String())
		}
	}
	if b, _ := os.ReadFile(path); string(b) != src {
		t.Errorf("dry run wrote the file:\n%s", b)
	}

	out.Reset()
	if code := Run([]string{"devcontainer", "-C", dir, "shop", "8080"}, &out, &errOut); code != 0 {
		t.Fatalf("write: %d %s", code, errOut.String())
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "// dev image") || strings.Contains(string(b), "appPort") || !strings.Contains(string(b), "--network-alias=bl-${localWorkspaceFolderBasename}") {
		t.Errorf("written file:\n%s", b)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 && runtime.GOOS != "windows" {
		t.Errorf("mode changed to %v", info.Mode().Perm())
	}

	out.Reset()
	errOut.Reset()
	if code := Run([]string{"devcontainer", "-C", dir, "--dry-run", "shop", "8080"}, &out, &errOut); code != 0 || out.Len() != 0 || !strings.Contains(errOut.String(), "already wired") {
		t.Errorf("second dry run: %d %q %q", code, out.String(), errOut.String())
	}
}

func TestConfigErrors(t *testing.T) {
	t.Setenv("BERTHLIGHT_PORT", "abc")
	if _, err := LoadConfig(); err == nil {
		t.Error("BERTHLIGHT_PORT=abc accepted")
	}
	t.Setenv("BERTHLIGHT_PORT", "")
	t.Setenv("BERTHLIGHT_BASE", " , ")
	if _, err := LoadConfig(); err == nil {
		t.Error("empty BERTHLIGHT_BASE accepted")
	}
}

func TestParseInterspersed(t *testing.T) {
	fs := newFlags("t", &bytes.Buffer{})
	target := fs.String("target", "", "")
	pos, err := parseInterspersed(fs, []string{"shop", "8080", "--target", "web"})
	if err != nil || len(pos) != 2 || pos[0] != "shop" || pos[1] != "8080" || *target != "web" {
		t.Errorf("got %q, %q, %v", pos, *target, err)
	}
}

func TestSkillInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows
	t.Setenv("PATH", t.TempDir())
	var out, errOut bytes.Buffer
	if code := Run([]string{"skill", "install"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "no coding agent found") {
		t.Errorf("nothing detected: %d %q", code, errOut.String())
	}
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	out.Reset()
	if code := Run([]string{"skill", "install"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "installed "+filepath.Join("~", ".claude", "skills", "berthlight", "SKILL.md")+" (Claude Code)") {
		t.Errorf("detected install: %d %q %q", code, out.String(), errOut.String())
	}
	proj := t.TempDir()
	out.Reset()
	if code := Run([]string{"skill", "install", "--agent", "all", "--project", "-C", proj}, &out, &errOut); code != 0 {
		t.Fatalf("project install: %d %q", code, errOut.String())
	}
	for _, want := range []string{"(Claude Code)", "(Codex, OpenCode, pi)", "Commit the skill"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("project install output lacks %q: %q", want, out.String())
		}
	}
	for _, p := range []string{".claude/skills/berthlight/SKILL.md", ".agents/skills/berthlight/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(proj, filepath.FromSlash(p))); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	out.Reset()
	if code := Run([]string{"skill", "install", "--agent", "all", "--project", "-C", proj}, &out, &errOut); code != 0 || strings.Count(out.String(), "unchanged") != 2 {
		t.Errorf("second install: %q", out.String())
	}
	out.Reset()
	if code := Run([]string{"skill", "uninstall", "--agent", "all", "--project", "-C", proj}, &out, &errOut); code != 0 || strings.Count(out.String(), "removed") != 2 {
		t.Errorf("uninstall: %q %q", out.String(), errOut.String())
	}
}

// TestPlainWithoutTerminal: piped or redirected output has no escape
// sequences (PRD 0001, R12).
func TestPlainWithoutTerminal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BERTHLIGHT_STATE_DIR", dir)
	tab := &route.Table{}
	tab.Put(route.Route{Name: "shop", Target: "web", Port: 8080})
	if err := route.Save(dir, tab); err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, args := range [][]string{{"ls"}, {"ls", "--json"}, {"url"}, {"skill", "show"}, {"help"}} {
		f.Truncate(0)
		f.Seek(0, 0)
		Run(args, f, f)
		b, _ := os.ReadFile(f.Name())
		if bytes.ContainsRune(b, 0x1b) {
			t.Errorf("berthlight %v wrote escape sequences to a file: %q", args, b)
		}
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"url", "--copy"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "--copy needs a terminal") {
		t.Errorf("url --copy without terminal: %d %q", code, errOut.String())
	}
}

func TestLsJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BERTHLIGHT_STATE_DIR", dir)
	var out bytes.Buffer
	if code := Run([]string{"ls", "--json"}, &out, &out); code != 0 || strings.TrimSpace(out.String()) != "{\n  \"routes\": []\n}" {
		t.Errorf("empty: %d %q", code, out.String())
	}
	tab := &route.Table{}
	tab.Put(route.Route{Name: "shop", Target: "bl-webshop", Port: 8080, NetworkName: "bl-webshop"})
	route.Save(dir, tab)
	out.Reset()
	Run([]string{"ls", "--json"}, &out, &out)
	var got struct {
		Routes []map[string]any `json:"routes"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"name": "shop", "url": "http://shop.localhost:7480", "target": "bl-webshop", "port": 8080.0, "network": "bl-webshop"}
	if len(got.Routes) != 1 || !reflect.DeepEqual(got.Routes[0], want) {
		t.Errorf("ls --json: %v", got.Routes)
	}
}

func TestStatusJSON(t *testing.T) {
	c := Config{Port: 7480, ProxyName: "berthlight", Bases: []string{"localhost"}}
	rep := status.Report{Routes: []status.RouteStatus{
		{Route: route.Route{Name: "shop", Target: "shop", Port: 8080}, URL: "http://shop.localhost:7480", OK: true, Detail: "HTTP 200"},
		{Route: route.Route{Name: "gallery", Target: "gallery", Port: 8089}, URL: "http://gallery.localhost:7480", Detail: "nothing listens"},
	}}
	var out bytes.Buffer
	writeJSON(&out, statusJSON(c, rep))
	for _, want := range []string{`"ok": false`, `"proxy": {`, `"name": "berthlight"`, `"detail": "nothing listens"`, `"ok": true`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status --json lacks %s:\n%s", want, out.String())
		}
	}
}

// TestWatch: notifications on changes only, a summary title, progress
// while something is down, and the title restored at the end.
func TestWatch(t *testing.T) {
	c := Config{Port: 7480, ProxyName: "berthlight", Bases: []string{"localhost"}}
	r := func(name string, ok bool) status.RouteStatus {
		rs := status.RouteStatus{Route: route.Route{Name: name, Target: name, Port: 8080}, URL: "http://" + name + ".localhost:7480", OK: ok, Detail: "HTTP 200"}
		if !ok {
			rs.Detail = "nothing listens on port 8080"
		}
		return rs
	}
	reports := []status.Report{
		{Routes: []status.RouteStatus{r("shop", true), r("gallery", false)}},
		{Routes: []status.RouteStatus{r("shop", true), r("gallery", false)}},
		{Routes: []status.RouteStatus{r("shop", true), r("gallery", true)}},
		{Proxy: "proxy container berthlight does not exist", Routes: []status.RouteStatus{r("shop", false), r("gallery", false)}},
	}
	run := func(term func(w *bytes.Buffer) tui.Term) string {
		ctx, cancel := context.WithCancel(context.Background())
		ticks := make(chan time.Time)
		i := 0
		// One check per report, then one more that stops the watch.
		check := func(context.Context) (status.Report, error) {
			if i == len(reports) {
				cancel()
				return reports[i-1], nil
			}
			i++
			return reports[i-1], nil
		}
		go func() {
			for range len(reports) {
				select {
				case ticks <- time.Time{}:
				case <-ctx.Done():
					return
				}
			}
		}()
		var buf bytes.Buffer
		clock := func() time.Time { return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC) }
		if err := watchWith(ctx, ticks, check, c, &buf, term(&buf), clock); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	got := run(func(w *bytes.Buffer) tui.Term { return tui.ForcedTerm(w, tui.Ghostty, "xterm-ghostty") })
	notes := regexp.MustCompile(`\x1b\]9;berthlight: ([^\x07]*)\x07`).FindAllStringSubmatch(got, -1)
	var said []string
	for _, n := range notes {
		said = append(said, n[1])
	}
	want := []string{"gallery.localhost is up", "proxy down: proxy container berthlight does not exist", "shop.localhost is down: nothing listens on port 8080", "gallery.localhost is down: nothing listens on port 8080"}
	if !reflect.DeepEqual(said, want) {
		t.Errorf("notifications:\n got %q\nwant %q", said, want)
	}
	for _, seq := range []string{"\x1b[22;0t", "\x1b]2;berthlight · 1/2 ok\x07", "\x1b]2;berthlight · 2/2 ok\x07", "\x1b]2;berthlight · proxy down\x07",
		"\x1b]9;4;3;0\x07", "\x1b]9;4;0;0\x07", "\x1b]9;4;2;100\x07", "\x1b[23;0t", "\x1b]8;;http://shop.localhost:7480\x1b\\"} {
		if !strings.Contains(got, seq) {
			t.Errorf("watch output lacks %q", seq)
		}
	}
	if !strings.HasSuffix(got, "\x1b]9;4;0;0\x07\x1b[23;0t") {
		t.Errorf("watch does not end by clearing progress and restoring the title: %q", got[max(0, len(got)-40):])
	}

	plain := run(func(w *bytes.Buffer) tui.Term { return tui.NewTerm(w) })
	wantPlain := "12:00:00 shop.localhost is up\n12:00:00 gallery.localhost is down: nothing listens on port 8080\n" +
		"12:00:00 gallery.localhost is up\n" +
		"12:00:00 proxy down: proxy container berthlight does not exist\n12:00:00 shop.localhost is down: nothing listens on port 8080\n12:00:00 gallery.localhost is down: nothing listens on port 8080\n"
	if plain != wantPlain {
		t.Errorf("plain watch:\n got %q\nwant %q", plain, wantPlain)
	}
}

func TestWantsHeader(t *testing.T) {
	for args, want := range map[string]bool{
		"":               true,
		"ls":             true,
		"status --watch": true,
		"up":             true,
		"demo":           true,
		"skill install":  true,
		"url":            false,
		"url --copy":     false,
		"ls --json":      false,
		"status --json":  false,
		"proxy":          false,
		"skill show":     false,
	} {
		if got := wantsHeader(strings.Fields(args)); got != want {
			t.Errorf("wantsHeader(%q) = %v, want %v", args, got, want)
		}
	}
}

func TestNoHeaderFlag(t *testing.T) {
	t.Setenv("BERTHLIGHT_STATE_DIR", t.TempDir())
	for _, args := range [][]string{{"--no-header", "ls"}, {"ls", "--no-header"}, {"-no-header", "ls"}} {
		var out, errOut bytes.Buffer
		if code := Run(args, &out, &errOut); code != 0 || !strings.Contains(out.String(), "No routes") {
			t.Errorf("Run(%q) = %d, %q %q", args, code, out.String(), errOut.String())
		}
	}
	got, found := stripFlag([]string{"status", "--no-header", "--json"}, "no-header")
	if !found || strings.Join(got, " ") != "status --json" {
		t.Errorf("stripFlag: %q %v", got, found)
	}
	if _, found := stripFlag([]string{"ls"}, "no-header"); found {
		t.Error("stripFlag found a flag that is not there")
	}
}

// TestReportFits: with a width, --watch cuts every line to fit, so a long
// message never wraps and throws the redraw off.
func TestReportFits(t *testing.T) {
	c := Config{Port: 7480, ProxyName: "berthlight", Bases: []string{"localhost"}}
	rep := status.Report{Routes: []status.RouteStatus{
		{Route: route.Route{Name: "api-rate-limits", Target: "x", Port: 8080}, URL: "http://api-rate-limits.localhost:7480",
			Detail: `no container answers to "api-rate-limits" on network bl-api-rate-limits; is the devcontainer running, with --network bl-api-rate-limits --network-alias api-rate-limits?`},
	}}
	var b strings.Builder
	writeReport(&b, tui.Term{}, c, rep, 80)
	for _, l := range strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n") {
		if n := len([]rune(l)); n > 80 {
			t.Errorf("line of %d characters: %q", n, l)
		}
	}
	if !strings.Contains(b.String(), "…") {
		t.Error("the long message was not cut")
	}
	b.Reset()
	writeReport(&b, tui.Term{}, c, rep, 0)
	if strings.Contains(b.String(), "…") {
		t.Error("cut without a width")
	}
	if fit("abc", 2) != "a…" || fit("abc", 3) != "abc" || fit("abc", 0) != "abc" {
		t.Error("fit")
	}
}
