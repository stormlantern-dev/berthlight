package proxy

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"stormlantern.nl/berthlight/internal/route"
)

func backendRoute(t *testing.T, name string, h http.Handler) route.Route {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	host, port, _ := net.SplitHostPort(u.Host)
	p, _ := strconv.Atoi(port)
	return route.Route{Name: name, Target: host, Port: p}
}

func TestLabel(t *testing.T) {
	h := NewHandler("localhost", "dev.example.com")
	tests := map[string]string{
		"shop.localhost":               "shop",
		"shop.localhost:7480":          "shop",
		"SHOP.LocalHost.":              "shop",
		"shop-fix.dev.example.com":     "shop-fix",
		"a.shop.localhost":             "",
		"localhost":                    "",
		".localhost":                   "",
		"shop.example.org":             "",
		"[::1]:7480":                   "",
		"shop.dev.example.com.evil.io": "",
	}
	for in, want := range tests {
		if got := h.Label(in); got != want {
			t.Errorf("Label(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRouting(t *testing.T) {
	var gotHost, gotXFH string
	rt := backendRoute(t, "shop", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotXFH = r.Host, r.Header.Get("X-Forwarded-Host")
		fmt.Fprint(w, "shop says hi")
	}))
	h := NewHandler()
	tab := &route.Table{}
	tab.Put(rt)
	h.SetTable(tab)
	front := httptest.NewServer(h)
	defer front.Close()

	get := func(host string) (int, string) {
		req, _ := http.NewRequest("GET", front.URL+"/x", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, body := get("shop.localhost:7480"); code != 200 || body != "shop says hi" {
		t.Errorf("shop: %d %q", code, body)
	}
	if gotHost != "shop.localhost:7480" || gotXFH != "shop.localhost:7480" {
		t.Errorf("upstream saw Host %q, X-Forwarded-Host %q; want the original host", gotHost, gotXFH)
	}

	code, body := get("nope.localhost:7480")
	if code != http.StatusNotFound || !strings.Contains(body, `no route for "nope.localhost:7480"`) || !strings.Contains(body, "shop.localhost -> ") {
		t.Errorf("unknown host: %d %q", code, body)
	}

	h.SetTable(&route.Table{})
	if code, body := get("nope.localhost"); code != 404 || !strings.Contains(body, "berthlight add") {
		t.Errorf("empty table: %d %q", code, body)
	}

	// An upstream that does not answer is a 502 that names the route.
	dead := &route.Table{}
	dead.Put(route.Route{Name: "dead", Target: "127.0.0.1", Port: freePort(t)})
	h.SetTable(dead)
	h.Logf = func(string, ...any) {}
	code, body = get("dead.localhost")
	if code != http.StatusBadGateway || !strings.Contains(body, `route "dead" goes to 127.0.0.1:`) || !strings.Contains(body, "0.0.0.0") {
		t.Errorf("dead upstream: %d %q", code, body)
	}
}

// TestErrorHeader: the proxy marks its own error responses with the
// reason, so status can tell them from a dev server's (PRD 0001, R6).
func TestErrorHeader(t *testing.T) {
	h := NewHandler()
	h.Logf = func(string, ...any) {}
	tab := &route.Table{}
	tab.Put(route.Route{Name: "refused", Target: "127.0.0.1", Port: freePort(t)})
	tab.Put(route.Route{Name: "unresolved", Target: "no-such-host.invalid", Port: 8080})
	tab.Put(backendRoute(t, "fine", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "app's own 502", http.StatusBadGateway)
	})))
	h.SetTable(tab)
	front := httptest.NewServer(h)
	defer front.Close()
	for host, want := range map[string]string{
		"nope.localhost":       ErrNoRoute,
		"refused.localhost":    ErrRefused,
		"unresolved.localhost": ErrUnresolved,
		"fine.localhost":       "",
	} {
		req, _ := http.NewRequest("GET", front.URL, nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := resp.Header.Get(ErrorHeader); got != want {
			t.Errorf("%s: %s %q, want %q", host, ErrorHeader, got, want)
		}
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// wsEcho is a minimal WebSocket server: it completes the handshake and then
// echoes raw bytes, which is all the proxy needs to pass through.
func wsEcho() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "want upgrade", 400)
			return
		}
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		c, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
			base64.StdEncoding.EncodeToString(sum[:]))
		rw.Flush()
		io.Copy(c, rw)
	})
}

func TestWebSocket(t *testing.T) {
	h := NewHandler()
	tab := &route.Table{}
	tab.Put(backendRoute(t, "hmr", wsEcho()))
	h.SetTable(tab)
	front := httptest.NewServer(h)
	defer front.Close()

	c, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(c, "GET /hmr HTTP/1.1\r\nHost: hmr.localhost:7480\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status %d, want 101", resp.StatusCode)
	}
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Errorf("accept %q", got)
	}
	fmt.Fprint(c, "ping through the proxy")
	buf := make([]byte, len("ping through the proxy"))
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping through the proxy" {
		t.Errorf("echo %q", buf)
	}
}

func TestServerSniffsTLS(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	tab := &route.Table{}
	tab.Put(backendRoute(t, "shop", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })))
	h.SetTable(tab)
	s := &Server{Handler: h, Logf: func(string, ...any) {}}
	done := make(chan error, 1)
	go func() { done <- s.Serve(l) }()
	defer func() {
		s.Close()
		l.Close()
	}()

	// Plain HTTP is served.
	req, _ := http.NewRequest("GET", "http://"+l.Addr().String()+"/", nil)
	req.Host = "shop.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "ok" {
		t.Errorf("http body %q", b)
	}

	// A TLS handshake without a certificate configured is closed, not
	// answered with HTTP.
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(2 * time.Second))
	c.Write([]byte{0x16, 0x03, 0x01, 0x00, 0x05, 1, 0, 0, 1, 0})
	n, err := c.Read(make([]byte, 16))
	if n != 0 || err == nil {
		t.Errorf("TLS without certificate: read %d bytes, err %v; want closed", n, err)
	}
	c.Close()
}
