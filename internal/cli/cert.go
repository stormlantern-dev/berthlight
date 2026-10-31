package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"stormlantern.nl/berthlight/internal/acme"
	"stormlantern.nl/berthlight/internal/naming"
	rt "stormlantern.nl/berthlight/internal/runtime"
)

// CertEnvFile, in the ACME directory, holds the DNS provider's credentials
// as KEY=VALUE lines, for runs without the user's shell (the renewal
// timer). The variables are lego's for the provider.
const CertEnvFile = "cert.env"

// cmdCert obtains or renews the wildcard certificate for the user's own
// base domain (PRD 0001, R7). It runs on the host, with the DNS
// credentials; the proxy only ever sees the certificate and key files.
type certOptions struct {
	domain, dns, server, email, resolvers string
	within                                time.Duration
	tos, timer                            bool
}

func newCertCmd() *cobra.Command {
	var o certOptions
	cmd := &cobra.Command{
		Use:   "cert --dns <provider>",
		Short: "Get or renew a trusted wildcard certificate for your own domain",
		Long: `cert gets a wildcard certificate for *.<domain>, a domain you own, from Let's
Encrypt by default, with the DNS-01 challenge; then the proxy serves HTTPS
on the same port and redirects plain http to https for those names. Run it
again to renew: it renews when a third of the certificate's lifetime is
left, and otherwise says until when the certificate is valid.

Point *.<domain> at 127.0.0.1 (an A record, and AAAA ::1) at your DNS
provider first, and put the domain in BERTHLIGHT_BASE, e.g.
dev.example.com,localhost. cert runs on the host and is the only part that
sees the DNS credentials: it reads them as lego does for the provider,
from the environment or from cert.env in BERTHLIGHT_ACME_DIR
(~/.config/berthlight; KEY=VALUE lines, chmod 600).

The first run creates an ACME account, which means agreeing to the CA's
terms of service: without --accept-tos it stops and shows where they are.
--install-timer renews daily from then on: a systemd user timer on Linux,
a launchd agent on macOS.`,
		Example: `  # try it out first, with Let's Encrypt's staging CA
  berthlight cert --dns cloudflare --server letsencrypt-staging --accept-tos

  # the first time: get it, and renew it daily from then on
  berthlight cert --dns gcloud --accept-tos --install-timer

  # renew by hand, with BERTHLIGHT_DNS_PROVIDER set
  berthlight cert`,
		Args:              noArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCert(o, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.domain, "domain", "", "base domain the certificate is for, as *.<domain> (default: the first in $BERTHLIGHT_BASE that is not localhost)")
	f.StringVar(&o.dns, "dns", "", "DNS provider, by lego's name: "+strings.Join(acme.Providers(), ", ")+" ($BERTHLIGHT_DNS_PROVIDER)")
	f.StringVar(&o.server, "server", "", "ACME directory URL, or a CA lego knows such as letsencrypt-staging ($BERTHLIGHT_ACME_SERVER; default Let's Encrypt)")
	f.StringVar(&o.email, "email", "", "contact address for the ACME account, optional ($BERTHLIGHT_ACME_EMAIL)")
	f.StringVar(&o.resolvers, "resolvers", "", "DNS servers to check the challenge on, host:port, comma-separated ($BERTHLIGHT_DNS_RESOLVERS; default the system's and the zone's own)")
	f.DurationVar(&o.within, "renew-within", 0, "renew when the certificate expires within this time (default: when a third of its lifetime is left)")
	f.BoolVar(&o.tos, "accept-tos", false, "agree to the CA's terms of service; needed once, when the ACME account is created")
	f.BoolVar(&o.timer, "install-timer", false, "also install a daily renewal: a systemd user timer, or a launchd agent on macOS")
	cmd.RegisterFlagCompletionFunc("dns", fixedCompletions(acme.Providers()))
	cmd.RegisterFlagCompletionFunc("server", fixedCompletions(acme.ServerCodes()))
	cmd.RegisterFlagCompletionFunc("domain", completeOwnBases)
	for _, name := range []string{"email", "resolvers", "renew-within"} {
		cmd.RegisterFlagCompletionFunc(name, cobra.NoFileCompletions)
	}
	return cmd
}

func runCert(o certOptions, stdout, stderr io.Writer) error {
	domain, dns, server, email, resolvers := &o.domain, &o.dns, &o.server, &o.email, &o.resolvers
	within, timer, tos := &o.within, &o.timer, &o.tos
	c, err := LoadConfig()
	if err != nil {
		return err
	}
	envFile := filepath.Join(c.ACMEDir, CertEnvFile)
	if err := loadEnvFile(envFile, stderr); err != nil {
		return err
	}
	or := func(v *string, env string) {
		if *v == "" {
			*v = os.Getenv(env)
		}
	}
	or(dns, "BERTHLIGHT_DNS_PROVIDER")
	or(server, "BERTHLIGHT_ACME_SERVER")
	or(email, "BERTHLIGHT_ACME_EMAIL")
	or(resolvers, "BERTHLIGHT_DNS_RESOLVERS")

	if *domain == "" {
		*domain = ownBase(c.Bases)
	}
	*domain = strings.Trim(strings.ToLower(*domain), ".")
	if *domain == "" {
		return errors.New("no base domain of your own: set BERTHLIGHT_BASE to it, e.g. dev.example.com (or dev.example.com,localhost to keep both), or pass --domain")
	}
	if err := checkDomain(*domain); err != nil {
		return err
	}
	if *dns == "" {
		fmt.Fprintf(stderr, "berthlight: which DNS provider holds %s? Pass --dns, one of: %s\n", *domain, strings.Join(acme.Providers(), ", "))
		fmt.Fprintf(stderr, "Its credentials go in the environment, or in %s, as lego documents for the provider.\n", envFile)
		return errUsage
	}

	opts := acme.Options{
		Domain:      *domain,
		Provider:    *dns,
		Server:      *server,
		Email:       *email,
		Resolvers:   splitList(*resolvers),
		AccountDir:  c.ACMEDir,
		StateDir:    c.StateDir,
		RenewWithin: *within,
		AcceptTOS:   *tos,
		Log:         slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := acme.Ensure(ctx, opts)
	var terms *acme.TermsError
	if errors.As(err, &terms) {
		return fmt.Errorf("%w\nRead them, then run this again with --accept-tos. Later runs, and the renewal timer, use the account and need no flag", err)
	}
	if err != nil {
		return err
	}
	day := func(t time.Time) string { return t.Local().Format("2006-01-02") }
	if res.Obtained {
		fmt.Fprintf(stdout, "obtained a certificate for *.%s, valid until %s; renewal is due from %s\n", *domain, day(res.NotAfter), day(res.RenewAt))
	} else {
		fmt.Fprintf(stdout, "the certificate for *.%s is valid until %s; renewal is due from %s\n", *domain, day(res.NotAfter), day(res.RenewAt))
	}
	if err := proxyReadsCerts(c, stderr); err != nil {
		return err
	}
	if !slices.Contains(c.Bases, *domain) {
		fmt.Fprintf(stderr, "berthlight: %s is not in BERTHLIGHT_BASE (%s); add it, then restart the proxy (berthlight down && berthlight up), so it accepts the names\n", *domain, strings.Join(c.Bases, ","))
	}
	if res.Obtained {
		fmt.Fprintf(stdout, "The proxy serves it within a second, and redirects http to https for *.%s.\n", *domain)
		fmt.Fprintf(stdout, "The names need a DNS record: *.%s pointing at 127.0.0.1 (A) and ::1 (AAAA).\n", *domain)
	}
	if *timer {
		args := []string{"cert", "--domain", *domain, "--dns", *dns}
		for _, f := range []struct{ name, v string }{{"server", *server}, {"email", *email}, {"resolvers", *resolvers}} {
			if f.v != "" {
				args = append(args, "--"+f.name, f.v)
			}
		}
		if *within != 0 {
			args = append(args, "--renew-within", within.String())
		}
		return installTimer(c, args, envFile, stdout)
	}
	return nil
}

// ownBase is the first base that is not localhost: one the user owns.
func ownBase(bases []string) string {
	for _, b := range bases {
		if b != "localhost" && !strings.HasSuffix(b, ".localhost") {
			return b
		}
	}
	return ""
}

// checkDomain refuses names no public CA issues for (PRD 0001, naming
// convention).
func checkDomain(d string) error {
	if d == "localhost" || strings.HasSuffix(d, ".localhost") {
		return fmt.Errorf("%s: no public CA issues certificates for localhost; use a domain you own, such as dev.example.com", d)
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return fmt.Errorf("%s: use a domain you own with at least two labels, such as dev.example.com", d)
	}
	for _, l := range labels {
		if !validLabel(l) {
			return fmt.Errorf("%s: %q is not a valid DNS label", d, l)
		}
	}
	return nil
}

func validLabel(l string) bool {
	if len(l) == 0 || len(l) > naming.MaxLabel || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	for _, r := range l {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// proxyReadsCerts makes sure a running proxy has the group that lets it
// read the key. A proxy started by a berthlight before R7 does not; it is
// recreated, as `rm` does, with its routes.
func proxyReadsCerts(c Config, stderr io.Writer) error {
	run, err := rt.Detect()
	if err != nil {
		return nil // no runtime, no proxy: it gets the group when it starts
	}
	if exists, running := run.ContainerState(c.ProxyName); !exists || !running {
		return nil
	}
	want := run.ProxyGroup()
	if want == "" {
		return nil
	}
	groups, err := run.ContainerGroups(c.ProxyName)
	if err != nil {
		return err
	}
	if slices.Contains(groups, want) {
		return nil
	}
	fmt.Fprintf(stderr, "berthlight: restarting proxy %s, so it can read the certificate key\n", c.ProxyName)
	if err := run.RemoveContainer(c.ProxyName); err != nil {
		return err
	}
	_, err = up(c, run, nil)
	return err
}

// loadEnvFile sets the variables in path that the environment does not
// already have. Lines are KEY=VALUE, optionally with `export ` in front
// and the value in quotes; # starts a comment. A missing file is fine.
func loadEnvFile(path string, stderr io.Writer) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Mode().Perm()&0o077 != 0 && os.Getegid() >= 0 {
		fmt.Fprintf(stderr, "berthlight: %s holds credentials but others may read it; chmod 600 it\n", path)
	}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" || strings.ContainsAny(k, " \t") {
			return fmt.Errorf("%s:%d: not KEY=VALUE", path, n)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			if v[0] == '"' {
				if u, err := strconv.Unquote(v); err == nil {
					v = u
				} else {
					v = v[1 : len(v)-1]
				}
			} else {
				v = v[1 : len(v)-1]
			}
		}
		if _, set := os.LookupEnv(k); !set {
			if err := os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}
