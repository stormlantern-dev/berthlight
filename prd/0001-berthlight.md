# berthlight: stable names for devcontainer dev servers

- **Status:** Accepted
- **Date:** 2026-10-23
- **Tracking issue:** #1, which holds the test plan and the verification record
- **First consumers:** two private projects of the author: a design system
  (component gallery on one port) and a Go web application (one devcontainer
  per git worktree, several running at once).

## Summary

A small command-line tool, installed once per machine, that gives every
devcontainer's dev server a stable name such as `http://gallery.localhost:7480`
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
- **G7.** A pleasure to use in a modern terminal, from the first run: links
  that open, progress that shows, a notice when a dev server comes up. Never
  in the way of scripts and agents, which get plain text.

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
browser ─► 127.0.0.1:7480 ──────────────► berthlight proxy ─► gallery:8089         (network bl-gallery)
                                                         ├─► shop:8080            (network bl-shop)
                                                         └─► shop-fix-login:8080  (network bl-shop-fix-login)
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
- publishes one port, on `127.0.0.1` only (and `::1`): **7480** by default,
  configurable. HTTP and HTTPS share it; the proxy tells them apart by the
  first byte of the connection (a TLS handshake starts with `0x16`). Once a
  certificate is configured (R7), plain HTTP gets a redirect to HTTPS on the
  same port;
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

**R4 (MUST). Names that need no setup.** Names live under `localhost` by
default, which browsers and systemd-resolved send to loopback without any DNS
change. Every name follows the naming convention below, so it works with one
wildcard certificate, with any DNS provider and ACME CA, and in the usual
places an application keeps its own URL.

*Fit:* on a fresh CI runner, a route is reachable with `curl` by name, with no
change to `/etc/hosts` or DNS.

### Naming convention

A full name is `<label>.<base>`.

