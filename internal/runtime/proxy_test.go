package runtime

import (
	"strings"
	"testing"
)

func TestRunArgs(t *testing.T) {
	s := ProxySpec{Name: "berthlight", Image: "img:1", HostPort: 7480, StateDir: "/home/u/.local/state/berthlight", Bases: []string{"localhost", "dev.example.com"}}
	got := strings.Join(s.RunArgs(true), " ")
	for _, want := range []string{
		"--network berthlight",
		"--read-only",
		"--cap-drop ALL",
		"--security-opt no-new-privileges",
		"--user 65532:65532",
		"--publish 127.0.0.1:7480:7480",
		"--publish [::1]:7480:7480",
		"--volume /home/u/.local/state/berthlight:/state:ro",
		"img:1 --state /state --port 7480 --base localhost --base dev.example.com",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("run args lack %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"sock", "--privileged", "0.0.0.0", "--cap-add"} {
		if strings.Contains(got, bad) {
			t.Errorf("run args contain %q:\n%s", bad, got)
		}
	}
	if v4 := strings.Join(s.RunArgs(false), " "); strings.Contains(v4, "::1") {
		t.Errorf("ipv6=false still publishes on ::1: %s", v4)
	}
}

func TestFilterNoise(t *testing.T) {
	in := "Emulate Docker CLI using podman. Create /etc/containers/nodocker to quiet msg.\nError: no such network"
	if got := filterNoise(in); got != "Error: no such network" {
		t.Errorf("filterNoise = %q", got)
	}
}
