// Package proxy is the server inside berthlight's proxy container. It sends
// each request to the route named by the first label of its Host header.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"syscall"

	"stormlantern.nl/berthlight/internal/naming"
	"stormlantern.nl/berthlight/internal/route"
)

type ctxKey struct{}

// ErrorHeader marks a response the proxy made itself, not the dev server,
// so `berthlight status` can tell a proxy 502 from an application's. Its
// value is one of the Err* constants.
const ErrorHeader = "Berthlight-Error"

// Values of ErrorHeader.
const (
	// ErrNoRoute: no route for the name.
	ErrNoRoute = "no-route"
	// ErrUnresolved: the route's alias does not resolve on its network; no
	// container with that alias is running there.
	ErrUnresolved = "unresolved"
	// ErrRefused: the container is there, but nothing listens on the port.
	ErrRefused = "refused"
	// ErrTimeout: the container did not answer in time.
	ErrTimeout = "timeout"
	// ErrUpstream: any other failure to reach the container.
	ErrUpstream = "upstream"
)

// classify names the reason a request to a container failed.
func classify(err error) string {
	var dns *net.DNSError
	var ne net.Error
	switch {
	case errors.As(err, &dns):
		return ErrUnresolved
	case refused(err):
		return ErrRefused
	case errors.As(err, &ne) && ne.Timeout():
		return ErrTimeout
	}
	return ErrUpstream
}

// wsaeconnrefused is Windows' "connection refused", which syscall does not
// map to ECONNREFUSED.
const wsaeconnrefused = 10061

func refused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var errno syscall.Errno
	return goruntime.GOOS == "windows" && errors.As(err, &errno) && errno == wsaeconnrefused
}

// Handler routes requests by Host header. It is safe for concurrent use;
// SetTable swaps the routes without interrupting requests in flight.
type Handler struct {
	bases []string
	table atomic.Pointer[route.Table]
	rp    *httputil.ReverseProxy
	// Logf logs proxy errors. Defaults to log.Printf.
	Logf func(format string, args ...any)
}

// NewHandler returns a handler that accepts names under the given base
// domains (default: localhost).
func NewHandler(bases ...string) *Handler {
	if len(bases) == 0 {
		bases = []string{naming.DefaultBase}
	}
	h := &Handler{Logf: log.Printf}
	for _, b := range bases {
		h.bases = append(h.bases, strings.Trim(strings.ToLower(b), "."))
	}
	h.table.Store(&route.Table{})
	h.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			r := pr.In.Context().Value(ctxKey{}).(route.Route)
			pr.SetURL(&url.URL{Scheme: "http", Host: r.Addr()})
			// Dev servers see the name the browser used, so host checks
			// and absolute URLs use it (PRD 0001, naming convention).
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
		},
		// Stream as it arrives: server-sent events and slow responses
		// from dev servers must not wait for a buffer to fill.
		FlushInterval: -1,
		ErrorHandler:  h.upstreamError,
	}
	return h
}

// SetTable replaces the routes.
func (h *Handler) SetTable(t *route.Table) { h.table.Store(t) }

// Table returns the current routes.
func (h *Handler) Table() *route.Table { return h.table.Load() }

// Label returns the route name for a Host header value, or "" when the host
// is not a single label under one of the handler's bases.
func (h *Handler) Label(host string) string {
	if hp, _, err := net.SplitHostPort(host); err == nil {
		host = hp
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, b := range h.bases {
		if label, ok := strings.CutSuffix(host, "."+b); ok && label != "" && !strings.Contains(label, ".") {
			return label
		}
	}
	return ""
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	label := h.Label(r.Host)
	rt, ok := h.Table().Lookup(label)
	if !ok {
		h.notFound(w, r)
		return
	}
	h.rp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, rt)))
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set(ErrorHeader, ErrNoRoute)
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, "berthlight: no route for %q.\n\n", r.Host)
	routes := h.Table().Routes
	if len(routes) == 0 {
		fmt.Fprintln(w, "There are no routes yet. Add one with: berthlight add <name> <port>")
		return
	}
	fmt.Fprintln(w, "Routes:")
	for _, rt := range routes {
		fmt.Fprintf(w, "  %s -> %s\n", naming.Host(rt.Name, h.bases[0]), rt.Addr())
	}
}

func (h *Handler) upstreamError(w http.ResponseWriter, r *http.Request, err error) {
	rt, _ := r.Context().Value(ctxKey{}).(route.Route)
	if errors.Is(err, context.Canceled) {
		return
	}
	h.Logf("route %s -> %s: %v", rt.Name, rt.Addr(), err)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set(ErrorHeader, classify(err))
	w.WriteHeader(http.StatusBadGateway)
	fmt.Fprintf(w, "berthlight: route %q goes to %s, which did not answer:\n  %v\n\n", rt.Name, rt.Addr(), err)
	fmt.Fprintln(w, "Is the dev server running, listening on 0.0.0.0 (not 127.0.0.1) inside its container, and on that port?")
}
