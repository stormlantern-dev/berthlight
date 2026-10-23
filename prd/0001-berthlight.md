# berthlight: stable names for devcontainer dev servers

- **Status:** Proposed
- **Date:** 2026-10-23
- **Tracking issue:** #1, which holds the test plan and the verification record
- **First consumers:** two private projects of the author: a design system
  (component gallery on one port) and a Go web application (one devcontainer
  per git worktree, several running at once).

## Summary

A small command-line tool, installed once per machine, that gives every
devcontainer's dev server a stable name such as `http://gallery.localhost:<port>`
instead of a published host port. Devcontainers stop publishing ports at all.
One proxy, bound to loopback, routes each name to the right container over a
container network. The same binary installs an agent skill into popular coding
agents, so an agent can wire up a new project the same way.

## Why

- **Fixed host ports collide.** Each project picks a port, or a range, and
  publishes it on the host. Two projects that picked the same port cannot run
  at the same time: today one devcontainer will not start because another holds
  its port.
- **Parallel work multiplies the problem.** One devcontainer per git worktree is
  a good way to work on several branches at once, but every extra container
  needs another free port, so projects publish ranges and guess which one is
  theirs.
- **Random ports fix collisions and break everything else.** A port the
  runtime picks is unknown until the container runs, so links, bookmarks,
  OAuth redirect URIs and agent instructions all go stale.
- **Agents hardcode ports.** A coding agent told "the server is on 8080" keeps
  using 8080 after the port changes.

A name per container fixes all four: it does not depend on which port is free,
it is the same every time, and it can be written into instructions.

## Goals

- **G1.** Start any number of devcontainers, from any number of projects and
  worktrees, without a port collision.
- **G2.** Each dev server has a stable URL that survives restarts and rebuilds.
- **G3.** Least privilege: no container-runtime socket in any container, no
  root on the host, nothing reachable from the network.
- **G4.** Works with Docker and rootless Podman, on Linux and macOS, and on
  Windows through WSL2.
- **G5.** Installable with the platform's usual package manager, and verifiable.
- **G6.** A coding agent can add a project to berthlight by following one skill,
  in whichever agent the author uses.

## Non-goals

- **Production ingress.** This is a local development tool. No load balancing,
  no rate limiting, no exposure beyond the machine.
- **Sharing on the LAN.** Names are reachable from the machine itself only.
- **Kubernetes and Compose orchestration.** A Compose-based devcontainer can
  still join the network, but berthlight does not manage Compose projects.
- **Windows containers.** Only Linux containers, which is what devcontainers
  run.

## Prior art and what was ruled out

