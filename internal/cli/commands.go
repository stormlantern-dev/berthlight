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

	"github.com/spf13/cobra"
	"stormlantern.nl/berthlight/internal/certs"
	"stormlantern.nl/berthlight/internal/devcontainer"
	"stormlantern.nl/berthlight/internal/naming"
	"stormlantern.nl/berthlight/internal/proxy"
	"stormlantern.nl/berthlight/internal/route"
	rt "stormlantern.nl/berthlight/internal/runtime"
	"stormlantern.nl/berthlight/internal/tui"
)

// urlFor is a route's URL: https once there is a certificate for the
// base (R7), since the proxy then redirects plain HTTP.
func urlFor(c Config, name string) string {
	return scheme(c) + "://" + naming.Host(name, c.Bases[0]) + ":" + strconv.Itoa(c.Port)
}

func scheme(c Config) string {
	if certs.Has(c.StateDir, c.Bases[0]) {
		return "https"
	}
	return "http"
}

type addOptions struct {
	target, network, project, dir string
	replace                       bool
}

func newAddCmd() *cobra.Command {
	var o addOptions
	cmd := &cobra.Command{
		Use:   "add [<name>] <port>",
		Short: "Route <name>.localhost to a container's port",
		Long: `add routes http://<name>.localhost:7480 to <port> on a container, over the
route's own container network, bl-<name> by default. It creates the
network, starts the proxy if it is not running, and joins the proxy to the
network. Then it prints the network and the alias the container must join
with: --network bl-<name> --network-alias <name>.

Without <name>, the name is the current git worktree's: the project name
(--project, default the main worktree's directory name), with the
worktree's suffix for any other worktree, such as shop-fix-login for
../webshop-fix-login. The initializeCommand that 'berthlight devcontainer'
writes runs add this way, so a wired devcontainer needs no add by hand.

A route owns its name, network and alias. add refuses one that another
route holds, and says which; --replace takes a name over.`,
		Example: `  # shop.localhost -> port 8080 of the container with alias shop on network bl-shop
  berthlight add shop 8080

  # the container answers to another alias, on a network of its own choosing
  berthlight add shop 8080 --target web --network bl-webshop

  # what a wired devcontainer's initializeCommand runs, for the worktree in ~/src/webshop
  berthlight add --project shop --target bl-webshop --network bl-webshop -C ~/src/webshop 8080`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdd(o, args, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.target, "target", "", "container network alias (default: the name)")
	f.StringVar(&o.network, "network", "", "the route's container network, starting with bl- (default: bl-<name>)")
	f.StringVar(&o.project, "project", "", "project name, when the name comes from the worktree (default: the main worktree's directory name)")
	f.StringVarP(&o.dir, "dir", "C", ".", "directory inside the worktree, when the name comes from the worktree")
	f.BoolVar(&o.replace, "replace", false, "take over a name another route holds")
	cmd.MarkFlagDirname("dir")
	for _, name := range []string{"target", "network", "project"} {
		cmd.RegisterFlagCompletionFunc(name, cobra.NoFileCompletions)
	}
	return cmd
}

func runAdd(o addOptions, pos []string, stdout, stderr io.Writer) error {
	var (
		name, portArg string
		err           error
	)
	switch len(pos) {
	case 1:
		portArg = pos[0]
		if name, err = naming.LabelFor(o.dir, o.project); err != nil {
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
	r := route.Route{Name: name, Target: name, Port: port, NetworkName: o.network}
	if o.target != "" {
		r.Target = o.target
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
	if exists && !old.SameOwner(r) && !o.replace {
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

func newRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a route",
		Long: `rm removes a route: the name stops answering, the proxy leaves the route's
network, and the network goes too once no container uses it. The dev
server's container keeps running; stop it the usual way.

A wired devcontainer adds its route again when it starts.`,
		Example: `  berthlight rm shop
  berthlight rm shop-fix-login   # after removing the worktree`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeRoutes,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "usage: berthlight rm <name>")
				return errUsage
			}
			return runRm(args[0], cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

func runRm(name string, stdout, stderr io.Writer) error {
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

func newLsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List routes",
		Long: `ls lists the routes: each name, its URL, the container alias and port it
goes to, and its network. It reads the routes and checks nothing; status
checks that each one answers.

--json prints the same as JSON, for scripts and agents: an object with a
"routes" list, each with name, url, target, port and network. Those field
names stay.`,
		Example: `  berthlight ls
  berthlight ls --json`,
		// Go's flag package let extra arguments pass; so does ls.
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLs(asJSON, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the routes as JSON")
	return cmd
}

func runLs(asJSON bool, stdout io.Writer) error {
	c, err := LoadConfig()
	if err != nil {
		return err
	}
	tab, err := route.Load(c.StateDir)
	if err != nil {
		return err
	}
	if asJSON {
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

func newUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Start the proxy",
		Long: `up starts the proxy container, listening on 127.0.0.1 only, and joins it
to every route's network. A proxy that is already running stays, and joins
any network it is missing. add starts the proxy too, so up is for after a
reboot, after 'berthlight down', or when status says the proxy is gone.

The port is BERTHLIGHT_PORT (default 7480); the container is named
BERTHLIGHT_PROXY_NAME (default berthlight). To change either, or
BERTHLIGHT_BASE, run 'berthlight down' first: a running proxy keeps what it
started with.`,
		Example: `  berthlight up
  berthlight down; berthlight up   # restart, e.g. after changing BERTHLIGHT_BASE`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUp(cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

func runUp(stdout, stderr io.Writer) error {
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
		spec := rt.ProxySpec{Name: c.ProxyName, Image: c.Image, HostPort: c.Port, StateDir: c.StateDir, Bases: c.Bases, Group: run.ProxyGroup()}
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

func newDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop the proxy",
		Long: `down stops and removes the proxy container. The routes stay, as do their
networks and the dev servers' containers: 'berthlight up', or the next
add, brings every route back.`,
		Example: `  berthlight down
  berthlight down; berthlight up   # restart the proxy`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDown(cmd.OutOrStdout())
		},
	}
}

func runDown(stdout io.Writer) error {
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

type urlOptions struct {
	project, base, dir string
	port               int
	copy               bool
}

func newURLCmd() *cobra.Command {
	var o urlOptions
	cmd := &cobra.Command{
		Use:   "url",
		Short: "Print the URL of the current worktree's dev server",
		Long: `url prints the URL the current git worktree's dev server has, or will have,
behind berthlight: the same name the worktree's devcontainer registers.
It works the name out from the worktree alone and checks nothing, so it is
fast enough for scripts, shell prompts and .env files.

The project name is the one the worktree's devcontainer.json gives
berthlight, if 'berthlight devcontainer' wired it; else the main
worktree's directory name. The URL is https once 'berthlight cert' got a
certificate for the base domain.

--copy also puts the URL on the clipboard, through the terminal (OSC 52),
which works over SSH and inside a container too.`,
		Example: `  berthlight url
  berthlight url --copy
  berthlight url -C ../webshop-fix-login   # another worktree's URL
  start (berthlight url)                   # nushell: open it in the browser`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runURL(o, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.project, "project", "", "project name (default: the one in the worktree's devcontainer.json, else the main worktree's directory name)")
	f.StringVar(&o.base, "base", "", "base domain (default: the first of $BERTHLIGHT_BASE, or localhost)")
	f.IntVar(&o.port, "port", 0, "proxy port (default: $BERTHLIGHT_PORT, or 7480)")
	f.StringVarP(&o.dir, "dir", "C", ".", "directory inside the worktree")
	f.BoolVar(&o.copy, "copy", false, "also put the URL on the clipboard, through the terminal (OSC 52)")
	cmd.MarkFlagDirname("dir")
	cmd.RegisterFlagCompletionFunc("base", completeBases)
	cmd.RegisterFlagCompletionFunc("project", cobra.NoFileCompletions)
	cmd.RegisterFlagCompletionFunc("port", cobra.NoFileCompletions)
	return cmd
}

func runURL(o urlOptions, stdout, stderr io.Writer) error {
	c, err := LoadConfig()
	if err != nil {
		return err
	}
	if o.base != "" {
		c.Bases = []string{o.base}
	}
	if o.port != 0 {
		c.Port = o.port
	}
	if o.project == "" {
		o.project = wiredProject(o.dir)
	}
	label, err := naming.LabelFor(o.dir, o.project)
	if err != nil {
		return err
	}
	u := urlFor(c, label)
	out := tui.NewTerm(stdout)
	fmt.Fprintln(stdout, out.Link(u, u))
	if o.copy {
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

type proxyOptions struct {
	state, listen string
	port          int
	bases         []string
}

// newProxyCmd is the server inside the proxy container; hidden, since
// berthlight up starts it.
func newProxyCmd() *cobra.Command {
	var o proxyOptions
	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Run the proxy server (inside the proxy container)",
		Long: `proxy is the server in the proxy container, which 'berthlight up' starts.
It routes requests by host name to the routes in <state>/routes.json and
serves the certificates there, reloading both when they change.`,
		Example:           `  berthlight proxy --state /state --port 7480 --base localhost`,
		Hidden:            true,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProxy(o, cmd.ErrOrStderr())
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.state, "state", "/state", "directory with routes.json and the certificates")
	f.IntVar(&o.port, "port", rt.ContainerPort, "port to listen on")
	f.StringVar(&o.listen, "listen", "", "listen on this address only, e.g. 127.0.0.1:7480 (default: every address the container has at start)")
	f.StringArrayVar(&o.bases, "base", nil, "accepted base domain (repeatable; default localhost)")
	cmd.MarkFlagDirname("state")
	return cmd
}

func runProxy(o proxyOptions, stderr io.Writer) error {
	logger := log.New(stderr, "berthlight proxy: ", log.LstdFlags)

	h := proxy.NewHandler(o.bases...)
	h.Logf = logger.Printf

	var ls []net.Listener
	if o.listen != "" {
		l, err := net.Listen("tcp", o.listen)
		if err != nil {
			return err
		}
		ls = []net.Listener{l}
	} else {
		var err error
		if ls, err = proxy.ListenStartupInterfaces(o.port); err != nil {
			return err
		}
	}
	for _, l := range ls {
		logger.Printf("listening on %s", l.Addr())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go route.Watch(ctx, o.state, 500*time.Millisecond,
		func(t *route.Table) {
			h.SetTable(t)
			logger.Printf("%d route(s) loaded", len(t.Routes))
		},
		func(err error) { logger.Printf("keeping previous routes: %v", err) })

	srv := &proxy.Server{Handler: h, Logf: logger.Printf}
	// Certificates the CLI writes (R7): once one is there, HTTPS is served
	// and plain HTTP for its names redirected.
	go certs.Watch(ctx, o.state, 500*time.Millisecond,
		func(s *certs.Set) {
			srv.SetCerts(s)
			logger.Printf("%d certificate(s) loaded %v", s.Len(), s.Names())
		},
		func(err error) { logger.Printf("keeping previous certificates: %v", err) })
	go func() {
		<-ctx.Done()
		srv.Close()
		for _, l := range ls {
			l.Close()
		}
	}()
	return srv.Serve(ls...)
}

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
