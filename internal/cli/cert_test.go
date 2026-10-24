package cli

import (
	"bytes"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"stormlantern.nl/berthlight/internal/certs"
	"stormlantern.nl/berthlight/internal/certs/certstest"
)

func TestOwnBase(t *testing.T) {
	for in, want := range map[string]string{
		"localhost":                     "",
		"localhost,dev.example.com":     "dev.example.com",
		"dev.example.com,localhost":     "dev.example.com",
		"x.localhost,dev2.example.com,": "dev2.example.com",
	} {
		if got := ownBase(strings.Split(in, ",")); got != want {
			t.Errorf("ownBase(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckDomain(t *testing.T) {
	for _, ok := range []string{"dev.example.com", "dev-1.stormlantern.nl", "xn--bcher-kva.example"} {
		if err := checkDomain(ok); err != nil {
			t.Errorf("checkDomain(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"localhost", "a.localhost", "com", "dev..example.com", "-dev.example.com", "dev_x.example.com"} {
		if err := checkDomain(bad); err == nil {
			t.Errorf("checkDomain(%q) accepted", bad)
		}
	}
}

func TestLoadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cert.env")
	content := "# Cloudflare\nexport BL_TEST_A=one\nBL_TEST_B=\"two words\"\nBL_TEST_C='three'\n\nBL_TEST_D=from-file\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BL_TEST_D", "from-env")
	for _, k := range []string{"BL_TEST_A", "BL_TEST_B", "BL_TEST_C"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	var errb bytes.Buffer
	if err := loadEnvFile(path, &errb); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"BL_TEST_A": "one", "BL_TEST_B": "two words", "BL_TEST_C": "three", "BL_TEST_D": "from-env"} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if errb.Len() != 0 {
		t.Errorf("warning for a 0600 file: %s", errb.String())
	}
	if err := loadEnvFile(filepath.Join(t.TempDir(), "none"), &errb); err != nil {
		t.Errorf("missing file: %v", err)
	}
	if err := os.WriteFile(path, []byte("not a pair\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadEnvFile(path, &errb); err == nil || !strings.Contains(err.Error(), ":1:") {
		t.Errorf("bad line: %v", err)
	}
}

func TestCertNeedsDomainAndProvider(t *testing.T) {
	t.Setenv("BERTHLIGHT_STATE_DIR", t.TempDir())
	t.Setenv("BERTHLIGHT_ACME_DIR", t.TempDir())
	t.Setenv("BERTHLIGHT_DNS_PROVIDER", "")
	t.Setenv("BERTHLIGHT_BASE", "localhost")
	var out, errb bytes.Buffer
	if code := Run([]string{"--no-header", "cert", "--dns", "exec"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "BERTHLIGHT_BASE") {
		t.Errorf("no own base: %d %s", code, errb.String())
	}
	t.Setenv("BERTHLIGHT_BASE", "dev.example.com,localhost")
	errb.Reset()
	if code := Run([]string{"--no-header", "cert"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "--dns") || !strings.Contains(errb.String(), "gcloud") {
		t.Errorf("no provider: %d %s", code, errb.String())
	}
}

// TestCertNeedsAcceptTOS: the first run, which creates the ACME account,
// says where the CA's terms are and which flag accepts them.
func TestCertNeedsAcceptTOS(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"newNonce": %q, "newAccount": %q, "newOrder": %q, "meta": {"termsOfService": "https://ca.example/terms"}}`,
			srv.URL+"/nonce", srv.URL+"/acct", srv.URL+"/order")
	}))
	defer srv.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEGO_CA_CERTIFICATES", caFile)
	t.Setenv("BERTHLIGHT_STATE_DIR", t.TempDir())
	t.Setenv("BERTHLIGHT_ACME_DIR", t.TempDir())
	t.Setenv("BERTHLIGHT_BASE", "dev.example.com")
	t.Setenv("BERTHLIGHT_RUNTIME", "no-such-runtime")
	t.Setenv("EXEC_PATH", "/bin/true")
	var out, errb bytes.Buffer
	code := Run([]string{"--no-header", "cert", "--dns", "exec", "--server", srv.URL + "/dir"}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "https://ca.example/terms") || !strings.Contains(errb.String(), "--accept-tos") {
		t.Errorf("without --accept-tos: %d %s", code, errb.String())
	}
}

// TestURLScheme: URLs are https once there is a certificate for the base.
func TestURLScheme(t *testing.T) {
	state := t.TempDir()
	c := Config{StateDir: state, Port: 7480, Bases: []string{"dev.example.com", "localhost"}}
	if got := urlFor(c, "shop"); got != "http://shop.dev.example.com:7480" {
		t.Errorf("without certificate: %s", got)
	}
	ca := certstest.NewCA(t)
	crt, key := ca.Leaf(t, 1, time.Hour, "*.dev.example.com")
	if err := certs.Write(state, "dev.example.com", crt, key); err != nil {
		t.Fatal(err)
	}
	if got := urlFor(c, "shop"); got != "https://shop.dev.example.com:7480" {
		t.Errorf("with certificate: %s", got)
	}
	c.Bases = []string{"localhost", "dev.example.com"}
	if got := urlFor(c, "shop"); got != "http://shop.localhost:7480" {
		t.Errorf("localhost first: %s", got)
	}
}

func TestSystemdUnits(t *testing.T) {
	service, timer := systemdUnits("/usr/bin/berthlight", []string{"cert", "--domain", "dev.example.com", "--dns", "gcloud"},
		[]string{"BERTHLIGHT_STATE_DIR=/home/u/.local/state/berthlight", "PATH=/usr/bin:/home/u/My Bin"})
	for _, want := range []string{
		"Type=oneshot",
		"Environment=BERTHLIGHT_STATE_DIR=/home/u/.local/state/berthlight\n",
		`Environment="PATH=/usr/bin:/home/u/My Bin"`,
		"ExecStart=/usr/bin/berthlight cert --domain dev.example.com --dns gcloud\n",
	} {
		if !strings.Contains(service, want) {
			t.Errorf("service lacks %q:\n%s", want, service)
		}
	}
	for _, want := range []string{"OnCalendar=daily", "Persistent=true", "WantedBy=timers.target"} {
		if !strings.Contains(timer, want) {
			t.Errorf("timer lacks %q:\n%s", want, timer)
		}
	}
	if got := systemdQuote(`50%$"x"`); got != `"50%%$$\"x\""` {
		t.Errorf("systemdQuote = %s", got)
	}
}

func TestLaunchdPlist(t *testing.T) {
	p := launchdPlist("/opt/homebrew/bin/berthlight", []string{"cert", "--dns", "cloudflare"}, []string{"PATH=/opt/homebrew/bin:/usr/bin", "X=a&b"}, "/Users/u/Library/Logs/berthlight-cert.log", 17)
	for _, want := range []string{
		"<string>nl.stormlantern.berthlight-cert</string>",
		"<string>/opt/homebrew/bin/berthlight</string>\n\t\t<string>cert</string>",
		"<key>X</key>\n\t\t<string>a&amp;b</string>",
		"<key>Minute</key>\n\t\t<integer>17</integer>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q:\n%s", want, p)
		}
	}
}
