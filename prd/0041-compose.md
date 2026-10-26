# Docker Compose devcontainers

- **Status:** Proposed
- **Date:** 2026-10-26
- **Tracking issue:** #41, which holds the test plan and the verification record
- **Builds on:** [0001](0001-berthlight.md), R1, R3, R5 and the worktree
  decision from #35
- **First consumer:** none yet. None of the author's projects uses a Compose
  devcontainer (checked 2026-10-26). See "What would change this plan".

## Summary

`berthlight devcontainer` today refuses a devcontainer that uses Docker Compose
(`dockerComposeFile` in `devcontainer.json`), and tells the user to wire the
network by hand. Done by hand, that wiring works for one worktree only.

This PRD makes Compose devcontainers first-class. Each worktree gets its own
Compose project, so its own database and other services. Only the
devcontainer's own service gets a berthlight name. The wiring lives in a small
override file that berthlight writes for each worktree, on the host, before
Compose starts.

The decision this PRD asks for: build Compose support now, without a first
consumer, or keep the refusal and document the wiring by hand.

## Why

- **The plain wiring does not apply.** For a Compose devcontainer, the Dev
  Container CLI ignores `runArgs`, which carry berthlight's `--network` and
  `--network-alias` (measured, see Evidence). The network has to be named in a
  Compose file.
- **A committed Compose file cannot name the worktree.** Compose interpolates
  environment variables, not devcontainer variables such as
  `${localWorkspaceFolderBasename}`. One fixed network name in a committed
  file puts every worktree on the same network and alias, which is the
  collision berthlight exists to remove.
- **Compose projects are where worktrees hurt most.** Every worktree runs a
  whole stack, the app plus a database or a queue. Published ports multiply
  with it, and so does the confusion about which database a dev server talks
  to.

## Goals

- **G1.** A Compose devcontainer is wired with the same one command as any
  other: `berthlight devcontainer <project> <port>`.
- **G2.** Each worktree runs its own Compose stack, side by side with the
  others. Only the dev server is reachable, by name; the other services stay
  internal to their stack.
- **G3.** Nothing machine- or worktree-specific is committed.
- **G4.** Git works inside a worktree's Compose devcontainer.

## Non-goals

- **Managing Compose.** berthlight does not run `compose up` or `down`, and
  does not watch Compose projects. The Dev Container CLI or the editor does
  that, as now.
- **Names for other services.** Only the devcontainer's `service` gets a
  berthlight name. A database or an admin UI in the same stack gets none.
  That can be a separate PRD if a consumer needs it.
- **Compose without a devcontainer.** Plain `docker compose` projects are out
  of scope (0001, Non-goals).
- **podman-compose.** The target is Docker Compose v2 as the Compose provider,
  with Docker or Podman as the runtime. podman-compose may work but is not
  tested or promised.
- **Kubernetes**, as in 0001.

## Evidence

