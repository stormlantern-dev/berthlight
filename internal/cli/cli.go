// Package cli implements berthlight's commands.
package cli

import (
	"context"
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
	// ACMEDir keeps what `cert` needs and the proxy must not see: the
	// ACME account key and cert.env with the DNS credentials. Never
	// inside StateDir, which the proxy mounts. $BERTHLIGHT_ACME_DIR.
	ACMEDir string
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
	if c.ACMEDir, err = acmeDir(); err != nil {
		return c, err
	}
	return c, nil
}

// acmeDir is ~/.config/berthlight on Linux. Elsewhere the state directory
// is already in the user's configuration directory, so it gets a sibling.
func acmeDir() (string, error) {
	if d := os.Getenv("BERTHLIGHT_ACME_DIR"); d != "" {
		return filepath.Abs(d)
	}
	if goruntime.GOOS == "linux" {
		if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
			return filepath.Join(d, "berthlight"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".config", "berthlight"), nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "berthlight-acme"), nil
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

// Run runs one command and returns the exit code: 0 on success, 1 on a
// failure and when status finds a problem, 2 on a usage error.
func Run(args []string, stdout, stderr io.Writer) int {
	// While completing, --no-header stays where it is: it is a word on
	// the command line being completed, and cobra knows the flag.
	completing := len(args) > 0 && strings.HasPrefix(args[0], "__complete")
	noHeader := false
	if !completing {
		args, noHeader = stripFlag(args, "no-header")
	}
	root := newRoot()
	args = normalizeArgs(root, args)
	// The header, on a terminal (PRD 0001, R12). Without arguments, and
	// for `demo`, the scene is the show and plays to its end; otherwise it
	// animates only while the command works, and stops on its last frame
	// at the command's first output.
	if !noHeader && wantsHeader(args) {
		h := startHeader(stdout, currentVersion())
		show := len(args) == 0 || args[0] == "demo"
		switch {
		case show && h != nil:
			if len(args) == 0 {
				var b strings.Builder
				rootUsage(&b)
				h.Print("\n" + b.String())
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			h.Play(ctx)
			stop()
			return 0
		case h != nil:
			defer h.Stop()
			stdout, stderr = h.Wrap(stdout), h.Wrap(stderr)
		}
	}
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	return execute(root, stderr)
}

// header is what Run needs of tui.Header; tests swap startHeader.
type header interface {
	Print(text string)
	Play(ctx context.Context)
	Wrap(w io.Writer) io.Writer
	Stop()
}

// startHeader starts the header on w, or returns nil where none is drawn.
var startHeader = func(w io.Writer, version string) header {
	if h := tui.StartHeader(w, version); h != nil {
		return h
	}
	return nil
}

// rootUsage is the overview: `berthlight help`, and under the scene when
// berthlight runs without arguments.
func rootUsage(w io.Writer) {
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
  cert --dns <provider>        get or renew a publicly trusted wildcard certificate for
                               your own base domain (DNS-01); then the proxy serves HTTPS
                               (the first run, which creates the ACME account, needs --accept-tos)
  cert --install-timer         renew daily (systemd user timer, or launchd on macOS)
  skill install                install the agent skill for Claude Code, Codex, OpenCode and pi
                               (skill uninstall removes it, skill show prints it)
  completion <shell>           print shell completion: bash, zsh, fish, powershell or nushell
  demo                         play the header animation
  version                      print the version
  help [<command>]             this overview, or a command's help (also <command> --help)

Environment:
  BERTHLIGHT_RUNTIME     docker or podman (default: whichever is installed, docker first)
  BERTHLIGHT_PORT        proxy port on loopback (default 7480)
  BERTHLIGHT_BASE        base domains, comma-separated (default localhost)
  BERTHLIGHT_IMAGE       proxy image
  BERTHLIGHT_PROXY_NAME  name of the proxy container and its network (default berthlight)
  BERTHLIGHT_STATE_DIR   where routes and certificates are kept
  BERTHLIGHT_ACME_DIR    the ACME account and cert.env, for cert (default ~/.config/berthlight)
  BERTHLIGHT_DNS_PROVIDER, BERTHLIGHT_ACME_SERVER, BERTHLIGHT_ACME_EMAIL, BERTHLIGHT_DNS_RESOLVERS
                         defaults for cert's --dns, --server, --email and --resolvers
  BERTHLIGHT_NO_ANIMATION  set to draw no header or progress animation (also off in CI and without a terminal)
  BERTHLIGHT_SCENE       amiga or c64: force the header's scene (default: amiga, c64 when a link is too slow)

Run 'berthlight <command> --help' for what a command does, its flags and examples.
`)
}

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
// output scripts read (url, JSON, the skill's text, completion), not help,
// and not the proxy.
func wantsHeader(args []string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		switch a {
		case "--json", "-json", "--help", "-help", "-h":
			return false
		}
	}
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "url", "proxy", "help", "completion", "__complete", "__completeNoDesc":
		return false
	case "skill":
		return len(args) < 2 || args[1] != "show"
	}
	return true
}
