package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tailscale/hujson"
	"stormlantern.nl/berthlight/internal/acme"
	"stormlantern.nl/berthlight/internal/route"
)

// gitWorktrees makes a repository in <tmp>/webshop with a second worktree
// in <tmp>/webshop-fix-login, and returns both directories.
func gitWorktrees(t *testing.T) (main, fix string) {
	t.Helper()
	root := t.TempDir()
	main, fix = filepath.Join(root, "webshop"), filepath.Join(root, "webshop-fix-login")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", main, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.MkdirAll(main, 0o755)
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "init")
	git("worktree", "add", "-q", fix)
	return main, fix
}

// initCommands returns berthlight's initializeCommand from each golden
// devcontainer.json: the command lines committed in users' projects.
func initCommands(t *testing.T) map[string][]string {
	t.Helper()
	files, _ := filepath.Glob("../devcontainer/testdata/*.golden.jsonc")
	if len(files) == 0 {
		t.Fatal("no golden files")
	}
	out := map[string][]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if b, err = hujson.Standardize(b); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var dc struct {
			InitializeCommand json.RawMessage `json:"initializeCommand"`
		}
		if err := json.Unmarshal(b, &dc); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var words []string
		if err := json.Unmarshal(dc.InitializeCommand, &words); err != nil {
			var byKey map[string]json.RawMessage
			if err := json.Unmarshal(dc.InitializeCommand, &byKey); err != nil {
				t.Fatalf("%s: initializeCommand: %v", f, err)
			}
			if err := json.Unmarshal(byKey["berthlight"], &words); err != nil {
				t.Fatalf("%s: initializeCommand.berthlight: %v", f, err)
			}
		}
		if len(words) < 2 || words[0] != "berthlight" || words[1] != "add" {
			t.Fatalf("%s: initializeCommand %q", f, words)
		}
		out[filepath.Base(f)] = words[1:]
	}
	return out
}

// TestInitializeCommands runs the initializeCommand each golden file holds,
// as the devcontainer CLI does with ${localWorkspaceFolder} filled in, and
// checks that every flag arrives: -C and --project make the name, --target
// and --network the route's alias and network. Each case ends at a route
// the state already has, before any container runtime is needed.
func TestInitializeCommands(t *testing.T) {
	main, fix := gitWorktrees(t)
	expand := func(words []string, dir string) []string {
		r := strings.NewReplacer("${localWorkspaceFolderBasename}", filepath.Base(dir), "${localWorkspaceFolder}", dir)
		out := make([]string, len(words))
		for i, w := range words {
			out[i] = r.Replace(w)
		}
		return out
	}
	cases := []struct {
		dir   string
		holds route.Route
		want  string
	}{
		// The name: --project shop, from the worktree -C names.
		{main, route.Route{Name: "shop", Target: "web", Port: 9999}, `route "shop" already goes to web:9999 on network bl-shop`},
		{fix, route.Route{Name: "shop-fix-login", Target: "web", Port: 9999}, `route "shop-fix-login" already goes to web:9999`},
		// --network bl-<folder>.
		{main, route.Route{Name: "gallery", Target: "gallery", Port: 8089, NetworkName: "bl-webshop"}, `network bl-webshop already belongs to route "gallery"`},
		// --target bl-<folder>.
		{fix, route.Route{Name: "gallery", Target: "bl-webshop-fix-login", Port: 8089}, `target "bl-webshop-fix-login" is already route "gallery"'s`},
	}
	for file, words := range initCommands(t) {
		for _, tc := range cases {
			state := t.TempDir()
			t.Setenv("BERTHLIGHT_STATE_DIR", state)
			tab := &route.Table{}
			tab.Put(tc.holds)
			if err := route.Save(state, tab); err != nil {
				t.Fatal(err)
			}
			args := expand(words, tc.dir)
			var out, errOut bytes.Buffer
			if code := Run(args, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), tc.want) {
				t.Errorf("%s: berthlight %q = %d, stderr %q; want %q", file, args, code, errOut.String(), tc.want)
			}
		}
	}
}