| Option | Why not |
|---|---|
| Fixed ports per project | The problem this solves. |
| Random host ports plus a lookup helper | No collisions, but no stable URL: links, OAuth redirect URIs and agent instructions go stale on every start. |
| [portless](https://github.com/vercel-labs/portless) (Vercel Labs, Apache-2.0) | Closest prior art: named `.localhost` URLs for dev servers. It routes to *host* ports, so a container still has to publish one, and no devcontainer hook can register a port that is only known after start. Its default HTTPS binds 443 with sudo and adds a local CA to the system trust store. npm only. |
| Traefik or nginx-proxy with the Docker provider | Discovers containers through the runtime socket. Read-only mounts do not limit the API; the socket is root-equivalent on Docker. A filtering socket proxy reduces this but adds a moving part. |
| Local CA (mkcert style) for HTTPS | A CA key on disk that the whole machine trusts. A leak signs anything. A publicly trusted certificate for a domain the user owns avoids this (R7). |
| OrbStack's `*.orb.local` names | macOS only, and proprietary. |

## Design overview

```
host                                       container runtime (rootless)
───────────────────────────────────────    ─────────────────────────────────────────
browser ─► 127.0.0.1:<proxy port> ───────► berthlight proxy ─► gallery:8089  (network bl-gallery)
                                                         └─► ib-main:8080  (network bl-ib-main)
berthlight CLI ─ writes routes, certs ───► (read-only mount)
             └─ creates networks, starts proxy, connects it to each network
```

- **One binary.** `berthlight` is the CLI on the host and, as
  `berthlight proxy`, the process inside the proxy container. The image is
  built from the same release.
- **Container networks with DNS.** User-defined networks resolve container
  aliases; the default bridge network does not. A devcontainer joins its
  network with `--network` and `--network-alias` in `runArgs`, and publishes
  nothing.
- **The proxy reads files, not the runtime.** Routes and certificates are files
  the CLI writes to a directory the proxy mounts read-only. No socket anywhere.

## Requirements

Priorities use RFC 2119: **MUST**, **SHOULD**, **MAY**.

**R1 (MUST). Networks.** `berthlight add <name> <port>` creates a dedicated
user-defined network for that route if it does not exist, and connects the
proxy to it. Each devcontainer is on its own network, so devcontainers cannot
reach each other, only the proxy can reach them. Works with Docker and with
rootless Podman.

*Fit:* on both runtimes, two devcontainers added as two routes: each is
reachable through the proxy by name, and neither can open a connection to the
other.

**R2 (MUST). A least-privilege proxy.** The proxy container:
- publishes one port, on `127.0.0.1` only (and `::1`);
- mounts no runtime socket;
- runs as a non-root user, with all capabilities dropped, a read-only root
  filesystem and `no-new-privileges`;
- mounts the routes and certificate directory read-only.

*Fit:* an integration test inspects the running proxy container and asserts
each point; a request from another host interface is refused.

**R3 (MUST). Routes.** `berthlight add`, `rm` and `ls` manage routes. A route
maps a name to a container alias and port. The proxy picks up a change without
restarting, and WebSockets (hot reload) pass through.

*Fit:* a route added while the proxy runs serves within 2 seconds; a WebSocket
echo through the proxy works.

**R4 (MUST). Names that need no setup.** Names live under `*.localhost` by
default, which browsers and systemd-resolved send to loopback without any DNS
change. Multi-level names (`main.ib.localhost`) work, so one project can have
one name per worktree.

*Fit:* on a fresh CI runner, a route is reachable with `curl` by name, with no
change to `/etc/hosts` or DNS.

**R5 (MUST). Devcontainer wiring.** `berthlight devcontainer <name> <port>`
edits a project's `devcontainer.json` in place: adds the `runArgs` for network
and alias, an `initializeCommand` that ensures the network and route exist, and
removes `appPort` entries for that port. It keeps comments and formatting
(JSONC). `--dry-run` prints the diff.

*Fit:* golden-file tests on `devcontainer.json` files with comments, existing
`runArgs`, and an existing `initializeCommand`; a re-run changes nothing.

**R6 (MUST). Status.** `berthlight status` lists each route with its URL and
whether its container answers, and says what is wrong when something does not:
proxy not running, network missing, name does not resolve.

*Fit:* tests for each failure, each with its own message.

**R7 (SHOULD). Publicly trusted HTTPS for a domain the user owns.**
`berthlight cert` obtains a wildcard certificate (`*.dev.example.com`) from an
ACME CA (Let's Encrypt by default) with the DNS-01 challenge, through
[lego](https://github.com/go-acme/lego) and any of its DNS providers. The DNS
credentials stay with the CLI on the host, read from the provider's usual
source (for example, Application Default Credentials for Google Cloud DNS,
including service-account impersonation). The proxy only receives the
certificate and key files. `berthlight cert --install-timer` installs a renewal
timer (systemd user timer, or launchd agent on macOS).

*Fit:* an integration test against Pebble (Let's Encrypt's test ACME server)
with a test DNS server issues and renews a certificate that the proxy then
serves; the proxy container has no DNS credentials in its environment or mounts.

**R8 (MUST). Agent skill.** The binary embeds one skill in the
[Agent Skills](https://agentskills.io) format (`SKILL.md`), versioned with the
binary. `berthlight skill install` writes it to the skill directory of each
agent asked for: Claude Code, Codex, OpenCode and pi, user-wide or per project
(`--project`). The skill tells an agent when and how to use R5 and R6, and how
to update a project's documentation and OAuth redirect URIs.

*Fit:* tests assert the target path per agent and scope, against each agent's
current documentation; the `SKILL.md` passes the format's validation (name
matches directory, description length).

**R9 (MUST). Distribution.** Each tagged release publishes, through
GoReleaser:
- a Homebrew formula in the organisation's tap (macOS and Linux);
- `.deb`, `.rpm` and `.apk` packages and archives on the GitHub release;
- an AUR `-bin` package;
- a Scoop manifest in the organisation's bucket;
- the proxy image, multi-arch (amd64, arm64);
- checksums signed with cosign (keyless) and GitHub build provenance
  attestations.

*Fit:* the release workflow does all of the above from a tag; CI installs the
release with `brew`, `scoop` and `dpkg` and runs `berthlight version`.

**R10 (SHOULD). winget.** A winget manifest, submitted by the release workflow
once the CLI is stable.

*Fit:* the package is accepted in `microsoft/winget-pkgs`.

**R11 (MUST). Continuous integration.**
- Linux, amd64 and arm64: unit tests and the integration tests (R1–R7) against
  Docker and rootless Podman, including a real `devcontainer up` with the
  Dev Container CLI.
- macOS and Windows: build, unit tests, `skill install`, and the CLI paths that
  do not need Linux containers (hosted runners for those platforms cannot run
  them).
- A GoReleaser snapshot build on every pull request.

*Fit:* the workflows exist and pass on main.

## Security

- **Exposure.** Only the proxy listens on the host, and only on loopback. Dev
  servers bind `0.0.0.0` *inside* their container, which is now reachable from
  the proxy's networks only, not from the host's interfaces.
- **Isolation between projects.** One network per route (R1) means a dev
  server with a test-only auth bypass cannot be reached from another project's
  container.
- **No socket, no root.** The proxy cannot start, stop or inspect containers.
  The CLI uses the user's own runtime (rootless Podman, or Docker with the
  user's existing access) and asks for nothing more.
- **Credentials.** DNS credentials (R7) never enter a container. The
  certificate key is readable by the user and the proxy only.
- **DNS rebinding protection.** Some routers and resolvers drop public answers
  that point at `127.0.0.1`, which a custom domain (R7) needs. `status` detects
  this and says so; `*.localhost` names are not affected.

## Phasing

1. **MVP:** R1–R6, R8, R9 and R11. HTTP on `*.localhost`. Adopted by both first
   consumers.
2. **HTTPS:** R7.
3. **winget:** R10.

## Decisions

- **Go, single binary.** lego is a Go library, GoReleaser covers every package
  target, and the same binary runs the proxy. Decided by the operator,
  2026-10-23.
- **Apache-2.0.** Permissive, with an explicit patent grant. Decided by the
  operator, 2026-10-23.
- **Name and home.** `berthlight`, in the `stormlantern-dev` organisation.
  Checked for collisions on GitHub, Homebrew, AUR, npm, PyPI, crates.io,
  Docker Hub and winget, and in trade mark searches, on 2026-10-23. Decided by
  the operator, 2026-10-23.

## Open questions

- **Proxy engine.** Own proxy on Go's standard library
  (`httputil.ReverseProxy`, which handles WebSocket upgrades), or Traefik with
  its file provider, or Caddy. The own proxy keeps it to one binary and one
  image, with no configuration to translate; Traefik and Caddy are proven.
  Proposed: own proxy.
- **Go module path.** `github.com/stormlantern-dev/berthlight` changes if the
  organisation is renamed. A vanity path such as
  `go.stormlantern.nl/berthlight` never changes, but needs a DNS record and a
  static page serving the `go-import` meta tag.
- **Proxy port.** One fixed high port, configurable. Which default.
- **Naming scheme for worktrees.** `<worktree>.<project>.localhost`, with what
  rule for the default when no name is given.
- **macOS runtimes.** Docker Desktop, OrbStack and Podman machine each run
  containers in a VM; publishing on the host's loopback behaves slightly
  differently in each. Needs testing on a Mac; CI cannot.

## What would change this plan

- **portless gains container-network routing** without a socket or published
  ports. Then contributing there is better than a second tool.
- **The Dev Container spec gains a way to name a container's ports** that tools
  can route to without the runtime socket.
- **Podman or Docker change user-defined network DNS** in a way that breaks
  alias resolution across networks.
