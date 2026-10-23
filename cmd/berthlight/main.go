// Command berthlight gives devcontainer dev servers stable names instead of
// published host ports. See prd/0001-berthlight.md.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"

	"stormlantern.nl/berthlight/internal/naming"
)

// DefaultPort is the proxy's port on loopback (PRD 0001, R2).
const DefaultPort = 7480

// version is set by the release build (-ldflags "-X main.version=...").
var version = ""

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, "berthlight", currentVersion())
		return 0
	case "url":
		return cmdURL(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "berthlight: unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: berthlight <command> [flags]

Commands:
  url       print the URL of the current worktree's dev server
  version   print the version
`)
}

func cmdURL(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("url", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "project name (default: the main worktree's directory name)")
	base := fs.String("base", naming.DefaultBase, "base domain")
	port := fs.Int("port", DefaultPort, "proxy port")
	dir := fs.String("C", ".", "directory inside the worktree")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	label, err := naming.LabelFor(*dir, *project)
	if err != nil {
		fmt.Fprintln(stderr, "berthlight:", err)
		return 1
	}
	fmt.Fprintf(stdout, "http://%s:%s\n", naming.Host(label, *base), strconv.Itoa(*port))
	return 0
}

func currentVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}
