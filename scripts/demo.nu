#!/usr/bin/env nu
# A ready-made berthlight demo: two projects whose devcontainers both
# publish port 5173 (Vite's), so they cannot run side by side, until berthlight
# wires them. Each dev server serves a page saying which project and
# worktree answered, and under which name it was reached.
#
#   ./scripts/demo.nu setup            # create ~/berthlight-demo, pull images
#   ./scripts/demo.nu up <folder>      # devcontainer up, with the right runtime
#   ./scripts/demo.nu rebuild <folder> # the same, replacing the container
#   ./scripts/demo.nu stop <folder>    # stop a folder's devcontainer
#   ./scripts/demo.nu steps            # the demo, step by step
#   ./scripts/demo.nu privileges       # what the proxy may do
#   ./scripts/demo.nu agent            # Claude Code at work, step 7
#   ./scripts/demo.nu teardown         # remove everything the demo made
#
# Needs berthlight, git, the Dev Container CLI (devcontainer) and Docker or
# Podman. The demo uses berthlight's own state, proxy and port 7480.

const BUSYBOX = "docker.io/library/busybox:1.37"
const PORT = 5173 # the dev servers' port, as Vite's
const ROUTES = ["shop", "shop-fix-login", "shop-dark", "shop-retro", "shop-minimal", "gallery"]

def default-dir [] { $env.HOME | path join "berthlight-demo" }

def runtime [] {
  if ($env.BERTHLIGHT_RUNTIME? | default "" | is-not-empty) { return $env.BERTHLIGHT_RUNTIME }
  if (which docker | is-not-empty) { return "docker" }
  "podman"
}

def main [] {
  print "usage: demo.nu <setup|up <folder>|rebuild <folder>|stop <folder>|steps|agent|privileges|teardown> [--dir <dir>]"
}

