// Package compose writes the override file that wires a Docker Compose
// devcontainer to berthlight (PRD 0041).
//
// For a Compose devcontainer, the Dev Container CLI ignores runArgs, and a
// committed Compose file cannot name the worktree: Compose interpolates
// environment variables, not devcontainer variables. So berthlight add,
// run as the devcontainer's initializeCommand on the host, writes a small
// override for this worktree before Compose reads its files. The override
// joins the devcontainer's service to the route's network, under the
// network's name as alias; stops publishing the dev server's port; and,
// in a linked worktree, mounts the repository's git directory where the
// worktree's gitdir points inside the container.
//
// The port and the git mount need the service as Compose sees it, merged
// from all of the project's files, which `compose config` gives. Without
// it, the override still joins the network, and the rest is left as is.
package compose

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"stormlantern.nl/berthlight/internal/route"
)

// FileName is the override's name, next to devcontainer.json, which lists
// it last in dockerComposeFile.
const FileName = "berthlight.compose.yml"

// validService is what Compose accepts as a service name.
var validService = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// Override is what the override file says about the devcontainer's
// service.
type Override struct {
	Service string
	// Network is the route's network, bl-<folder>, which the service joins
	// with Alias as its alias.
	Network, Alias string
	// Default keeps the service on its project's default network. A
	// service without networks is on it implicitly, and naming another
	// network in an override would take it off.
	Default bool
	// ReplacePorts replaces the service's published ports with Ports, in
	// Compose's long syntax: with !override, or !reset when none are left.
	ReplacePorts bool
	Ports        []json.RawMessage
	// Mounts are bind mounts to add, such as the repository's git
	// directory.
	Mounts []Mount
}

// Mount is a bind mount from the host into the container.
type Mount struct {
	Source, Target string
}

// Validate checks the names that go into the file, so that a hostile
// folder or service name cannot add YAML of its own. Every value is
// quoted besides, as a JSON string, which YAML reads as one scalar.
func (o Override) Validate() error {
	if !validService.MatchString(o.Service) {
		return fmt.Errorf("service %q is not a valid Compose service name", o.Service)
	}
	if err := route.ValidNetwork(o.Network); err != nil {
		return err
	}
	if !validService.MatchString(o.Alias) {
		return fmt.Errorf("alias %q is not a valid network alias", o.Alias)
	}
	for _, m := range o.Mounts {
		if m.Source == "" || !path.IsAbs(m.Target) {
			return fmt.Errorf("mount %q:%q needs a source and an absolute target", m.Source, m.Target)
		}
	}
	for _, p := range o.Ports {
		var v map[string]any
		if err := json.Unmarshal(p, &v); err != nil {
			return fmt.Errorf("published port %s is not an object: %w", p, err)
		}
	}
	return nil
}

// Render returns the override file.
func (o Override) Render() ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("# Written by berthlight add on each start of the devcontainer, for this\n")
	b.WriteString("# worktree only (PRD 0041). Do not edit or commit it.\n")
	b.WriteString("services:\n")
	fmt.Fprintf(&b, "  %s:\n", quote(o.Service))
	b.WriteString("    networks:\n")
	if o.Default {
		b.WriteString("      \"default\": {}\n")
	}
	fmt.Fprintf(&b, "      %s:\n        aliases: [%s]\n", quote(o.Network), quote(o.Alias))
	if o.ReplacePorts {
		if len(o.Ports) == 0 {
			b.WriteString("    ports: !reset []\n")
		} else {
			b.WriteString("    ports: !override\n")
			for _, p := range o.Ports {
				var compact bytes.Buffer
				if err := json.Compact(&compact, p); err != nil {
					return nil, err
				}
				fmt.Fprintf(&b, "      - %s\n", escapeDollar(compact.String()))
			}
		}
	}
	if len(o.Mounts) > 0 {
		b.WriteString("    volumes:\n")
		for _, m := range o.Mounts {
			fmt.Fprintf(&b, "      - {\"type\": \"bind\", \"source\": %s, \"target\": %s}\n", quote(m.Source), quote(m.Target))
		}
	}
	b.WriteString("networks:\n")
	fmt.Fprintf(&b, "  %s:\n    external: true\n", quote(o.Network))
	return b.Bytes(), nil
}

