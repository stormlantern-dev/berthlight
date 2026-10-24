package certs

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"stormlantern.nl/berthlight/internal/certs/certstest"
)

func TestWriteLoad(t *testing.T) {
	state := t.TempDir()
	ca := certstest.NewCA(t)
	if Has(state, "dev.example.com") {
		t.Fatal("Has before Write")
	}
	if s, err := Load(state); err != nil || s.Len() != 0 {
		t.Fatalf("Load of nothing: %v, %d", err, s.Len())
	}
	crt, key := ca.Leaf(t, 7, time.Hour, "*.dev.example.com")
	if err := Write(state, "dev.example.com", crt, key); err != nil {
		t.Fatal(err)
	}
	if !Has(state, "dev.example.com") {
		t.Error("Has after Write")
	}
	leaf, err := Read(state, "dev.example.com")
	if err != nil || leaf.SerialNumber.Int64() != 7 {
		t.Fatalf("Read: %v %v", leaf, err)
	}
	s, err := Load(state)
	if err != nil || s.Len() != 1 {
		t.Fatalf("Load: %v, %d", err, s.Len())
	}
	for host, want := range map[string]bool{
		"shop.dev.example.com":           true,
		"shop-fix-login.dev.example.com": true,
		"SHOP.dev.example.com.:7480":     true,
		"a.shop.dev.example.com":         false, // one label (R4)
		"dev.example.com":                false,
		"shop.localhost":                 false,
	} {
		if got := s.Covers(host); got != want {
			t.Errorf("Covers(%q) = %v, want %v", host, got, want)
		}
	}
	if c, err := s.GetCertificate(&tls.ClientHelloInfo{ServerName: "shop.dev.example.com"}); err != nil || c.Leaf.SerialNumber.Int64() != 7 {
		t.Errorf("GetCertificate: %v", err)
	}
	if _, err := s.GetCertificate(&tls.ClientHelloInfo{ServerName: "shop.localhost"}); err == nil {
		t.Error("GetCertificate for an uncovered name succeeded")
	}

	if runtime.GOOS == "windows" {
		return
	}
	// The key is for the user and the proxy's group only.
	_, keyFile := Files(state, "dev.example.com")
	for path, want := range map[string]os.FileMode{Dir(state): 0o750, keyFile: 0o640} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s: mode %o, want %o", filepath.Base(path), fi.Mode().Perm(), want)
		}
	}
}

func TestWriteRefusesMismatch(t *testing.T) {
	ca := certstest.NewCA(t)
	crt, _ := ca.Leaf(t, 1, time.Hour, "*.dev.example.com")
	_, key := ca.Leaf(t, 2, time.Hour, "*.dev.example.com")
	if err := Write(t.TempDir(), "dev.example.com", crt, key); err == nil {
		t.Error("Write of a certificate with another's key succeeded")
	}
}

// TestWatch: the proxy picks up a certificate, a renewal, and keeps the
// last good set while the files do not load.
func TestWatch(t *testing.T) {
	state := t.TempDir()
	ca := certstest.NewCA(t)
	got := make(chan *Set, 10)
	errs := make(chan error, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Watch(ctx, state, 10*time.Millisecond, func(s *Set) { got <- s }, func(err error) { errs <- err })

	next := func() *Set {
		t.Helper()
		select {
		case s := <-got:
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("no set within 5s")
			return nil
		}
	}
	if s := next(); s.Len() != 0 {
		t.Fatalf("first set has %d", s.Len())
	}
	crt, key := ca.Leaf(t, 1, time.Hour, "*.dev.example.com")
	if err := Write(state, "dev.example.com", crt, key); err != nil {
		t.Fatal(err)
	}
	if s := next(); s.Len() != 1 || s.certs[0].Leaf.SerialNumber.Int64() != 1 {
		t.Fatalf("after write: %d", s.Len())
	}

	// A key that does not match: reported, and no new set.
	_, other := ca.Leaf(t, 9, time.Hour, "*.dev.example.com")
	_, keyFile := Files(state, "dev.example.com")
	if err := os.WriteFile(keyFile, other, 0o640); err != nil {
		t.Fatal(err)
	}
	select {
	case <-errs:
	case s := <-got:
		t.Fatalf("mismatched pair applied: %d", s.Len())
	case <-time.After(5 * time.Second):
		t.Fatal("mismatch not reported")
	}

	crt, key = ca.Leaf(t, 2, time.Hour, "*.dev.example.com")
	if err := Write(state, "dev.example.com", crt, key); err != nil {
		t.Fatal(err)
	}
	if s := next(); s.certs[0].Leaf.SerialNumber.Int64() != 2 {
		t.Fatalf("renewal not picked up: serial %v", s.certs[0].Leaf.SerialNumber)
	}
}
