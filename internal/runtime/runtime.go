// Package runtime drives the container runtime (Docker or Podman) through
// its command line. berthlight never talks to the runtime's socket and never
// gives a container access to it; the CLI runs as the user, with the user's
// own access.
package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Label marks every network and container berthlight creates.
const Label = "io.stormlantern.berthlight"

// Runtime is a container runtime CLI.
type Runtime struct {
	// Bin is the executable: docker or podman.
	Bin string
}

// Detect picks the runtime: $BERTHLIGHT_RUNTIME, else docker, else podman.
func Detect() (Runtime, error) {
	if b := os.Getenv("BERTHLIGHT_RUNTIME"); b != "" {
		if _, err := exec.LookPath(b); err != nil {
			return Runtime{}, fmt.Errorf("BERTHLIGHT_RUNTIME=%s: %w", b, err)
		}
		return Runtime{Bin: b}, nil
	}
	for _, b := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(b); err == nil {
			return Runtime{Bin: b}, nil
		}
	}
	return Runtime{}, errors.New("neither docker nor podman found; install one, or set BERTHLIGHT_RUNTIME")
}

// Run runs the runtime with args and returns its standard output.
func (r Runtime) Run(args ...string) (string, error) {
	cmd := exec.Command(r.Bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(filterNoise(errb.String()))
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("%s %s: %s", r.Bin, strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

// filterNoise drops the podman-docker shim's notice.
func filterNoise(s string) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "Emulate Docker CLI using podman") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

// Logs returns a container's output, standard output and error together.
func (r Runtime) Logs(name string) (string, error) {
	out, err := exec.Command(r.Bin, "logs", name).CombinedOutput()
	return filterNoise(string(out)), err
}

// NetworkExists reports whether a network with this name exists.
func (r Runtime) NetworkExists(name string) bool {
	_, err := r.Run("network", "inspect", name)
	return err == nil
}

// EnsureNetwork creates a user-defined bridge network if it does not exist,
// labelled with kind ("route" or "proxy"). User-defined networks resolve
// container aliases; the default one does not.
func (r Runtime) EnsureNetwork(name, kind string) error {
	if r.NetworkExists(name) {
		return nil
	}
	_, err := r.Run("network", "create", "--label", Label+"="+kind, name)
	if err != nil && r.NetworkExists(name) {
		return nil // created concurrently
	}
	return err
}

// RemoveNetwork removes a network. It fails while containers use it.
func (r Runtime) RemoveNetwork(name string) error {
	_, err := r.Run("network", "rm", name)
	return err
}

// ContainerState reports whether a container exists and whether it runs.
func (r Runtime) ContainerState(name string) (exists, running bool) {
	out, err := r.Run("container", "inspect", "--format", "{{.State.Running}}", name)
	if err != nil {
		return false, false
	}
	return true, strings.TrimSpace(out) == "true"
}

// ContainerNetworks returns the networks a container is connected to.
func (r Runtime) ContainerNetworks(name string) ([]string, error) {
	out, err := r.Run("container", "inspect", "--format", "{{json .NetworkSettings.Networks}}", name)
	if err != nil {
		return nil, err
	}
	var nets map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &nets); err != nil {
		return nil, fmt.Errorf("parsing networks of %s: %w", name, err)
	}
	var names []string
	for n := range nets {
		names = append(names, n)
	}
	return names, nil
}

// Connect connects a container to a network unless it already is.
func (r Runtime) Connect(network, container string) error {
	nets, err := r.ContainerNetworks(container)
	if err != nil {
		return err
	}
	for _, n := range nets {
		if n == network {
			return nil
		}
	}
	_, err = r.Run("network", "connect", network, container)
	return err
}

// Disconnect disconnects a container from a network if it is connected.
func (r Runtime) Disconnect(network, container string) error {
	nets, err := r.ContainerNetworks(container)
	if err != nil {
		return err
	}
	for _, n := range nets {
		if n == network {
			_, err = r.Run("network", "disconnect", network, container)
			return err
		}
	}
	return nil
}

// RemoveContainer force-removes a container if it exists.
func (r Runtime) RemoveContainer(name string) error {
	if exists, _ := r.ContainerState(name); !exists {
		return nil
	}
	_, err := r.Run("rm", "-f", name)
	return err
}
