// Package certs keeps the proxy's TLS certificates in the state directory
// (PRD 0001, R7). The CLI writes them; the proxy, which mounts the state
// directory read-only, reads them and reloads them when they change. Nothing
// here talks to a CA or to DNS: that stays with the CLI, in package acme.
package certs

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"stormlantern.nl/berthlight/internal/route"
)

// DirName is the certificates' directory inside the state directory.
const DirName = "certs"

// Dir returns the certificates' directory.
func Dir(stateDir string) string { return filepath.Join(stateDir, DirName) }

// Files returns the certificate and key files for a base domain. The
// certificate is for *.<base>: names are one label (R4).
func Files(stateDir, base string) (crt, key string) {
	p := filepath.Join(Dir(stateDir), base)
	return p + ".crt", p + ".key"
}

// Has reports whether there is a certificate for base.
func Has(stateDir, base string) bool {
	crt, _ := Files(stateDir, base)
	_, err := os.Stat(crt)
	return err == nil
}

// Read returns the leaf certificate for base.
func Read(stateDir, base string) (*x509.Certificate, error) {
	crt, _ := Files(stateDir, base)
	b, err := os.ReadFile(crt)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s: no PEM certificate", crt)
	}
	return x509.ParseCertificate(blk.Bytes)
}

// Write stores the certificate chain and key for base, each file replaced
// atomically. The key is readable by its owner and group only (0640), in a
// directory only they can enter (0750); the proxy runs with that group as a
// supplementary group (see runtime.ProxySpec), so it can read the key and
// other users on the machine cannot.
func Write(stateDir, base string, chainPEM, keyPEM []byte) error {
	if _, err := tls.X509KeyPair(chainPEM, keyPEM); err != nil {
		return fmt.Errorf("certificate and key for %s do not go together: %w", base, err)
	}
	if err := route.MakeDir(stateDir); err != nil {
		return err
	}
	dir := Dir(stateDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		return err
	}
	if err := chgrp(dir); err != nil {
		return err
	}
	crt, key := Files(stateDir, base)
	// Key first: until the certificate follows, the pair does not match,
	// and the proxy keeps the previous one.
	if err := writeFile(key, keyPEM, 0o640); err != nil {
		return err
	}
	return writeFile(crt, chainPEM, 0o644)
}

func writeFile(path string, b []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := chgrp(tmp.Name()); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// chgrp gives a file the user's own group, which the proxy is given (on
// macOS, a new file takes its directory's group instead). Windows has no
// groups; there, berthlight runs in WSL anyway.
func chgrp(path string) error {
	gid := os.Getegid()
	if gid < 0 {
		return nil
	}
	return os.Chown(path, -1, gid)
}

// Set is the certificates the proxy serves.
type Set struct {
	certs []tls.Certificate
}

// Len is the number of certificates.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.certs)
}

// Names lists the names the certificates cover, for logs.
func (s *Set) Names() []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, c := range s.certs {
		out = append(out, c.Leaf.DNSNames...)
	}
	return out
}

// Covers reports whether a certificate covers host (with or without port).
func (s *Set) Covers(host string) bool {
	if s == nil {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, c := range s.certs {
		if c.Leaf.VerifyHostname(host) == nil {
			return true
		}
	}
	return false
}

// GetCertificate picks the certificate for a TLS handshake by its server
// name (SNI); for tls.Config.GetCertificate.
func (s *Set) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if s.Len() == 0 {
		return nil, errors.New("no certificate configured")
	}
	name := strings.ToLower(hello.ServerName)
	for i := range s.certs {
		if s.certs[i].Leaf.VerifyHostname(name) == nil {
			return &s.certs[i], nil
		}
	}
	if name == "" {
		return &s.certs[0], nil
	}
	return nil, fmt.Errorf("no certificate for %q", hello.ServerName)
}

// Load reads every certificate in the state directory: each <base>.crt
// with its <base>.key. No directory is an empty set.
func Load(stateDir string) (*Set, error) {
	entries, err := os.ReadDir(Dir(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return &Set{}, nil
	}
	if err != nil {
		return nil, err
	}
	s := &Set{}
	for _, e := range entries {
		base, ok := strings.CutSuffix(e.Name(), ".crt")
		if !ok || strings.HasPrefix(base, ".") {
			continue
		}
		crt, key := Files(stateDir, base)
		c, err := tls.LoadX509KeyPair(crt, key)
		if err != nil {
			return nil, fmt.Errorf("certificate for %s: %w", base, err)
		}
		s.certs = append(s.certs, c)
	}
	return s, nil
}

// Watch polls the certificates' directory and calls apply with the set
// whenever a file changes, starting with the current set. While the files
// do not load (a certificate written before its key, a key the proxy may
// not read), the error goes to onErr, the previous set stays, and the next
// poll tries again. Polling works on every bind mount, as for routes.
func Watch(ctx context.Context, stateDir string, every time.Duration, apply func(*Set), onErr func(error)) {
	var last []byte
	first, failing := true, false
	check := func() {
		sig, err := signature(Dir(stateDir))
		if err != nil {
			onErr(err)
			return
		}
		changed := first || !bytes.Equal(sig, last)
		if !changed && !failing {
			return
		}
		last, first = sig, false
		s, err := Load(stateDir)
		if err != nil {
			if changed { // once per change; retries stay quiet
				onErr(err)
			}
			failing = true
			return
		}
		failing = false
		apply(s)
	}
	check()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			check()
		}
	}
}

// signature describes the directory's files, so a change to any shows.
func signature(dir string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	for _, n := range names {
		if strings.HasPrefix(n, ".") {
			continue
		}
		fi, err := os.Stat(filepath.Join(dir, n))
		if err != nil {
			continue // replaced while we looked; the next poll sees it
		}
		fmt.Fprintf(&b, "%s %d %d\n", n, fi.Size(), fi.ModTime().UnixNano())
	}
	return b.Bytes(), nil
}