- **`<base>`** is `localhost` by default, or a zone the user controls, such as
  `dev.example.com` (R7). Nothing else: `.local` belongs to mDNS
  ([RFC 6762](https://www.rfc-editor.org/rfc/rfc6762)), `.test` and
  `.internal` need DNS set up on every machine and cannot get a publicly
  trusted certificate, and real TLDs such as `.dev` and `.app` belong to their
  registry and are on the browsers' HSTS preload list.
- **`<label>`** is exactly **one DNS label**: `<project>` for a project's main
  worktree, `<project>-<worktree>` for any other worktree. For example
  `shop.localhost`, `shop-fix-login.localhost`, `gallery.localhost`.

Why one label:

- **Certificates.** A wildcard certificate covers one label:
  `*.dev.example.com` covers `shop-fix-login.dev.example.com` but not
  `fix-login.shop.dev.example.com`. One label means one certificate for every
  project and worktree, from any ACME CA.
- **OAuth.** Identity providers do not accept wildcard redirect URIs, so each
  name that signs in must be registered by hand. The main worktree's name,
  `<project>.<base>`, never changes; register that one, once per provider.
  Other worktrees use the project's development sign-in bypass. Providers
  differ on whether they accept plain `http` on a name other than
  `localhost`; an `https` name under the user's own domain (R7) is accepted
  by any provider that accepts `https`.
- **Host checks.** Dev servers that check the `Host` header (Vite's
  `server.allowedHosts`, Django's `ALLOWED_HOSTS`, Rails' `config.hosts` and
  others) accept a domain suffix. Allowing `.<base>` once covers every
  worktree. The proxy passes the original `Host` header through unchanged.
- **Cookies.** Cookies without a `Domain` attribute stay with their exact
  name, so worktrees do not share sessions. Projects must not set `Domain` to
  the base.

Rules for a label:

- Letters, digits and hyphens only, lowercase, 1 to 63 characters, starting
  with a letter and not ending with a hyphen. No underscores: they are not
  valid in host names, and some URL and cookie parsers reject them.
- No `--` in the third and fourth position, which is reserved for
  internationalised names (`xn--`).
- `<project>` comes from `--project`, else from the main worktree's
  directory name. The skill (R8) suggests a short one (`shop` for a
  directory named `webshop`), because it is typed and appears in every
  worktree's name.
- `<worktree>` is the worktree's directory name with the project's directory
  name stripped from the front (`webshop-fix-login` gives
  `fix-login`). Not the branch: a worktree can switch branch, and its name
  must not change when it does.
- Anything else is lowercased, every run of invalid characters becomes one
  hyphen, and a label longer than 63 characters is cut to 58 and given a
  hyphen and four hex characters of a hash of the full name, so two long
  names stay distinct.
- `add` refuses a name another route holds, unless `--replace`.

`berthlight url` prints the current worktree's full URL, so scripts and
`.env` files can take it instead of hard-coding it.

*Fit:* table tests for the derivation (main and other worktrees, prefix
stripping, slashes, underscores, non-ASCII, `--` in position 3–4, length
cut-off with hash); an integration test where two worktrees of one project
get distinct names, both served under one wildcard certificate in the R7
test.

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
`berthlight cert` obtains a wildcard certificate (`*.dev.example.com`), which
covers every name because names are one label (R4), from any ACME CA
(Let's Encrypt by default) with the DNS-01 challenge, through
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
to update a project's documentation and OAuth redirect URIs. Codex, OpenCode
and pi all read the shared Agent Skills location `.agents/skills`, and Claude
Code reads `.claude/skills`, so one install for all four writes two copies.

*Fit:* tests assert the target path per agent and scope, against each agent's
current documentation; the `SKILL.md` passes the format's validation (name
matches directory, description length).

**R9 (MUST). Distribution.** Each tagged release publishes, through
GoReleaser:
- a Homebrew cask in the organisation's tap (macOS and Linux; GoReleaser
  replaced formulas with casks in v2.10);
- `.deb`, `.rpm` and `.apk` packages and archives on the GitHub release;
- an AUR `-bin` package;
- a Scoop manifest in the organisation's bucket;
- the proxy image, multi-arch (amd64, arm64). The CLI reads the image
  reference from its configuration, with the release's image as default, so
  a move of the registry or organisation needs no new release;
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
  do not need Linux containers. Hosted runners for those platforms cannot run
  Linux containers: the macOS runners are virtual machines without nested
  virtualisation, and the Windows runners run Windows containers only.
- macOS integration tests (SHOULD): on a self-hosted runner on the author's
  Mac, against Docker Desktop, OrbStack and Podman machine. Started by hand
  or by a release tag only, never by a pull request, so code from a fork
  never runs there.
- A GoReleaser snapshot build on every pull request.

*Fit:* the workflows exist and pass on main.

**R12 (SHOULD). Terminal experience.** On an interactive terminal, berthlight
uses what modern terminals (Ghostty, Kitty, WezTerm, iTerm2, Windows Terminal,
VTE-based ones) understand, and falls back to plain text everywhere else:
- **Links.** Every URL it prints is a hyperlink (OSC 8).
- **Copy.** `berthlight url --copy` puts the URL on the clipboard through the
  terminal (OSC 52), which also works over SSH and inside a container.
- **Progress.** While the proxy or a route comes up, a sailboat crosses the
  water to its berth and moors when the dev server answers, in Unicode
  characters, with an ASCII boat where emoji do not render (the Linux
  console). Each frame is one synchronized update (mode 2026), so terminals
  that support it never show half a frame. Operations that finish within
  150 ms draw nothing.
- **Watch.** `berthlight status --watch` keeps the status (R6) on screen and
  current: a progress indicator in the tab or taskbar while a route starts
  (OSC 9;4), the tab title as a summary ("berthlight · 3/3 ok"), and a
  desktop notification when a route comes up or goes down (OSC 9 and 777;
  OSC 99 in Kitty).
- **Graphics (MAY).** Where the Kitty graphics protocol is available (Kitty,
  Ghostty), a pixel-art boat in place of the Unicode one.
- **Plain without a terminal.** Animations and escape sequences go to
  standard error, and only when it is a terminal; standard output is the same
  with or without one. No animation with `TERM=dumb`, in CI, or with
  `BERTHLIGHT_NO_ANIMATION`; no colour with `NO_COLOR`. `--json` on `ls` and
  `status` gives agents and scripts a stable format.

*Fit:* golden tests of the rendered frames (width stays constant while the
boat moves, berthed and failed states); a test that piped output has no
escape sequences; a test per escape sequence (OSC 8, 52, 9;4, 9, 777, 99)
against its specification; a manual check in Ghostty and Kitty, recorded on
the tracking issue.

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
2. **Terminal experience:** R12. The boat already shows on `up` and `add`.
3. **HTTPS:** R7.
4. **winget:** R10.

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
- **Module path `stormlantern.nl/berthlight`.** One namespace on the
  operator's own domain for all modules, so a rename of the GitHub
  organisation does not change import paths. Served from a small static site
  that lists the modules and generates a page with the `go-import` tag for
  each. Ecosystems without vanity paths follow the organisation: npm scopes are
  independent of GitHub anyway, and Homebrew, Scoop and AUR sources follow
  GitHub's redirect after a rename. Decided by the operator, 2026-10-23.
- **Proxy port 7480.** High, not registered with IANA, free on the
  operator's machines; HTTP and HTTPS share it (R2). Decided by the operator,
  2026-10-23.
- **One-label names per worktree** (R4, naming convention). Decided by the
  operator, 2026-10-23, with the condition that names work with ACME and
  ordinary application configuration and do not depend on one vendor.
- **macOS testing on the operator's Mac** through a self-hosted runner (R11).
  Paid hosted macOS runners have the same limit as the free ones; bare-metal
  Mac hosting costs more than this project warrants. Decided by the operator,
  2026-10-23.

- **Own proxy on Go's standard library** (`httputil.ReverseProxy`, which
  handles WebSocket upgrades), not Traefik or Caddy. One binary and one image,
  no configuration to translate, and the shared HTTP/HTTPS port (R2) is a few
  lines. Decided by the operator, 2026-10-23.

## Open questions

- **macOS runtimes.** Docker Desktop, OrbStack and Podman machine each run
  containers in a VM; publishing on the host's loopback behaves slightly
  differently in each. To be found out on the self-hosted runner (R11).

## What would change this plan

- **portless gains container-network routing** without a socket or published
  ports. Then contributing there is better than a second tool.
- **The Dev Container spec gains a way to name a container's ports** that tools
  can route to without the runtime socket.
- **Podman or Docker change user-defined network DNS** in a way that breaks
  alias resolution across networks.
