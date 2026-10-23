package cli

import (
	"bytes"
	"strings"
	"testing"

	"stormlantern.nl/berthlight/internal/route"
)

func TestRun(t *testing.T) {
	t.Setenv("BERTHLIGHT_STATE_DIR", t.TempDir())
	tests := []struct {
		args     []string
		code     int
		inStdout string
		inStderr string
	}{
		{[]string{"version"}, 0, "berthlight ", ""},
		{[]string{"help"}, 0, "Usage:", ""},
		{nil, 2, "", "Usage:"},
		{[]string{"nope"}, 2, "", `unknown command "nope"`},
		{[]string{"url", "-C", t.TempDir()}, 1, "", "git rev-parse"},
		{[]string{"add", "shop"}, 2, "", "usage: berthlight add"},
		{[]string{"add", "Shop!", "8080"}, 1, "", "not a valid label"},
		{[]string{"add", "shop", "http"}, 1, "", "not a number"},
		{[]string{"add", "shop", "70000"}, 1, "", "out of range"},
		{[]string{"rm"}, 2, "", "usage: berthlight rm"},
		{[]string{"rm", "nothing"}, 1, "", `no route named "nothing"`},
		{[]string{"ls"}, 0, "No routes", ""},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		code := Run(tt.args, &out, &errOut)
		if code != tt.code {
			t.Errorf("Run(%q) = %d, want %d (stderr: %s)", tt.args, code, tt.code, errOut.String())
		}
		if !strings.Contains(out.String(), tt.inStdout) {
			t.Errorf("Run(%q) stdout %q, want it to contain %q", tt.args, out.String(), tt.inStdout)
		}
		if !strings.Contains(errOut.String(), tt.inStderr) {
			t.Errorf("Run(%q) stderr %q, want it to contain %q", tt.args, errOut.String(), tt.inStderr)
		}
	}
}

func TestLsShowsURLs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BERTHLIGHT_STATE_DIR", dir)
	t.Setenv("BERTHLIGHT_PORT", "9999")
	t.Setenv("BERTHLIGHT_BASE", "dev.example.com,localhost")
	tab := &route.Table{}
	tab.Put(route.Route{Name: "shop", Target: "web", Port: 8080})
	if err := route.Save(dir, tab); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := Run([]string{"ls"}, &out, &out); code != 0 {
		t.Fatalf("ls: %d %s", code, out.String())
	}
	for _, want := range []string{"http://shop.dev.example.com:9999", "web:8080", "bl-shop"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("ls output %q lacks %q", out.String(), want)
		}
	}
}

func TestConfigErrors(t *testing.T) {
	t.Setenv("BERTHLIGHT_PORT", "abc")
	if _, err := LoadConfig(); err == nil {
		t.Error("BERTHLIGHT_PORT=abc accepted")
	}
	t.Setenv("BERTHLIGHT_PORT", "")
	t.Setenv("BERTHLIGHT_BASE", " , ")
	if _, err := LoadConfig(); err == nil {
		t.Error("empty BERTHLIGHT_BASE accepted")
	}
}

func TestParseInterspersed(t *testing.T) {
	fs := newFlags("t", &bytes.Buffer{})
	target := fs.String("target", "", "")
	pos, err := parseInterspersed(fs, []string{"shop", "8080", "--target", "web"})
	if err != nil || len(pos) != 2 || pos[0] != "shop" || pos[1] != "8080" || *target != "web" {
		t.Errorf("got %q, %q, %v", pos, *target, err)
	}
}
