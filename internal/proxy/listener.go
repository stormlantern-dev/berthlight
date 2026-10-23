package proxy

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// tlsRecordHandshake is the first byte of every TLS connection.
const tlsRecordHandshake = 0x16

// Server serves HTTP and, once configured, HTTPS on the same listeners. It
// tells them apart by the first byte of each connection (PRD 0001, R2).
type Server struct {
	Handler http.Handler
	// TLS, when set, serves connections that start with a TLS handshake.
	// Nil until certificates exist (R7); such connections are closed.
	TLS *tls.Config
	// Logf defaults to log.Printf.
	Logf func(format string, args ...any)

	mu   sync.Mutex
	http *http.Server
	tlsL *chanListener
	httL *chanListener
}

// Serve accepts connections on every listener until Close.
func (s *Server) Serve(ls ...net.Listener) error {
	if s.Logf == nil {
		s.Logf = log.Printf
	}
	s.mu.Lock()
	s.httL = newChanListener(ls[0].Addr())
	s.tlsL = newChanListener(ls[0].Addr())
	s.http = &http.Server{Handler: s.Handler, ReadHeaderTimeout: 10 * time.Second}
	srv := s.http
	s.mu.Unlock()

	errc := make(chan error, len(ls)+2)
	go func() { errc <- srv.Serve(s.httL) }()
	if s.TLS != nil {
		go func() { errc <- srv.Serve(tls.NewListener(s.tlsL, s.TLS)) }()
	}
	for _, l := range ls {
		go func() { errc <- s.accept(l) }()
	}
	err := <-errc
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// Close stops the server.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.http == nil {
		return nil
	}
	s.httL.Close()
	s.tlsL.Close()
	return s.http.Close()
}

func (s *Server) accept(l net.Listener) error {
	defer l.Close()
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go s.dispatch(c)
	}
}

func (s *Server) dispatch(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(c)
	b, err := br.Peek(1)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		c.Close()
		return
	}
	pc := &peekedConn{Conn: c, r: br}
	if b[0] == tlsRecordHandshake {
		if s.TLS == nil {
			s.Logf("TLS connection from %s, but no certificate is configured", c.RemoteAddr())
			c.Close()
			return
		}
		s.tlsL.push(pc)
		return
	}
	s.httL.push(pc)
}

type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// chanListener hands connections that dispatch already accepted to
// http.Server.
type chanListener struct {
	addr net.Addr
	ch   chan net.Conn
	once sync.Once
	done chan struct{}
}

func newChanListener(addr net.Addr) *chanListener {
	return &chanListener{addr: addr, ch: make(chan net.Conn), done: make(chan struct{})}
}

func (l *chanListener) push(c net.Conn) {
	select {
	case l.ch <- c:
	case <-l.done:
		c.Close()
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return l.addr }

// ListenStartupInterfaces listens on port on every non-loopback address the
// container has when the proxy starts. The proxy starts on its own network,
// where the runtime delivers published-port traffic; route networks are
// connected later and get no listener. So a devcontainer on a route network
// cannot use the proxy to reach another route's container (PRD 0001, R1).
func ListenStartupInterfaces(port int) ([]net.Listener, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var ls []net.Listener
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() {
			continue
		}
		l, err := net.Listen("tcp", net.JoinHostPort(ipn.IP.String(), fmt.Sprint(port)))
		if err != nil {
			for _, l := range ls {
				l.Close()
			}
			return nil, err
		}
		ls = append(ls, l)
	}
	if len(ls) == 0 {
		return nil, errors.New("no non-loopback interface to listen on")
	}
	return ls, nil
}
