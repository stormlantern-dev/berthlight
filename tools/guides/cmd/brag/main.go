// Command brag records the README's brag picture: `berthlight status
// --watch` in a terminal while twelve dev servers come up one by one,
// each in its own container on its own network, routed by name.
//
// It runs real containers with the real binary, on the host (Docker or
// Podman), and writes an asciicast v2 recording for cmd/gif:
//
//	go run ./cmd/brag -berthlight ../../berthlight -out brag.cast
//
// The proxy uses berthlight's defaults (container berthlight, port 7480),
// so stop your own proxy first; the routes live in a temporary state
// directory and everything is removed afterwards.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"stormlantern.nl/berthlight/tools/guides/cast"
)

// The routes: a few projects, some with worktrees, as on a busy day.
var routes = []string{
	"shop", "shop-fix-login", "shop-checkout-v2", "gallery", "gallery-dark-mode", "blog",
	"docs", "api", "api-rate-limits", "admin", "storybook", "mailpit",
}

const (
	cols, rows = 96, 34
	backend    = "docker.io/library/busybox:1.37"
	prompt     = "\x1b[1;36m~/projects\x1b[0m \x1b[35m$\x1b[0m "
	command    = "berthlight status --watch"
)

func main() {
	bin := flag.String("berthlight", "berthlight", "the berthlight binary to record")
	out := flag.String("out", "brag.cast", "recording to write")
	runtime := flag.String("runtime", "podman", "docker or podman")
	image := flag.String("image", "", "proxy image (BERTHLIGHT_IMAGE); default: the binary's own")
	flag.Parse()

	state, err := os.MkdirTemp("", "berthlight-brag-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(state)
	env := environment(state, *runtime, *image)
	run := func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
		return nil
	}
	defer cleanup(run, *bin, *runtime)
	if err := run(*bin, "up"); err != nil {
		log.Fatal(err)
	}
	if err := run(*runtime, "pull", "-q", backend); err != nil {
		log.Fatal(err)
	}

	var (
		mu     sync.Mutex
		events []cast.Event
		start  = time.Now()
	)
	emit := func(at float64, data string) {
		mu.Lock()
		events = append(events, cast.Event{Time: at, Data: data})
		mu.Unlock()
	}
	// The prompt, and the command typed at it.
	at := 0.4
	emit(at, prompt)
	at += 0.6
	for _, r := range command {
		emit(at, string(r))
		at += 0.055
	}
	at += 0.3
	emit(at, "\r\n")
	offset := at

	watch := exec.Command(*bin, "status", "--watch")
	watch.Env = env
	f, err := pty.StartWithSize(watch, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		log.Fatal(err)
	}
	start = time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64*1024)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				emit(offset+time.Since(start).Seconds(), string(buf[:n]))
			}
			if err != nil {
				return
			}
		}
	}()

	time.Sleep(2500 * time.Millisecond)
	for _, name := range routes {
		if err := serve(run, *runtime, name); err != nil {
			log.Print(err)
			break
		}
		if err := run(*bin, "add", name, "8080"); err != nil {
			log.Print(err)
			break
		}
		time.Sleep(350 * time.Millisecond)
	}
	time.Sleep(4 * time.Second)
	watch.Process.Signal(syscall.SIGINT)
	watch.Wait()
	f.Close()
	<-done
	emit(offset+time.Since(start).Seconds()+0.2, prompt)

	w, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer w.Close()
	if err := cast.Write(w, cast.Header{Width: cols, Height: rows, Title: "berthlight status --watch"}, events); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: %d events, %.1fs\n", *out, len(events), events[len(events)-1].Time)
}

// environment is the recording's environment: Ghostty's, so the header is
// drawn with the Kitty graphics protocol and notifications are sent, with
// nothing that would turn the header off.
func environment(state, runtime, image string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "CI", "TMUX", "KITTY_WINDOW_ID", "SSH_CONNECTION", "SSH_TTY", "BERTHLIGHT_NO_ANIMATION", "NO_COLOR",
			"TERM", "TERM_PROGRAM", "COLORTERM", "BERTHLIGHT_STATE_DIR", "BERTHLIGHT_PORT", "BERTHLIGHT_PROXY_NAME", "BERTHLIGHT_BASE":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "TERM=xterm-ghostty", "TERM_PROGRAM=ghostty", "COLORTERM=truecolor",
		"BERTHLIGHT_STATE_DIR="+state, "BERTHLIGHT_RUNTIME="+runtime)
	if image != "" {
		env = append(env, "BERTHLIGHT_IMAGE="+image)
	}
	return env
}

// serve starts a dev server for a route: a container on the route's own
// network, under the route's name, answering on port 8080.
func serve(run func(string, ...string) error, runtime, name string) error {
	network := "bl-" + name
	if err := run(runtime, "network", "create", "--label", "io.stormlantern.berthlight=route", network); err != nil {
		return err
	}
	page := fmt.Sprintf("mkdir /www && echo '%s says hi' > /www/index.html && httpd -f -p 8080 -h /www", name)
	if err := run(runtime, "run", "-d", "--rm", "--name", "brag-"+name, "--network", network, "--network-alias", name, backend, "sh", "-c", page); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for run(runtime, "exec", "brag-"+name, "wget", "-q", "-O-", "http://127.0.0.1:8080/") != nil {
		if time.Now().After(deadline) {
			return fmt.Errorf("dev server %s did not start", name)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func cleanup(run func(string, ...string) error, bin, runtime string) {
	for _, name := range routes {
		run(runtime, "rm", "-f", "brag-"+name)
	}
	for _, name := range routes {
		run(bin, "rm", name)
		run(runtime, "network", "rm", "bl-"+name)
	}
	run(bin, "down")
	run(runtime, "network", "rm", "berthlight")
	_ = io.Discard
}