// TestFlagForms: the forms Go's flag package accepted keep working, next
// to cobra's own.
func TestFlagForms(t *testing.T) {
	main, _ := gitWorktrees(t)
	t.Setenv("BERTHLIGHT_STATE_DIR", t.TempDir())
	tests := []struct {
		args     []string
		code     int
		inStdout string
		inStderr string
	}{
		{[]string{"ls", "-json"}, 0, `"routes": []`, ""},
		{[]string{"ls", "--json=true"}, 0, `"routes": []`, ""},
		{[]string{"ls", "-json=true"}, 0, `"routes": []`, ""},
		{[]string{"status", "-json", "-watch"}, 2, "", "--json and --watch do not go together"},
		{[]string{"url", "-C", main}, 0, "http://webshop.localhost:7480", ""},
		{[]string{"url", "-C=" + main}, 0, "http://webshop.localhost:7480", ""},
		{[]string{"url", "--C", main}, 0, "http://webshop.localhost:7480", ""},
		{[]string{"url", "--dir", main}, 0, "http://webshop.localhost:7480", ""},
		{[]string{"url", "-dir", main}, 0, "http://webshop.localhost:7480", ""},
		{[]string{"url", "-C", main, "-project", "shop", "-port", "9000"}, 0, "http://shop.localhost:9000", ""},
		{[]string{"url", "-C", main, "-base=dev.example.com"}, 0, "http://webshop.dev.example.com:7480", ""},
		{[]string{"devcontainer", "-dry-run", "-C", t.TempDir(), "shop", "8080"}, 1, "", "no devcontainer.json"},
		{[]string{"ls", "--", "-json"}, 0, "No routes", ""},
		{[]string{"ls", "-bogus"}, 2, "", "berthlight: unknown shorthand flag: 'b' in -bogus"},
		{[]string{"ls", "--bogus"}, 2, "", "berthlight: unknown flag: --bogus"},
		{[]string{"url", "--port", "x"}, 2, "", `invalid argument "x" for "--port"`},
		{[]string{"-help"}, 0, "Usage: berthlight", ""},
		{[]string{"-h"}, 0, "Usage: berthlight", ""},
		{[]string{"--version"}, 0, "berthlight ", ""},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		code := Run(tt.args, &out, &errOut)
		if code != tt.code || !strings.Contains(out.String(), tt.inStdout) || !strings.Contains(errOut.String(), tt.inStderr) {
			t.Errorf("Run(%q) = %d, stdout %q, stderr %q; want %d, %q, %q", tt.args, code, out.String(), errOut.String(), tt.code, tt.inStdout, tt.inStderr)
		}
	}
}

// TestNoHeaderAnywhere: --no-header goes before the command, after it,
// between its arguments and at the end.
func TestNoHeaderAnywhere(t *testing.T) {
	main, _ := gitWorktrees(t)
	t.Setenv("BERTHLIGHT_STATE_DIR", t.TempDir())
	for _, args := range [][]string{
		{"--no-header", "url", "-C", main},
		{"url", "--no-header", "-C", main},
		{"url", "-C", main, "--no-header"},
		{"-no-header", "url", "-C", main},
		{"url", "-C", main, "-no-header"},
	} {
		var out, errOut bytes.Buffer
		if code := Run(args, &out, &errOut); code != 0 || strings.TrimSpace(out.String()) != "http://webshop.localhost:7480" {
			t.Errorf("Run(%q) = %d, %q %q", args, code, out.String(), errOut.String())
		}
	}
}

// fakeHeader records what Run does with the header.
type fakeHeader struct {
	calls []string
}

func (h *fakeHeader) Print(text string) {
	h.calls = append(h.calls, "print")
	if !strings.Contains(text, "Usage: berthlight") {
		h.calls = append(h.calls, "print without the overview")
	}
}
func (h *fakeHeader) Play(context.Context)       { h.calls = append(h.calls, "play") }
func (h *fakeHeader) Wrap(w io.Writer) io.Writer { h.calls = append(h.calls, "wrap"); return w }
func (h *fakeHeader) Stop()                      { h.calls = append(h.calls, "stop") }

