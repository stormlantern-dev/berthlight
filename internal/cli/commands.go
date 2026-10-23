package cli

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"

	"stormlantern.nl/berthlight/internal/naming"
	"stormlantern.nl/berthlight/internal/proxy"
	"stormlantern.nl/berthlight/internal/route"
	rt "stormlantern.nl/berthlight/internal/runtime"
)

func urlFor(c Config, name string) string {
	return "http://" + naming.Host(name, c.Bases[0]) + ":" + strconv.Itoa(c.Port)
}

func cmdAdd(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("add", stderr)
	target := fs.String("target", "", "container network alias (default: the name)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return errUsage
	}
	if len(pos) != 2 {
		fmt.Fprintln(stderr, "usage: berthlight add <name> <port> [--target alias]")
		return errUsage
	}
	port, err := strconv.Atoi(pos[1])
	if err != nil {
		return fmt.Errorf("port %q is not a number", pos[1])
	}
	r := route.Route{Name: pos[0], Target: pos[0], Port: port}
	if *target != "" {
		r.Target = *target
	}
	if err := r.Validate(); err != nil {
		return err
	}
	c, err := LoadConfig()
	if err != nil {
		return err
	}
	run, err := rt.Detect()
	if err != nil {
		return err
	}
	if err := up(c, run); err != nil {
		return err
	}
	if err := run.EnsureNetwork(r.Network(), "route"); err != nil {
		return err
	}
	if err := run.Connect(r.Network(), c.ProxyName); err != nil {
		return err
	}
	tab, err := route.Load(c.StateDir)
	if err != nil {
		return err
	}
	tab.Put(r)
	if err := route.Save(c.StateDir, tab); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s -> %s\n", urlFor(c, r.Name), r.Addr())
	fmt.Fprintf(stdout, "Join the container to it: --network %s --network-alias %s\n", r.Network(), r.Target)
	return nil
}

func cmdRm(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("rm", stderr)
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: berthlight rm <name>")
		return errUsage
	}
	name := fs.Arg(0)
	c, err := LoadConfig()
	if err != nil {
		return err
	}
	tab, err := route.Load(c.StateDir)
	if err != nil {
		return err
	}
	if !tab.Remove(name) {
		return fmt.Errorf("no route named %q", name)
	}
	if err := route.Save(c.StateDir, tab); err != nil {
		return err
	}
	run, err := rt.Detect()
	if err != nil {
		return err
	}
	network := route.NetworkName(name)
	if exists, _ := run.ContainerState(c.ProxyName); exists {
		if err := run.Disconnect(network, c.ProxyName); err != nil {
			return err
		}
	}
	if run.NetworkExists(network) {
		if err := run.RemoveNetwork(network); err != nil {
			fmt.Fprintf(stderr, "berthlight: kept network %s, still in use by a container\n", network)
		}
	}
	fmt.Fprintf(stdout, "removed %s\n", name)
	return nil
}

func cmdLs(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("ls", stderr)
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	c, err := LoadConfig()
	if err != nil {
		return err
	}
	tab, err := route.Load(c.StateDir)
	if err != nil {
		return err
	}
	if len(tab.Routes) == 0 {
		fmt.Fprintln(stdout, "No routes. Add one with: berthlight add <name> <port>")
		return nil
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tURL\tTARGET\tNETWORK")
	for _, r := range tab.Routes {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Name, urlFor(c, r.Name), r.Addr(), r.Network())
	}
	return w.Flush()
}

func cmdUp(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("up", stderr)
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	c, err := LoadConfig()
	if err != nil {
		return err
	}
	run, err := rt.Detect()
	if err != nil {
		return err
	}
	if err := up(c, run); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "proxy %s listening on 127.0.0.1:%d\n", c.ProxyName, c.Port)
	return nil
}

