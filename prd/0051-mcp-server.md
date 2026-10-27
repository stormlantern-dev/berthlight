# An MCP server for berthlight

- **Status:** Accepted: build it, phase 1 first. Decided by the operator,
  2026-10-27
- **Date:** 2026-10-27
- **Tracking issue:** #51, which holds the test plan and the verification record
- **Builds on:** [0001](0001-berthlight.md) (R6 status, the agent skill),
  [0041](0041-compose.md), and the editor survey in
  [0047](0047-vscode-extension.md)
- **First consumer:** the operator, who uses Zed, where no extension can add
  a status bar item or a panel

## Summary

`berthlight mcp` runs a Model Context Protocol server on stdio. The AI
assistant in any editor that speaks MCP can then ask berthlight, through typed
tools rather than a shell:
- what the URL of this worktree is, and whether it is up;
- what the state of the proxy and all routes is;
- to wire a project's devcontainer.json.

Those editors include Zed, VS Code (Copilot), Cursor, Claude Code, Codex and
OpenCode. One server in the binary that already exists replaces a plugin per
editor for everything an assistant needs.

The decision this PRD asks for: **build `berthlight mcp` as a thin stdio
layer over the existing commands**, rather than a Zed extension, or nothing
on top of the agent skill (see "What was ruled out").

## Why

