package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"syscall"
	"time"

	"stormlantern.nl/berthlight/internal/devcontainer"
	"stormlantern.nl/berthlight/internal/naming"
	"stormlantern.nl/berthlight/internal/proxy"
	"stormlantern.nl/berthlight/internal/route"
	rt "stormlantern.nl/berthlight/internal/runtime"
	"stormlantern.nl/berthlight/internal/tui"
)

func urlFor(c Config, name string) string {
	return "http://" + naming.Host(name, c.Bases[0]) + ":" + strconv.Itoa(c.Port)
}

func cmdAdd(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("add", stderr)
	target := fs.String("target", "", "container network alias (default: the name)")
	network := fs.String("network", "", "the route's container network, starting with bl- (default: bl-<name>)")
	project := fs.String("project", "", "project name, when the name comes from the worktree (default: the main worktree's directory name)")
	dir := fs.String("C", ".", "directory inside the worktree, when the name comes from the worktree")
	replace := fs.Bool("replace", false, "take over a name another route holds")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return errUsage
	}
	var name, portArg string
	switch len(pos) {
	case 1:
		portArg = pos[0]
		if name, err = naming.LabelFor(*dir, *project); err != nil {
			return fmt.Errorf("deriving the name from the worktree: %w", err)
		}
	case 2:
		name, portArg = pos[0], pos[1]
	default:
		fmt.Fprintln(stderr, "usage: berthlight add [<name>] <port> [--target alias] [--network bl-…] [--replace]")
		fmt.Fprintln(stderr, "       without <name>, the name is the current worktree's (see berthlight url)")
		return errUsage
	}
	port, err := strconv.Atoi(portArg)
	if err != nil {
		return fmt.Errorf("port %q is not a number", portArg)
	}
	r := route.Route{Name: name, Target: name, Port: port, NetworkName: *network}
	if *target != "" {
		r.Target = *target
	}
	if r.NetworkName == route.NetworkName(r.Name) {
		r.NetworkName = ""
	}
	if err := r.Validate(); err != nil {
		return err
	}
	c, err := LoadConfig()
	if err != nil {
		return err
	}
	tab, err := route.Load(c.StateDir)
	if err != nil {
		return err
	}
	if err := tab.Conflict(r); err != nil {
		return err
	}
	old, exists := tab.Lookup(r.Name)
	if exists && !old.SameOwner(r) && !*replace {
		return fmt.Errorf("route %q already goes to %s on network %s; to take the name over, add --replace", r.Name, old.Addr(), old.Network())
	}
	run, err := rt.Detect()
	if err != nil {
		return err
	}
	if exists && old.Network() != r.Network() {
		if err := removeRoute(c, run, tab, old, stderr); err != nil {
			return err
		}
	}
	boat := tui.Start(stderr, "starting the proxy")
	if err := up(c, run, boat); err != nil {
		boat.Fail(err.Error())
		return err
	}
	boat.Step(0.9, "joining "+r.Network())
	if err := run.EnsureNetwork(r.Network(), "route"); err != nil {
		boat.Fail(err.Error())
		return err
	}
	if err := run.Connect(r.Network(), c.ProxyName); err != nil {
		boat.Fail(err.Error())
		return err
	}
	boat.Done(r.Name + " berthed")
	tab.Put(r)
	if err := route.Save(c.StateDir, tab); err != nil {
		return err
	}
	u := urlFor(c, r.Name)
	fmt.Fprintf(stdout, "%s -> %s\n", tui.NewTerm(stdout).Link(u, u), r.Addr())
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
	r, ok := tab.Lookup(name)
	if !ok {
		return fmt.Errorf("no route named %q", name)
	}
	run, err := rt.Detect()
	if err != nil {
		return err
	}
	if err := removeRoute(c, run, tab, r, stderr); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "removed %s\n", name)
	return nil
}

// removeRoute deletes r from the table, saves it, takes the proxy off the
// route's network and removes the network if nothing else uses it.
func removeRoute(c Config, run rt.Runtime, tab *route.Table, r route.Route, stderr io.Writer) error {
	tab.Remove(r.Name)
	if err := route.Save(c.StateDir, tab); err != nil {
		return err
	}
	network := r.Network()
	// Recreate the proxy rather than disconnect it: on rootless Podman, a
	// disconnect breaks the published port's forwarding. The new proxy
	// starts on its own network and joins the remaining routes' networks.
	if exists, _ := run.ContainerState(c.ProxyName); exists {
		nets, err := run.ContainerNetworks(c.ProxyName)
		if err != nil {
			return err
		}
		if slices.Contains(nets, network) {
			if err := run.RemoveContainer(c.ProxyName); err != nil {
				return err
			}
			if err := up(c, run, nil); err != nil {
				return err
			}
		}
	}
	if run.NetworkExists(network) {
		if err := run.RemoveNetwork(network); err != nil {
			fmt.Fprintf(stderr, "berthlight: kept network %s, still in use by a container\n", network)
		}
	}
	return nil
}