// up makes sure the proxy runs and is connected to every route's network.
// A stopped proxy is recreated rather than started: the proxy listens only
// on the networks it has at start (see proxy.ListenStartupInterfaces), so it
// must start on its own network alone.
func up(c Config, run rt.Runtime) error {
	exists, running := run.ContainerState(c.ProxyName)
	if exists && !running {
		if err := run.RemoveContainer(c.ProxyName); err != nil {
			return err
		}
		exists = false
	}
	if !exists {
		if err := os.MkdirAll(c.StateDir, 0o755); err != nil {
			return err
		}
		spec := rt.ProxySpec{Name: c.ProxyName, Image: c.Image, HostPort: c.Port, StateDir: c.StateDir, Bases: c.Bases}
		if err := run.StartProxy(spec); err != nil {
			return err
		}
		if err := waitForPort(c.Port, 10*time.Second); err != nil {
			return err
		}
	}
	tab, err := route.Load(c.StateDir)
	if err != nil {
		return err
	}
	for _, r := range tab.Routes {
		if err := run.EnsureNetwork(r.Network(), "route"); err != nil {
			return err
		}
		if err := run.Connect(r.Network(), c.ProxyName); err != nil {
			return err
		}
	}
	return nil
}

func waitForPort(port int, timeout time.Duration) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(timeout)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("proxy did not start listening on %s: %w", addr, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func cmdDown(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("down", stderr)
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	c, err := LoadConfig()
	if err != nil {
		return err
	}
	run, err := rt.Detect()
	if err != nil {
		return err
	}
	if err := run.RemoveContainer(c.ProxyName); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "proxy %s stopped; routes kept\n", c.ProxyName)
	return nil
}

func cmdURL(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("url", stderr)
	project := fs.String("project", "", "project name (default: the main worktree's directory name)")
	base := fs.String("base", "", "base domain (default: the first of $BERTHLIGHT_BASE, or localhost)")
	port := fs.Int("port", 0, "proxy port (default: $BERTHLIGHT_PORT, or 7480)")
	dir := fs.String("C", ".", "directory inside the worktree")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	c, err := LoadConfig()
	if err != nil {
		return err
	}
	if *base != "" {
		c.Bases = []string{*base}
	}
	if *port != 0 {
		c.Port = *port
	}
	label, err := naming.LabelFor(*dir, *project)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, urlFor(c, label))
	return nil
}

// cmdProxy is the server inside the proxy container.
func cmdProxy(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("proxy", stderr)
	state := fs.String("state", "/state", "directory with routes.json")
	port := fs.Int("port", rt.ContainerPort, "port to listen on")
	listen := fs.String("listen", "", "listen on this address only, e.g. 127.0.0.1:7480 (default: every address the container has at start)")
	var bases multiFlag
	fs.Var(&bases, "base", "accepted base domain (repeatable; default localhost)")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	logger := log.New(stderr, "berthlight proxy: ", log.LstdFlags)

	h := proxy.NewHandler(bases...)
	h.Logf = logger.Printf

	var ls []net.Listener
	if *listen != "" {
		l, err := net.Listen("tcp", *listen)
		if err != nil {
			return err
		}
		ls = []net.Listener{l}
	} else {
		var err error
		if ls, err = proxy.ListenStartupInterfaces(*port); err != nil {
			return err
		}
	}
	for _, l := range ls {
		logger.Printf("listening on %s", l.Addr())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go route.Watch(ctx, *state, 500*time.Millisecond,
		func(t *route.Table) {
			h.SetTable(t)
			logger.Printf("%d route(s) loaded", len(t.Routes))
		},
		func(err error) { logger.Printf("keeping previous routes: %v", err) })

	srv := &proxy.Server{Handler: h, Logf: logger.Printf}
	go func() {
		<-ctx.Done()
		srv.Close()
		for _, l := range ls {
			l.Close()
		}
	}()
	return srv.Serve(ls...)
}

type multiFlag []string

func (m *multiFlag) String() string     { return fmt.Sprint([]string(*m)) }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