# The landing page, a plain file in the project: __NAME__ becomes the
# folder (project or worktree) it runs in, __HUE__ and __HUE2__ the
# project's colours. It shows the name it was reached at.
const PAGE = r##'<!doctype html><meta charset=utf-8><title>__NAME__</title>
<style>
body{margin:0;min-height:100vh;display:grid;place-items:center;font:500 22px system-ui,sans-serif;color:#fff;
background:linear-gradient(135deg,hsl(__HUE__ 65% 42%),hsl(__HUE2__ 70% 28%))}
h1{font-size:72px;margin:0 0 8px}p{margin:6px 0;opacity:.9}code{background:#0004;padding:4px 12px;border-radius:8px}
</style>
<div><h1>__NAME__</h1><p>a dev server in its own devcontainer, on port 5173</p>
<p>you reached it at <code id=h></code></p></div>
<script>document.getElementById('h').textContent=location.host</script>
'##

# The dev server: busybox httpd serving index.html, filled in.
const SERVE = r##'#!/bin/sh
mkdir -p /tmp/www
name=$(basename "$PWD")
hue=$(( $(echo "${name%%-*}" | cksum | cut -d' ' -f1) % 360 ))
sed -e "s/__NAME__/$name/g" -e "s/__HUE__/$hue/g" -e "s/__HUE2__/$(( (hue+50) % 360 ))/g" index.html > /tmp/www/index.html
httpd -p 5173 -h /tmp/www
'##

def devcontainer-json [name: string] {
  $"// The ($name) development container, with its dev server on port ($PORT).
{
  \"name\": \"($name)\",
  \"image\": \"($BUSYBOX)\",
  \"appPort\": [($PORT)],
  \"postStartCommand\": \"sh .devcontainer/serve.sh\"
}
"
}

def make-project [dir: path, name: string] {
  let p = ($dir | path join $name)
  mkdir ($p | path join ".devcontainer")
  devcontainer-json $name | save -f ($p | path join ".devcontainer" "devcontainer.json")
  $SERVE | save -f ($p | path join ".devcontainer" "serve.sh")
  $PAGE | save -f ($p | path join "index.html")
  $"# ($name)\n\nA demo project for berthlight.\n" | save -f ($p | path join "README.md")
  ^git -C $p init -q -b main
  # Its own identity, so commits work during the demo on any machine.
  ^git -C $p config user.name "berthlight demo"
  ^git -C $p config user.email "demo@example.com"
  ^git -C $p add -A
  ^git -C $p commit -q -m $"A devcontainer with a dev server on port ($PORT)"
  print $"created ($p)"
}

# Let Claude Code work through the demo without asking at each step: the
# commands the demo needs, and edits in the demo folder, where the agent
# makes its worktrees. Not committed, so worktrees do not inherit it.
def agent-settings [dir: path, p: path] {
  mkdir ($p | path join ".claude")
  {
    permissions: {
      allow: ["Bash(git:*)" "Bash(devcontainer:*)" "Bash(berthlight:*)" "Bash(curl:*)" "Bash(ls:*)" "Bash(cat:*)" "Edit" "Write"]
      additionalDirectories: [$dir]
    }
  } | to json | save -f ($p | path join ".claude" "settings.local.json")
  "/.claude/\n" | save -a ($p | path join ".git" "info" "exclude")
}

# Create the demo projects and pull the images, so nothing waits on the
# network during the demo.
def "main setup" [--dir: path] {
  let dir = ($dir | default (default-dir))
  if ($dir | path exists) { error make { msg: $"($dir) exists; run teardown first" } }
  for cmd in [berthlight git devcontainer (runtime)] {
    if (which $cmd | is-empty) { error make { msg: $"($cmd) not found" } }
  }
  mkdir $dir
  make-project $dir "webshop"
  make-project $dir "gallery"
  for name in [webshop gallery] { agent-settings $dir ($dir | path join $name) }
  # The webshop's agent gets the skill now; the gallery's in step 4, live.
  do { cd ($dir | path join "webshop"); ^berthlight --no-header skill install --agent claude --project | ignore }
  print $"pulling ($BUSYBOX)"
  ^(runtime) pull -q $BUSYBOX | ignore
  # Start the proxy once, to pull its image; stop it again only if it was
  # not already running for other projects.
  let proxy = ($env.BERTHLIGHT_PROXY_NAME? | default "berthlight")
  let running = (^(runtime) ps -q --filter $"name=^($proxy)$" | str trim | is-not-empty)
  if $running {
    print "the proxy is already running; leaving it as it is"
  } else {
    print "starting and stopping the proxy once, to pull its image"
    ^berthlight --no-header up
    ^berthlight --no-header down
  }
  print ""
  print $"Ready in ($dir). Next: ./scripts/demo.nu steps"
}

# devcontainer up for a folder, with the runtime berthlight uses. It prints
# one line: where the dev server answers, or why it did not start. The
# Dev Container CLI's own log, which is long and has harmless errors in it
# (busybox has no /etc/os-release), only with --verbose or on failure.
def dc-up [folder: path, extra: list<string>, verbose: bool] {
  let f = ($folder | path expand)
  let name = ($f | path basename)
  if $verbose {
    ^devcontainer up --workspace-folder $f --docker-path (runtime) ...$extra
    return
  }
  print -n $"starting ($name)… "
  let r = (^devcontainer up --workspace-folder $f --docker-path (runtime) ...$extra | complete)
  let log = $"($r.stdout)\n($r.stderr)"
  let outcome = ($r.stdout | lines | where ($it | str starts-with '{"outcome"') | last 1 | each { from json } | get -o 0)
  if $r.exit_code == 0 and ($outcome.outcome? == "success") {
    let wired = (open --raw ($f | path join ".devcontainer" "devcontainer.json") | str contains "berthlight")
    let url = if $wired { ^berthlight url -C $f | str trim } else { $"http://localhost:($PORT)" }
    print $"(ansi green)up(ansi reset) at ($url)"
    return
  }
  let why = if ($log =~ '(?i)address already in use|port is already allocated|bind: address') {
    $"port ($PORT) is already in use"
  } else {
    $outcome.message? | default "see the log above"
  }
  if not ($log =~ '(?i)address already in use|port is already allocated|bind: address') { print ""; print $log }
  print $"(ansi red)failed(ansi reset): ($why)"
  exit 1
}

# devcontainer up for a folder, with the runtime berthlight uses.
def "main up" [folder: path, --verbose (-v)] { dc-up $folder [] $verbose }

# The same, replacing the container: after changing devcontainer.json.
def "main rebuild" [folder: path, --verbose (-v)] { dc-up $folder [--remove-existing-container] $verbose }

# Stop the devcontainer of a folder, as if its dev server went down.
def "main stop" [folder: path] {
  let f = ($folder | path expand)
  let ids = (^(runtime) ps -q --filter $"label=devcontainer.local_folder=($f)" | lines)
  if ($ids | is-empty) { error make { msg: $"no running devcontainer for ($f)" } }
  ^(runtime) stop -t 1 ...$ids | ignore
  print $"stopped the devcontainer of ($f | path basename)"
}

# What the proxy may do: its user, whether its root filesystem is
# read-only, and the capabilities the kernel lets it use, read from /proc.
def "main privileges" [] {
  let i = (^(runtime) inspect berthlight --format '{{.Config.User}} {{.HostConfig.ReadonlyRootfs}} {{.State.Pid}}' | str trim | split row " ")
  let cap = (open --raw $"/proc/($i.2)/status" | lines | where $it starts-with "CapEff" | first | split row -r '\s+' | get 1)
  print $"runs as:                ($i.0)"
  print $"read-only root:         ($i.1)"
  print $"effective capabilities: ($cap)(if ($cap | str replace -a '0' '' | is-empty) { '  (none)' } else { '' })"
}

# The task for the agent in step 6: three variants of the webshop, each in
# its own worktree and devcontainer, side by side.
const AGENT_TASK = "Maak drie varianten van de landingspagina van deze webshop: dark, retro en minimal. Geef elke variant een eigen git worktree naast deze map (../webshop-dark, ../webshop-retro, ../webshop-minimal) en pas daar index.html aan, zodat de pagina echt bij de variant past. Commit in elke worktree. Start elke devcontainer met devcontainer up --workspace-folder <map>, controleer met berthlight dat alle drie antwoorden, en geef me tot slot de drie URLs."

# Start Claude Code in the webshop with the task: edits accepted, the
# demo's commands allowed by the settings setup wrote.
def "main agent" [--dir: path] {
  cd (($dir | default (default-dir)) | path join "webshop")
  ^claude --permission-mode acceptEdits $AGENT_TASK
}

# The machine's address on the local network, for showing the proxy is not
# reachable there.
def lan-ip [] {
  let r = (^ip -4 -o addr show scope global | complete)
  if $r.exit_code != 0 or ($r.stdout | is-empty) { return "<your LAN address>" }
  $r.stdout | lines | first | split row -r '\s+' | get 3 | split row "/" | first
}

# The demo, step by step, with the commands to type.
def "main steps" [--dir: path] {
  let dir = ($dir | default (default-dir))
  let d = ($env.CURRENT_FILE? | default "./scripts/demo.nu")
  print $"
 1. The problem: two projects, both on port ($PORT).
      cd ($dir)
      ($d) up webshop          # http://localhost:($PORT) is the webshop
      ($d) up gallery          # fails: port ($PORT) is already in use

 2. The show.
      berthlight

 3. Wire the webshop: preview, apply, rebuild.
      cd webshop
      berthlight devcontainer shop ($PORT) --dry-run
      berthlight devcontainer shop ($PORT)
      git commit -qam 'Use berthlight'
      ($d) rebuild .           # now at http://shop.localhost:7480
      berthlight status

 4. The gallery, by an agent: in ($dir)/gallery, in Claude Code:
      berthlight skill install --agent claude --project
      claude 'Wire this project to berthlight, project name gallery.'
      ($d) rebuild .           # http://gallery.localhost:7480, next to the shop

 5. A second worktree of the webshop, running next to the first.
      cd ($dir)/webshop
      git worktree add ../webshop-fix-login
      ($d) up ../webshop-fix-login   # http://shop-fix-login.localhost:7480
      berthlight url -C ../webshop-fix-login --copy

 6. Watch it: stop a dev server and start it again.
      berthlight status --watch      # in a second tab
      ($d) stop ../webshop-fix-login  # a notification: down
      ($d) up ../webshop-fix-login    # and up again

 7. An agent at work: three variants, three worktrees, side by side.
      ($d) agent                     # Claude Code, with the task
      berthlight status              # shop-dark, shop-retro, shop-minimal

 8. Least privilege, and nothing on the network.
      ($d) privileges   # user 65532, read-only, no capabilities
      curl http://(lan-ip):7480   # refused: loopback only

 9. Afterwards: ./scripts/demo.nu teardown
"
}

# Remove the demo's containers, routes, worktrees and folders.
def "main teardown" [--dir: path] {
  let dir = ($dir | default (default-dir))
  let rt = (runtime)
  if ($dir | path exists) {
    for f in (ls $dir | where type == dir | get name) {
      let ids = (^$rt ps -aq --filter $"label=devcontainer.local_folder=($f | path expand)" | lines)
      if ($ids | is-not-empty) {
        print $"removing the devcontainer of ($f | path basename)"
        ^$rt rm -f ...$ids | ignore
      }
    }
  }
  for r in $ROUTES {
    let res = (^berthlight --no-header rm $r | complete)
    if $res.exit_code == 0 { print $"removed route ($r)" }
  }
  let left = (^berthlight ls --json | from json | get routes | length)
  if $left == 0 {
    ^berthlight --no-header down | ignore
    ^$rt network rm berthlight | complete | ignore
    print "stopped the proxy"
  } else {
    print $"kept the proxy: ($left) other route\(s)"
  }
  if ($dir | path exists) {
    rm -rf $dir
    print $"removed ($dir)"
  }
}
