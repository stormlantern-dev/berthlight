package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"stormlantern.nl/berthlight/internal/naming"
	"stormlantern.nl/berthlight/internal/route"
	rt "stormlantern.nl/berthlight/internal/runtime"
)

// The MCP server (PRD 0051): the same answers as the CLI, as typed tools
// for the assistant in an editor. stdio only; it opens no socket.

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve berthlight's tools to an AI assistant (MCP, on stdio)",
		Long: `mcp runs a Model Context Protocol server on stdin and stdout, for the AI
assistant in an editor: Zed, VS Code, Cursor, Claude Code and others. The
editor starts it; it stops when the editor closes stdin. It opens no port.

Its tools only read, so an editor can allow them once:
  status  the proxy and every route, and whether each answers
  url     the URL of a worktree's dev server, and whether it answers
  ls      the routes

'berthlight mcp config <client>' prints what to put in the editor's
settings.`,
		Example: `  berthlight mcp config zed
  berthlight mcp config claude`,
		Args:              noArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			err := newMCPServer().Run(ctx, &mcp.StdioTransport{})
			if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		},
	}
	cmd.AddCommand(newMCPConfigCmd())
	return cmd
}

type urlIn struct {
	Dir string `json:"dir,omitempty" jsonschema:"absolute path of a directory inside the git worktree, usually the project you work in; a path inside a running devcontainer works too; default: the directory the editor started the server in"`
}

