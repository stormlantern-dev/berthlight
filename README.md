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

**Status:** early development. See [the PRD](prd/0001-berthlight.md) and #1.

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
berthlight url      # this worktree's URL
berthlight status   # the proxy and every route, and what is wrong if anything is
```

## Development

Open the repository in its devcontainer, or use Go 1.27 on the host:

```sh
go test ./...
go run ./cmd/berthlight url
```

## License

[Apache-2.0](LICENSE)
