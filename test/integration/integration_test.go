//go:build integration

// Integration tests against a real container runtime (PRD 0001, R1–R3).
//
// They need Docker or Podman and a proxy image:
//
//	podman build -t localhost/berthlight:test .
//	BERTHLIGHT_IMAGE=localhost/berthlight:test go test -tags integration ./test/integration
//
// Everything they create is named blt-<random>-… and removed afterwards.
package integration

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"stormlantern.nl/berthlight/internal/cli"
	"stormlantern.nl/berthlight/internal/route"
	rt "stormlantern.nl/berthlight/internal/runtime"
)

const backendImage = "docker.io/library/busybox:1.37"

type env struct {
	t     *testing.T
	run   rt.Runtime
	id    string
	port  int
	proxy string
}

func setup(t *testing.T) *env {
	if os.Getenv("BERTHLIGHT_IMAGE") == "" {
		t.Skip("BERTHLIGHT_IMAGE not set; build the proxy image first")
	}
	run, err := rt.Detect()
	if err != nil {
		t.Skip(err)
	}
	b := make([]byte, 3)
	rand.Read(b)
	e := &env{t: t, run: run, id: "blt-" + hex.EncodeToString(b), port: freePort(t)}
	e.proxy = e.id + "-proxy"
	t.Setenv("BERTHLIGHT_STATE_DIR", t.TempDir())
	t.Setenv("BERTHLIGHT_PORT", strconv.Itoa(e.port))
	t.Setenv("BERTHLIGHT_PROXY_NAME", e.proxy)
	t.Setenv("BERTHLIGHT_BASE", "localhost")
	if _, err := run.Run("pull", "-q", backendImage); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.cleanup)
	return e
}

func (e *env) name(s string) string { return e.id + "-" + s }

func (e *env) cleanup() {
	if e.t.Failed() {
		logs, err := e.run.Logs(e.proxy)
		e.t.Logf("proxy logs (%v):\n%s", err, logs)
		insp, _ := e.run.Run("container", "inspect", "--format", "{{json .NetworkSettings}}", e.proxy)
		e.t.Logf("proxy network settings: %s", insp)
	}
	out, _ := e.run.Run("ps", "-a", "--format", "{{.Names}}")
	for _, n := range strings.Fields(out) {
		if strings.HasPrefix(n, e.id) {
			e.run.Run("rm", "-f", n)
		}
	}
	out, _ = e.run.Run("network", "ls", "--format", "{{.Name}}")
	for _, n := range strings.Fields(out) {
		if strings.HasPrefix(n, e.id) || strings.HasPrefix(n, "bl-"+e.id) {
			e.run.Run("network", "rm", n)
		}
	}
}

func (e *env) cli(args ...string) string {
	e.t.Helper()
	var out, errb bytes.Buffer
	if code := cli.Run(args, &out, &errb); code != 0 {
		e.t.Fatalf("berthlight %s: exit %d\n%s%s", strings.Join(args, " "), code, out.String(), errb.String())
	}
	return out.String()
}

// backend starts a busybox web server answering body, on the route's
// network under the route's name as alias.
func (e *env) backend(routeName, body string) {
	e.t.Helper()
	e.backendOn(routeName, route.NetworkName(routeName), routeName, body)
}

