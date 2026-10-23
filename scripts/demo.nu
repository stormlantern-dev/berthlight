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
#   ./scripts/demo.nu teardown         # remove everything the demo made
#
# Needs berthlight, git, the Dev Container CLI (devcontainer) and Docker or
# Podman. The demo uses berthlight's own state, proxy and port 7480.

const BUSYBOX = "docker.io/library/busybox:1.37"
const PORT = 5173 # the dev servers' port, as Vite's
const ROUTES = ["shop", "shop-fix-login", "gallery"]

def default-dir [] { $env.HOME | path join "berthlight-demo" }

def runtime [] {
  if ($env.BERTHLIGHT_RUNTIME? | default "" | is-not-empty) { return $env.BERTHLIGHT_RUNTIME }
  if (which docker | is-not-empty) { return "docker" }
  "podman"
}

def main [] {
  print "usage: demo.nu <setup|up <folder>|rebuild <folder>|stop <folder>|steps|privileges|teardown> [--dir <dir>]"
}

# The dev server: busybox httpd with a page in the project's colour, saying
# which folder (project or worktree) it runs in, and the name it was
# reached at.
const SERVE = r##'#!/bin/sh
mkdir -p /tmp/www
name=$(basename "$PWD")
hue=$(( $(echo "${name%%-*}" | cksum | cut -d' ' -f1) % 360 ))
cat > /tmp/www/index.html <<EOF
<!doctype html><meta charset=utf-8><title>$name</title>
<style>
body{margin:0;min-height:100vh;display:grid;place-items:center;font:500 22px system-ui,sans-serif;color:#fff;
background:linear-gradient(135deg,hsl($hue 65% 42%),hsl($(( (hue+50) % 360 )) 70% 28%))}
h1{font-size:72px;margin:0 0 8px}p{margin:6px 0;opacity:.9}code{background:#0004;padding:4px 12px;border-radius:8px}
</style>
<div><h1>$name</h1><p>a dev server in its own devcontainer, on port 5173</p>
<p>you reached it at <code id=h></code></p></div>
<script>document.getElementById('h').textContent=location.host</script>
EOF
httpd -p 5173 -h /tmp/www
'##

def devcontainer-json [name: string] {
  $"// The ($name) development container. Its dev server answers on port
// ($PORT), published on the host: while it runs, no other project can use it.
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
  $"# ($name)\n\nA demo project for berthlight.\n" | save -f ($p | path join "README.md")
  ^git -C $p init -q -b main
  # Its own identity, so commits work during the demo on any machine.
  ^git -C $p config user.name "berthlight demo"
  ^git -C $p config user.email "demo@example.com"
  ^git -C $p add -A
  ^git -C $p commit -q -m $"A devcontainer with a dev server on port ($PORT)"
  print $"created ($p)"
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
  print $"pulling ($BUSYBOX)"
  ^(runtime) pull -q $BUSYBOX | ignore
  print "starting and stopping the proxy once, to pull its image"
  ^berthlight --no-header up
  ^berthlight --no-header down
  print ""
  print $"Ready in ($dir). Next: ./scripts/demo.nu steps"
}

def dc-up [folder: path, extra: list<string>] {
  let f = ($folder | path expand)
  ^devcontainer up --workspace-folder $f --docker-path (runtime) ...$extra
}

# devcontainer up for a folder, with the runtime berthlight uses.
def "main up" [folder: path] { dc-up $folder [] }

# The same, replacing the container: after changing devcontainer.json.
def "main rebuild" [folder: path] { dc-up $folder [--remove-existing-container] }

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
      berthlight skill install --project
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

 7. Least privilege, and nothing on the network.
      ($d) privileges   # user 65532, read-only, no capabilities
      curl http://(lan-ip):7480   # refused: loopback only

 8. Afterwards: ./scripts/demo.nu teardown
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
