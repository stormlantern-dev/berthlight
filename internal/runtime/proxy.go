package runtime

import (
	"fmt"
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
}

// RunArgs returns the arguments for `<runtime> run` that start the proxy
// with least privilege (PRD 0001, R2). ipv6 adds a ::1 port binding.
func (s ProxySpec) RunArgs(ipv6 bool) []string {
	port := strconv.Itoa(s.HostPort)
	args := []string{
		"run", "-d",
		"--name", s.Name,
		"--label", Label + "=proxy",
		"--network", s.Name,
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--user", "65532:65532",
		"--publish", "127.0.0.1:" + port + ":" + strconv.Itoa(ContainerPort),
	}
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

// StartProxy creates the proxy's own network and runs the proxy on it. It
// tries a ::1 binding as well and falls back to 127.0.0.1 alone where the
// runtime cannot publish on IPv6.
func (r Runtime) StartProxy(s ProxySpec) error {
	if err := r.EnsureNetwork(s.Name, "proxy"); err != nil {
		return err
	}
	_, err := r.Run(s.RunArgs(true)...)
	if err == nil {
		return nil
	}
	_ = r.RemoveContainer(s.Name)
	if _, err2 := r.Run(s.RunArgs(false)...); err2 != nil {
		return fmt.Errorf("starting proxy: %w (with ::1: %v)", err2, err)
	}
	return nil
}

// IsRouteNetwork reports whether a network name belongs to a route.
func IsRouteNetwork(name string) bool { return strings.HasPrefix(name, "bl-") }