func cmdLs(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("ls", stderr)
	asJSON := fs.Bool("json", false, "print the routes as JSON")
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
	if *asJSON {
		out := struct {
			Routes []routeJSON `json:"routes"`
		}{Routes: []routeJSON{}}
		for _, r := range tab.Routes {
			out.Routes = append(out.Routes, newRouteJSON(c, r))
		}
		return writeJSON(stdout, out)
	}
	if len(tab.Routes) == 0 {
		fmt.Fprintln(stdout, "No routes. Add one with: berthlight add <name> <port>")
		return nil
	}
	term := tui.NewTerm(stdout)
	rows := [][]tui.Cell{{{Text: "NAME"}, {Text: "URL"}, {Text: "TARGET"}, {Text: "NETWORK"}}}
	for _, r := range tab.Routes {
		u := urlFor(c, r.Name)
		rows = append(rows, []tui.Cell{{Text: r.Name}, {Text: u, Show: term.Link(u, u)}, {Text: r.Addr()}, {Text: r.Network()}})
	}
	return tui.Table(stdout, rows)
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
	boat := tui.Start(stderr, "starting the proxy")
	if err := up(c, run, boat); err != nil {
		boat.Fail(err.Error())
		return err
	}
	boat.Done("proxy berthed")
	fmt.Fprintf(stdout, "proxy %s listening on 127.0.0.1:%d\n", c.ProxyName, c.Port)
	return nil
}

// up makes sure the proxy runs and is connected to every route's network.
// A stopped proxy is recreated rather than started: the proxy listens only
// on the networks it has at start (see proxy.ListenStartupInterfaces), so it
// must start on its own network alone.
//
// boat, which may be nil, shows the progress.
func up(c Config, run rt.Runtime, boat *tui.Progress) error {
	boat.Step(0.05, "looking for the proxy")
	exists, running := run.ContainerState(c.ProxyName)
	if exists && !running {
		if err := run.RemoveContainer(c.ProxyName); err != nil {
			return err
		}
		exists = false
	}
	if !exists {
		if err := route.MakeDir(c.StateDir); err != nil {
			return err
		}
		spec := rt.ProxySpec{Name: c.ProxyName, Image: c.Image, HostPort: c.Port, StateDir: c.StateDir, Bases: c.Bases}
		boat.Step(0.15, "starting the proxy")
		if err := run.StartProxy(spec); err != nil {
			return err
		}
		boat.Step(0.5, "waiting for 127.0.0.1:"+strconv.Itoa(c.Port))
		if err := waitForPort(c.Port, 10*time.Second); err != nil {
			return err
		}
	}
	tab, err := route.Load(c.StateDir)
	if err != nil {
		return err
	}
	for i, r := range tab.Routes {
		boat.Step(0.6+0.3*float64(i)/float64(len(tab.Routes)), "joining "+r.Network())
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
	project := fs.String("project", "", "project name (default: the one in the worktree's devcontainer.json, else the main worktree's directory name)")
	base := fs.String("base", "", "base domain (default: the first of $BERTHLIGHT_BASE, or localhost)")
	port := fs.Int("port", 0, "proxy port (default: $BERTHLIGHT_PORT, or 7480)")
	dir := fs.String("C", ".", "directory inside the worktree")
	cp := fs.Bool("copy", false, "also put the URL on the clipboard, through the terminal (OSC 52)")
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
	if *project == "" {
		*project = wiredProject(*dir)
	}
	label, err := naming.LabelFor(*dir, *project)
	if err != nil {
		return err
	}
	u := urlFor(c, label)
	out := tui.NewTerm(stdout)
	fmt.Fprintln(stdout, out.Link(u, u))
	if *cp {
		// The clipboard sequence goes to whichever stream is the terminal,
		// so `berthlight url --copy > file` still copies.
		term := out
		if !term.TTY() {
			term = tui.NewTerm(stderr)
		}
		if !term.Copy(u) {
			return errors.New("--copy needs a terminal: it asks the terminal to set the clipboard")
		}
		fmt.Fprintln(stderr, "copied to the clipboard (if the terminal allows it)")
	}
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

// wiredProject is the project name the worktree's devcontainer.json gives
// berthlight, if `berthlight devcontainer` wired it, so url names the same
// route the devcontainer gets; "" otherwise.
func wiredProject(dir string) string {
	wt, err := naming.FindWorktree(dir)
	if err != nil {
		return ""
	}
	path, err := findDevcontainer(wt.Dir)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	p, _ := devcontainer.Project(b)
	return p
}