All measured on 2026-10-26 with Dev Container CLI 0.89.0, Docker Compose
5.6.1 and Podman 6.1.4 (rootless, Compose through Podman's API socket). The
test project had two worktrees: `api`, and `api-feat` made with
`--relative-paths`. Each had an `app` service (the devcontainer) and a `db`
service, and an `initializeCommand` that wrote
`.devcontainer/berthlight.compose.yml` and created the network.

1. **`initializeCommand` runs before Compose reads its files.** The override
   file did not exist before the first run. The CLI log shows the
   `initializeCommand` (`init-ran-for-api`) before the first
   `docker compose -f …/compose.yml -f …/berthlight.compose.yml`. Compose then
   used the file the command had just written.
2. **Each worktree is its own Compose project.** The CLI ran
   `docker compose --project-name api_devcontainer` and
   `--project-name api-feat_devcontainer`. Both stacks ran at once:
   `api_devcontainer-db-1` and `api-feat_devcontainer-db-1` on their own
   default networks.
3. **`runArgs` is ignored for Compose.** A `--label=from-runargs=yes` in
   `runArgs` was absent from the `app` containers' labels.
4. **The override puts only the dev service on berthlight's network.**
   `api_devcontainer-app-1` was on `api_devcontainer_default` and `bl-api`,
   with alias `bl-api`. `db` was only on `api_devcontainer_default`. With
   routes added for both worktrees, `curl http://api.localhost:7597/`
   returned `api answers`, and `http://api-feat.localhost:7597/` returned
   `api-feat answers`. `berthlight status` reported both `ok, HTTP 200`.
5. **`--mount-git-worktree-common-dir` does nothing for Compose.** In
   `api-feat`'s container, with the flag, `git status` failed:
   `fatal: not a git repository: (null)`. Its `.git` read
   `gitdir: ../api/.git/worktrees/api-feat`. Compose's own volumes decide
   the mounts, as with `workspaceMount` (devcontainers/cli#1243).
6. **An override volume fixes git.** Adding
   `- <host>/api/.git:/api/.git` to the `app` service in an override, so
   that the relative gitdir resolves from `/workspace`, made `git status`
   report `## api-feat` and `git log` show the commit.

Not verified:

- That Compose files never see devcontainer variables. This was not tested;
  it rests on the Dev Containers spec, which substitutes them in
  `devcontainer.json` only.
- VS Code: the Dev Containers extension follows the same spec, but it was not
  run.
- Docker Desktop and macOS.
- Compose's `!reset` tag with Podman's Compose provider (see Open questions).

## Prior art and what was ruled out

- **`runArgs`, as for image and Dockerfile devcontainers.** Ignored for
  Compose (Evidence 3).
- **A fixed network in the committed Compose file.** Every worktree would
  share the network and the alias (Why).
- **A variable in `.env`.** Compose reads the project directory's `.env`, but
  that file is often the project's own, and is committed or holds secrets.
  Writing to it per worktree would clobber it.
- **`--mount-git-worktree-common-dir` for git.** No effect with Compose
  (Evidence 5).
- **Editing the user's Compose file.** It is the project's file, often shared
  with CI and other tools. berthlight adds an override file and touches only
  `devcontainer.json`, as it does now.

## Design overview

`berthlight devcontainer <project> <port>` on a Compose devcontainer:

1. Adds `berthlight.compose.yml` to `dockerComposeFile`, turning a string
   into a list.
2. Sets `initializeCommand` to run `berthlight add`, as now, with a new flag
   that names the override file and the service.
3. Adds the override file to `.devcontainer/.gitignore`.
4. Shows all of it with `--dry-run`, as now.

On every start, `berthlight add` (the `initializeCommand`, on the host) writes
the override for this worktree. The override:

- joins the `service` to `bl-<worktree folder>`, with that alias, keeping its
  default network;
- stops publishing `<port>` for that service;
- mounts the repository's git directory where the worktree's `gitdir` points,
  when the folder is a linked worktree.

Then it registers the route, as now. Compose merges the override over the
project's own files.

## Requirements

Priorities use RFC 2119: **MUST**, **SHOULD**, **MAY**.

**C1 (MUST). One command wires a Compose devcontainer.**
`berthlight devcontainer <project> <port>` accepts a `devcontainer.json` with
`dockerComposeFile` and `service`, makes the changes in the design overview,
and keeps comments and layout, as R5 does. A second run changes nothing.
*Fit:* golden-file tests for `dockerComposeFile` as a string and as a list,
with and without an existing `initializeCommand`; a second run gives no diff;
`--dry-run` shows every file it would touch.

**C2 (MUST). The override is per worktree and never committed.** The
`initializeCommand` writes the override on each start, from the worktree's
folder name, and the override is listed in `.devcontainer/.gitignore`.
*Fit:* after wiring and starting two worktrees, `git status` is clean in
both, and each worktree's override names its own network.

**C3 (MUST). Only the dev service gets a name.** The override puts the
devcontainer's `service` on `bl-<folder>`, with that alias, and keeps it on
its project's default network. Other services are unchanged.
*Fit:* an integration test starts a Compose devcontainer with a second
service. It checks that the second service is on no `bl-` network, that the
dev service still reaches the second service by its service name, and that
the proxy reaches the dev service.

**C4 (MUST). Worktrees run side by side.** Two worktrees of one Compose
project run at once, each with its own stack and its own name.
*Fit:* an integration test, with a real `devcontainer up` on Docker and on
Podman, gets each worktree's own response through the proxy. This is
Evidence 4, automated.

**C5 (MUST). The published port goes.** The dev service stops publishing
`<port>` on the host. Other ports it publishes stay.
*Fit:* in the integration test, the dev service publishes no host port for
`<port>`, and a second published port stays. How to do this without editing
the user's Compose file is an open question.

**C6 (SHOULD). Git works in a worktree's Compose devcontainer.** For a
linked worktree, the override mounts the repository's git directory where the
worktree's `gitdir` resolves inside the container: next to the workspace for
a relative `gitdir`, at the same path for an absolute one.
*Fit:* in the integration test, `test -f "$(gitdir)/HEAD"` holds inside the
second worktree's container, as in #35. A real `git status` was checked by
hand on the test project (Evidence 6).

**C7 (SHOULD). Docs, skill and status know Compose.**
- The README and the skill describe the Compose wiring.
- `status` says what to do when a Compose dev service is not on its network.
- The refusal message goes.
*Fit:* the skill validates; the README section exists; a status test covers
the message.

**C8 (MAY). Compose without the CLI's `--project-name`.** If an editor names
Compose projects differently than the CLI does, the wiring still works,
because nothing in it depends on the project name. *Fit:* the design holds no
project name. Checked by review.

## Security

Unchanged from 0001. The proxy joins only `bl-` networks, so it cannot reach
a stack's other services: the database of a worktree stays unreachable from
the host and from other stacks. The override is written by the CLI on the
host, from the folder name, the service and the port. It takes no other
input, so a hostile worktree name cannot add arbitrary YAML: labels are
validated by R4's rules before they are written.

## Phasing

1. **`berthlight add` writes the override** (C2, C3, C5 in part). That is
   enough for a hand-wired `devcontainer.json`, and it settles the open
   question on ports.
2. **`berthlight devcontainer` wires Compose** (C1), with the integration
   tests (C4).
3. **Git** (C6), then docs and status (C7).

## Open questions

- **Removing one published port without editing the Compose file.** Compose
  2.24+ has `!reset` and `!override` tags, which an override can use to
  replace a service's `ports` list. That needs berthlight to read the
  project's Compose files to keep the other ports, and needs Podman's Compose
  provider to support the tags. *Settled by:* phase 1, tested on Docker and
  Podman.
- **The override's name and place.** `.devcontainer/berthlight.compose.yml`
  is assumed. *Settled by:* phase 1; a project with its Compose files
  elsewhere.
- **VS Code.** Whether the extension runs `initializeCommand` before reading
  Compose files, as the CLI does. *Settled by:* one run in VS Code.

## What would change this plan

- **No consumer appears.** Without a Compose project to dogfood on, phase 2
  waits: phase 1 plus a README section on wiring by hand may be enough.
- **Devcontainer variables reach Compose files.** If the Dev Containers spec
  starts substituting `${localWorkspaceFolderBasename}` in Compose files, a
  committed override can replace the generated one.
- **`--mount-git-worktree-common-dir` learns Compose.** Then C6 is the CLI's,
  not berthlight's.
- **The CLI reads Compose files before `initializeCommand`.** That would
  break the design. Evidence 1 shows the opposite on 0.89.0, so a CI test
  guards it.
