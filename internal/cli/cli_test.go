package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"stormlantern.nl/berthlight/internal/route"
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