// backendOn starts a busybox web server answering body on port 8080, as
// container name on network under alias. An empty body starts a container
// with nothing listening.
func (e *env) backendOn(name, network, alias, body string) {
	e.t.Helper()
	if err := e.run.EnsureNetwork(network, "route"); err != nil {
		e.t.Fatal(err)
	}
	cmd := fmt.Sprintf("mkdir /www && echo %s > /www/index.html && httpd -f -p 8080 -h /www", body)
	if body == "" {
		cmd = "sleep 600"
	}
	_, err := e.run.Run("run", "-d", "--name", name, "--network", network, "--network-alias", alias, backendImage, "sh", "-c", cmd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body == "" {
		return
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := e.run.Run("exec", name, "wget", "-q", "-O-", "http://127.0.0.1:8080/"); err == nil {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("backend %s did not start", name)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// status runs `berthlight status` and returns its exit code and output.
func (e *env) status() (int, string) {
	var out bytes.Buffer
	code := cli.Run([]string{"status"}, &out, &out)
	return code, out.String()
}

// waitStatus polls status until its output contains want.
func (e *env) waitStatus(want string, code int) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, out := e.status()
		if c == code && strings.Contains(out, want) {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("status did not report %q (exit %d) within 10s; last (exit %d):\n%s", want, code, c, out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestDevcontainerRoute follows what a devcontainer wired by `berthlight
// devcontainer` does (PRD 0001, R5), and what status says at each step (R6).
func TestDevcontainerRoute(t *testing.T) {
	e := setup(t)
	project := e.name("shop")
	// The workspace folder: a git repository whose directory name gives
	// the network, as ${localWorkspaceFolderBasename} does.
	ws := filepath.Join(t.TempDir(), e.name("webshop"))
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", ws).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	network := "bl-" + filepath.Base(ws)
	initCmd := []string{"add", "--project", project, "--target", network, "--network", network, "-C", ws, "8080"}

	// initializeCommand, before the container exists.
	e.cli(initCmd...)
	if !e.run.NetworkExists(network) {
		t.Fatalf("network %s not created", network)
	}
	e.waitStatus(`no container answers to "`+network+`" on network `+network, 1)

	// The container starts with runArgs --network and --network-alias,
	// but its dev server is not up yet.
	e.backendOn(e.name("dc"), network, network, "")
	e.waitStatus("nothing listens on port 8080", 1)
	e.run.Run("rm", "-f", e.name("dc"))

	e.backendOn(e.name("dc"), network, network, "hello-from-devcontainer")
	e.waitBody(project+".localhost", "hello-from-devcontainer", 10*time.Second)
	e.waitStatus("ok, HTTP 200", 0)
	if _, out := e.status(); !strings.Contains(out, "http://"+project+".localhost:"+strconv.Itoa(e.port)) {
		t.Errorf("status lacks the URL:\n%s", out)
	}

	// R4: a second worktree of the project gets its own name, network and
	// alias, and each name reaches its own container, though the proxy is
	// on both networks.
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", ws, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v %s", strings.Join(args, " "), err, out)
		}
	}
	git("commit", "-q", "--allow-empty", "-m", "init")
	wt := ws + "-fix-login"
	git("worktree", "add", "-q", wt)
	network2 := "bl-" + filepath.Base(wt)
	e.cli("add", "--project", project, "--target", network2, "--network", network2, "-C", wt, "8080")
	e.backendOn(e.name("dc2"), network2, network2, "hello-from-fix-login")
	e.waitBody(project+"-fix-login.localhost", "hello-from-fix-login", 10*time.Second)
	for i := 0; i < 10; i++ {
		e.waitBody(project+".localhost", "hello-from-devcontainer", time.Second)
		e.waitBody(project+"-fix-login.localhost", "hello-from-fix-login", time.Second)
	}
	e.run.Run("rm", "-f", e.name("dc2"))
	e.cli("rm", project+"-fix-login")

	// Rebuilding the container runs initializeCommand again: no change.
	e.cli(initCmd...)
	// Another checkout with the same name is refused.
	var out bytes.Buffer
	if code := cli.Run([]string{"add", project, "8080", "--network", "bl-" + e.name("other")}, &out, &out); code != 1 || !strings.Contains(out.String(), "--replace") {
		t.Errorf("add over another checkout's route: %d %s", code, out.String())
	}

	e.cli("down")
	e.waitStatus("does not exist; start it with: berthlight up", 1)
	e.cli("up")
	e.waitStatus("ok, HTTP 200", 0)

	// rm takes the route's own network away.
	e.run.Run("rm", "-f", e.name("dc"))
	e.cli("rm", project)
	if e.run.NetworkExists(network) {
		t.Errorf("network %s kept after rm", network)
	}
}

func (e *env) get(host string) (int, string, error) {
	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(e.port)+"/", nil)
	req.Host = host
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b)), nil
}

