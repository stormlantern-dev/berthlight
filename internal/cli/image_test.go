package cli

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	rt "stormlantern.nl/berthlight/internal/runtime"
)

// fakeRuntime is a shell script standing in for docker or podman. It logs
// each call's arguments, one call per line, and fails every call except
// those matched by ok, a case pattern such as `"image inspect "*|"pull "*`.
func fakeRuntime(t *testing.T, ok string) (rt.Runtime, func() string) {
	t.Helper()
	return scriptRuntime(t, ok+") exit 0 ;;")
}

// scriptRuntime is fakeRuntime with the shell case arms given in full, so
// calls can print, as `container inspect` does. Calls no arm matches fail.
func scriptRuntime(t *testing.T, arms string) (rt.Runtime, func() string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake runtime is a shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\ncase \"$*\" in\n" + arms + "\nesac\necho \"Error: unknown: no such image\" >&2\nexit 125\n"
	bin := filepath.Join(dir, "fake-runtime")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return rt.Runtime{Bin: bin}, func() string {
		b, _ := os.ReadFile(log)
		return string(b)
	}
}

func TestPullImage(t *testing.T) {
	for _, tc := range []struct {
		ok, calls string
		fails     bool
	}{
		{ok: `"image inspect "*`, calls: "image inspect img\n"},
		{ok: `"pull "*`, calls: "image inspect img\npull -q img\n"},
		{ok: "never", calls: "image inspect img\npull -q img\n", fails: true},
	} {
		run, calls := fakeRuntime(t, tc.ok)
		err := run.PullImage("img")
		if (err != nil) != tc.fails || calls() != tc.calls {
			t.Errorf("ok %q: err %v, calls %q; want failure %v, calls %q", tc.ok, err, calls(), tc.fails, tc.calls)
		}
	}
}

// TestUpMissingImage (#63): with no image to be had, up says which image and
// how to choose another, and never tries to run the proxy.
func TestUpMissingImage(t *testing.T) {
	run, calls := fakeRuntime(t, "never")
	c := Config{ProxyName: "berthlight", Image: "ghcr.io/stormlantern-dev/berthlight:edge", Port: 7480, StateDir: t.TempDir()}
	t.Setenv("BERTHLIGHT_IMAGE", "")
	_, err := up(c, run, nil)
	if err == nil {
		t.Fatal("up succeeded without an image")
	}
	for _, want := range []string{"ghcr.io/stormlantern-dev/berthlight:edge", "BERTHLIGHT_IMAGE", "built from main", "ghcr.io/stormlantern-dev/berthlight:latest", "no such image"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	for _, l := range strings.Split(calls(), "\n") {
		if strings.HasPrefix(l, "run ") {
			t.Errorf("up ran the proxy without an image: %s", l)
		}
	}
}

func TestImageError(t *testing.T) {
	cause := errors.New("podman pull -q x: Error: denied")
	multi := imageError("x:1", false, errors.New("warning: retrying (1/3)\nwarning: retrying (2/3)\nError: manifest unknown"))
	if strings.Contains(multi.Error(), "retrying") || !strings.HasSuffix(multi.Error(), "\nError: manifest unknown") {
		t.Errorf("not just the runtime's last line:\n%v", multi)
	}
	for _, tc := range []struct {
		image   string
		fromEnv bool
		want    []string
		not     string
	}{
		{"ghcr.io/stormlantern-dev/berthlight:edge", false, []string{"development build", "built from main", "berthlight:latest", "BERTHLIGHT_IMAGE"}, ""},
		{"ghcr.io/stormlantern-dev/berthlight:0.8.1", false, []string{"0.8.1", "network", "BERTHLIGHT_IMAGE"}, "development build"},
		{"localhost/mine:dev", true, []string{"localhost/mine:dev", "BERTHLIGHT_IMAGE names it"}, "development build"},
	} {
		err := imageError(tc.image, tc.fromEnv, cause)
		if !strings.HasSuffix(err.Error(), "\n"+cause.Error()) {
			t.Errorf("%s: the runtime's error is not the last line:\n%v", tc.image, err)
		}
		if !errors.Is(err, cause) {
			t.Errorf("%s: the runtime's error is not wrapped", tc.image)
		}
		for _, w := range tc.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: lacks %q:\n%v", tc.image, w, err)
			}
		}
		if tc.not != "" && strings.Contains(err.Error(), tc.not) {
			t.Errorf("%s: mentions %q:\n%v", tc.image, tc.not, err)
		}
	}
}

// runningProxy is the case arms of a runtime with a running proxy on image;
// pullable says which images it has or can pull. Every other call
// succeeds.
func runningProxy(image, pullable string) string {
	return `*"{{.State.Running}}"*) echo true; exit 0 ;;
*"{{.Config.Image}}"*) echo ` + image + `; exit 0 ;;
"image inspect "*|"pull "*) case "$*" in *` + pullable + `) exit 0 ;; esac ;;
*) exit 0 ;;`
}

// listening is a port that answers, as the new proxy's would.
func listening(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

// TestUpReplacesProxy (#70): after an upgrade, up replaces a proxy on
// another image, once the new image is there; on the same image it leaves
// the proxy alone; without the new image the old proxy keeps running.
func TestUpReplacesProxy(t *testing.T) {
	t.Setenv("BERTHLIGHT_IMAGE", "")
	const old, want = "ghcr.io/stormlantern-dev/berthlight:0.8.1", "ghcr.io/stormlantern-dev/berthlight:0.8.2"
	for _, tc := range []struct {
		name, running, pullable string
		replaced, errWant       string
		calls, noCalls          []string
	}{
		{name: "older image", running: old, pullable: want, replaced: old,
			calls: []string{"rm -f berthlight", "--name berthlight", want}},
		{name: "same image", running: want, pullable: want,
			noCalls: []string{"rm -f", "run -d", "pull"}},
		{name: "runtime spells it out", running: "docker.io/" + want, pullable: want,
			noCalls: []string{"rm -f", "run -d"}},
		{name: "new image missing", running: old, pullable: "nothing",
			errWant: "the proxy keeps running " + old, noCalls: []string{"rm -f", "run -d"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run, calls := scriptRuntime(t, runningProxy(tc.running, tc.pullable))
			c := Config{ProxyName: "berthlight", Image: want, Port: listening(t), StateDir: t.TempDir()}
			replaced, err := up(c, run, nil)
			switch {
			case tc.errWant == "" && err != nil:
				t.Fatalf("up: %v", err)
			case tc.errWant != "" && (err == nil || !strings.Contains(err.Error(), tc.errWant) || !strings.Contains(err.Error(), want)):
				t.Fatalf("up: error %v, want it to name %s and say %q", err, want, tc.errWant)
			}
			if replaced != tc.replaced {
				t.Errorf("replaced %q, want %q", replaced, tc.replaced)
			}
			log := calls()
			for _, w := range tc.calls {
				if !strings.Contains(log, w) {
					t.Errorf("no call with %q:\n%s", w, log)
				}
			}
			for _, w := range tc.noCalls {
				for _, l := range strings.Split(log, "\n") {
					if strings.HasPrefix(l, w) {
						t.Errorf("unexpected call %q", l)
					}
				}
			}
		})
	}
}