// quote is s as a JSON string, which YAML reads as a double-quoted scalar,
// with $ doubled so Compose does not interpolate it.
func quote(s string) string {
	q, _ := json.Marshal(s)
	return escapeDollar(string(q))
}

func escapeDollar(s string) string { return strings.ReplaceAll(s, "$", "$$") }

// Config is the part of `compose config --format json` berthlight reads.
type Config struct {
	Services map[string]Service `json:"services"`
}

// Service is one service of a merged Compose configuration.
type Service struct {
	NetworkMode string                     `json:"network_mode"`
	Networks    map[string]json.RawMessage `json:"networks"`
	Ports       []json.RawMessage          `json:"ports"`
	Volumes     []Volume                   `json:"volumes"`
}

// Volume is a service's volume, in Compose's long syntax.
type Volume struct {
	Type   string `json:"type"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// Git is a linked worktree's pointer to its repository.
type Git struct {
	// Worktree is the worktree's directory on the host.
	Worktree string
	// GitDir is the gitdir its .git file names, relative to Worktree or
	// absolute.
	GitDir string
	// CommonDir is the repository's git directory on the host.
	CommonDir string
}

// Plan works out the override for service from the project's merged
// configuration. cfg is nil when Compose could not be read; git is nil in
// a main worktree. notes say what the override leaves out, and why.
func Plan(cfg *Config, service, network, alias string, port int, git *Git) (o Override, notes []string, err error) {
	o = Override{Service: service, Network: network, Alias: alias, Default: true}
	if cfg == nil {
		if git != nil && filepath.IsAbs(git.GitDir) {
			m, note := gitMount(Service{}, *git)
			if note != "" {
				notes = append(notes, note)
			}
			if m != nil {
				o.Mounts = append(o.Mounts, *m)
			}
		}
		return o, notes, o.Validate()
	}
	svc, ok := cfg.Services[service]
	if !ok {
		return o, nil, fmt.Errorf("the Compose files have no service %q; check service in devcontainer.json", service)
	}
	if svc.NetworkMode != "" {
		return o, nil, fmt.Errorf("service %q has network_mode %q, so it cannot join berthlight's network %s; remove network_mode", service, svc.NetworkMode, network)
	}
	if len(svc.Networks) > 0 {
		_, o.Default = svc.Networks["default"]
	}
	o.Ports, o.ReplacePorts = withoutPort(svc.Ports, port)
	if git != nil {
		m, note := gitMount(svc, *git)
		if note != "" {
			notes = append(notes, note)
		}
		if m != nil {
			o.Mounts = append(o.Mounts, *m)
		}
	}
	return o, notes, o.Validate()
}

// withoutPort returns the published ports other than those of the
// container's TCP port, and whether there were any of those.
func withoutPort(ports []json.RawMessage, port int) ([]json.RawMessage, bool) {
	keep := []json.RawMessage{}
	found := false
	for _, p := range ports {
		var v struct {
			Target   int    `json:"target"`
			Protocol string `json:"protocol"`
		}
		if json.Unmarshal(p, &v) == nil && v.Target == port && (v.Protocol == "" || v.Protocol == "tcp") {
			found = true
			continue
		}
		keep = append(keep, p)
	}
	return keep, found
}

// gitMount finds where git's gitdir resolves inside the container, from
// the bind mount that holds the worktree, and returns the mount that puts
// the repository's git directory there. It returns nil when the directory
// is there already, or when the container's path cannot be told, with a
// note saying so.
func gitMount(svc Service, g Git) (*Mount, string) {
	if filepath.IsAbs(g.GitDir) {
		// The same path inside the container as outside.
		target := filepath.ToSlash(g.CommonDir)
		if !path.IsAbs(target) {
			return nil, fmt.Sprintf("the worktree's gitdir %s has no path in a Linux container; make it relative: git worktree repair --relative-paths", g.GitDir)
		}
		if c, ok := containerPath(svc, g.CommonDir); ok && c == target {
			return nil, ""
		}
		return &Mount{Source: g.CommonDir, Target: target}, ""
	}
	wt, ok := containerPath(svc, g.Worktree)
	if !ok {
		return nil, fmt.Sprintf("no volume of the service mounts %s, so git in its container is left as is", g.Worktree)
	}
	hostGitDir := filepath.Join(g.Worktree, g.GitDir)
	rel, err := filepath.Rel(g.CommonDir, hostGitDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Sprintf("the worktree's gitdir %s is not inside %s, so git in its container is left as is", g.GitDir, g.CommonDir)
	}
	target := path.Join(wt, filepath.ToSlash(g.GitDir))
	if rel != "." {
		for range strings.Split(filepath.ToSlash(rel), "/") {
			target = path.Dir(target)
		}
	}
	if target == "/" {
		return nil, fmt.Sprintf("the worktree's gitdir %s resolves to / in its container, so git there is left as is", g.GitDir)
	}
	if c, ok := containerPath(svc, g.CommonDir); ok && c == target {
		return nil, ""
	}
	return &Mount{Source: g.CommonDir, Target: target}, ""
}

// containerPath is where a host directory appears inside the service's
// container, through the bind mount of it or of the nearest parent.
func containerPath(svc Service, host string) (string, bool) {
	host = realPath(host)
	best, bestLen := "", -1
	for _, v := range svc.Volumes {
		if v.Type != "bind" || v.Source == "" || !path.IsAbs(v.Target) {
			continue
		}
		src := realPath(v.Source)
		rel, err := filepath.Rel(src, host)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		if len(src) > bestLen {
			best, bestLen = path.Join(v.Target, filepath.ToSlash(rel)), len(src)
		}
	}
	return best, bestLen >= 0
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// LinkedGit returns the git pointer of the worktree at dir, or nil when
// dir is a main worktree (its .git is a directory) or not in git at all.
// commonDir is the repository's git directory.
func LinkedGit(dir, commonDir string) (*Git, error) {
	b, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			return nil, nil // a directory, or no .git
		}
		return nil, err
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
	if !ok {
		return nil, fmt.Errorf("%s: not a gitdir file", filepath.Join(dir, ".git"))
	}
	return &Git{Worktree: dir, GitDir: filepath.FromSlash(gitdir), CommonDir: commonDir}, nil
}

// Command returns the Compose command for a runtime: `<runtime> compose`,
// as the Dev Container CLI uses it, else docker-compose.
func Command(runtimeBin string) ([]string, error) {
	cands := [][]string{{runtimeBin, "compose"}, {"docker-compose"}}
	var errs []string
	for _, c := range cands {
		if _, err := exec.LookPath(c[0]); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		cmd := exec.Command(c[0], append(c[1:], "version")...)
		if out, err := cmd.CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("%s version: %v %s", strings.Join(c, " "), err, strings.TrimSpace(string(out))))
			continue
		}
		return c, nil
	}
	return nil, fmt.Errorf("no Compose found: %s", strings.Join(errs, "; "))
}

// Load asks Compose for the project's merged configuration from files.
// It needs no daemon: config only reads and merges the files.
func Load(command []string, files []string) (*Config, error) {
	args := append([]string{}, command[1:]...)
	for _, f := range files {
		args = append(args, "-f", f)
	}
	args = append(args, "config", "--format", "json")
	cmd := exec.Command(command[0], args...)
	if len(files) > 0 {
		cmd.Dir = filepath.Dir(files[0])
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := lastLine(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s config: %s", strings.Join(command, " "), msg)
	}
	var cfg Config
	if err := json.Unmarshal(out.Bytes(), &cfg); err != nil {
		return nil, fmt.Errorf("reading %s config: %w", strings.Join(command, " "), err)
	}
	return &cfg, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Write writes the override to path, through a temporary file so Compose
// never reads half of it.
func Write(path string, content []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".berthlight-compose-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