// waitBody polls until host answers 200 with body, and returns how long it
// took.
func (e *env) waitBody(host, body string, timeout time.Duration) time.Duration {
	e.t.Helper()
	start := time.Now()
	var last string
	for time.Since(start) < timeout {
		code, got, err := e.get(host)
		if err == nil && code == 200 && got == body {
			return time.Since(start)
		}
		last = fmt.Sprintf("%d %q %v", code, got, err)
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatalf("%s did not answer %q within %s; last: %s", host, body, timeout, last)
	return 0
}

func TestProxy(t *testing.T) {
	e := setup(t)
	a, b := e.name("a"), e.name("b")

	// R1, R3: add a route; the proxy starts, joins the route's network.
	e.cli("add", a, "8080")
	e.backend(a, "hello-from-a")
	e.waitBody(a+".localhost", "hello-from-a", 15*time.Second)

	// R4: the name resolves with no change to /etc/hosts or DNS. curl, like
	// browsers, sends *.localhost to loopback itself.
	if curl, err := exec.LookPath("curl"); err == nil {
		out, err := exec.Command(curl, "-sfS", "--max-time", "5", "http://"+a+".localhost:"+strconv.Itoa(e.port)+"/").CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "hello-from-a" {
			t.Errorf("curl by name: %v %q", err, out)
		}
	} else if os.Getenv("CI") != "" {
		t.Error("curl not found")
	}

	// R3: a route added while the proxy runs serves within 2 seconds.
	e.backend(b, "hello-from-b")
	e.cli("add", b, "8080")
	if took := e.waitBody(b+".localhost:"+strconv.Itoa(e.port), "hello-from-b", 5*time.Second); took > 2*time.Second {
		t.Errorf("new route served after %s, want within 2s", took)
	}

	// The ls output names both.
	if out := e.cli("ls"); !strings.Contains(out, a+".localhost") || !strings.Contains(out, b+".localhost") {
		t.Errorf("ls: %s", out)
	}

	// Unknown names get a 404 that says so.
	if code, body, err := e.get("nope.localhost"); err != nil || code != 404 || !strings.Contains(body, "no route") {
		t.Errorf("unknown host: %d %q %v", code, body, err)
	}

	t.Run("isolation", func(t *testing.T) {
		// R1: A cannot reach B directly...
		if out, err := e.run.Run("exec", a, "wget", "-q", "-T", "2", "-O-", "http://"+b+":8080/"); err == nil {
			t.Errorf("container a reached b directly: %q", out)
		}
		// ...nor through the proxy, which does not listen on route networks.
		ip := e.proxyIP(route.NetworkName(a))
		out, err := e.run.Run("exec", a, "wget", "-q", "-T", "2", "-O-", "--header", "Host: "+b+".localhost", "http://"+net.JoinHostPort(ip, "7480")+"/")
		if err == nil {
			t.Errorf("container a reached b through the proxy at %s: %q", ip, out)
		}
	})

	t.Run("least privilege", func(t *testing.T) { e.checkProxySecurity(t) })

	t.Run("loopback only", func(t *testing.T) {
		addrs, _ := net.InterfaceAddrs()
		tried := 0
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			tried++
			c, err := net.DialTimeout("tcp", net.JoinHostPort(ipn.IP.String(), strconv.Itoa(e.port)), time.Second)
			if err == nil {
				c.Close()
				t.Errorf("proxy port reachable on %s", ipn.IP)
			}
		}
		if tried == 0 {
			t.Skip("no non-loopback interface on this host")
		}
	})

	// R3: rm stops routing, and leaves the other routes alone.
	e.cli("rm", b)
	deadline := time.Now().Add(2 * time.Second)
	for {
		code, body, err := e.get(b + ".localhost")
		if code == 404 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("route %s still served after rm: %d %q %v", b, code, body, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.waitBody(a+".localhost", "hello-from-a", 2*time.Second)

	// A stopped proxy comes back with its routes on `up`.
	e.cli("down")
	e.cli("up")
	e.waitBody(a+".localhost", "hello-from-a", 10*time.Second)
}

func (e *env) proxyIP(network string) string {
	e.t.Helper()
	out, err := e.run.Run("container", "inspect", "--format", "{{json .NetworkSettings.Networks}}", e.proxy)
	if err != nil {
		e.t.Fatal(err)
	}
	var nets map[string]struct{ IPAddress string }
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &nets); err != nil {
		e.t.Fatal(err)
	}
	ip := nets[network].IPAddress
	if ip == "" {
		e.t.Fatalf("proxy has no address on %s: %s", network, out)
	}
	return ip
}

// checkProxySecurity asserts PRD 0001, R2 on the running proxy container.
func (e *env) checkProxySecurity(t *testing.T) {
	out, err := e.run.Run("container", "inspect", e.proxy)
	if err != nil {
		t.Fatal(err)
	}
	var info []struct {
		State  struct{ Pid int }
		Config struct{ User string }
		Mounts []struct {
			Source, Destination string
			RW                  bool
		}
		HostConfig struct {
			ReadonlyRootfs bool
			Privileged     bool
			CapAdd         []string
			PortBindings   map[string][]struct{ HostIp, HostPort string }
		}
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil || len(info) != 1 {
		t.Fatalf("inspect: %v", err)
	}
	c := info[0]
	if c.Config.User != "65532:65532" {
		t.Errorf("user %q, want 65532:65532", c.Config.User)
	}
	if !c.HostConfig.ReadonlyRootfs {
		t.Error("root filesystem is writable")
	}
	if c.HostConfig.Privileged || len(c.HostConfig.CapAdd) > 0 {
		t.Errorf("privileged %v, cap-add %v", c.HostConfig.Privileged, c.HostConfig.CapAdd)
	}
	if len(c.Mounts) != 1 || c.Mounts[0].Destination != "/state" || c.Mounts[0].RW {
		t.Errorf("mounts %+v, want only /state read-only", c.Mounts)
	}
	for _, m := range c.Mounts {
		if strings.Contains(m.Source, ".sock") {
			t.Errorf("runtime socket mounted: %s", m.Source)
		}
	}
	for port, bs := range c.HostConfig.PortBindings {
		for _, b := range bs {
			if b.HostIp != "127.0.0.1" && b.HostIp != "::1" {
				t.Errorf("port %s published on %q", port, b.HostIp)
			}
		}
	}
	// Capabilities and no_new_privs as the kernel sees them.
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", c.State.Pid))
	if err != nil {
		t.Logf("cannot read proxy process status (%v); skipping kernel checks", err)
		return
	}
	for _, line := range strings.Split(string(status), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		switch f[0] {
		case "CapEff:", "CapPrm:", "CapBnd:":
			if strings.Trim(f[1], "0") != "" {
				t.Errorf("%s %s, want none", f[0], f[1])
			}
		case "NoNewPrivs:":
			if f[1] != "1" {
				t.Errorf("NoNewPrivs %s, want 1", f[1])
			}
		}
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
