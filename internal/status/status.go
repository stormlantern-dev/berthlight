// Package status checks the proxy and each route, and says in plain words
// what is wrong when something is (PRD 0001, R6).
package status

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"stormlantern.nl/berthlight/internal/naming"
	"stormlantern.nl/berthlight/internal/proxy"
	"stormlantern.nl/berthlight/internal/route"
)

// Runtime is what status needs from the container runtime.
type Runtime interface {
	ContainerState(name string) (exists, running bool)
	ContainerNetworks(name string) ([]string, error)
	NetworkExists(name string) bool
}

// Checker checks berthlight's state.
type Checker struct {
	Runtime   Runtime
	Bin       string // runtime executable, for commands in messages
	ProxyName string
	Port      int
	Bases     []string
	// Resolve looks up a host name. Default: the system resolver.
	Resolve func(ctx context.Context, host string) ([]string, error)
	// Timeout bounds each probe. Default 10 s: a dev server's first
	// request may compile the application.
	Timeout time.Duration
	// Reload is how long to wait before asking again when the proxy has
	// not loaded a route: it rereads routes.json every 500 ms, so a route
	// added a moment ago may not be in yet. Default 600 ms.
	Reload time.Duration
}

// Report is the outcome of a check.
type Report struct {
	// Proxy is empty when the proxy runs and answers, else what is wrong.
	Proxy  string
	Routes []RouteStatus
}

// OK reports whether the proxy and every route work.
func (r Report) OK() bool {
	if r.Proxy != "" {
		return false
	}
	for _, rs := range r.Routes {
		if !rs.OK {
			return false
		}
	}
	return true
}

// RouteStatus is one route's state.
type RouteStatus struct {
	Route route.Route
	URL   string
	OK    bool
	// Detail is the HTTP status when OK, else what is wrong.
	Detail string
}

// Check checks the proxy and every route in t.
func (c Checker) Check(ctx context.Context, t *route.Table) Report {
	if c.Resolve == nil {
		c.Resolve = net.DefaultResolver.LookupHost
	}
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Second
	}
	if c.Reload == 0 {
		c.Reload = 600 * time.Millisecond
	}
	var rep Report
	rep.Proxy = c.checkProxy()
	var nets []string
	if rep.Proxy == "" {
		var err error
		if nets, err = c.Runtime.ContainerNetworks(c.ProxyName); err != nil {
			rep.Proxy = fmt.Sprintf("cannot read the proxy's networks: %v", err)
		}
	}
	rep.Routes = make([]RouteStatus, len(t.Routes))
	var wg sync.WaitGroup
	for i, r := range t.Routes {
		rep.Routes[i] = RouteStatus{Route: r, URL: c.url(r.Name)}
		if rep.Proxy != "" {
			rep.Routes[i].Detail = "unknown: the proxy is not working"
			continue
		}
		wg.Go(func() { c.checkRoute(ctx, &rep.Routes[i], nets) })
	}
	wg.Wait()
	return rep
}

func (c Checker) url(name string) string {
	return "http://" + naming.Host(name, c.Bases[0]) + ":" + strconv.Itoa(c.Port)
}

func (c Checker) checkProxy() string {
	exists, running := c.Runtime.ContainerState(c.ProxyName)
	switch {
	case !exists:
		return fmt.Sprintf("proxy container %s does not exist; start it with: berthlight up", c.ProxyName)
	case !running:
		return fmt.Sprintf("proxy container %s has stopped; start it with: berthlight up (its logs: %s logs %s)", c.ProxyName, c.Bin, c.ProxyName)
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(c.Port))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return fmt.Sprintf("proxy container %s runs, but nothing answers on %s (%v); is BERTHLIGHT_PORT the port it was started with? Restart it with: berthlight down && berthlight up", c.ProxyName, addr, err)
	}
	conn.Close()
	return ""
}

func (c Checker) checkRoute(ctx context.Context, rs *RouteStatus, proxyNets []string) {
	r := rs.Route
	network := r.Network()
	fail := func(format string, args ...any) { rs.Detail = fmt.Sprintf(format, args...) }
	if !c.Runtime.NetworkExists(network) {
		fail("network %s does not exist; start the devcontainer (its initializeCommand creates it) or run: berthlight add %s %d", network, r.Name, r.Port)
		return
	}
	if !slices.Contains(proxyNets, network) {
		fail("the proxy is not on network %s; run: berthlight up", network)
		return
	}
	host := naming.Host(r.Name, c.Bases[0])
	if msg := c.checkResolves(ctx, host); msg != "" {
		fail("%s", msg)
		return
	}
	code, reason, err := c.probe(ctx, host)
	if err == nil && reason == proxy.ErrNoRoute {
		select { // a route added a moment ago: give the proxy its reload
		case <-ctx.Done():
		case <-time.After(c.Reload):
			code, reason, err = c.probe(ctx, host)
		}
	}
	switch {
	case err != nil:
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			fail("no answer within %s from %s; the dev server may still be starting", c.Timeout, r.Addr())
		} else {
			fail("request through the proxy failed: %v", err)
		}
	case reason == proxy.ErrNoRoute:
		fail("the proxy has not loaded this route; see its logs: %s logs %s", c.Bin, c.ProxyName)
	case reason == proxy.ErrUnresolved:
		fail("no container answers to %q on network %s; is the devcontainer running, with --network %s --network-alias %s?", r.Target, network, network, r.Target)
	case reason == proxy.ErrRefused:
		fail("container %s runs, but nothing listens on port %d; start the dev server, listening on 0.0.0.0 (not 127.0.0.1)", r.Target, r.Port)
	case reason == proxy.ErrTimeout:
		fail("container %s did not answer on port %d in time", r.Target, r.Port)
	case reason != "":
		fail("container %s could not be reached on port %d; see the proxy's logs: %s logs %s", r.Target, r.Port, c.Bin, c.ProxyName)
	default:
		rs.OK = true
		rs.Detail = fmt.Sprintf("HTTP %d", code)
	}
}

// checkResolves checks that a name under a custom base resolves to this
// machine. Names under localhost need no DNS: browsers and curl send them
// to loopback themselves (RFC 6761), though the system resolver may not.
func (c Checker) checkResolves(ctx context.Context, host string) string {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	addrs, err := c.Resolve(ctx, host)
	if err != nil || len(addrs) == 0 {
		return fmt.Sprintf("%s does not resolve (%v); it needs a DNS record pointing at 127.0.0.1, and some routers drop such answers (DNS rebinding protection)", host, err)
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip == nil || !ip.IsLoopback() {
			return fmt.Sprintf("%s resolves to %s, not to this machine; its DNS record must point at 127.0.0.1", host, strings.Join(addrs, ", "))
		}
	}
	return ""
}

// probe sends a HEAD request through the proxy and returns the status and
// the proxy's error reason, if the proxy made the response itself.
func (c Checker) probe(ctx context.Context, host string) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(c.Port))+"/", nil)
	if err != nil {
		return 0, "", err
	}
	req.Host = host + ":" + strconv.Itoa(c.Port)
	client := &http.Client{
		// A redirect is an answer; do not follow it.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get(proxy.ErrorHeader), nil
}
