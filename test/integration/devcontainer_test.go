//go:build integration

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"stormlantern.nl/berthlight/internal/cli"
)

// TestRealDevcontainer runs the Dev Container CLI on a project wired by
// `berthlight devcontainer`, in two worktrees (PRD 0001, R5 and R11). The
// initializeCommand runs the berthlight binary from PATH, as on a user's
// machine.
//
// It needs `devcontainer` on PATH, and skips without it unless
// BERTHLIGHT_REQUIRE_DEVCONTAINER is set, as in CI.
func TestRealDevcontainer(t *testing.T) {
	dcBin, err := exec.LookPath("devcontainer")
	if err != nil {
		if os.Getenv("BERTHLIGHT_REQUIRE_DEVCONTAINER") != "" {
			t.Fatal("devcontainer CLI not found; install it with: npm install -g @devcontainers/cli")
		}
		t.Skip("devcontainer CLI not found")
	}
	e := setup(t)

	// The initializeCommand calls `berthlight`; put this build first on
	// PATH, or the binary $BERTHLIGHT_BIN names.
	bin := t.TempDir()
	if prebuilt := os.Getenv("BERTHLIGHT_BIN"); prebuilt != "" {
		b, err := os.ReadFile(prebuilt)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "berthlight"), b, 0o755); err != nil {
			t.Fatal(err)
		}
	} else {
		build := exec.Command("go", "build", "-o", filepath.Join(bin, "berthlight"), "./cmd/berthlight")
		build.Dir = filepath.Join("..", "..")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("go build: %v\n%s", err, out)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := t.TempDir()
	mainDir := filepath.Join(root, e.name("webshop"))
	if err := os.MkdirAll(filepath.Join(mainDir, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Each container serves the name of its workspace folder, so the test
	// can tell which one answered.
	config := `// A dev server on port 8080, published the old way.
{
  "name": "webshop",
  "image": "` + backendImage + `",
  "appPort": [8080],
  "postStartCommand": "mkdir -p /www && basename $PWD > /www/index.html && httpd -p 8080 -h /www"
}
`
	if err := os.WriteFile(filepath.Join(mainDir, ".devcontainer", "devcontainer.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", mainDir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v %s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q")

	project := e.name("shop")
	var out bytes.Buffer
	if code := cli.Run([]string{"devcontainer", "-C", mainDir, project, "8080"}, &out, &out); code != 0 {
		t.Fatalf("berthlight devcontainer: %d\n%s", code, out.String())
	}
	git("add", "-A")
	git("commit", "-q", "-m", "devcontainer")
	fix := mainDir + "-fix-login"
	git("worktree", "add", "-q", fix)

	// Remove the devcontainers before the networks they are on.
	t.Cleanup(func() {
		for _, dir := range []string{mainDir, fix} {
			ids, _ := e.run.Run("ps", "-aq", "--filter", "label=devcontainer.local_folder="+dir)
			for _, id := range strings.Fields(ids) {
				e.run.Run("rm", "-f", id)
			}
		}
	})
	for _, dir := range []string{mainDir, fix} {
		cmd := exec.Command(dcBin, "up", "--workspace-folder", dir, "--docker-path", e.run.Bin)
		var log bytes.Buffer
		cmd.Stdout, cmd.Stderr = &log, &log
		if err := cmd.Run(); err != nil || !strings.Contains(log.String(), `"outcome":"success"`) {
			t.Fatalf("devcontainer up %s: %v\n%s", filepath.Base(dir), err, log.String())
		}
	}

	// Each worktree's name reaches its own container, repeatedly, though
	// the proxy is on both networks.
	for i := 0; i < 5; i++ {
		e.waitBody(project+".localhost", filepath.Base(mainDir), 15*time.Second)
		e.waitBody(project+"-fix-login.localhost", filepath.Base(fix), 15*time.Second)
	}
	e.waitStatus("ok, HTTP 200", 0)
	if _, s := e.status(); strings.Count(s, "ok, HTTP 200") != 2 {
		t.Errorf("status:\n%s", s)
	}
}
