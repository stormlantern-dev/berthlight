package acme

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
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

func TestDue(t *testing.T) {
	nb := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := &x509.Certificate{NotBefore: nb, NotAfter: nb.Add(90 * 24 * time.Hour)}
	if got, want := Due(c, 0), nb.Add(60*24*time.Hour); !got.Equal(want) {
		t.Errorf("Due default = %s, want %s (a third left)", got, want)
	}
	if got, want := Due(c, 7*24*time.Hour), nb.Add(83*24*time.Hour); !got.Equal(want) {
		t.Errorf("Due 7d = %s, want %s", got, want)
	}
}

// TestEnsureNotDue: a certificate that is not due is kept, without asking
// the CA or the DNS provider anything.
func TestEnsureNotDue(t *testing.T) {
	state := t.TempDir()
	ca := certstest.NewCA(t)
	crt, key := ca.Leaf(t, 1, 90*24*time.Hour, "*.dev.example.com")
	if err := certs.Write(state, "dev.example.com", crt, key); err != nil {
		t.Fatal(err)
	}
	res, err := Ensure(context.Background(), Options{
		Domain: "dev.example.com", Provider: "no-such-provider", Server: "http://127.0.0.1:1/directory",
		StateDir: state, AccountDir: t.TempDir(),
	})
	if err != nil || res.Obtained {
		t.Fatalf("Ensure: %+v %v", res, err)
	}
	// Due: now it needs the provider, and says which ones there are.
	_, err = Ensure(context.Background(), Options{
		Domain: "dev.example.com", Provider: "no-such-provider", Server: "http://127.0.0.1:1/directory",
		StateDir: state, AccountDir: t.TempDir(), RenewWithin: 365 * 24 * time.Hour,
	})
	if err == nil || !strings.Contains(err.Error(), "gcloud") {
		t.Errorf("due with an unknown provider: %v", err)
	}
}

// TestTermsNeeded: a new account is not registered unless the CA's terms
// are accepted, and the refusal names the terms' URL from the directory.
func TestTermsNeeded(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dir" {
			t.Errorf("unexpected request %s %s: nothing past the directory before the terms are accepted", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"newNonce": %q, "newAccount": %q, "newOrder": %q, "revokeCert": %q, "keyChange": %q,
			"meta": {"termsOfService": "https://ca.example/terms.pdf"}}`,
			srv.URL+"/nonce", srv.URL+"/acct", srv.URL+"/order", srv.URL+"/revoke", srv.URL+"/key")
	}))
	defer srv.Close()
	t.Setenv("LEGO_CA_CERTIFICATES", trustTestServer(t, srv))
	t.Setenv("EXEC_PATH", "/bin/true")
	acct := t.TempDir()
	_, err := Ensure(context.Background(), Options{
		Domain: "dev.example.com", Provider: "exec", Server: srv.URL + "/dir",
		StateDir: t.TempDir(), AccountDir: acct,
	})
	var terms *TermsError
	if !errors.As(err, &terms) || terms.Terms != "https://ca.example/terms.pdf" || !strings.Contains(err.Error(), "https://ca.example/terms.pdf") {
		t.Fatalf("without AcceptTOS: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(acct, "accounts")); len(entries) != 0 {
		t.Errorf("an account was saved without the terms accepted: %v", entries)
	}
}

// trustTestServer writes srv's certificate to a file, for
// LEGO_CA_CERTIFICATES: lego speaks HTTPS only.
func trustTestServer(t *testing.T, srv *httptest.Server) string {
	path := filepath.Join(t.TempDir(), "ca.pem")
	b := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAccountDirOutsideState(t *testing.T) {
	state := t.TempDir()
	_, err := Ensure(context.Background(), Options{
		Domain: "dev.example.com", Provider: "exec", StateDir: state, AccountDir: filepath.Join(state, "acme"),
	})
	if err == nil || !strings.Contains(err.Error(), "inside the state directory") {
		t.Errorf("account dir in state dir: %v", err)
	}
}

func TestServerURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                            DefaultServer,
		"letsencrypt-staging":         "https://acme-staging-v02.api.letsencrypt.org/directory",
		"https://127.0.0.1:14000/dir": "https://127.0.0.1:14000/dir",
	} {
		if got, err := serverURL(in); err != nil || got != want {
			t.Errorf("serverURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := serverURL("no-such-ca"); err == nil {
		t.Error("unknown CA code accepted")
	}
}

func TestAccountRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a, err := loadAccount(dir, "https://ca.example/dir", "me@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.save(); err != nil {
		t.Fatal(err)
	}
	b, err := loadAccount(dir, "https://ca.example/dir", "")
	if err != nil {
		t.Fatal(err)
	}
	if b.Email != "me@example.com" || b.KeyPEM != a.KeyPEM || b.key == nil {
		t.Error("account did not round-trip")
	}
	other, _ := loadAccount(dir, "https://other.example/dir", "")
	if other.KeyPEM == a.KeyPEM {
		t.Error("two servers share an account key")
	}
}

func TestProviders(t *testing.T) {
	for _, want := range []string{"cloudflare", "exec", "gcloud", "route53", "dnsupdate"} {
		found := false
		for _, p := range Providers() {
			found = found || p == want
		}
		if !found {
			t.Errorf("provider %s missing", want)
		}
	}
	if _, err := NewProvider("nope"); err == nil {
		t.Error("unknown provider accepted")
	}
	// A provider without its configuration says what it misses.
	t.Setenv("EXEC_PATH", "")
	if _, err := NewProvider("exec"); err == nil || !strings.Contains(err.Error(), "EXEC_PATH") {
		t.Errorf("exec without EXEC_PATH: %v", err)
	}
}
