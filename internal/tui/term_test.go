package tui

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestDetectKind(t *testing.T) {
	tests := []struct {
		env  map[string]string
		want Kind
	}{
		{map[string]string{"TERM_PROGRAM": "ghostty"}, Ghostty},
		{map[string]string{"TERM": "xterm-ghostty"}, Ghostty},
		{map[string]string{"KITTY_WINDOW_ID": "1"}, Kitty},
		{map[string]string{"TERM": "xterm-kitty"}, Kitty},
		{map[string]string{"TERM_PROGRAM": "WezTerm"}, WezTerm},
		{map[string]string{"TERM_PROGRAM": "iTerm.app"}, ITerm},
		{map[string]string{"WT_SESSION": "x"}, WindowsTerminal},
		{map[string]string{"TERM": "xterm-256color"}, Unknown},
		{map[string]string{"TERM_PROGRAM": "tmux"}, Unknown},
	}
	for _, tt := range tests {
		if got := DetectKind(func(k string) string { return tt.env[k] }); got != tt.want {
			t.Errorf("%v: %q, want %q", tt.env, got, tt.want)
		}
	}
}

// TestNotATerminal: without a terminal, nothing but the plain text.
func TestNotATerminal(t *testing.T) {
	var buf bytes.Buffer
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	for _, term := range []Term{NewTerm(&buf), NewTerm(f), NewTerm(devnull), {}} {
		if term.TTY() {
			t.Fatal("a buffer or file is a terminal")
		}
		if got := term.Link("http://a.localhost:7480", "a"); got != "a" {
			t.Errorf("link: %q", got)
		}
		if term.Copy("x") || term.Notify("t", "b") {
			t.Error("copy or notify on a non-terminal")
		}
		term.Title("x")
		term.PushTitle()
		term.PopTitle()
		term.Progress(ProgressNormal, 50)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %q", buf.String())
	}
}

// TestSequences checks each escape sequence byte for byte against its
// specification (see the links on each function).
func TestSequences(t *testing.T) {
	run := func(kind Kind, term string, f func(Term)) string {
		var buf bytes.Buffer
		f(ForcedTerm(&buf, kind, term))
		return buf.String()
	}
	tests := []struct {
		name string
		got  string
		want string
	}{
		// OSC 8: ESC ] 8 ; params ; URI ST text ESC ] 8 ; ; ST
		{"OSC 8 link", ForcedTerm(nil, Unknown, "xterm-256color").Link("http://shop.localhost:7480", "shop"),
			"\x1b]8;;http://shop.localhost:7480\x1b\\shop\x1b]8;;\x1b\\"},
		{"no link on the Linux console", ForcedTerm(nil, Unknown, "linux").Link("http://x", "x"), "x"},
		// OSC 52: ESC ] 52 ; c ; base64 BEL
		{"OSC 52 copy", run(Unknown, "xterm", func(t Term) { t.Copy("http://shop.localhost:7480") }),
			"\x1b]52;c;aHR0cDovL3Nob3AubG9jYWxob3N0Ojc0ODA=\x07"},
		// OSC 2 title, XTWINOPS 22/23 push and pop.
		{"OSC 2 title", run(Unknown, "xterm", func(t Term) { t.Title("berthlight · 2/3 ok") }), "\x1b]2;berthlight · 2/3 ok\x07"},
		{"title without control characters", run(Unknown, "xterm", func(t Term) { t.Title("a\x07b\x1bc") }), "\x1b]2;abc\x07"},
		{"push and pop title", run(Unknown, "xterm", func(t Term) { t.PushTitle(); t.PopTitle() }), "\x1b[22;0t\x1b[23;0t"},
		// OSC 9;4: ESC ] 9 ; 4 ; state ; percent BEL, only where supported.
		{"OSC 9;4 Ghostty", run(Ghostty, "xterm-ghostty", func(t Term) { t.Progress(ProgressIndeterminate, 0) }), "\x1b]9;4;3;0\x07"},
		{"OSC 9;4 Windows Terminal", run(WindowsTerminal, "", func(t Term) { t.Progress(ProgressNormal, 150) }), "\x1b]9;4;1;100\x07"},
		{"no OSC 9;4 on iTerm2", run(ITerm, "", func(t Term) { t.Progress(ProgressNormal, 50) }), ""},
		{"no OSC 9;4 on WezTerm", run(WezTerm, "", func(t Term) { t.Progress(ProgressNormal, 50) }), ""},
		{"no OSC 9;4 on unknown", run(Unknown, "xterm", func(t Term) { t.Progress(ProgressNormal, 50) }), ""},
		// OSC 99 (Kitty): title part with d=0, then body part, ST terminated.
		{"OSC 99 Kitty", run(Kitty, "xterm-kitty", func(t Term) { t.Notify("berthlight", "shop.localhost is up") }),
			"\x1b]99;i=berthlight:d=0:p=title;berthlight\x1b\\\x1b]99;i=berthlight:d=1:p=body;shop.localhost is up\x1b\\"},
		// OSC 9 notification: ESC ] 9 ; text BEL.
		{"OSC 9 Ghostty", run(Ghostty, "xterm-ghostty", func(t Term) { t.Notify("berthlight", "up") }), "\x1b]9;berthlight: up\x07"},
		{"OSC 9 iTerm2", run(ITerm, "", func(t Term) { t.Notify("berthlight", "up") }), "\x1b]9;berthlight: up\x07"},
		{"no notification on unknown", run(Unknown, "xterm", func(t Term) { t.Notify("berthlight", "up") }), ""},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, tt.got, tt.want)
		}
	}
}

var escapes = regexp.MustCompile(`\x1b\]8;;[^\x1b]*\x1b\\`)

func TestTableAlignsAroundLinks(t *testing.T) {
	term := ForcedTerm(nil, Unknown, "xterm")
	rows := [][]Cell{
		{{Text: "NAME"}, {Text: "URL"}, {Text: "STATUS"}},
		{{Text: "shop"}, {Text: "http://shop.localhost:7480", Show: term.Link("http://shop.localhost:7480", "http://shop.localhost:7480")}, {Text: "ok"}},
		{{Text: "shop-fix-login"}, {Text: "http://shop-fix-login.localhost:7480", Show: term.Link("http://shop-fix-login.localhost:7480", "http://shop-fix-login.localhost:7480")}, {Text: "down"}},
	}
	var buf bytes.Buffer
	if err := Table(&buf, rows); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b]8;;http://shop.localhost:7480\x1b\\") {
		t.Fatal("links missing")
	}
	visible := escapes.ReplaceAllString(buf.String(), "")
	lines := strings.Split(strings.TrimSuffix(visible, "\n"), "\n")
	col := strings.Index(lines[0], "STATUS")
	for _, l := range lines[1:] {
		if i := strings.LastIndex(l, "  ") + 2; i != col {
			t.Errorf("STATUS column at %d in %q, header at %d", i, l, col)
		}
	}
}
