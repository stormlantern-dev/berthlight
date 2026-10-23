// Command berthlight gives devcontainer dev servers stable names instead of
// published host ports. See prd/0001-berthlight.md.
package main

import (
	"os"

	"stormlantern.nl/berthlight/internal/cli"
)

// version is set by the release build (-ldflags "-X main.version=...").
var version = ""

func main() {
	cli.Version = version
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
