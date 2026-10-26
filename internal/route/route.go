// Package route stores berthlight's routes: which name goes to which
// container alias and port.
//
// The table is one JSON file, routes.json, in a state directory. The CLI
// writes it; the proxy mounts the directory read-only and reloads the file
// when it changes. Writes go to a temporary file that is renamed over the old
// one, so the proxy never reads half a file.
package route

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"stormlantern.nl/berthlight/internal/naming"
)

// FileName is the routes file inside the state directory.
const FileName = "routes.json"

// Route sends requests for one name to one container port.
type Route struct {
	// Name is the DNS label: the part before the base domain.
	Name string `json:"name"`
	// Target is the container's network alias.
	Target string `json:"target"`
	// Port is the dev server's port inside the container.
	Port int `json:"port"`
	// NetworkName is the route's container network. Empty means
	// bl-<name>. A devcontainer sets it to bl-<workspace folder name>,
	// because devcontainer.json can name the folder but cannot derive the
	// route name from it (see the devcontainer package).
	NetworkName string `json:"network,omitempty"`
	// Compose is the devcontainer's Compose service, when the route is a
	// Compose devcontainer's (PRD 0041). The service joins the network
	// through berthlight's override file, not runArgs, so status says so.
	Compose string `json:"compose,omitempty"`
}

// Addr is the upstream address, target:port.
func (r Route) Addr() string { return r.Target + ":" + strconv.Itoa(r.Port) }

// Network is the name of the route's own container network.
func (r Route) Network() string {
	if r.NetworkName != "" {
		return r.NetworkName
	}
	return NetworkName(r.Name)
}

// NetworkPrefix starts the name of every route network, so berthlight only
// ever connects the proxy to networks it made for routes.
const NetworkPrefix = "bl-"

// NetworkName returns the default container network for a route name.
func NetworkName(name string) string { return NetworkPrefix + name }

// validNetwork is what Docker and Podman accept as a network name, with
// berthlight's prefix.
var validNetwork = regexp.MustCompile(`^bl-[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// ValidNetwork checks a route network name.
func ValidNetwork(name string) error {
	if !validNetwork.MatchString(name) {
		return fmt.Errorf("network %q is not a valid route network: it must start with %q followed by a letter or digit, then letters, digits, '_', '.' or '-'", name, NetworkPrefix)
	}
	return nil
}

// Validate checks that the route can be served.
func (r Route) Validate() error {
	if r.Name == "" || naming.Sanitize(r.Name) != r.Name || len(r.Name) > naming.MaxLabel {
		return fmt.Errorf("route name %q is not a valid label (lowercase letters, digits, single hyphens, at most %d characters)", r.Name, naming.MaxLabel)
	}
	if r.Target == "" {
		return fmt.Errorf("route %q: empty target", r.Name)
	}
	if r.Port < 1 || r.Port > 65535 {
		return fmt.Errorf("route %q: port %d out of range", r.Name, r.Port)
	}
	if r.NetworkName != "" {
		if err := ValidNetwork(r.NetworkName); err != nil {
			return fmt.Errorf("route %q: %w", r.Name, err)
		}
	}
	return nil
}

// SameOwner reports whether two routes for one name come from the same
// place: the same network and target. Only the port may differ, as when a
// project moves its dev server to another port.
func (r Route) SameOwner(o Route) bool {
	return r.Name == o.Name && r.Network() == o.Network() && r.Target == o.Target
}

// Table is the set of routes, by name.
type Table struct {
	Routes []Route `json:"routes"`
}

// Lookup returns the route with the given name.
func (t *Table) Lookup(name string) (Route, bool) {
	if t == nil {
		return Route{}, false
	}
	for _, r := range t.Routes {
		if r.Name == name {
			return r, true
		}
	}
	return Route{}, false
}

// Put adds a route or replaces the one with the same name.
func (t *Table) Put(r Route) {
	for i := range t.Routes {
		if t.Routes[i].Name == r.Name {
			t.Routes[i] = r
			return
		}
	}
	t.Routes = append(t.Routes, r)
	sort.Slice(t.Routes, func(i, j int) bool { return t.Routes[i].Name < t.Routes[j].Name })
}

// Conflict returns an error when another route than r.Name uses r's
// network or target. Routes never share a network, or they could reach each
// other. They never share a target either: the proxy is on every route's
// network and resolves targets through all of them, so two containers with
// one alias on two networks would answer for each other's name.
func (t *Table) Conflict(r Route) error {
	for _, o := range t.Routes {
		if o.Name == r.Name {
			continue
		}
		if o.Network() == r.Network() {
			return fmt.Errorf("network %s already belongs to route %q", r.Network(), o.Name)
		}
		if strings.EqualFold(o.Target, r.Target) {
			return fmt.Errorf("target %q is already route %q's; each route needs its own alias, because the proxy resolves aliases on every route network", r.Target, o.Name)
		}
	}
	return nil
}

// Remove deletes the route with the given name and reports whether it existed.
func (t *Table) Remove(name string) bool {
	for i := range t.Routes {
		if t.Routes[i].Name == name {
			t.Routes = append(t.Routes[:i], t.Routes[i+1:]...)
			return true
		}
	}
	return false
}

// Load reads the table from dir. A missing file is an empty table.
func Load(dir string) (*Table, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return &Table{}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse decodes and validates a routes file.
func Parse(b []byte) (*Table, error) {
	var t Table
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	seen := map[string]bool{}
	for i, r := range t.Routes {
		if err := r.Validate(); err != nil {
			return nil, err
		}
		if seen[r.Name] {
			return nil, fmt.Errorf("%s: route %q appears twice", FileName, r.Name)
		}
		seen[r.Name] = true
		earlier := Table{Routes: t.Routes[:i]}
		if err := earlier.Conflict(r); err != nil {
			return nil, fmt.Errorf("%s: route %q: %w", FileName, r.Name, err)
		}
	}
	return &t, nil
}

// MakeDir creates the state directory, and makes it readable and
// searchable by everyone even if it existed, or the umask was strict: the
// proxy reads it as another user. Routes are not secret.
func MakeDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o055 != 0o055 {
		return os.Chmod(dir, info.Mode().Perm()|0o755)
	}
	return nil
}

// Save writes the table to dir atomically. The directory and file are
// readable by everyone: the proxy runs as an unprivileged user that, under
// rootless Podman, is not the file's owner. Routes are not secret.
func Save(dir string, t *Table) error {
	if err := MakeDir(dir); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".routes-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, FileName))
}