// urlOut is the url tool's result.
type urlOut struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	Registered bool   `json:"registered"`
	OK         *bool  `json:"ok,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

type lsOut struct {
	Routes []routeJSON `json:"routes"`
}

func readOnly(title string) *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: &no}
}

func newMCPServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "berthlight", Title: "berthlight", Version: currentVersion()}, nil)
	mcp.AddTool(s, &mcp.Tool{
		Name: "url",
		Description: "The URL of a git worktree's dev server behind berthlight, and whether it answers now. " +
			"Call this before opening the dev server in a browser or Playwright, instead of guessing a localhost port. " +
			"Pass the worktree's path as dir.",
		Annotations: readOnly("Dev server URL"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in urlIn) (*mcp.CallToolResult, urlOut, error) {
		out, err := urlInfo(ctx, in.Dir)
		return nil, out, err
	})
	mcp.AddTool(s, &mcp.Tool{
		Name: "status",
		Description: "Check berthlight's proxy and every route: which dev servers answer, and for each problem what is wrong. " +
			"Call this when a dev server URL does not load.",
		Annotations: readOnly("berthlight status"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, statusOut, error) {
		out, err := statusInfo(ctx)
		return nil, out, err
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ls",
		Description: "List berthlight's routes: each name, URL, container alias, port and network. It checks nothing; status does.",
		Annotations: readOnly("berthlight routes"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, lsOut, error) {
		out, err := lsInfo()
		return nil, out, err
	})
	return s
}

func lsInfo() (lsOut, error) {
	c, err := LoadConfig()
	if err != nil {
		return lsOut{}, err
	}
	tab, err := route.Load(c.StateDir)
	if err != nil {
		return lsOut{}, err
	}
	out := lsOut{Routes: []routeJSON{}}
	for _, r := range tab.Routes {
		out.Routes = append(out.Routes, newRouteJSON(c, r))
	}
	return out, nil
}

func statusInfo(ctx context.Context) (statusOut, error) {
	c, err := LoadConfig()
	if err != nil {
		return statusOut{}, err
	}
	run, err := rt.Detect()
	if err != nil {
		return statusOut{}, err
	}
	tab, err := route.Load(c.StateDir)
	if err != nil {
		return statusOut{}, err
	}
	return statusJSON(c, newChecker(c, run).Check(ctx, tab)), nil
}

// urlInfo is `berthlight url -C dir`, with the route's health when it is
// registered. A health check that cannot run says so in detail; the URL
// is still the answer.
func urlInfo(ctx context.Context, dir string) (urlOut, error) {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return urlOut{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil && path.IsAbs(filepath.ToSlash(dir)) {
		// A path from inside a devcontainer, as an editor's assistant sees
		// it: find the host directory mounted there.
		if host, ok, merr := containerToHost(dir); merr != nil {
			return urlOut{}, merr
		} else if ok {
			abs = host
			fi, err = os.Stat(abs)
		}
	}
	if err != nil {
		return urlOut{}, fmt.Errorf("dir %s: %w", dir, err)
	} else if !fi.IsDir() {
		return urlOut{}, fmt.Errorf("dir %s is not a directory", dir)
	}
	c, err := LoadConfig()
	if err != nil {
		return urlOut{}, err
	}
	label, err := naming.LabelFor(abs, wiredProject(abs))
	if err != nil {
		return urlOut{}, err
	}
	out := urlOut{Name: label, URL: urlFor(c, label)}
	tab, err := route.Load(c.StateDir)
	if err != nil {
		return urlOut{}, err
	}
	r, ok := tab.Lookup(label)
	if !ok {
		out.Detail = "no route yet: the worktree's devcontainer registers it when it starts (berthlight devcontainer wires that)"
		return out, nil
	}
	out.Registered = true
	run, err := rt.Detect()
	if err != nil {
		out.Detail = "not checked: " + err.Error()
		return out, nil
	}
	one := &route.Table{Routes: []route.Route{r}}
	rep := newChecker(c, run).Check(ctx, one)
	switch {
	case rep.Proxy != "":
		f := false
		out.OK, out.Detail = &f, "proxy: "+rep.Proxy
	case len(rep.Routes) == 1:
		ok := rep.Routes[0].OK
		out.OK, out.Detail = &ok, rep.Routes[0].Detail
	}
	return out, nil
}

// containerToHost finds the host directory behind a path inside a running
// container. ok is false when no container mounts it.
func containerToHost(dir string) (string, bool, error) {
	run, err := rt.Detect()
	if err != nil {
		return "", false, nil
	}
	mounts, err := run.BindMounts()
	if err != nil {
		return "", false, nil
	}
	return hostPath(dir, mounts)
}

// hostPath maps dir through the bind mount with the longest matching
// destination. Where containers disagree, one on a berthlight network
// wins; if that does not settle it, it is an error rather than a guess.
func hostPath(dir string, mounts []rt.BindMount) (string, bool, error) {
	// Container paths are POSIX paths, whatever the host is.
	dir = path.Clean(dir)
	best := 0
	var hits []rt.BindMount
	for _, m := range mounts {
		d := path.Clean(m.Destination)
		if d == "/" || (dir != d && !strings.HasPrefix(dir, d+"/")) {
			continue
		}
		switch {
		case len(d) > best:
			best, hits = len(d), []rt.BindMount{m}
		case len(d) == best:
			hits = append(hits, m)
		}
	}
	pick := func(only func(rt.BindMount) bool) (map[string][]string, []string) {
		paths := map[string][]string{}
		var order []string
		for _, m := range hits {
			if !only(m) {
				continue
			}
			p := filepath.Join(m.Source, filepath.FromSlash(strings.TrimPrefix(dir, path.Clean(m.Destination))))
			if _, seen := paths[p]; !seen {
				order = append(order, p)
			}
			paths[p] = append(paths[p], m.Container)
		}
		return paths, order
	}
	paths, order := pick(func(rt.BindMount) bool { return true })
	if len(order) > 1 {
		if p, o := pick(func(m rt.BindMount) bool { return slices.ContainsFunc(m.Networks, rt.IsRouteNetwork) }); len(o) > 0 {
			paths, order = p, o
		}
	}
	switch len(order) {
	case 0:
		return "", false, nil
	case 1:
		return order[0], true, nil
	}
	var which []string
	for _, p := range order {
		which = append(which, fmt.Sprintf("%s (%s)", p, strings.Join(paths[p], ", ")))
	}
	return "", false, fmt.Errorf("dir %s is mounted from more than one host directory: %s; pass the host path", dir, strings.Join(which, "; "))
}

// MCP client settings (M6). Printed, never written: the editor's config
// is the user's.

var mcpClients = []string{"claude", "zed"}

func newMCPConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config <client>",
		Short: "Print the settings that add berthlight's MCP server to an editor",
		Long: `config prints what to add to a client's settings so it starts
'berthlight mcp', with the path of this berthlight binary. It changes no
file.

  zed     a "context_servers" entry for Zed's settings.json
  claude  the 'claude mcp add' command for Claude Code`,
		Example: `  berthlight mcp config zed
  berthlight mcp config claude`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: mcpClients,
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			if p, err := filepath.EvalSymlinks(exe); err == nil {
				exe = p
			}
			return writeMCPConfig(cmd.OutOrStdout(), args[0], exe)
		},
	}
}

func writeMCPConfig(w io.Writer, client, exe string) error {
	switch client {
	case "zed":
		type server struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		}
		v := map[string]any{"context_servers": map[string]server{
			"berthlight": {Command: exe, Args: []string{"mcp"}, Env: map[string]string{}},
		}}
		b, _ := json.MarshalIndent(v, "", "  ")
		fmt.Fprintf(w, "// Add to Zed's settings.json (zed: open settings), merging with any\n// \"context_servers\" already there:\n%s\n", b)
	case "claude":
		fmt.Fprintf(w, "claude mcp add --scope user berthlight -- %s mcp\n", exe)
	default:
		return &usageError{msg: fmt.Sprintf("unknown client %q; one of: %v", client, mcpClients), usage: true}
	}
	return nil
}
