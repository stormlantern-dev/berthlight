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
	"sort"
	"strconv"

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
}

// Addr is the upstream address, target:port.
func (r Route) Addr() string { return r.Target + ":" + strconv.Itoa(r.Port) }

// Network is the name of the route's own container network.
func (r Route) Network() string { return NetworkName(r.Name) }

// NetworkName returns the container network for a route name.
func NetworkName(name string) string { return "bl-" + name }

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
	return nil
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
	for _, r := range t.Routes {
		if err := r.Validate(); err != nil {
			return nil, err
		}
		if seen[r.Name] {
			return nil, fmt.Errorf("%s: route %q appears twice", FileName, r.Name)
		}
		seen[r.Name] = true
	}
	return &t, nil
}

// Save writes the table to dir atomically. The directory and file are
// readable by everyone: the proxy runs as an unprivileged user that, under
// rootless Podman, is not the file's owner. Routes are not secret.
func Save(dir string, t *Table) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
