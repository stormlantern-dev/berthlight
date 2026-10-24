package acme

import (
	"fmt"
	"slices"
	"strings"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/cloudflare"
	"github.com/go-acme/lego/v5/providers/dns/digitalocean"
	"github.com/go-acme/lego/v5/providers/dns/dnsupdate"
	"github.com/go-acme/lego/v5/providers/dns/exec"
	"github.com/go-acme/lego/v5/providers/dns/gcloud"
	"github.com/go-acme/lego/v5/providers/dns/hetzner"
	"github.com/go-acme/lego/v5/providers/dns/httpreq"
	"github.com/go-acme/lego/v5/providers/dns/route53"
)

// providers are the DNS providers berthlight builds in, by lego's names.
// Each reads its configuration and credentials from the environment
// variables lego documents for it (https://go-acme.github.io/lego/dns/),
// including the _FILE variants. Not all of lego's providers: together they
// would make the binary, which is also the proxy image, ten times larger.
// exec, httpreq and dnsupdate (RFC 2136) reach any other DNS service.
var providers = map[string]func() (challenge.Provider, error){
	"cloudflare":   func() (challenge.Provider, error) { return cloudflare.NewDNSProvider() },
	"digitalocean": func() (challenge.Provider, error) { return digitalocean.NewDNSProvider() },
	"dnsupdate":    func() (challenge.Provider, error) { return dnsupdate.NewDNSProvider() },
	"exec":         func() (challenge.Provider, error) { return exec.NewDNSProvider() },
	"gcloud":       func() (challenge.Provider, error) { return gcloud.NewDNSProvider() },
	"hetzner":      func() (challenge.Provider, error) { return hetzner.NewDNSProvider() },
	"httpreq":      func() (challenge.Provider, error) { return httpreq.NewDNSProvider() },
	"route53":      func() (challenge.Provider, error) { return route53.NewDNSProvider() },
}

// aliases are other names lego accepts for the same providers.
var aliases = map[string]string{"rfc2136": "dnsupdate"}

// Providers lists the DNS providers' names.
func Providers() []string {
	var names []string
	for n := range providers {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// NewProvider makes the named DNS provider from its environment variables.
func NewProvider(name string) (challenge.Provider, error) {
	name = strings.ToLower(name)
	if a, ok := aliases[name]; ok {
		name = a
	}
	mk, ok := providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown DNS provider %q; berthlight has %s", name, strings.Join(Providers(), ", "))
	}
	p, err := mk()
	if err != nil {
		return nil, fmt.Errorf("DNS provider %s: %w", name, err)
	}
	return p, nil
}
