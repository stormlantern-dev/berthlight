// Package acme obtains and renews the wildcard certificate for a base
// domain the user owns (PRD 0001, R7), with the DNS-01 challenge, through
// lego. It runs in the CLI on the host only: the DNS credentials it reads
// from the environment never reach the proxy, which only gets the
// certificate and key files (package certs).
package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certcrypto"
	"github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge/dns01"
	"github.com/go-acme/lego/v5/lego"
	legolog "github.com/go-acme/lego/v5/log"
	"github.com/go-acme/lego/v5/registration"

	"stormlantern.nl/berthlight/internal/certs"
)

// DefaultServer is Let's Encrypt's production directory.
const DefaultServer = lego.DirectoryURLLetsEncrypt

// Options says what certificate to get, and from where.
type Options struct {
	// Domain is the base domain; the certificate is for *.<Domain>.
	Domain string
	// Provider names the DNS provider (see Providers).
	Provider string
	// Server is the ACME directory URL, or a CA code lego knows, such as
	// "letsencrypt-staging". Default: Let's Encrypt.
	Server string
	// Email, optional, is given to the CA as the account's contact.
	Email string
	// Resolvers, when set, are the DNS servers (host:port) to check the
	// challenge record's propagation on, instead of the system's and the
	// zone's authoritative ones.
	Resolvers []string
	// AccountDir keeps the ACME account key. It must not be inside
	// StateDir, which the proxy mounts.
	AccountDir string
	// StateDir is where the certificate and key go.
	StateDir string
	// RenewWithin renews a certificate that expires within this time.
	// Zero: when less than a third of its lifetime is left, as Let's
	// Encrypt advises, whatever lifetime the CA gives.
	RenewWithin time.Duration
	// AcceptTOS agrees to the CA's terms of service, which registering a
	// new ACME account requires. Not needed once the account exists.
	AcceptTOS bool
	// Now defaults to time.Now.
	Now func() time.Time
	// Log receives lego's progress. Default: discarded.
	Log *slog.Logger
}

// Result says what Ensure did.
type Result struct {
	// Obtained is true when a new certificate was written.
	Obtained bool
	// NotAfter is when the certificate in place expires.
	NotAfter time.Time
	// RenewAt is when it is due for renewal.
	RenewAt time.Time
}

// TermsError says that a new ACME account needs the CA's terms of service
// accepted, and where they are.
type TermsError struct {
	Server string
	// Terms is the terms' URL from the CA's directory; empty if it has none.
	Terms string
}

func (e *TermsError) Error() string {
	if e.Terms == "" {
		return fmt.Sprintf("a new ACME account with %s means agreeing to the CA's terms of service", e.Server)
	}
	return fmt.Sprintf("a new ACME account with %s means agreeing to the CA's terms of service: %s", e.Server, e.Terms)
}

// Due returns when a certificate is due for renewal.
func Due(c *x509.Certificate, within time.Duration) time.Time {
	if within <= 0 {
		within = c.NotAfter.Sub(c.NotBefore) / 3
	}
	return c.NotAfter.Add(-within)
}

