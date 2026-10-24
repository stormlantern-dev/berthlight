//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"go/build"
	"io"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"stormlantern.nl/berthlight/internal/certs"
	"stormlantern.nl/berthlight/internal/certs/certstest"
)

// pebble is a running Pebble, Let's Encrypt's test ACME server, with
// pebble-challtestsrv as the DNS server it validates DNS-01 against.
type pebble struct {
	directory string         // ACME directory URL
	dns       string         // challtestsrv's DNS server, host:port
	mgmt      string         // challtestsrv's management API URL
	caFile    string         // trusts Pebble's own HTTPS
	roots     *x509.CertPool // trusts the certificates Pebble issues
}

// findTool finds a Go-installed binary on PATH or in GOPATH/bin.
func findTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	p := filepath.Join(build.Default.GOPATH, "bin", name)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// startPebble starts Pebble and challtestsrv on free loopback ports. CI
// installs both (BERTHLIGHT_REQUIRE_PEBBLE); elsewhere the test skips
// without them:
//
//	go install github.com/letsencrypt/pebble/v2/cmd/pebble@v2.10.1 github.com/letsencrypt/pebble/v2/cmd/pebble-challtestsrv@v2.10.1
func startPebble(t *testing.T) *pebble {
	t.Helper()
	pebbleBin, challBin := findTool("pebble"), findTool("pebble-challtestsrv")
	if pebbleBin == "" || challBin == "" {
		if os.Getenv("BERTHLIGHT_REQUIRE_PEBBLE") != "" {
			t.Fatal("pebble and pebble-challtestsrv not found, and BERTHLIGHT_REQUIRE_PEBBLE is set")
		}
		t.Skip("pebble and pebble-challtestsrv not installed")
	}
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// Pebble's own HTTPS certificate, from a CA the CLI is told to trust.
	ca := certstest.NewCA(t)
	crt, key := ca.Leaf(t, 1, 24*time.Hour, "localhost", "127.0.0.1")
	p := &pebble{caFile: write("ca.pem", ca.PEM())}
	dnsAddr := "127.0.0.1:" + strconv.Itoa(freePort(t))
	mgmtAddr := "127.0.0.1:" + strconv.Itoa(freePort(t))
	acmeAddr := "127.0.0.1:" + strconv.Itoa(freePort(t))
	pebbleMgmt := "127.0.0.1:" + strconv.Itoa(freePort(t))
	p.dns, p.mgmt, p.directory = dnsAddr, "http://"+mgmtAddr, "https://"+acmeAddr+"/dir"
	config := fmt.Sprintf(`{"pebble": {
  "listenAddress": %q, "managementListenAddress": %q,
  "certificate": %q, "privateKey": %q,
  "httpPort": %d, "tlsPort": %d,
  "ocspResponderURL": "", "externalAccountBindingRequired": false,
  "retryAfter": {"authz": 1, "order": 1},
  "keyAlgorithm": "ecdsa",
  "profiles": {"default": {"description": "90 days, as Let's Encrypt", "validityPeriod": 7776000}}
}}`, acmeAddr, pebbleMgmt, write("pebble.crt", crt), write("pebble.key", key), freePort(t), freePort(t))
	cfg := write("pebble.json", []byte(config))

	start := func(name string, env []string, bin string, args ...string) {
		var logs bytes.Buffer
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		cmd.Stdout, cmd.Stderr = &logs, &logs
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cmd.Process.Kill()
			cmd.Wait()
			if t.Failed() {
				t.Logf("%s logs:\n%s", name, logs.String())
			}
		})
	}
	start("challtestsrv", nil, challBin, "-dnsserver", dnsAddr, "-management", mgmtAddr,
		"-http01", "", "-https01", "", "-tlsalpn01", "", "-doh", "", "-defaultIPv4", "127.0.0.1", "-defaultIPv6", "")
	// No sleeps, no bad nonces, no reused authorizations: a renewal
	// passes DNS-01 again.
	start("pebble", []string{"PEBBLE_VA_NOSLEEP=1", "PEBBLE_WFE_NONCEREJECT=0", "PEBBLE_AUTHZREUSE=0"},
		pebbleBin, "-config", cfg, "-dnsserver", dnsAddr)

	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: ca.Pool}}}
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := client.Get(p.directory)
		if err == nil {
			resp.Body.Close()
			if c, err := net.Dial("tcp", mgmtAddr); err == nil {
				c.Close()
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pebble did not start: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// The root Pebble issues under, fresh at each start.
	resp, err := client.Get("https://" + pebbleMgmt + "/roots/0")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	p.roots = x509.NewCertPool()
	if !p.roots.AppendCertsFromPEM(root) {
		t.Fatalf("pebble root: %q", root)
	}
	return p
}

// dnsScript is the exec DNS provider's program (lego's EXEC_PATH): it
// sets the challenge record in challtestsrv. It stands for a real
// provider's API, and refuses to work without its "credential".
const dnsScript = `#!/bin/sh
set -e
[ "$BL_TEST_DNS_SECRET" = "%s" ] || { echo "no DNS credential" >&2; exit 1; }
case "$1" in
present) curl -sfS -X POST -d "{\"host\":\"$2\",\"value\":\"$3\"}" "$BL_TEST_CHALLTESTSRV/set-txt" ;;
cleanup) curl -sfS -X POST -d "{\"host\":\"$2\"}" "$BL_TEST_CHALLTESTSRV/clear-txt" ;;
esac
`

// TestHTTPS covers PRD 0001, R7 and the HTTPS half of R2: `berthlight
// cert` obtains a wildcard certificate from Pebble with DNS-01; the proxy
// serves it for two worktrees' names on the same port as HTTP, redirects
// plain HTTP for them, and picks up a renewal; the DNS credentials never
// reach the proxy.
func TestHTTPS(t *testing.T) {
	peb := startPebble(t)
	e := setup(t)
	base := e.id + ".example.com"
	t.Setenv("BERTHLIGHT_BASE", base+",localhost")
	acmeDir := t.TempDir()
	t.Setenv("BERTHLIGHT_ACME_DIR", acmeDir)
	t.Setenv("LEGO_CA_CERTIFICATES", peb.caFile)

	// The provider's configuration and credential go in cert.env, as for
	// the renewal timer. cert sets them in this process; unset them after.
	secret := "dns-secret-" + e.id
	script := filepath.Join(acmeDir, "dns.sh")
	if err := os.WriteFile(script, []byte(fmt.Sprintf(dnsScript, secret)), 0o700); err != nil {
		t.Fatal(err)
	}
	env := fmt.Sprintf("EXEC_PATH=%s\nEXEC_POLLING_INTERVAL=1\nBL_TEST_DNS_SECRET=%s\nBL_TEST_CHALLTESTSRV=%s\n", script, secret, peb.mgmt)
	if err := os.WriteFile(filepath.Join(acmeDir, "cert.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"EXEC_PATH", "EXEC_POLLING_INTERVAL", "BL_TEST_DNS_SECRET", "BL_TEST_CHALLTESTSRV"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	// Two worktrees of one project, on HTTP first.
	main, fix := e.name("shop"), e.name("shop-fix-login")
	e.cli("add", main, "8080")
	e.backend(main, "hello-from-main")
	e.cli("add", fix, "8080")
	e.backend(fix, "hello-from-fix")
	e.waitBody(main+"."+base, "hello-from-main", 15*time.Second)

	certArgs := []string{"cert", "--dns", "exec", "--server", peb.directory, "--resolvers", peb.dns}
	if out := e.cli(certArgs...); !strings.Contains(out, "obtained a certificate for *."+base) {
		t.Fatalf("cert: %s", out)
	}
	serial := e.waitHTTPS(main+"."+base, "hello-from-main", peb.roots, nil)
	if got := e.waitHTTPS(fix+"."+base, "hello-from-fix", peb.roots, nil); got.Cmp(serial) != 0 {
		t.Errorf("two names, two certificates: %s and %s", serial, got)
	}

	// Plain HTTP on the same port: a redirect to HTTPS for the names the
	// certificate covers, and still HTTP for those it does not.
	if code, loc := e.redirect(main + "." + base); code != http.StatusPermanentRedirect || loc != "https://"+main+"."+base+":"+strconv.Itoa(e.port)+"/" {
		t.Errorf("http for %s: %d %q", main, code, loc)
	}
	e.waitBody(main+".localhost", "hello-from-main", 2*time.Second)
	if out := e.cli("ls"); !strings.Contains(out, "https://"+fix+"."+base+":") {
		t.Errorf("ls lacks the https URL:\n%s", out)
	}

	// Not due: kept. Due: renewed through DNS-01 again, and served.
	if out := e.cli(certArgs...); !strings.Contains(out, "is valid until") {
		t.Errorf("cert when not due: %s", out)
	}
	if out := e.cli(append(certArgs, "--renew-within", "8760h")...); !strings.Contains(out, "obtained") {
		t.Fatalf("renewal: %s", out)
	}
	if renewed := e.waitHTTPS(fix+"."+base, "hello-from-fix", peb.roots, serial); renewed.Cmp(serial) == 0 {
		t.Error("renewal not served")
	}

	t.Run("no DNS credentials in the proxy", func(t *testing.T) {
		insp, err := e.run.Run("container", "inspect", e.proxy)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{secret, "EXEC_", "BL_TEST", "LEGO", acmeDir} {
			if strings.Contains(insp, s) {
				t.Errorf("proxy container's configuration contains %q", s)
			}
		}
		// What the proxy mounts holds routes and certificates only.
		state := os.Getenv("BERTHLIGHT_STATE_DIR")
		filepath.WalkDir(state, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(state, path)
			if rel != "routes.json" && !strings.HasPrefix(rel, certs.DirName+string(filepath.Separator)) {
				t.Errorf("unexpected file in the state directory: %s", rel)
			}
			if b, _ := os.ReadFile(path); bytes.Contains(b, []byte(secret)) {
				t.Errorf("%s contains the DNS credential", rel)
			}
			return nil
		})
		_, key := certs.Files(state, base)
		if fi, err := os.Stat(key); err != nil || fi.Mode().Perm() != 0o640 {
			t.Errorf("key file: %v %v, want mode 640", fi.Mode(), err)
		}
		e.checkProxySecurity(t)
	})
}

// waitHTTPS polls until host answers body over TLS through the proxy port
// with a certificate that verifies against roots, and is not the one with
// serial old, if given. It returns the certificate's serial.
func (e *env) waitHTTPS(host, body string, roots *x509.CertPool, old *big.Int) *big.Int {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		var serial *big.Int
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: roots, ServerName: host,
				VerifyConnection: func(cs tls.ConnectionState) error {
					serial = cs.PeerCertificates[0].SerialNumber
					return nil
				},
			},
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:"+strconv.Itoa(e.port))
			},
		}
		c := &http.Client{Timeout: 3 * time.Second, Transport: tr}
		resp, err := c.Get("https://" + host + ":" + strconv.Itoa(e.port) + "/")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			got := strings.TrimSpace(string(b))
			if resp.StatusCode == 200 && got == body && (old == nil || old.Cmp(serial) != 0) {
				tr.CloseIdleConnections()
				return serial
			}
			last = fmt.Sprintf("%d %q, serial %s", resp.StatusCode, got, serial)
		} else {
			last = err.Error()
		}
		tr.CloseIdleConnections()
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatalf("https://%s did not answer %q within 10s; last: %s", host, body, last)
	return nil
}

// redirect sends plain HTTP for host to the proxy and returns the status
// and Location, without following.
func (e *env) redirect(host string) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(e.port)+"/", nil)
	req.Host = host + ":" + strconv.Itoa(e.port)
	c := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Location")
}
