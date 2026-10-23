package status

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"stormlantern.nl/berthlight/internal/proxy"
	"stormlantern.nl/berthlight/internal/route"
)

type fakeRuntime struct {
	exists, running bool
	proxyNets       []string
	networks        map[string]bool
}

func (f fakeRuntime) ContainerState(string) (bool, bool)         { return f.exists, f.running }
func (f fakeRuntime) ContainerNetworks(string) ([]string, error) { return f.proxyNets, nil }
func (f fakeRuntime) NetworkExists(n string) bool                { return f.networks[n] }

// fakeProxy answers like the proxy would for each route name.
func fakeProxy(t *testing.T) int {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		label, _, _ := strings.Cut(r.Host, ".")
		switch label {
		case "ok", "custom":
			w.WriteHeader(http.StatusOK)
		case "apperror":
			w.WriteHeader(http.StatusBadGateway) // the app's own 502
		case "redirect":
			http.Redirect(w, r, "/login", http.StatusFound)
		case "slow":
			time.Sleep(500 * time.Millisecond)
		case "noroute":
			w.Header().Set(proxy.ErrorHeader, proxy.ErrNoRoute)
			w.WriteHeader(http.StatusNotFound)
		default: // unresolved, refused, timeout, upstream
			w.Header().Set(proxy.ErrorHeader, label)
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	_, p, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(p)
	return port
}

func table(names ...string) *route.Table {
	t := &route.Table{}
	for _, n := range names {
		t.Put(route.Route{Name: n, Target: n, Port: 8080})
	}
	return t
}

func healthy(names ...string) fakeRuntime {
	f := fakeRuntime{exists: true, running: true, proxyNets: []string{"berthlight"}, networks: map[string]bool{}}
	for _, n := range names {
		f.proxyNets = append(f.proxyNets, "bl-"+n)
		f.networks["bl-"+n] = true
	}
	return f
}

func checker(rt Runtime, port int) Checker {
	return Checker{
		Runtime: rt, Bin: "podman", ProxyName: "berthlight", Port: port,
		Bases:   []string{"localhost"},
		Timeout: 200 * time.Millisecond,
		Resolve: func(ctx context.Context, host string) ([]string, error) {
			return nil, errors.New("unexpected lookup of " + host)
		},
	}
}

func TestProxyProblems(t *testing.T) {
	port := fakeProxy(t)
	closed := func() int {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}()
	tests := []struct {
		name string
		rt   fakeRuntime
		port int
		want string
	}{
		{"missing", fakeRuntime{}, port, "does not exist; start it with: berthlight up"},
		{"stopped", fakeRuntime{exists: true}, port, "has stopped"},
		{"port closed", healthy("ok"), closed, "nothing answers on 127.0.0.1:"},
	}
	for _, tt := range tests {
		rep := checker(tt.rt, tt.port).Check(context.Background(), table("ok"))
		if !strings.Contains(rep.Proxy, tt.want) {
			t.Errorf("%s: proxy %q, want it to contain %q", tt.name, rep.Proxy, tt.want)
		}
		if rep.OK() || rep.Routes[0].OK || !strings.Contains(rep.Routes[0].Detail, "proxy is not working") {
			t.Errorf("%s: routes %+v", tt.name, rep.Routes)
		}
	}
}

func TestRouteProblems(t *testing.T) {
	port := fakeProxy(t)
	tests := []struct {
		route string
		rt    fakeRuntime
		ok    bool
		want  string
	}{
		{"ok", healthy("ok"), true, "HTTP 200"},
		{"apperror", healthy("apperror"), true, "HTTP 502"},
		{"redirect", healthy("redirect"), true, "HTTP 302"},
		{"gone", healthy(), false, "network bl-gone does not exist"},
		{"detached", func() fakeRuntime { f := healthy(); f.networks["bl-detached"] = true; return f }(), false, "the proxy is not on network bl-detached"},
		{"noroute", healthy("noroute"), false, "has not loaded this route"},
		{"unresolved", healthy("unresolved"), false, `no container answers to "unresolved" on network bl-unresolved`},
		{"refused", healthy("refused"), false, "nothing listens on port 8080"},
		{"timeout", healthy("timeout"), false, "did not answer on port 8080 in time"},
		{"upstream", healthy("upstream"), false, "could not be reached on port 8080"},
		{"slow", healthy("slow"), false, "may still be starting"},
	}
	seen := map[string]string{}
	for _, tt := range tests {
		rep := checker(tt.rt, port).Check(context.Background(), table(tt.route))
		if rep.Proxy != "" {
			t.Fatalf("%s: proxy %q", tt.route, rep.Proxy)
		}
		rs := rep.Routes[0]
		if rs.OK != tt.ok || !strings.Contains(rs.Detail, tt.want) {
			t.Errorf("%s: ok=%v %q, want ok=%v and %q", tt.route, rs.OK, rs.Detail, tt.ok, tt.want)
		}
		if rep.OK() != tt.ok {
			t.Errorf("%s: report OK %v", tt.route, rep.OK())
		}
		if rs.URL != "http://"+tt.route+".localhost:"+strconv.Itoa(port) {
			t.Errorf("%s: url %q", tt.route, rs.URL)
		}
		// Each failure has its own message.
		if !tt.ok {
			if other, dup := seen[tt.want]; dup {
				t.Errorf("%s and %s share a message", other, tt.route)
			}
			seen[tt.want] = tt.route
		}
	}
}

func TestResolution(t *testing.T) {
	port := fakeProxy(t)
	tests := []struct {
		addrs []string
		err   error
		ok    bool
		want  string
	}{
		{[]string{"127.0.0.1", "::1"}, nil, true, "HTTP 200"},
		{nil, errors.New("no such host"), false, "custom.dev.example.com does not resolve"},
		{[]string{"192.0.2.7"}, nil, false, "resolves to 192.0.2.7, not to this machine"},
	}
	for _, tt := range tests {
		c := checker(healthy("custom"), port)
		c.Bases = []string{"dev.example.com"}
		c.Resolve = func(ctx context.Context, host string) ([]string, error) { return tt.addrs, tt.err }
		rs := c.Check(context.Background(), table("custom")).Routes[0]
		if rs.OK != tt.ok || !strings.Contains(rs.Detail, tt.want) {
			t.Errorf("%v %v: ok=%v %q, want %q", tt.addrs, tt.err, rs.OK, rs.Detail, tt.want)
		}
	}
}

func TestNoRoutes(t *testing.T) {
	rep := checker(healthy(), fakeProxy(t)).Check(context.Background(), &route.Table{})
	if !rep.OK() || len(rep.Routes) != 0 {
		t.Errorf("%+v", rep)
	}
}
