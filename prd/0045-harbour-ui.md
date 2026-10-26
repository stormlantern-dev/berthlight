# Harbour: an optional graphical UI

- **Status:** Accepted: a local web UI, as an extra option next to the CLI. Decided
  by the operator, 2026-10-26
- **Date:** 2026-10-26
- **Tracking issue:** #45, which holds the test plan and the verification record
- **Builds on:** [0001](0001-berthlight.md) (R2 security, R6 status, R12
  terminal experience) and [0041](0041-compose.md)
- **First consumers:** the developers who test the Compose support (#41), for
  whom a command line is not the natural way in

## Summary

`berthlight ui` opens **Harbour**, a small graphical window onto berthlight in
the browser. It shows the proxy and every route as boats in a harbour, and
lets people do the everyday things without the command line:
- start and stop the proxy;
- open or copy a URL;
- remove a route;
- wire a project.

It looks like the header scene, an Amiga Workbench in the spirit of the boat,
the octopus and the canal houses, drawn by the same code. It is optional:
nothing runs unless someone starts it, and the CLI stays complete without it.

The decision this PRD asks for: **a local web UI started by the CLI**, as
proposed here, rather than a terminal UI, a native app or a UI served by the
proxy (see "What was ruled out").

## Why

- **Not everyone lives in a terminal.** berthlight's users open their dev
  servers in a browser anyway. A page there, next to their tabs, is where a
  status check or an "open" belongs for people who don't reach for a shell
  first.
- **Status is visual by nature.** Which worktrees are up, which are down and
  why is a list of boats, some moored and some adrift. `status --watch`
  already says it in text, the tab title and notifications; a picture says
  it at a glance.
- **The style is part of the product.** The header exists to make a first
  impression that hooks people (R12, #21). A UI in the same style carries
  that into everyday use.

## Goals

- **G1.** See the proxy and every route, with its health, live, without a
  command line.
- **G2.** Do the everyday actions from the same place: proxy up and down,
  open or copy a URL, remove a route, wire a project.
- **G3.** No weaker than the CLI on security. Nothing reachable from the
  network, from other users on the machine, or from other websites in the
  same browser.
- **G4.** Optional and light. Nothing runs, listens or installs unless the
  user starts it; no new runtime dependency.
- **G5.** Unmistakably berthlight: the Amiga retro style of the header, with
  the boat, the octopus and the canal houses.

## Non-goals

- **Replacing the CLI.** Everything stays possible on the command line. The
  UI covers the everyday subset; `cert`, `skill`, `completion` and the like
  stay CLI-only.
- **Managing devcontainers.** The UI does not start, stop or rebuild
  devcontainers, just as the CLI does not; that stays with the Dev Container
  CLI or the editor.
- **Remote or shared access.** No LAN, no other machines, no other users of
  this machine.
- **A native app or tray icon.** See "What was ruled out". An editor
  extension has its own PRD.
- **Themes.** One look: Harbour's. Reduced motion is an accessibility
  setting, not a theme.

## Constraints and evidence

- **The scene is already drawn in Go, as pixels.** `tui.Frame(t)` returns an
  `*image.RGBA` of `SceneW`×`SceneH` (80×20) chunky pixels. The Amiga
  version is `HiW`×`HiH` (`SceneW*hk` × `SceneH*hk`, with `hk = 4`, so
  320×80). The terminal header sends these as PNG through the Kitty graphics
  protocol (`internal/tui/scene.go`, `amiga.go`, `header.go`). The UI can
  serve the same frames, pixel for pixel, rather than redraw them.
- **The story exists.** In the scene's own comment, a boat passes at the
  waterline; an octopus surfaces, swipes and misses; the boat moors at a quay
  with three canal houses whose windows light up one by one
  (`internal/tui/scene.go`). Harbour reuses these sprites and gives them a
  meaning: moored is up, the octopus is trouble, a lit window is a healthy
  route.
- **Status is already structured.** `status --json` prints
  `status.Report`, with a top-level `ok`, a `proxy` object (name, port, ok,
  problem) and `routes`, whose field names are stable (`status --help`). The
  UI reads the same report, so the UI and the CLI cannot disagree.
- **Actions already exist as functions.** Since #37 every command is a cobra
  command over plain Go functions. The UI calls those functions; it does not
  shell out to `berthlight`.
- **The binary is 26.2 MB** (v0.6.0, linux/amd64, measured). The UI's
  embedded assets are a budget on top of that.
- **The proxy must not gain control.** 0001 R2: the proxy container has no
  runtime socket and reads its state read-only. Control stays on the host.

Not verified:

- **Font licence.** Whether a Topaz-like bitmap font is available under a
  licence that allows embedding. The original Topaz is Commodore's; see Open
  questions.
- **Browser behaviour for loopback pages.** That browsers' Private Network
  Access rules do not get in the way of a page on `127.0.0.1` talking to its
  own origin. They should not, as it is same-origin, but this is untested.

## What was ruled out

- **A full-screen terminal UI.** It is still a terminal, which is what the
  target users avoid. The Amiga graphics also need the Kitty protocol, so
  most terminals would get the chunky version. It may come later for SSH
  users, but it is not this PRD.
- **A native window (Wails, webview, Fyne).** These need cgo or per-platform
  toolchains, which breaks the static, cross-compiled GoReleaser build of
  R9, and every platform's packaging with it.
- **A UI served by the proxy at `berthlight.localhost:7480`.** That would put
  control next to, or inside, the least-privilege proxy, which would then
  need a path back to the host, or the runtime socket. That conflicts with
  R2.
- **A tray icon.** Platform-specific (cgo on macOS and Windows), and it shows
  little; a browser tab shows more for less.
- **A JavaScript framework and a frontend build.** A build step brings npm
  into CI and the supply chain, for a UI of a few views. Plain HTML, CSS and
  JavaScript, embedded with `go:embed`, are enough.

## Design overview

`berthlight ui` starts an HTTP server inside the CLI process, on
`127.0.0.1` and a random free port. It prints a URL with a one-time token,
opens it in the default browser (unless `--no-browser`), and stops on
Ctrl-C. The token is exchanged for a `SameSite=Strict`, `HttpOnly` cookie on
the first request and is then invalid, so the URL in the history and the
terminal is useless afterwards.

The page has three parts, in a Workbench-like window:
- **The title bar is the header scene,** playing once, then resting.
- **The harbour:** a quay with one berth per route. A route that is up has
  its boat moored and a lit window in its house. A route that is down has
  its boat adrift and the octopus near it, and the reason from `status` in a
  speech-bubble-like requester. A route still starting has its boat sailing
  in.
- **A menu and requesters:** proxy up and down, open and copy a URL, remove a
  route (with a confirm requester), and wire a project, which shows the
  diff, then applies it.

The page polls a small JSON API on the same origin about once a second. The
server answers from the same functions as `status --json` and the commands.

## Requirements

Priorities use RFC 2119: **MUST**, **SHOULD**, **MAY**.

**U1 (MUST). Optional, one command.** `berthlight ui` starts Harbour, prints
its URL and opens the browser; `--no-browser` only prints. Nothing about
Harbour runs, listens or is written to disk otherwise, and the CLI works
unchanged without it.
*Fit:* a test starts `ui`, reads the URL, gets a 200, sends an interrupt, and
sees the port closed; with no `ui` process, nothing listens beyond the proxy
(checked with `ss` in the integration test).

**U2 (MUST). Local and private.**
- It listens on `127.0.0.1` only, on a random port.
- Every request needs the session cookie, which only the one-time token
  grants.
- Requests whose `Host` is not `127.0.0.1:<port>` are refused (DNS
  rebinding).
- Changing requests must be `POST`, with a matching `Origin` (CSRF).
- No CORS.
- A strict Content-Security-Policy, with no inline script and no third-party
  origins.
*Fit:* tests for each:
- a second use of the token gets 403;
- a request without the cookie gets 403;
- a wrong `Host` gets 421 or 403;
- a cross-origin `POST` gets 403;
- the response headers carry the CSP;
- a request from another interface fails to connect.

**U3 (MUST). Live status.** Harbour shows the proxy (up or down, its port)
and every route with its URL and status, from the same report as `status
--json`, refreshed about every second. Every state that `status` reports has
a picture and its text.
*Fit:* an integration test stops a route's container and sees its berth turn
"down" with `status`'s detail within 3 s, then "up" after starting it again.

**U4 (MUST). Everyday actions.** Proxy up and down, open a URL in a new tab,
copy a URL, and remove a route (after a confirm requester). Each calls the
same Go function as the CLI.
*Fit:* an end-to-end test drives each action through the UI's API and checks
the result with the CLI (`ls`, `status`).

**U5 (SHOULD). Wire a project.** Given a project folder, a project name and a
port, Harbour shows the same diff as `devcontainer --dry-run` (image,
Dockerfile or Compose), and applies it on confirmation. Folders are typed or
pasted as paths: a browser cannot browse the host's file system.
*Fit:* an end-to-end test wires a test project through the API and gets the
same file as `berthlight devcontainer` does.

**U6 (MUST). Harbour's look.**
- **Pixels come from the shared code:** the title scene and the sprites are
  drawn by the same Go code as the terminal header, and scaled without
  smoothing (`image-rendering: pixelated`).
- **Amiga Workbench window style:** frames, a menu, requesters, and a
  bitmap font (see Open questions).
- **Moored, adrift, octopus and lit window** carry the meanings in the
  design overview.
*Fit:*
- golden-image tests for the sprites in each route state;
- the title scene's frames are byte-identical to `tui.FrameHi`'s;
- a reviewed screenshot of each state on #45, accepted by the operator.

**U7 (MUST). Usable by everyone.**
- Animation stops under `prefers-reduced-motion`.
- Every picture has its text (status, reason, URL), readable by a screen
  reader.
- Every action works from the keyboard.
- Contrast meets WCAG AA for text.
*Fit:* an automated accessibility check (axe through Playwright) reports no
violations; a keyboard-only run of every U4 action passes.

**U8 (SHOULD). Notifications.** Opt-in browser notifications when a route goes
down or comes up, with the same short text as `status --watch` ("shop.localhost
is down").
*Fit:* the end-to-end test grants the permission and sees one notification
per change.

**U9 (MUST). Light and offline.** All assets are embedded in the binary, with
no requests to the internet and no runtime dependency beyond a browser. The
binary grows by at most 1.5 MB.
*Fit:* the release snapshot reports the size difference; a test serves every
asset with the network blocked; CSP violations are zero.

**U10 (MAY). One window per machine.** A second `berthlight ui` opens the
running one instead of a second server.
*Fit:* two starts give one listener and two browser openings of the same
origin.

## Security

Harbour can do what the CLI can do, so it runs with the user's rights in the
user's process, and only while the user keeps it running. The threats are
other websites in the user's browser (CSRF, DNS rebinding) and other users
on the machine (a loopback port is reachable by all local users). U2 answers
both: an unguessable one-time token traded for a strict cookie, `Host` and
`Origin` checks, and loopback only. The proxy is untouched (R2): Harbour
talks to the runtime the way the CLI does, from the host.

## Success metrics

- At least two of the Compose testers (#41) use Harbour to check their routes
  in a normal working week, and say so on #45.
- The operator wires a new project through Harbour without touching the CLI.

## Phasing

1. **A read-only harbour** (U1, U2, U3, U6, U7, U9): the window, the title
   scene and the berths with live status. This proves the security model and
   the look before anything can change state.
2. **Actions and notifications** (U4, U8).
3. **Wiring** (U5) and, if wanted, one window per machine (U10).

## Open questions

- **The font.** A bitmap font in the Topaz style with a licence that allows
  embedding, or one drawn for berthlight, in the style of the scene's own
  letters. *Settled by:* phase 1; a licence check recorded in the PRD.
- **Frames from the server or drawn in the browser.** The server can send the
  scene and sprites as PNG frames, which stays pixel-identical with no port of
  the drawing code. Or the browser can draw them, which is lighter on a
  live page. *Settled by:* a phase 1 spike, measuring CPU at one frame per
  second for 20 routes.
- **Where wiring starts.** Typing a path is clumsy. Alternatives are a list of
  git repositories under folders the user names, or of folders with a
  `devcontainer.json`. *Settled by:* the testers in phase 3.

## What would change this plan

- **The testers want the terminal.** Then a full-screen terminal UI replaces
  phase 1, and this PRD is superseded.
- **A pure-Go native window without cgo becomes practical.** Then a window of
  its own may beat a browser tab.
- **The security model does not hold.** If browsers block or weaken the
  loopback same-origin set-up, Harbour stops at phase 1 until that is solved.
