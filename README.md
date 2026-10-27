# berthlight

[![CI](https://github.com/stormlantern-dev/berthlight/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/stormlantern-dev/berthlight/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/stormlantern-dev/berthlight)](https://github.com/stormlantern-dev/berthlight/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/stormlantern.nl/berthlight.svg)](https://pkg.go.dev/stormlantern.nl/berthlight)
[![Go](https://img.shields.io/github/go-mod/go-version/stormlantern-dev/berthlight)](go.mod)
[![License](https://img.shields.io/github/license/stormlantern-dev/berthlight)](LICENSE)
[![Signed releases](https://img.shields.io/badge/releases-cosign%20%2B%20provenance-2ea44f)](#install)

Stable names for devcontainer dev servers, without published ports.

```
http://gallery.localhost:7480           instead of    http://localhost:8089
http://shop-fix-login.localhost:7480                  http://localhost:8083 (or was it 8084?)
```

One proxy, bound to loopback, routes each name to its container over a
per-project container network. No runtime socket, no root, nothing reachable
from the network. The same binary installs an agent skill for Claude Code,
Codex, OpenCode and pi.

![Twelve dev servers coming up in berthlight status --watch, each at its own name](docs/brag.gif)

*Twelve dev servers in their own devcontainers, each at its own name, coming up one by one in `berthlight status --watch` in Ghostty. Recorded from real containers with [tools/guides](tools/guides). Run `berthlight` on its own for the rest of the show.*

**Status:** early development. See [the PRD](prd/0001-berthlight.md) and #1.

## Install

```sh
brew install stormlantern-dev/tap/berthlight                 # macOS, Linux
scoop bucket add stormlantern https://github.com/stormlantern-dev/scoop-bucket
scoop install stormlantern/berthlight                        # Windows
paru -S berthlight-bin                                       # Arch
```

Debian, Fedora and Alpine packages and plain archives are on each
[release](https://github.com/stormlantern-dev/berthlight/releases). Every
release has checksums signed with cosign and GitHub build provenance; the
release notes say how to verify them. The proxy image,
`ghcr.io/stormlantern-dev/berthlight:<version>`, is signed the same way.

winget is on its way: `winget install Stormlantern.Berthlight` works once
the package is accepted in
[microsoft/winget-pkgs](https://github.com/microsoft/winget-pkgs). Until then,
use Scoop. Releases test the manifests with `winget validate` and
`winget install` and push them to the organisation's fork. A person then
opens the pull request upstream, on purpose: automating it would take a
long-lived user token, which this project does not keep (PRD, R10).

## Use

Wire a project's devcontainer once, in its main worktree:

```sh
berthlight devcontainer shop 8080 --dry-run   # show the change
berthlight devcontainer shop 8080             # make it
```

This edits `devcontainer.json` in place, keeping its comments: the container
joins its own network (`bl-<folder name>`) instead of publishing a port, and
an `initializeCommand` registers the route on the host before the container
starts. Each worktree gets its own name: `shop.localhost` for the main
worktree, `shop-fix-login.localhost` for a worktree in `webshop-fix-login`.
The dev server must listen on `0.0.0.0` inside the container.

Give every worktree its own devcontainer. For git to work inside one, the
worktree must point at the repository with a relative path, and the
container needs the repository's git directory beside it:

```sh
git config --global worktree.useRelativePaths true   # once; git 2.48+
git worktree add ../webshop-fix-login
devcontainer up --workspace-folder ../webshop-fix-login --mount-git-worktree-common-dir
```

`git worktree repair --relative-paths` converts existing worktrees.
`devcontainer exec` needs the same flag. A `workspaceMount` in
`devcontainer.json` turns the flag off
([devcontainers/cli#1243](https://github.com/devcontainers/cli/issues/1243)).
`berthlight devcontainer` points out what is missing.

### Docker Compose devcontainers

A devcontainer with `dockerComposeFile` is wired with the same command. Compose
ignores `runArgs`, and a committed Compose file cannot name the worktree, so
berthlight wires it through an override file instead:

- `berthlight devcontainer` adds `berthlight.compose.yml`, next to
  `devcontainer.json`, last in `dockerComposeFile`, lists it in the
  `.gitignore` there, and sets the `initializeCommand` to
  `berthlight add … --compose-service <service> --compose-override …`.
  `--dry-run` shows both files.
- On each start, before Compose reads its files, that command writes the
  override for the worktree. The devcontainer's `service` joins
  `bl-<folder name>` under that alias and keeps its own networks; it stops
  publishing the dev server's port on the host, while its other published
  ports stay; and in a linked worktree, the repository's git directory is
  mounted where the worktree's `gitdir` points inside the container, so git
  works there without `--mount-git-worktree-common-dir` (which Compose
  ignores).

Each worktree runs its own Compose project, so its own database and other
services. Only the dev service gets a name; the proxy cannot reach the rest of
the stack. berthlight reads the merged configuration with
`docker compose config` (or `podman compose config`, which needs Docker
Compose v2 as Podman's Compose provider). Without Compose, the override still
joins the network, and `add` says what it left as it was.

```sh
berthlight url              # this worktree's URL (--copy puts it on the clipboard)
berthlight status           # the proxy and every route, and what is wrong if anything is
berthlight status --watch   # keep it on screen; a notification when a route comes up or goes down
```

In a modern terminal (Ghostty, Kitty, WezTerm, iTerm2, Windows Terminal) URLs
are clickable, a small demo scene heads each command (animated in Kitty and
Ghostty, also over SSH, where a slow connection drops frames or gets the
chunky C64 version rather than holding the command up;
`BERTHLIGHT_SCENE=amiga` or `=c64` picks one; `berthlight demo` plays it in
full; `--no-header` leaves it out), and `--watch` shows a summary in the tab title and progress in
the tab while something is down. Piped or redirected, the output is plain
text, and `ls --json` and `status --json` give scripts and agents a stable
format.

### HTTPS on your own domain

For `https://shop.dev.example.com:7480`, with a certificate every browser
trusts and no local CA, use a domain you control. Point
`*.dev.example.com` at `127.0.0.1` (an `A` record, and `AAAA` `::1`) at
your DNS provider, then get a wildcard certificate with the DNS-01
challenge, from Let's Encrypt by default (in nushell):

```nu
$env.BERTHLIGHT_BASE = "dev.example.com,localhost"   # in your config.nu
berthlight down; berthlight up                       # the proxy accepts the new base
berthlight cert --dns gcloud --accept-tos --install-timer   # get it, and renew it daily
```

The first run creates an ACME account with the CA, which means agreeing to
its terms of service: without `--accept-tos` it stops and shows where they
are. Later runs, and the renewal timer, use that account and need no flag.

`berthlight cert` runs on the host and is the only part that sees your DNS
credentials. It reads them as [lego](https://go-acme.github.io/lego/dns/)
does for the provider: from the environment, or from
`~/.config/berthlight/cert.env` (`KEY=VALUE` lines, `chmod 600`), which the
renewal timer reads too. Built in are `gcloud` (Application Default
Credentials, `GCE_PROJECT`, optionally `GCE_IMPERSONATE_SERVICE_ACCOUNT`),
`cloudflare`, `route53`, `digitalocean`, `hetzner`, `dnsupdate` (RFC 2136:
BIND, PowerDNS, Knot), and `exec` and `httpreq` for anything else. `--server
letsencrypt-staging` tries it out first.

The certificate and key go in the state directory, the key readable by you
and the proxy only. The proxy picks them up within a second, serves HTTPS on
the same port 7480, and redirects plain http to https for the names the
certificate covers; `*.localhost` stays on http. `berthlight url`, `ls` and
`status` give https URLs from then on, and `status` says when the
certificate does not verify. A renewal starts when a third of the
certificate's lifetime is left (`--renew-within` to change that); the timer
is a systemd user timer on Linux and a launchd agent on macOS.

Coding agents can do the wiring too. `berthlight skill install` installs a
skill that teaches Claude Code, Codex, OpenCode and pi how, including the dev
server settings, docs and OAuth redirect URIs around it. Add `--project` to
put it in the repository for everyone working on it.

The AI assistant in your editor can ask berthlight directly. `berthlight
mcp` is an MCP server on stdio with three read-only tools: `url` (a
worktree's URL, and whether it answers), `status` and `ls`. Because they
only read, an editor can allow them once. `berthlight mcp config zed`
prints the entry for Zed's settings.json:

```json
{
  "context_servers": {
    "berthlight": { "command": "/usr/bin/berthlight", "args": ["mcp"], "env": {} }
  }
}
```

For Claude Code, `berthlight mcp config claude` prints the `claude mcp add`
command. Other MCP clients (VS Code, Cursor) take the same command and
argument.

`berthlight help` lists the commands, and `berthlight <command> --help`
says what one does, with examples.

### Shell completion

berthlight completes its commands, flags and values: route names for `rm`,
agents for `skill --agent`, DNS providers and CAs for `cert`, directories
for `-C`. The deb, rpm and apk packages, the AUR package and Homebrew
install it for bash, zsh and fish; the Linux packages for nushell too. For
an archive, or Scoop, set it up yourself:

```sh
berthlight completion bash > ~/.local/share/bash-completion/completions/berthlight   # needs bash-completion
berthlight completion zsh > "${fpath[1]}/_berthlight"                                # before compinit
berthlight completion fish > ~/.config/fish/completions/berthlight.fish
berthlight completion powershell >> $PROFILE
```

In nushell, `berthlight completion nushell` prints an external completer
that asks berthlight what to complete and hands every other command to the
completer you had, such as carapace's. Put it in an autoload directory,
which nushell runs after `config.nu`:

```nu
berthlight completion nushell | save -f ($nu.user-autoload-dirs | first | path join berthlight.nu)
```

If carapace completes everything for you and you would rather keep it that
way, its cobra bridge works with berthlight as well. Give it a spec instead
of the file above (on macOS, in `~/Library/Application Support/carapace/specs`):

```nu
mkdir ~/.config/carapace/specs
'name: berthlight
parsing: disabled
completion:
  positionalany: ["$carapace.bridge.Cobra([berthlight])"]
' | save -f ~/.config/carapace/specs/berthlight.yaml
```

## Development

Open the repository in its devcontainer, or use Go 1.27 on the host:

```sh
go test ./...
go run ./cmd/berthlight url
```

## License

[Apache-2.0](LICENSE)
