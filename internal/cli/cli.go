// Package cli implements berthlight's commands.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"strconv"
	"strings"
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
		fmt.Fprintln(stderr, "berthlight:", err)
		return 1
	}
	return 0
}

var errUsage = errors.New("usage")

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: berthlight <command> [flags]

Commands:
  add <name> <port>   route <name>.localhost to a container's port
  rm <name>           remove a route
  ls                  list routes
  up                  start the proxy (add does this too)
  down                stop the proxy
  url                 print the URL of the current worktree's dev server
  version             print the version

Environment:
  BERTHLIGHT_RUNTIME     docker or podman (default: whichever is installed, docker first)
  BERTHLIGHT_PORT        proxy port on loopback (default 7480)
  BERTHLIGHT_BASE        base domains, comma-separated (default localhost)
  BERTHLIGHT_IMAGE       proxy image
  BERTHLIGHT_STATE_DIR   where routes are kept
`)
}

// newFlags returns a flag set that prints its errors to stderr.
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
