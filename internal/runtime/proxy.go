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

// IsRouteNetwork reports whether a network name belongs to a route.
func IsRouteNetwork(name string) bool { return strings.HasPrefix(name, "bl-") }