// TestHeaderRules: bare berthlight and demo play the scene to its end,
// bare with the overview under it; other commands have their output stop
// it (Wrap) and stop it at the end; the commands whose output is read by
// scripts, completion and help get none.
func TestHeaderRules(t *testing.T) {
	t.Setenv("BERTHLIGHT_STATE_DIR", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var h *fakeHeader
	defer func(f func(io.Writer, string) header) { startHeader = f }(startHeader)
	startHeader = func(io.Writer, string) header {
		h = &fakeHeader{}
		return h
	}
	tests := []struct {
		args []string
		want string // the header's calls; "none" when no header starts
	}{
		{nil, "print play"},
		{[]string{"demo"}, "play"},
		{[]string{"ls"}, "wrap wrap stop"},
		{[]string{"version"}, "wrap wrap stop"},
		{[]string{"rm", "nothing"}, "wrap wrap stop"},
		{[]string{"skill"}, "wrap wrap stop"},
		{[]string{"url"}, "none"},
		{[]string{"proxy", "--listen", "bad"}, "none"},
		{[]string{"skill", "show"}, "none"},
		{[]string{"ls", "--json"}, "none"},
		{[]string{"ls", "-json"}, "none"},
		{[]string{"completion", "bash"}, "none"},
		{[]string{"__complete", "rm", ""}, "none"},
		{[]string{"__completeNoDesc", "rm", ""}, "none"},
		{[]string{"help"}, "none"},
		{[]string{"help", "add"}, "none"},
		{[]string{"--help"}, "none"},
		{[]string{"-h"}, "none"},
		{[]string{"add", "--help"}, "none"},
		{[]string{"--no-header", "ls"}, "none"},
		{[]string{"--no-header"}, "none"},
		{[]string{"--no-header", "demo"}, "none"},
	}
	for _, tt := range tests {
		h = nil
		var out, errOut bytes.Buffer
		Run(tt.args, &out, &errOut)
		got := "none"
		if h != nil {
			got = strings.Join(h.calls, " ")
		}
		if got != tt.want {
			t.Errorf("berthlight %q: header %q, want %q", tt.args, got, tt.want)
		}
	}
	// Without a terminal: no scene, the overview as a usage error, and a
	// word from demo.
	startHeader = func(io.Writer, string) header { return nil }
	var out, errOut bytes.Buffer
	if code := Run(nil, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "Usage: berthlight") {
		t.Errorf("bare, without a terminal: %d %q", code, errOut.String())
	}
	out.Reset()
	if code := Run([]string{"demo"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "plays on a terminal") {
		t.Errorf("demo, without a terminal: %d %q", code, out.String())
	}
}

// TestExitCodes: 0, 1 on failure and on problems status found (without a
// message of its own), 2 on usage errors.
func TestExitCodes(t *testing.T) {
	for _, tt := range []struct {
		err  error
		code int
		msg  string
	}{
		{nil, 0, ""},
		{errProblems, 1, ""},
		{io.ErrUnexpectedEOF, 1, "berthlight: unexpected EOF\n"},
		{errUsage, 2, ""},
		{&usageError{msg: "nope"}, 2, "berthlight: nope\n"},
	} {
		root := &cobra.Command{Use: "berthlight", SilenceErrors: true, SilenceUsage: true,
			RunE: func(*cobra.Command, []string) error { return tt.err }}
		root.SetArgs([]string{})
		var errOut bytes.Buffer
		if code := execute(root, &errOut); code != tt.code || errOut.String() != tt.msg {
			t.Errorf("%v: %d %q, want %d %q", tt.err, code, errOut.String(), tt.code, tt.msg)
		}
	}
}

// complete runs `berthlight __complete args...` and returns the values
// offered, without descriptions, and the directive line.
func complete(t *testing.T, args ...string) ([]string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := Run(append([]string{"__complete"}, args...), &out, &errOut); code != 0 {
		t.Fatalf("__complete %q: %d %s", args, code, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var values []string
	for _, l := range lines[:len(lines)-1] {
		v, _, _ := strings.Cut(l, "\t")
		values = append(values, v)
	}
	return values, lines[len(lines)-1]
}

func TestCompletion(t *testing.T) {
	state := t.TempDir()
	t.Setenv("BERTHLIGHT_STATE_DIR", state)
	t.Setenv("BERTHLIGHT_BASE", "dev.example.com,localhost")
	tab := &route.Table{}
	tab.Put(route.Route{Name: "shop", Target: "web", Port: 8080})
	tab.Put(route.Route{Name: "shop-fix-login", Target: "bl-webshop-fix-login", Port: 8080})
	tab.Put(route.Route{Name: "gallery", Target: "gallery", Port: 8089})
	if err := route.Save(state, tab); err != nil {
		t.Fatal(err)
	}
	has := func(got []string, want ...string) bool {
		for _, w := range want {
			if !slices.Contains(got, w) {
				return false
			}
		}
		return true
	}
	tests := []struct {
		args      []string
		want      []string
		exact     bool
		directive string
	}{
		{[]string{""}, []string{"add", "rm", "ls", "status", "devcontainer", "up", "down", "url", "cert", "skill", "completion", "demo", "version", "help"}, false, ":4"},
		{[]string{"st"}, []string{"status"}, true, ":4"},
		{[]string{"skill", ""}, []string{"install", "show", "uninstall"}, true, ":4"},
		{[]string{"completion", ""}, []string{"bash", "fish", "nushell", "powershell", "zsh"}, true, ":4"},
		{[]string{"status", "--"}, []string{"--json", "--watch", "--no-header", "--help"}, true, ":4"},
		{[]string{"rm", ""}, []string{"gallery", "shop", "shop-fix-login"}, true, ":4"},
		{[]string{"rm", "shop-"}, []string{"shop-fix-login"}, true, ":4"},
		{[]string{"rm", "shop", ""}, nil, true, ":4"},
		{[]string{"--no-header", "rm", "g"}, []string{"gallery"}, true, ":4"},
		{[]string{"skill", "install", "--agent", ""}, []string{"claude", "codex", "opencode", "pi", "all"}, true, ":4"},
		{[]string{"skill", "uninstall", "--agent", "claude,"}, []string{"claude,codex", "claude,opencode", "claude,pi"}, true, ":4"},
		{[]string{"cert", "--dns", ""}, acme.Providers(), true, ":4"},
		{[]string{"cert", "--server", "letsencrypt"}, []string{"letsencrypt", "letsencrypt-staging"}, true, ":4"},
		{[]string{"cert", "--domain", ""}, []string{"dev.example.com"}, true, ":4"},
		{[]string{"url", "--base", ""}, []string{"dev.example.com", "localhost"}, true, ":4"},
		{[]string{"devcontainer", "--file", ""}, []string{"json", "jsonc"}, true, ":8"},
		{[]string{"devcontainer", "-C", ""}, nil, true, ":16"},
		{[]string{"add", "--dir", ""}, nil, true, ":16"},
		{[]string{"skill", "install", "-C", ""}, nil, true, ":16"},
		{[]string{"status", "-json", "--w"}, []string{"--watch"}, true, ":4"},
	}
	for _, tt := range tests {
		got, directive := complete(t, tt.args...)
		ok := has(got, tt.want...)
		if tt.exact {
			ok = len(got) == len(tt.want) && ok
		}
		if !ok || directive != tt.directive {
			t.Errorf("__complete %q = %q %s, want %q %s", tt.args, got, directive, tt.want, tt.directive)
		}
	}
	if got, _ := complete(t, ""); slices.Contains(got, "proxy") {
		t.Error("the hidden proxy command is offered")
	}
}

// TestCompletionScripts: every shell gets a script, without the header or
// anything else around it.
func TestCompletionScripts(t *testing.T) {
	for shell, want := range map[string]string{
		"bash":       "complete -o default -F __start_berthlight berthlight",
		"zsh":        "#compdef berthlight",
		"fish":       "complete -c berthlight",
		"powershell": "Register-ArgumentCompleter",
		"nushell":    "berthlight __complete",
	} {
		var out, errOut bytes.Buffer
		if code := Run([]string{"completion", shell}, &out, &errOut); code != 0 || !strings.Contains(out.String(), want) || errOut.Len() != 0 {
			t.Errorf("completion %s: %d, stderr %q, lacks %q", shell, code, errOut.String(), want)
		}
		if bytes.ContainsRune(out.Bytes(), 0x1b) {
			t.Errorf("completion %s has escape sequences", shell)
		}
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"completion", "tcsh"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), `no completion for "tcsh"`) {
		t.Errorf("completion tcsh: %d %q", code, errOut.String())
	}
}

// TestHelp: every command a user sees says what it does, explains when to
// use it and shows how; the overview lists every command and every
// BERTHLIGHT_ variable berthlight reads.
func TestHelp(t *testing.T) {
	root := newRoot()
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if !sub.IsAvailableCommand() && sub.Name() != "help" {
				continue
			}
			if sub.Short == "" || sub.Long == "" || sub.Example == "" {
				t.Errorf("%s lacks Short, Long or Example", sub.CommandPath())
			}
			walk(sub)
		}
	}
	walk(root)

	var out bytes.Buffer
	if code := Run([]string{"help"}, &out, &out); code != 0 {
		t.Fatal(code)
	}
	overview := out.String()
	for _, c := range root.Commands() {
		if c.IsAvailableCommand() && !strings.Contains(overview, "\n  "+c.Name()+" ") {
			t.Errorf("the overview does not list %s", c.Name())
		}
	}
	if !strings.Contains(overview, "--no-header") {
		t.Error("the overview lacks --no-header")
	}
	vars := map[string]bool{}
	re := regexp.MustCompile(`BERTHLIGHT_[A-Z_]+`)
	filepath.WalkDir("../..", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() && (d.Name() == "test" || d.Name() == "tools" || strings.HasPrefix(d.Name(), ".")) && path != "../.." {
			return filepath.SkipDir
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			b, _ := os.ReadFile(path)
			for _, v := range re.FindAllString(string(b), -1) {
				vars[v] = true
			}
		}
		return nil
	})
	if len(vars) < 10 {
		t.Fatalf("found only %v in the sources", vars)
	}
	for v := range vars {
		if !strings.Contains(overview, v) {
			t.Errorf("the overview's Environment lacks %s", v)
		}
	}

	out.Reset()
	if code := Run([]string{"status", "--help"}, &out, &out); code != 0 || !strings.Contains(out.String(), "Examples:") || !strings.Contains(out.String(), "--no-header") {
		t.Errorf("status --help: %d %q", code, out.String())
	}
}
