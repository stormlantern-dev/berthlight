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

// TestRealComposeDevcontainer runs the Dev Container CLI on a Compose
// devcontainer wired by `berthlight devcontainer`, in two worktrees (PRD
// 0041). Each worktree's stack has the dev service, app, and a second
// service, db. It checks:
//
//   - C4: both stacks run side by side, each answering by its own name
//     through the proxy;
//   - C3: db is on no bl- network, app reaches db by its service name, and
//     the proxy reaches app;
//   - C5: app no longer publishes the dev server's port, and still
//     publishes its second port;
//   - C6: in the linked worktree's container, the gitdir resolves;
//   - C2: git status is clean in both worktrees, and each override names
//     its own network.
//
// Compose comes from the runtime: `docker compose`, or `podman compose`
// with Docker Compose v2 as its provider.
func TestRealComposeDevcontainer(t *testing.T) {
	dcBin := devcontainerCLI(t)
	e := setup(t)
	berthlightOnPath(t)

	root := t.TempDir()
	mainDir := filepath.Join(root, e.name("api"))
	dc := filepath.Join(mainDir, ".devcontainer")
	if err := os.MkdirAll(dc, 0o755); err != nil {
		t.Fatal(err)
	}
	// app publishes the dev server's port and a debugger's; both get
	// ephemeral host ports, so worktrees and test runs do not collide.
	composeFile := `services:
  app:
    image: ` + backendImage + `
    command: sleep 3600
    ports:
      - "8080"
      - "9229"
    volumes:
      - ..:/workspace
  db:
    image: ` + backendImage + `
    command: sh -c "mkdir -p /www && echo db-answers > /www/index.html && exec httpd -f -p 5432 -h /www"
`
	config := `// app and its database, from Compose.
{
  "name": "api",
  "dockerComposeFile": "compose.yml",
  "service": "app",
  "workspaceFolder": "/workspace",
  "postStartCommand": "mkdir -p /www && echo ${localWorkspaceFolderBasename} > /www/index.html && httpd -p 8080 -h /www"
}
`
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dc, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("compose.yml", composeFile)
	write("devcontainer.json", config)
	gitIn := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v %s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	gitIn(mainDir, "init", "-q")

	project := e.name("api")
	var out bytes.Buffer
	if code := cli.Run([]string{"devcontainer", "-C", mainDir, project, "8080"}, &out, &out); code != 0 {
		t.Fatalf("berthlight devcontainer: %d\n%s", code, out.String())
	}
	gitIn(mainDir, "add", "-A")
	gitIn(mainDir, "commit", "-q", "-m", "devcontainer")
	feat := mainDir + "-feat"
	relative := gitHasRelativeWorktrees()
	if relative {
		gitIn(mainDir, "worktree", "add", "-q", "--relative-paths", feat)
	} else {
		gitIn(mainDir, "worktree", "add", "-q", feat)
	}
	t.Logf("linked worktree with a relative gitdir: %v", relative)

	// containers returns the IDs of the containers with all these labels.
	containers := func(labels ...string) []string {
		args := []string{"ps", "-aq"}
		for _, l := range labels {
			args = append(args, "--filter", "label="+l)
		}
		ids, _ := e.run.Run(args...)
		return strings.Fields(ids)
	}
	label := func(id, key string) string {
		v, err := e.run.Run("container", "inspect", "--format", `{{index .Config.Labels "`+key+`"}}`, id)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(v)
	}
	// Remove both stacks before the networks they are on. The Compose
	// project name is the CLI's own; the test reads it from the labels.
	t.Cleanup(func() {
		for _, dir := range []string{mainDir, feat} {
			for _, id := range containers("devcontainer.local_folder=" + dir) {
				p := label(id, "com.docker.compose.project")
				for _, sid := range containers("com.docker.compose.project=" + p) {
					e.run.Run("rm", "-f", sid)
				}
				e.run.Run("network", "rm", p+"_default")
			}
		}
	})
	for _, dir := range []string{mainDir, feat} {
		cmd := exec.Command(dcBin, "up", "--workspace-folder", dir, "--docker-path", e.run.Bin)
		var log bytes.Buffer
		cmd.Stdout, cmd.Stderr = &log, &log
		if err := cmd.Run(); err != nil || !strings.Contains(log.String(), `"outcome":"success"`) {
			t.Fatalf("devcontainer up %s: %v\n%s", filepath.Base(dir), err, log.String())
		}
	}

	// C4: each worktree's name reaches its own stack's app.
	for i := 0; i < 3; i++ {
		e.waitBody(project+".localhost", filepath.Base(mainDir), 15*time.Second)
		e.waitBody(project+"-feat.localhost", filepath.Base(feat), 15*time.Second)
	}

	stacks := map[string]bool{}
	for _, dir := range []string{mainDir, feat} {
		base := filepath.Base(dir)
		ids := containers("devcontainer.local_folder="+dir, "com.docker.compose.service=app")
		if len(ids) != 1 {
			t.Fatalf("%s: app containers %q", base, ids)
		}
		app := ids[0]
		p := label(app, "com.docker.compose.project")
		stacks[p] = true
		dbs := containers("com.docker.compose.project="+p, "com.docker.compose.service=db")
		if len(dbs) != 1 {
			t.Fatalf("%s: db containers %q", base, dbs)
		}

		// C3: app is on bl-<folder> and its stack's network; db only on
		// the stack's.
		appNets, err := e.run.ContainerNetworks(app)
		if err != nil {
			t.Fatal(err)
		}
		if !contains(appNets, "bl-"+base) || len(appNets) < 2 {
			t.Errorf("%s: app is on %q, want bl-%s and its default network", base, appNets, base)
		}
		dbNets, err := e.run.ContainerNetworks(dbs[0])
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range dbNets {
			if strings.HasPrefix(n, "bl-") {
				t.Errorf("%s: db is on %s", base, n)
			}
		}
		if got, err := e.run.Run("exec", app, "wget", "-q", "-O-", "http://db:5432/"); err != nil || strings.TrimSpace(got) != "db-answers" {
			t.Errorf("%s: app does not reach db by name: %v %q", base, err, got)
		}

		// C5: 8080 is no longer published; 9229 still is.
		ports, err := e.run.Run("port", app)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(ports, "8080/tcp") || !strings.Contains(ports, "9229/tcp") {
			t.Errorf("%s: app publishes:\n%s\nwant 9229/tcp and not 8080/tcp", base, ports)
		}

		// C2: the override names this worktree's network, and git
		// ignores it.
		ov, err := os.ReadFile(filepath.Join(dir, ".devcontainer", "berthlight.compose.yml"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(ov), `"bl-`+base+`":`) {
			t.Errorf("%s: override:\n%s", base, ov)
		}
		if st := gitIn(dir, "status", "--porcelain"); st != "" {
			t.Errorf("%s: git status:\n%s", base, st)
		}
		t.Logf("%s's override:\n%s", base, ov)
	}
	if len(stacks) != 2 {
		t.Errorf("the worktrees share a Compose project: %v", stacks)
	}

	// C6: git works inside the linked worktree's container: its .git
	// file's gitdir resolves there (busybox has no git, so check the
	// path).
	app := containers("devcontainer.local_folder="+feat, "com.docker.compose.service=app")[0]
	got, err := e.run.Run("exec", "-w", "/workspace", app, "sh", "-c", `test -f "$(sed 's/^gitdir: //' .git)/HEAD" && echo resolves`)
	if err != nil || !strings.Contains(got, "resolves") {
		gitFile, _ := os.ReadFile(filepath.Join(feat, ".git"))
		t.Errorf("gitdir does not resolve in the linked worktree's container: %v %s (.git: %s)", err, got, gitFile)
	}

	e.waitStatus("ok, HTTP 200", 0)
	if _, s := e.status(); strings.Count(s, "ok, HTTP 200") != 2 {
		t.Errorf("status:\n%s", s)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
