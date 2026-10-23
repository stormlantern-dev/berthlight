---
name: berthlight
description: Give a devcontainer's dev server a stable name such as http://shop.localhost:7480 instead of a published host port, with berthlight. Use when a project's devcontainer.json publishes ports (appPort), when devcontainers or git worktrees collide on a port, when the user asks for a stable dev URL or mentions berthlight, when a dev server behind berthlight does not answer, or when docs, .env files or OAuth redirect URIs hard-code localhost:<port>.
license: Apache-2.0
compatibility: Needs the berthlight CLI on the host (not inside the container), Docker or Podman, and devcontainers. Docker Compose devcontainers need manual wiring.
metadata:
  version: "{{VERSION}}"
  homepage: https://github.com/stormlantern-dev/berthlight
---

# berthlight

berthlight routes `http://<name>.localhost:7480` to a dev server inside a
devcontainer. One proxy listens on the host's loopback; each devcontainer
joins its own container network instead of publishing a port, so projects
and worktrees never collide and their URLs never change.

Names are one DNS label: `<project>` for a project's main worktree,
`<project>-<worktree>` for any other (`shop.localhost`,
`shop-fix-login.localhost`). The worktree part comes from the worktree's
directory name, never its branch.

## Before you start

- berthlight runs **on the host**. Inside a devcontainer it is usually not
  installed and cannot reach the container runtime. If you are inside one,
  edit files there, but ask the user to run `berthlight` commands on the
  host, or run them where `berthlight version` works.
- If `berthlight` is not installed, tell the user how (do not install it
  yourself): `brew install stormlantern-dev/tap/berthlight`, `scoop install
  stormlantern/berthlight` after `scoop bucket add stormlantern
  https://github.com/stormlantern-dev/scoop-bucket`, `paru -S
  berthlight-bin`, or a package from the GitHub releases.

## Wire a project

1. **Find the dev server's port** inside the container: `appPort` or
   `forwardPorts` in `.devcontainer/devcontainer.json`, the dev script in
   `package.json`, `manage.py runserver`, `rails s`, a Go `ListenAndServe`.
2. **Pick the project name** with the user: short, because it appears in
   every worktree's URL (`shop` for a directory named `webshop`).
   Lowercase letters, digits and single hyphens, starting with a letter.
3. **Preview, then apply** in the main worktree:
   ```sh
   berthlight devcontainer shop 8080 --dry-run
   berthlight devcontainer shop 8080
   ```
   This edits `devcontainer.json` in place and keeps its comments. It adds
   `runArgs` (`--network` and `--network-alias`, both
   `bl-${localWorkspaceFolderBasename}`), adds an `initializeCommand` that
   registers the route on the host before the container starts (an existing
   command is kept, in the object form that runs both), and removes the
   `appPort` entries for that port. A second run changes nothing. It refuses
   a `runArgs` network of another name, and Compose devcontainers.
4. **Make the dev server listen on all interfaces inside the container**,
   not only on 127.0.0.1, or the proxy cannot reach it:
   - Vite: `server.host: true` (or `vite --host 0.0.0.0`)
   - Next.js: `next dev -H 0.0.0.0`
   - Django: `manage.py runserver 0.0.0.0:8000`
   - Rails: `rails server -b 0.0.0.0`
   - Go: `:8080`, not `127.0.0.1:8080`
5. **Allow the name in host checks**, as a suffix so every worktree passes:
   - Vite: `server.allowedHosts: ['.localhost']`
   - webpack-dev-server: `allowedHosts: ['.localhost']`
   - Next.js: `allowedDevOrigins: ['*.localhost']`
   - Django: `ALLOWED_HOSTS = ['.localhost']`
   - Rails: `config.hosts << '.localhost'`

   The proxy passes the original `Host` header through unchanged.
6. **Hot reload through the proxy**: the browser reaches the dev server on
   port 7480, so a dev server that tells its client which port to use must
   say 7480. Vite: `server.hmr.clientPort: 7480`. WebSockets pass through.
7. **Rebuild the devcontainer** (Dev Containers: Rebuild Container, or
   `devcontainer up --workspace-folder . --remove-existing-container`).
8. **Check**: `berthlight url` prints this worktree's URL; `berthlight
   status` checks the proxy and every route and exits 1 when something is
   wrong. Open the URL.

## Update the project around it

- **Docs and scripts**: replace `localhost:<port>` with the name URL, or
  with `$(berthlight url)` where a command can run it. Remove port tables
  and "which port is mine" advice. Note in the README that dev URLs come
  from berthlight and that the dev server must bind 0.0.0.0.
- **.env files**: set the app's own URL (`APP_URL`, `NEXTAUTH_URL`,
  `PUBLIC_URL`, ...) from `berthlight url` rather than a fixed port.
- **OAuth redirect URIs**: providers do not accept wildcards, so register
  the main worktree's URL once per provider, e.g.
  `http://shop.localhost:7480/auth/callback`. It never changes. Other
  worktrees use the project's development sign-in bypass. Some providers
  refuse plain http on names other than `localhost`; say so rather than
  working around it.
- **Cookies**: never set `Domain=.localhost` (or the base domain): each
  worktree keeps its own session only when cookies have no `Domain`.

## When it does not work

Run `berthlight status`. Each problem has its own message:

| status says | do |
|---|---|
| proxy container does not exist / has stopped | `berthlight up` |
| nothing answers on 127.0.0.1:7480 | port mismatch: `berthlight down && berthlight up` |
| network bl-... does not exist | start the devcontainer (its initializeCommand creates it) |
| the proxy is not on network bl-... | `berthlight up` |
| no container answers to "bl-..." | devcontainer not running, or runArgs missing: run step 3 again and rebuild |
| nothing listens on port N | dev server not started, wrong port, or bound to 127.0.0.1 (step 4) |
| has not loaded this route | check the proxy's logs (`docker logs berthlight` or `podman logs berthlight`) |

A browser page from berthlight itself (404 "no route", 502 "did not
answer") says the same. A 403 or "Invalid Host header" from the app is
step 5.

## Do not

- Publish the dev server's port again (`appPort`, `-p`): that brings the
  collisions back.
- Edit berthlight's `routes.json` by hand; use `berthlight add`/`rm`/`ls`.
- Use `berthlight add --replace` without asking: it takes a name over from
  another checkout.
- Put two devcontainers on one `bl-` network or give two routes the same
  alias; berthlight refuses both, because they would answer for each other.
