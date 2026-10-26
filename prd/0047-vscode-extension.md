# A VS Code extension for berthlight

- **Status:** Proposed
- **Date:** 2026-10-26
- **Tracking issue:** #47, which holds the test plan and the verification record
- **Builds on:** [0001](0001-berthlight.md) (R5 wiring, R6 status, R8 skill),
  [0041](0041-compose.md) and [0045](0045-harbour-ui.md) (Harbour's look)
- **First consumers:** VS Code users among the Compose testers (#41); the
  operator, if the extension works in VSCodium (see Open questions)

## Summary

A VS Code extension that brings berthlight to the people who open their
devcontainers with "Reopen in Container" and seldom touch a terminal:
- the current worktree's URL and health in the status bar;
- all routes in a side view;
- "Wire this project" as a diff they review and apply.

It runs on the user's machine, next to berthlight and the container runtime,
also while the window is connected to a devcontainer. It drives the
berthlight CLI rather than reimplementing it. Like Harbour, it is an extra:
the CLI stays complete without it.

The decision this PRD asks for: build an extension that wraps the CLI, for VS
Code first, with VSCodium (Open VSX) as a SHOULD.

## Why

- **The editor is where these users are.** A devcontainer user in VS Code
  starts, rebuilds and enters containers from the editor. The question "what
  is this worktree's URL, and is it up?" belongs in the same place.
- **VS Code's own port forwarding works against berthlight.** VS Code
  forwards container ports to `localhost`, picking another port on a clash.
  That brings back the guessing berthlight removes. The extension can point
  to the berthlight URL instead.
- **Wiring is a file change, and editors are good at those.** A diff the user
  reviews in the editor is the natural form of `berthlight devcontainer
  --dry-run`.

## Goals

- **G1.** The current worktree's berthlight URL is one click away, with its
  health visible, in every window: local or connected to a devcontainer.
- **G2.** All routes, their health, and the everyday actions (open, copy,
  remove, proxy up/down) in one view.
- **G3.** Wiring a project from the editor, as a reviewed diff, followed by
  a rebuild.
- **G4.** No weaker than the CLI on security, and no long-lived publishing
  secrets.
- **G5.** Recognisably berthlight, with icons and, optionally, the Harbour
  scene in the editor.

## Non-goals

- **Replacing the Dev Containers extension** or managing containers. Starting,
  rebuilding and attaching stay with the Dev Containers extension (or, in
  VSCodium, its replacement); the extension only asks it to rebuild.
- **Reimplementing berthlight in TypeScript.** Every answer comes from the
  CLI (`--json` where there is one); the extension parses and shows it.
- **Other editors** (JetBrains, Zed, Neovim). Each would need its own PRD.
- **Installing berthlight.** The extension detects a missing CLI and explains
  how to install it; it does not download binaries.

## Constraints and evidence

- **A `ui` extension runs locally, also in a container window.** From VS
  Code's documentation on remote extensions: `"extensionKind": ["ui"]`
  "indicates the extension must run close to the UI", and in a container
  window such an extension runs in the local extension host, while
  `workspace` extensions run in the container
  ([remote extensions](https://code.visualstudio.com/api/advanced-topics/remote-extensions),
  [extension host](https://code.visualstudio.com/api/advanced-topics/extension-host)).
  berthlight runs on the host (0001), so the extension must be `ui`.
- **The CLI already speaks JSON.** `ls --json` and `status --json` have stable
  field names (`status --help`). `url` prints one line, and `devcontainer
  --dry-run` prints a unified diff.
- **Marketplace publishing can avoid a stored token.** `vsce publish
  --azure-credential` publishes with a Microsoft Entra ID identity, which
  GitHub Actions can obtain through OIDC federation. This is the same idea as
  the project's GCP Workload Identity Federation (R9). It is documented by
  Microsoft for Azure login, and used for the Marketplace in a community
  write-up
  ([Azure OIDC](https://learn.microsoft.com/en-us/azure/developer/github/connect-from-azure-openid-connect),
  [write-up](https://dev.to/emrecodes/publishing-to-the-vs-code-marketplace-with-ci-4p2h)).
  Not yet tried here.
- **VSCodium has no Microsoft Dev Containers extension.** It is proprietary
  and not on Open VSX; VSCodium users use community alternatives, for
  example DevPod-based ones
  ([Home Assistant forum](https://community.home-assistant.io/t/devcontainer-alternative-for-vscodium/1013879),
  [vscodium-devpodcontainers](https://codeberg.org/3timeslazy/vscodium-devpodcontainers)).
  The operator's own containers carry a `codium-devcontainer-…` name, so
  this matters to the first consumer.

Not verified:

- **How a `ui` extension knows a container window's host folder.** It is
  probably encoded in the `dev-container+…` remote authority, or found
  through the container's `devcontainer.local_folder` label. It is needed to
  run `berthlight url -C <host folder>`.
- **Whether Open VSX supports publishing without a long-lived token.**
- **Whether Marketplace publishing with `--azure-credential` works** for a
  publisher linked to an Entra application. The write-up warns this link is
  the hard part.

## What was ruled out

- **Running in the container (`workspace` kind).** berthlight and the
  runtime are on the host; from the container, the extension could neither
  run the CLI nor reach the runtime.
- **Talking to the runtime or `routes.json` directly.** That would duplicate
  the CLI's logic and drift from it. The CLI is the one implementation.
- **Embedding Harbour's server.** The extension does not start `berthlight
  ui`'s web server: a webview can show the same pixels from bundled assets,
  with no listening port.
- **A personal access token for publishing.** It conflicts with G4 and the
  project's no-stored-secrets rule (R9).

## Design overview

The extension lives in `editors/vscode/` in this repository, written in
TypeScript, bundled with esbuild, and released separately (tags `vscode-v*`).

It runs `berthlight` from `PATH`, or from a configured path, with fixed
arguments and no shell, and reads its JSON. It offers:
- **A status bar item:** the window's worktree URL and a health dot. Click to
  open it in the browser; the menu offers copy, or VS Code's Simple Browser.
- **A "berthlight" view** in the Remote Explorer or the activity bar: the
  proxy and every route, with health, and actions on each. Refreshed every
  few seconds while visible.
- **Commands:** "Wire this project to berthlight" (opens the diff from
  `devcontainer --dry-run` in a diff editor; Apply writes it, then offers
  "Rebuild Container"), "Open this worktree's URL", "Proxy up", "Proxy down".
- **Notifications** when the window's own route goes down or comes up.
- **Optionally, a Harbour panel:** a webview with the harbour from 0045,
  built from the same assets.

## Requirements

Priorities use RFC 2119: **MUST**, **SHOULD**, **MAY**.

**V1 (MUST). Runs where berthlight runs.** `extensionKind` is `["ui"]`. In a
window connected to a devcontainer, the extension runs on the host and finds
the window's host folder.
*Fit:* an automated test (`@vscode/test-electron`, with a devcontainer) shows
the right URL in a container window; checked by hand once in VS Code.

**V2 (MUST). The CLI is the only backend.** Every value shown comes from
`berthlight` output. The extension runs the binary with an argument list,
never through a shell, and handles a missing or old CLI with a message that
says how to install or update it.
*Fit:* unit tests with a fake CLI cover:
- parsing of `ls --json` and `status --json`;
- a missing binary;
- a version older than the extension supports;
- the arguments, checked for no shell interpolation.

**V3 (MUST). This worktree's URL and health in the status bar.** It is
hidden when the folder is not wired. It updates within 5 s of a change.
*Fit:* an integration test stops and starts the route and sees the item
change; clicking opens the URL.

**V4 (MUST). The routes view and its actions.** It shows the proxy and every
route with its health and `status`'s detail. Each route can be opened,
copied and removed (after a confirm), and the proxy can be started and
stopped.
*Fit:* integration tests drive each action and check the result with the CLI.

**V5 (SHOULD). Wire this project.** The diff from `devcontainer --dry-run`
opens in a diff editor; Apply writes exactly that; then the extension offers
"Rebuild Container" through the Dev Containers extension when it is present.
Wiring needs a trusted workspace (Workspace Trust).
*Fit:*
- an integration test wires a test project and gets the same file as the
  CLI;
- in an untrusted workspace the command is disabled.

**V6 (SHOULD). Notifications** for the window's own route, with the same text
as `status --watch`, and a setting to turn them off.
*Fit:* an integration test sees one notification per change.

**V7 (MUST). Publishing without stored secrets.** The release workflow
publishes to the Visual Studio Marketplace through Entra ID federation from
GitHub Actions. Open VSX publishing follows the same rule, or waits.
*Fit:*
- a tagged release publishes, and the repository holds no Marketplace or
  Open VSX token;
- *Verify release credentials* covers the federation.

**V8 (SHOULD). VSCodium.** The extension is published on Open VSX and works in
VSCodium. Container windows work through whichever devcontainer extension
VSCodium uses, as far as V1's host-folder detection allows.
*Fit:* checked by hand by the operator in VSCodium; recorded on #47.

**V9 (MAY). Harbour in the editor.** A webview panel with Harbour's harbour
(0045), from bundled assets, with no listening port and a strict CSP.
*Fit:* the panel renders the same frames as 0045's golden images; no network
requests in the webview's devtools.

**V10 (MUST). Quality bar.** Every command has a title in the command
palette, every view has an empty state that says what to do, and the
extension activates only when needed: on its view, a command, or a workspace
with a `devcontainer.json`.
*Fit:* a test checks the activation events; startup cost is measured with
VS Code's "Show Running Extensions" and stays under 50 ms.

## Security

The extension runs with the user's rights on the host, as the CLI does,
and only executes the berthlight binary with fixed arguments. It opens no
port. Writing files (V5) needs Workspace Trust, so a hostile repository
opened in Restricted Mode cannot trigger it. Publishing uses federated
identity, not a stored token (V7).

## Success metrics

- Two of the Compose testers who use VS Code install the extension and keep
  it installed for two weeks.
- Fewer "which port is it?" moments: a tester reports opening worktrees
  through the extension rather than VS Code's forwarded ports.

## Phasing

1. **Read-only:** the status bar and the routes view (V1, V2, V3, V4 without
   actions, V10), published to the Marketplace (V7). This settles the
   host-folder question and publishing.
2. **Actions and notifications** (V4, V6).
3. **Wiring** (V5), then VSCodium (V8) and Harbour (V9).

## Open questions

- **The host folder of a container window** (V1). *Settled by:* phase 1, by
  testing the remote authority and the container label.
- **VSCodium and its devcontainer extension.** Which extension the operator
  uses to open containers in VSCodium, and whether V1 can work with it.
  *Settled by:* the operator; a phase 3 trial.
- **Open VSX without a stored token.** *Settled by:* research before phase
  3; until then Open VSX waits, rather than storing a token.
- **Suppressing VS Code's port forwarding for wired ports** (`forwardPorts`,
  `portsAttributes` with `onAutoForward: ignore`). Should `berthlight
  devcontainer` write it, or the extension? *Settled by:* phase 2.

## What would change this plan

- **The host folder cannot be found from a container window.** Then V1 falls
  back to the routes view and asks the user to pick, and the status bar shows
  only in local windows.
- **Marketplace federation does not work.** Then publishing waits; a stored
  token is not the fallback.
- **The testers do not use VS Code.** Then the extension waits for a consumer,
  like Compose did.
