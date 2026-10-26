package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"stormlantern.nl/berthlight/internal/route"
	rt "stormlantern.nl/berthlight/internal/runtime"
)

// TestDevcontainerCompose: on a Compose devcontainer, --dry-run shows
// every file devcontainer touches, the new .gitignore included; writing
// changes devcontainer.json and creates the .gitignore; a second run
// changes nothing (PRD 0041, C1 and C2).
func TestDevcontainerCompose(t *testing.T) {
	dir := t.TempDir()
	dc := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dc, 0o755)
	path := filepath.Join(dc, "devcontainer.json")
	src := "{\n  // app and db\n  \"dockerComposeFile\": \"compose.yml\",\n  \"service\": \"app\",\n  \"workspaceFolder\": \"/workspace\"\n}\n"
	os.WriteFile(path, []byte(src), 0o644)

	var out, errOut bytes.Buffer
	if code := Run([]string{"devcontainer", "-C", dir, "--dry-run", "api", "3000"}, &out, &errOut); code != 0 {
		t.Fatalf("dry run: %d %s", code, errOut.String())
	}
	for _, want := range []string{
		`+  "dockerComposeFile": ["compose.yml", "berthlight.compose.yml"],`,
		`"--compose-service", "app", "--compose-override", ".devcontainer/berthlight.compose.yml"`,
		"--- /dev/null\n+++ b/" + filepath.ToSlash(filepath.Join(dc, ".gitignore")),
		"+berthlight.compose.yml",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "runArgs") {
		t.Errorf("dry run adds runArgs:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "berthlight.compose.yml for its worktree") {
		t.Errorf("dry run does not mention the override: %q", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dc, ".gitignore")); err == nil {
		t.Error("dry run wrote the .gitignore")
	}

	out.Reset()
	if code := Run([]string{"devcontainer", "-C", dir, "api", "3000"}, &out, &errOut); code != 0 {
		t.Fatalf("write: %d %s", code, errOut.String())
	}
	if gi, _ := os.ReadFile(filepath.Join(dc, ".gitignore")); !strings.Contains(string(gi), "\nberthlight.compose.yml\n") {
		t.Errorf(".gitignore: %q", gi)
	}
	if strings.Contains(out.String(), "--mount-git-worktree-common-dir") {
		t.Errorf("advises the CLI's git mount, which Compose ignores:\n%s", out.String())
	}

	out.Reset()
	errOut.Reset()
	if code := Run([]string{"devcontainer", "-C", dir, "--dry-run", "api", "3000"}, &out, &errOut); code != 0 || out.Len() != 0 || !strings.Contains(errOut.String(), "already wired") {
		t.Errorf("second dry run: %d %q %q", code, out.String(), errOut.String())
	}
}

// TestComposeOverrideWithoutCompose: when Compose cannot read the
// project's files, add still writes an override that joins the network,
// says what it leaves out, and in a linked worktree with an absolute
// gitdir mounts the repository's git directory at its own path.
func TestComposeOverrideWithoutCompose(t *testing.T) {
	_, fix := gitWorktrees(t)
	dc := filepath.Join(fix, ".devcontainer")
	os.MkdirAll(dc, 0o755)
	os.WriteFile(filepath.Join(dc, "devcontainer.json"), []byte(`{"dockerComposeFile": ["no-such-compose.yml", "berthlight.compose.yml"], "service": "app"}`), 0o644)
	o := addOptions{dir: fix, composeService: "app", composeOverride: ".devcontainer/berthlight.compose.yml"}
	r := route.Route{Name: "webshop-fix-login", Target: "bl-webshop-fix-login", Port: 8080, NetworkName: "bl-webshop-fix-login"}
	var errOut bytes.Buffer
	if err := writeComposeOverride(o, r, rt.Runtime{Bin: "berthlight-no-such-runtime"}, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "cannot read the Compose configuration") {
		t.Errorf("stderr %q", errOut.String())
	}
	b, err := os.ReadFile(filepath.Join(dc, "berthlight.compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	wants := []string{`"bl-webshop-fix-login":` + "\n        aliases: [\"bl-webshop-fix-login\"]", `"default": {}`, "external: true"}
	if runtime.GOOS != "windows" { // an absolute Windows path has no place in a Linux container
		wants = append(wants, `/webshop/.git", "target": "/`)
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("override lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ports") {
		t.Errorf("override touches ports without Compose:\n%s", got)
	}
}

func TestAddComposeFlagsTogether(t *testing.T) {
	t.Setenv("BERTHLIGHT_STATE_DIR", t.TempDir())
	var out, errOut bytes.Buffer
	if code := Run([]string{"add", "--compose-service", "app", "shop", "8080"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "go together") {
		t.Errorf("%d %q", code, errOut.String())
	}
}
