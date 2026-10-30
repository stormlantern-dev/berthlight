package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ContainerPort is the port the proxy listens on inside its container.
const ContainerPort = 7480

// ProxySpec describes the proxy container.
type ProxySpec struct {
	// Name of the container, and of its own network.
	Name string
	// Image to run; its entrypoint is `berthlight proxy`.
	Image string
	// HostPort is published on loopback.
	HostPort int
	// StateDir holds routes.json; mounted read-only.
	StateDir string
	// Bases are the accepted base domains.
	Bases []string
	// Group, if set, is a supplementary group for the proxy: the group,
	// as the container sees it, of the files the CLI writes, so the proxy
	// can read the certificate key, which other users cannot (R7).
	Group string
}

// RunArgs returns the arguments for `<runtime> run` that start the proxy
// with least privilege (PRD 0001, R2). ipv6 adds a ::1 port binding.
// gwPriority keeps the proxy's own network as the target of the published
// port when route networks are connected later (Docker 28 and later; without
// it, Docker may move the port to a route network, where the proxy does not
// listen).
func (s ProxySpec) RunArgs(ipv6, gwPriority bool) []string {
	port := strconv.Itoa(s.HostPort)
	network := s.Name
	if gwPriority {
		network = "name=" + s.Name + ",gw-priority=1000"
	}
	args := []string{
		"run", "-d",
		"--name", s.Name,
		"--label", Label + "=proxy",
		"--network", network,
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--user", "65532:65532",
	}
	if s.Group != "" {
		args = append(args, "--group-add", s.Group)
	}
	args = append(args, "--publish", "127.0.0.1:"+port+":"+strconv.Itoa(ContainerPort))
	if ipv6 {
		args = append(args, "--publish", "[::1]:"+port+":"+strconv.Itoa(ContainerPort))
	}
	args = append(args,
		"--volume", s.StateDir+":/state:ro",
		s.Image,
		"--state", "/state",
		"--port", strconv.Itoa(ContainerPort),
	)
	for _, b := range s.Bases {
		args = append(args, "--base", b)
	}
	return args
}

// PullImage makes sure image is there: present locally, or pulled now.
func (r Runtime) PullImage(image string) error {
	if _, err := r.Run("image", "inspect", image); err == nil {
		return nil
	}
	_, err := r.Run("pull", "-q", image)
	return err
}

// StartProxy creates the proxy's own network and runs the proxy on it. It
// tries the most specific options first and falls back where the runtime
// does not support them: gw-priority (Docker 28+) and a ::1 binding.
func (r Runtime) StartProxy(s ProxySpec) error {
	if err := r.EnsureNetwork(s.Name, "proxy"); err != nil {
		return err
	}
	var errs []string
	for _, opt := range []struct{ ipv6, gw bool }{{true, true}, {false, true}, {true, false}, {false, false}} {
		_, err := r.Run(s.RunArgs(opt.ipv6, opt.gw)...)
		if err == nil {
			return nil
		}
		errs = append(errs, err.Error())
		_ = r.RemoveContainer(s.Name)
	}
	return fmt.Errorf("starting proxy failed:\n  %s", strings.Join(errs, "\n  "))
}

// ProxyGroup is the group that the files the CLI writes have inside the
// proxy container: the user's own group, except where the runtime maps the
// user to root in a user namespace (rootless Podman or Docker), where it is
// 0. Empty where there are no groups (Windows).
func (r Runtime) ProxyGroup() string {
	gid := os.Getegid()
	if gid < 0 {
		return ""
	}
	if gid != 0 && r.rootless() {
		return "0"
	}
	return strconv.Itoa(gid)
}

// rootless reports whether the runtime runs in the user's user namespace.
// Podman's format comes first, so Podman behind a docker shim is found too.
func (r Runtime) rootless() bool {
	if out, err := r.Run("info", "--format", "{{.Host.Security.Rootless}}"); err == nil {
		return strings.TrimSpace(out) == "true"
	}
	out, err := r.Run("info", "--format", "{{json .SecurityOptions}}")
	return err == nil && strings.Contains(out, "rootless")
}

// ContainerGroups returns a container's supplementary groups.
func (r Runtime) ContainerGroups(name string) ([]string, error) {
	out, err := r.Run("container", "inspect", "--format", "{{json .HostConfig.GroupAdd}}", name)
	if err != nil {
		return nil, err
	}
	var groups []string
	if s := strings.TrimSpace(out); s != "" && s != "null" {
		if err := json.Unmarshal([]byte(s), &groups); err != nil {
			return nil, fmt.Errorf("parsing groups of %s: %w", name, err)
		}
	}
	return groups, nil
}

// IsRouteNetwork reports whether a network name belongs to a route.
func IsRouteNetwork(name string) bool { return strings.HasPrefix(name, "bl-") }