- **Editor plugins are per platform.** A VS Code extension (0047) covers the
  VS Code family only. Zed extensions are Rust compiled to WASM, and their API
  offers "languages, debuggers, themes, snippets, MCP servers, slash commands",
  with no status bar or panel
  ([Zed docs](https://zed.dev/docs/extensions/developing-extensions.html)).
  MCP is the one integration surface those editors share.
- **Read-only tools can be approved once; a shell cannot.** The skill (0001)
  teaches agents to run `berthlight url` and `status` in a shell. That works,
  but every shell call is either approved by hand or allowed wholesale. An MCP
  tool that is marked read-only can be allowed on its own, so "is my dev
  server up?" stops costing a click, without giving the agent a shell.
- **Structured answers, not parsed text.** The tools return the same JSON as
  `status --json` and `ls --json`. The assistant doesn't scrape a table meant
  for people.

## Goals

- **G1.** An assistant in Zed, VS Code or Claude Code gets the URL and health
  of the current worktree with one tool call, with no shell.
- **G2.** The same assistant can wire a project, with the user's approval, and
  is told what to do next (rebuild the container).
- **G3.** No weaker than the CLI on security. The server can do nothing the
  CLI can't, and opens nothing to the network.
- **G4.** No new runtime, no new install: the same binary, and only when an
  editor starts it.

## Non-goals

- **Replacing the skill.** The skill stays for agents with a shell and for
  explaining the why. The server is an extra way in, not a rewrite.
- **A Zed extension.** The settings snippet (M6) is enough to add a local
  server. Packaging one as a Zed extension can follow if people ask.
- **Remote or HTTP transport.** stdio only. A listening MCP server would need
  the whole of Harbour's protection (0045 U2) for a use nobody has asked for.
- **MCP resources and prompts.** Zed "currently supports MCP's Tools and
  Prompts features" and not resources
  ([Zed docs](https://zed.dev/docs/ai/mcp)). Everything goes through tools so
  every client gets it.
- **Running inside a sandboxed or containerised agent.** The server talks to
  Docker or Podman on the host, like the CLI. An agent inside a devcontainer
  or an OpenShell sandbox doesn't get it (see "What was ruled out").
- **`cert`, `skill install`, `completion`, the header.** These are one-off
  operator tasks, not things an assistant needs mid-task.

## Constraints and evidence

- **Official Go SDK.** `github.com/modelcontextprotocol/go-sdk`, latest
  `v1.8.0` (2026-09-04, from `proxy.golang.org/.../@latest`). It is a stable
  major version, maintained by the MCP project together with Google, so no
  third-party framework is needed.
- **Zed config.** A local stdio server is added under `context_servers` with
  `command`, `args` and `env`
  ([Zed docs](https://zed.dev/docs/ai/mcp)). Zed runs it on the host, not in
  the dev container, so it reaches the container engine like the CLI does.
  *Not verified in Zed with a dev container open; that is a test in #51.*
- **The JSON exists.** `status --json` already returns the proxy and, per
  route, `name`, `url`, `target`, `port`, `network`, `ok` and `detail`
  (`internal/cli/status.go`). `ls --json` returns the routes. The tools reuse
  these types.
- **The working directory is not known to the server.** An MCP client starts
  the server once, in a directory of its choosing. Tools that are about "this
  worktree" take a `dir` argument, defaulting to that directory. MCP roots
  are no way out: the SDK refuses `roots/list` "while serving a request on
  protocol version 2026-07-28" (SEP-2322), and the roots feature is
  deprecated by SEP-2577 (the SDK's `ClientCapabilities.RootsV2` comment).
  The `url` tool's description asks the assistant to pass the worktree's
  path. *Found while building phase 1.*

## What was ruled out

| Option | Why not |
|---|---|
| **A Zed extension with its own UI** | The API has no status bar or panel. What it can do (slash commands, an MCP server) is what M1–M6 give anyway. |
| **Only the skill** | It works, but needs a shell and an approval per call, and returns text for people (see "Why"). |
| **An HTTP MCP server, shared with Harbour** | It would put a second listener behind the same token and Origin rules for no extra client. stdio is what the editors start. |
| **A tool per CLI command** | `cert`, `proxy`, `completion` and friends would be noise in the assistant's tool list, and every extra tool costs context in every request. |
| **Integrating with NVIDIA OpenShell now** | See below. |

**NVIDIA OpenShell.** OpenShell runs agents (Claude Code, Codex, OpenCode,
Copilot CLI) in sandboxes, with egress allowed only by policy
([overview](https://docs.nvidia.com/openshell/latest/about/overview)). With
the Docker and Podman drivers the workload container has "networking turned
off", and the agent "can't reach a service, the gateway, DNS, or another
private address directly"
([architecture](https://docs.nvidia.com/openshell/latest/about/architecture)).
Dev servers in a sandbox reach the host through `openshell forward start
<port> <sandbox>`, and NemoClaw's docs note that a forward refuses a port
another sandbox already uses
([NemoClaw](https://docs.nvidia.com/nemoclaw/user-guide/openclaw/manage-sandboxes/operate-sandboxes/run-sandboxes)).
That is the collision berthlight solves. But berthlight routes to a container
on a `bl-` network by alias, and an OpenShell workload has no network to put
on one. Fitting them together needs a new kind of route, to a host port
(`openshell forward`) rather than a network alias, reached from the proxy
container. The docs read for this PRD don't say which address a forward binds
(*not verified*). Nobody on this project runs OpenShell yet, and it is on
0.1.x. So: nothing now. Revisit when someone uses it, starting with #49
(attach a running container), which adds the same idea of a route to
something berthlight didn't start.

## Design overview

```
editor (Zed, VS Code, Claude Code)
  └─ starts: berthlight mcp           (stdio, host side)
       ├─ tools: status, url, ls       (read-only)
       ├─ tools: devcontainer, rm, up  (change something; client asks the user)
       └─ the same code as the CLI commands, same state dir, same engine
```

The server is a cobra subcommand that builds an `mcp.Server` from the Go SDK,
registers the tools with JSON schemas derived from Go structs, and calls the
same functions the CLI commands call. It writes nothing to stdout but MCP
messages, and logs go to stderr.

## Requirements

Priorities use RFC 2119: **MUST**, **SHOULD**, **MAY**.

**M1 (MUST). One command, stdio only.** `berthlight mcp` speaks MCP over stdin
and stdout and exits when stdin closes. It opens no socket. It prints nothing
outside the protocol on stdout.
*Fit:* an integration test runs the server with the SDK's client, calls
`initialize` and lists the tools; after the client closes stdin, the process
exits within a second. `ss -lntp` during the test shows no listener owned by
the process.

**M2 (MUST). Read-only tools.**
- `status`: the proxy and every route with health, the same data as
  `status --json`.
- `url`: given `dir` (default: the server's working directory), the route's name, URL and health, resolved like `berthlight url`
  in that directory, including `--project` from devcontainer.json (#26).
- `ls`: the routes, like `ls --json`.

Each has `readOnlyHint: true`.
*Fit:* golden tests compare each tool's structured result with the CLI's JSON
for the same state. A test on a worktree wired with `--project` returns the
wired name. The tool list shows `readOnlyHint` on all three.

**M3 (MUST). Changing tools need the user.** `devcontainer` (wire a project,
with the arguments of `berthlight devcontainer`), `rm` (remove a route) and
`up` (start the proxy) carry `readOnlyHint: false`. `rm` also carries
`destructiveHint: true`. Each result says in one line what the user should do
next, such as "rebuild the container". No tool runs `down`, `cert` or `skill`.
*Fit:* the tool list has exactly these six tools, with these hints. A test
wires a temp project through the tool and gets the same devcontainer.json as
the CLI (the golden files under `internal/devcontainer/testdata`).

**M4 (MUST). No more power than the CLI.** Tools run as the user, use the
same state dir and engine, and touch only:
- the state dir;
- a devcontainer.json (and the Compose override) inside the `dir` they were
  given.

`dir` must be an existing directory. Errors come back as tool errors with the
CLI's message, never as a crash.
*Fit:* tests for:
- a `dir` that doesn't exist;
- a `dir` with no devcontainer.json;
- the engine being down.

Each returns `isError` with the CLI's text, and the server keeps answering.

**M5 (SHOULD). Useful descriptions.** Each tool's description says when to use
it in one or two sentences ("Call `url` before opening the dev server in a
browser or Playwright"), so the assistant picks it without the skill loaded.
*Fit:* in Claude Code with only the server configured (no skill), the prompt
"open the dev server of this worktree in Playwright" leads to a `url` call
first, in three of three runs.

**M6 (SHOULD). Setup snippets, not config edits.** `berthlight mcp config
<client>` prints the snippet for `zed`, `vscode`, `cursor` and `claude`, with
the absolute path of the running binary. It never edits an editor's config
file. The README shows the Zed snippet.
*Fit:* golden tests per client. Pasting the Zed snippet into Zed 1.25 makes
the server appear with six tools.

**M7 (MAY). Live status notifications.** While a client is connected, the
server MAY send `notifications/tools/list_changed` or log messages when a
route goes up or down, reusing `status --watch`.
*Fit:* deferred until a client shows them to the user.

## Security

- stdio only (M1): nothing on the network, no token, no Origin to check.
- The editor decides who may start the server; it runs with the user's rights,
  like the CLI the same user could run.
- The hints (M2, M3) let clients auto-approve the three read-only tools and ask
  for the rest. A client that ignores hints is no worse off than an agent with
  a shell and the skill.
- Tool input is data. A route name or `dir` from the assistant goes through
  the same validation as CLI arguments (name rules from 0001, path checks in
  M4).

## Success metrics

- The operator uses it in Zed for two weeks and reaches for `berthlight url` in
  a terminal less than before (self-reported).
- One of the Compose testers (#41) with a different editor configures it from
  the README alone.

## Phasing

1. **M1, M2, M4, M6 for Zed and Claude:** read-only, which answers the
   question whether assistants use it at all.
2. **M3, M5, the other snippets:** wiring through the assistant.
3. **M7**, only if a client surfaces it.

## Open questions

- **Does Zed's assistant run the server with a dev container open, and does
  `dir` resolve to the host path?** Owner: Claude, with the operator's Zed.
  Settled by the first test in #51.
- **Roots support per client.** Settled while building phase 1: roots are
  deprecated (see "Constraints and evidence"), so `dir` defaults to the
  server's working directory.

## What would change this plan

- Zed adding a status bar or panel API: then a Zed extension that shows status
  without asking becomes worth a PRD of its own.
- Assistants using the skill's shell commands just as smoothly, with no
  approval friction: then the server adds little, and phase 2 stops.
- Someone running berthlight with OpenShell: then a host-port route (see "What
  was ruled out") gets its own issue.