// Ensure makes sure the state directory has a certificate for
// *.<Domain> that is not due for renewal, obtaining one if needed.
func Ensure(ctx context.Context, o Options) (Result, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	wildcard := "*." + o.Domain
	if c, err := certs.Read(o.StateDir, o.Domain); err == nil && c.VerifyHostname("x."+o.Domain) == nil {
		due := Due(c, o.RenewWithin)
		if o.Now().Before(due) {
			return Result{NotAfter: c.NotAfter, RenewAt: due}, nil
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		o.Log.Warn("replacing the certificate in place, which does not read", "error", err)
	}
	if inside(o.AccountDir, o.StateDir) {
		return Result{}, fmt.Errorf("the ACME account directory %s is inside the state directory %s, which the proxy mounts; keep them apart", o.AccountDir, o.StateDir)
	}

	server, err := serverURL(o.Server)
	if err != nil {
		return Result{}, err
	}
	provider, err := NewProvider(o.Provider)
	if err != nil {
		return Result{}, err
	}
	legolog.SetDefault(o.Log)

	acct, err := loadAccount(o.AccountDir, server, o.Email)
	if err != nil {
		return Result{}, err
	}
	config := lego.NewConfig(acct)
	config.CADirURL = server
	config.UserAgent = "berthlight"
	client, err := lego.NewClient(config)
	if err != nil {
		return Result{}, fmt.Errorf("ACME server %s: %w", server, err)
	}
	var opts []dns01.ChallengeOption
	if len(o.Resolvers) > 0 {
		// The record need only be visible on the resolvers given: split
		// DNS, a private resolver, or a test DNS server.
		dns01.SetDefaultClient(dns01.NewClient(&dns01.Options{RecursiveNameservers: o.Resolvers}))
		opts = append(opts, dns01.DisableAuthoritativeNssPropagationRequirement())
	}
	if err := client.Challenge.SetDNS01Provider(provider, opts...); err != nil {
		return Result{}, err
	}
	if acct.Registration == nil {
		// A new account agrees to the CA's terms: only when the user
		// says so. An existing account already has, so renewals and the
		// timer need nothing more.
		if !o.AcceptTOS {
			return Result{}, &TermsError{Server: server, Terms: client.GetServerMetadata().TermsOfService}
		}
		reg, err := client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return Result{}, fmt.Errorf("registering an ACME account with %s: %w", server, err)
		}
		acct.Registration = reg
		if err := acct.save(); err != nil {
			return Result{}, err
		}
	}
	res, err := client.Certificate.Obtain(ctx, certificate.ObtainRequest{
		Domains: []string{wildcard},
		KeyType: certcrypto.EC256,
		Bundle:  true,
	})
	if err != nil {
		return Result{}, fmt.Errorf("obtaining a certificate for %s: %w", wildcard, err)
	}
	if err := certs.Write(o.StateDir, o.Domain, res.Certificate, res.PrivateKey); err != nil {
		return Result{}, err
	}
	c, err := certs.Read(o.StateDir, o.Domain)
	if err != nil {
		return Result{}, err
	}
	return Result{Obtained: true, NotAfter: c.NotAfter, RenewAt: Due(c, o.RenewWithin)}, nil
}

func serverURL(s string) (string, error) {
	if s == "" {
		return DefaultServer, nil
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("ACME server %q is not a URL", s)
		}
		return s, nil
	}
	u, err := lego.GetDirectoryURL(s)
	if err != nil {
		return "", fmt.Errorf("ACME server %q: not a URL, nor a CA lego knows (%s)", s, strings.Join(lego.GetAllCodes(), ", "))
	}
	return u, nil
}

// inside reports whether dir is root or below it.
func inside(dir, root string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// account is an ACME account: a key and its registration with one server.
type account struct {
	path         string
	Email        string                `json:"email,omitempty"`
	KeyPEM       string                `json:"key"`
	Registration *acme.ExtendedAccount `json:"registration,omitempty"`
	key          crypto.Signer
}

func (a *account) GetEmail() string                       { return a.Email }
func (a *account) GetRegistration() *acme.ExtendedAccount { return a.Registration }
func (a *account) GetPrivateKey() crypto.Signer           { return a.key }

// loadAccount reads the account for server, or makes a new key. Each
// server has its own file, named after its host and a hash of its URL.
func loadAccount(dir, server, email string) (*account, error) {
	u, _ := url.Parse(server)
	sum := sha256.Sum256([]byte(server))
	path := filepath.Join(dir, "accounts", u.Hostname()+"-"+hex.EncodeToString(sum[:4])+".json")
	a := &account{path: path}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		der, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return nil, err
		}
		a.key, a.Email = key, email
		a.KeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
		return a, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(b, a); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	blk, _ := pem.Decode([]byte(a.KeyPEM))
	if blk == nil {
		return nil, fmt.Errorf("%s: no key", path)
	}
	key, err := x509.ParseECPrivateKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	a.key = key
	return a, nil
}

// save writes the account for its owner only.
func (a *account) save() error {
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.path, append(b, '\n'), 0o600)
}
