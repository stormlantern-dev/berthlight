// Package cli implements berthlight's commands.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"

	"stormlantern.nl/berthlight/internal/tui"
)

// DefaultPort is the proxy's port on loopback (PRD 0001, R2).
const DefaultPort = 7480

// Version is the release version; set by main.
var Version = ""

// Config is read from the environment, so tests and unusual setups can
// change it without flags on every command.
type Config struct {
	// StateDir holds routes.json. $BERTHLIGHT_STATE_DIR.
	StateDir string
	// Port is the proxy's host port. $BERTHLIGHT_PORT.
	Port int
	// Image is the proxy image. $BERTHLIGHT_IMAGE.
	Image string
	// ProxyName names the proxy container and its network.
	// $BERTHLIGHT_PROXY_NAME.
	ProxyName string
	// Bases are the accepted base domains, first one used in URLs.
	// $BERTHLIGHT_BASE, comma-separated.
	Bases []string
}

// LoadConfig reads the configuration from the environment.
func LoadConfig() (Config, error) {
	c := Config{
		Port:      DefaultPort,
		Image:     "ghcr.io/stormlantern-dev/berthlight:" + imageTag(),
		ProxyName: "berthlight",
		Bases:     []string{"localhost"},
	}
	if v := os.Getenv("BERTHLIGHT_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			return c, fmt.Errorf("BERTHLIGHT_PORT=%q is not a port", v)
		}
		c.Port = p
	}
	if v := os.Getenv("BERTHLIGHT_IMAGE"); v != "" {
		c.Image = v
	}
	if v := os.Getenv("BERTHLIGHT_PROXY_NAME"); v != "" {
		c.ProxyName = v
	}
	if v := os.Getenv("BERTHLIGHT_BASE"); v != "" {
		c.Bases = nil
		for _, b := range strings.Split(v, ",") {
			if b = strings.Trim(strings.TrimSpace(strings.ToLower(b)), "."); b != "" {
				c.Bases = append(c.Bases, b)
			}
		}
		if len(c.Bases) == 0 {
			return c, fmt.Errorf("BERTHLIGHT_BASE=%q has no domains", v)
		}
	}
	dir, err := stateDir()
	if err != nil {
		return c, err
	}
	c.StateDir = dir
	return c, nil
}

func stateDir() (string, error) {
	if d := os.Getenv("BERTHLIGHT_STATE_DIR"); d != "" {
		return filepath.Abs(d)
	}
	if goruntime.GOOS == "linux" {
		if d := os.Getenv("XDG_STATE_HOME"); d != "" {
			return filepath.Join(d, "berthlight"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "state", "berthlight"), nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "berthlight"), nil
}

func currentVersion() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

// imageTag is the release's image tag; development builds use "edge".
func imageTag() string {
	v := currentVersion()
	if v == "(devel)" || strings.Contains(v, "+dirty") || strings.Contains(v, "-0.") {
		return "edge"
	}
	return strings.TrimPrefix(v, "v")
}

// Run runs one command and returns the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	args, noHeader := stripFlag(args, "no-header")
	// The header, on a terminal (PRD 0001, R12). Without arguments, and
	// for `demo`, the scene is the show and plays to its end; otherwise it
	// animates only while the command works, and stops on its last frame
	// at the command's first output.
	if !noHeader && wantsHeader(args) {
		h := tui.StartHeader(stdout, currentVersion())
		show := len(args) == 0 || args[0] == "demo"
		switch {
		case show && h != nil:
			if len(args) == 0 {
				var b strings.Builder
				usage(&b)
				h.Print("\n" + b.String())
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			h.Play(ctx)
			stop()
			return 0
		case show && len(args) > 0:
			fmt.Fprintln(stdout, "berthlight demo plays on a terminal (not in CI, nor with BERTHLIGHT_NO_ANIMATION set).")
			return 0
		case h != nil:
			defer h.Stop()
			stdout, stderr = h.Wrap(stdout), h.Wrap(stderr)
		}
	}
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	cmds := map[string]func([]string, io.Writer, io.Writer) error{
		"add":   cmdAdd,
		"rm":    cmdRm,
		"ls":    cmdLs,
		"up":    cmdUp,
		"down":  cmdDown,
		"url":   cmdURL,
		"proxy": cmdProxy,

		"status":       cmdStatus,
		"devcontainer": cmdDevcontainer,
		"skill":        cmdSkill,
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, "berthlight", currentVersion())
		return 0
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	}
	cmd, ok := cmds[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "berthlight: unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
	if err := cmd(args[1:], stdout, stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) || errors.Is(err, errUsage) {
			return 2
		}
		if errors.Is(err, errProblems) {
			return 1
		}
		fmt.Fprintln(stderr, "berthlight:", err)
		return 1
	}
	return 0
}

var errUsage = errors.New("usage")

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: berthlight <command> [flags]

Global flags:
  --no-header                  leave out the header (the little demo scene)

Commands:
  add [<name>] <port>          route <name>.localhost to a container's port
                               (without <name>: the current worktree's name)
  rm <name>                    remove a route
  ls [--json]                  list routes
  status [--watch] [--json]    check the proxy and each route, say what is wrong
  devcontainer <project> <port>
                               wire the project's devcontainer.json to berthlight
  up                           start the proxy (add does this too)
  down                         stop the proxy
  url [--copy]                 print the URL of the current worktree's dev server
  skill install                install the agent skill for Claude Code, Codex, OpenCode and pi
  demo                         play the header animation
  version                      print the version

Environment:
  BERTHLIGHT_RUNTIME     docker or podman (default: whichever is installed, docker first)
  BERTHLIGHT_PORT        proxy port on loopback (default 7480)
  BERTHLIGHT_BASE        base domains, comma-separated (default localhost)
  BERTHLIGHT_IMAGE       proxy image
  BERTHLIGHT_STATE_DIR   where routes are kept
  BERTHLIGHT_NO_ANIMATION  set to draw no header or progress animation (also off in CI and without a terminal)
`)
}

// newFlags returns a flag set that prints its errors to stderr.
// stripFlag removes a global boolean flag, given as --name or -name
// anywhere on the command line, and reports whether it was there.
func stripFlag(args []string, name string) ([]string, bool) {
	out := make([]string, 0, len(args))
	found := false
	for _, a := range args {
		if a == "--"+name || a == "-"+name {
			found = true
			continue
		}
		out = append(out, a)
	}
	return out, found
}

// wantsHeader says whether a command shows the header: not the ones whose
// output scripts read (url, JSON, the skill's text) and not the proxy.
func wantsHeader(args []string) bool {
	for _, a := range args {
		if a == "--json" || a == "-json" {
			return false
		}
	}
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "url", "proxy":
		return false
	case "skill":
		return len(args) < 2 || args[1] != "show"
	}
	return true
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// parseInterspersed parses flags that may come after positional arguments,
// as in `add shop 8080 --target web`.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}
