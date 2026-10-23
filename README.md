# berthlight

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

*Twelve dev servers in their own devcontainers, each at its own name, coming up one by one in `berthlight status --watch` in Ghostty. Recorded from real containers with [tools/guides](tools/guides).*

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

```sh
berthlight url              # this worktree's URL (--copy puts it on the clipboard)
berthlight status           # the proxy and every route, and what is wrong if anything is
berthlight status --watch   # keep it on screen; a notification when a route comes up or goes down
```

In a modern terminal (Ghostty, Kitty, WezTerm, iTerm2, Windows Terminal) URLs
are clickable, a small demo scene heads each command (animated in Kitty and
Ghostty; `berthlight demo` plays it in full; `--no-header` leaves it out), and `--watch` shows a summary in the tab title and progress in
the tab while something is down. Piped or redirected, the output is plain
text, and `ls --json` and `status --json` give scripts and agents a stable
format.

Coding agents can do the wiring too. `berthlight skill install` installs a
skill that teaches Claude Code, Codex, OpenCode and pi how, including the dev
server settings, docs and OAuth redirect URIs around it. Add `--project` to
put it in the repository for everyone working on it.

## Development

Open the repository in its devcontainer, or use Go 1.27 on the host:

```sh
go test ./...
go run ./cmd/berthlight url
```

## License

[Apache-2.0](LICENSE)
