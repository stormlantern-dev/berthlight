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

## Development

Open the repository in its devcontainer, or use Go 1.27 on the host:

```sh
go test ./...
go run ./cmd/berthlight url
```

## License

[Apache-2.0](LICENSE)
