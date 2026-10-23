package tui

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	xterm "golang.org/x/term"
)

// Terminal features beyond the boat (PRD 0001, R12): hyperlinks, the
// clipboard, the tab title, a progress indicator and desktop
// notifications. Each is an escape sequence that only goes to an
// interactive terminal, and progress and notifications only to terminals
// known to understand them, so nothing ever shows up as garbage in a pipe,
// a log or an older terminal.

// Kind is the terminal program, as far as the environment tells.
type Kind string

// Terminals with features worth telling apart.
const (
	Unknown         Kind = ""
	Ghostty         Kind = "ghostty"
	Kitty           Kind = "kitty"
	WezTerm         Kind = "wezterm"
	ITerm           Kind = "iterm2"
	WindowsTerminal Kind = "windows-terminal"
)

// DetectKind reads the variables terminals set for their programs.
func DetectKind(env func(string) string) Kind {
	switch {
	case env("TERM_PROGRAM") == "ghostty" || env("TERM") == "xterm-ghostty" || env("GHOSTTY_RESOURCES_DIR") != "":
		return Ghostty
	case env("KITTY_WINDOW_ID") != "" || env("TERM") == "xterm-kitty":
		return Kitty
	case env("TERM_PROGRAM") == "WezTerm":
		return WezTerm
	case env("TERM_PROGRAM") == "iTerm.app":
		return ITerm
	case env("WT_SESSION") != "":
		return WindowsTerminal
	}
	return Unknown
}

// Term writes terminal features to w. The zero Term, and one for a writer
// that is not a terminal, does nothing and returns text unchanged.
type Term struct {
	w    io.Writer
	tty  bool
	kind Kind
	term string
}

// NewTerm returns a Term for w if it is an interactive terminal.
func NewTerm(w io.Writer) Term {
	f, ok := w.(*os.File)
	if !ok || !isTerminal(f) {
		return Term{w: w}
	}
	return Term{w: w, tty: true, kind: DetectKind(os.Getenv), term: os.Getenv("TERM")}
}

// TTY reports whether the Term writes to an interactive terminal.
func (t Term) TTY() bool { return t.tty }

// Kind is the detected terminal program.
func (t Term) Kind() Kind { return t.kind }

// links: every terminal that parses OSC ignores the ones it does not know,
// except the Linux console and dumb terminals.
func (t Term) links() bool { return t.tty && t.term != "dumb" && t.term != "linux" }

// Link returns text as a hyperlink to url (OSC 8), or text unchanged.
// https://gist.github.com/egmontkob/eb114294efbcd5adb1944c9f3cb5feda
func (t Term) Link(url, text string) string {
	if !t.links() {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// Copy puts text on the system clipboard through the terminal (OSC 52,
// clipboard "c"), which also works over SSH and inside a container. It
// reports whether it sent the sequence; whether the terminal honours it
// is up to the terminal's settings.
// https://invisible-island.net/xterm/ctlseqs/ctlseqs.html#h3-Operating-System-Commands
func (t Term) Copy(text string) bool {
	if !t.links() {
		return false
	}
	fmt.Fprint(t.w, "\x1b]52;c;"+base64.StdEncoding.EncodeToString([]byte(text))+"\x07")
	return true
}

// Title sets the window or tab title (OSC 2). PushTitle saves the current
// one and PopTitle restores it (XTWINOPS 22 and 23).
func (t Term) Title(s string) {
	if t.links() {
		fmt.Fprint(t.w, "\x1b]2;"+sanitize(s)+"\x07")
	}
}

// PushTitle saves the title so PopTitle can restore it.
func (t Term) PushTitle() {
	if t.links() {
		fmt.Fprint(t.w, "\x1b[22;0t")
	}
}

// PopTitle restores the title PushTitle saved.
func (t Term) PopTitle() {
	if t.links() {
		fmt.Fprint(t.w, "\x1b[23;0t")
	}
}

// ProgressState is the state of the tab or taskbar progress indicator.
type ProgressState int

// States of OSC 9;4, as ConEmu defined it and Windows Terminal and
// Ghostty follow it.
// https://learn.microsoft.com/windows/terminal/tutorials/progress-bar-sequences
const (
	ProgressOff           ProgressState = 0
	ProgressNormal        ProgressState = 1
	ProgressError         ProgressState = 2
	ProgressIndeterminate ProgressState = 3
)

// Progress shows the progress indicator (OSC 9;4), on terminals known to
// support it. Elsewhere OSC 9 means a notification (iTerm2, WezTerm), so it
// is not sent to unknown terminals. Ghostty clears it after about 15
// seconds; callers resend it at least once a second while it should show.
func (t Term) Progress(st ProgressState, percent int) {
	if !t.tty || (t.kind != Ghostty && t.kind != WindowsTerminal) {
		return
	}
	fmt.Fprintf(t.w, "\x1b]9;4;%d;%d\x07", st, min(max(percent, 0), 100))
}

// Notify shows a desktop notification, in the dialect of the terminal:
// OSC 99 for Kitty, OSC 9 for Ghostty, WezTerm and iTerm2. Other terminals
// get nothing. It reports whether it sent one.
//   - OSC 99: https://sw.kovidgoyal.net/kitty/desktop-notifications/
//   - OSC 9: https://ghostty.org/docs/vt/osc/conemu (text without a ConEmu
//     sub-id is a notification), https://iterm2.com/documentation-escape-codes.html
func (t Term) Notify(title, body string) bool {
	if !t.tty {
		return false
	}
	title, body = sanitize(title), sanitize(body)
	switch t.kind {
	case Kitty:
		// The title, then the body; d=0 says more follows, d=1 that this
		// is the last part. i ties the parts together.
		fmt.Fprint(t.w, "\x1b]99;i=berthlight:d=0:p=title;"+title+"\x1b\\")
		fmt.Fprint(t.w, "\x1b]99;i=berthlight:d=1:p=body;"+body+"\x1b\\")
	case Ghostty, WezTerm, ITerm:
		fmt.Fprint(t.w, "\x1b]9;"+title+": "+body+"\x07")
	default:
		return false
	}
	return true
}

// sanitize removes control characters, which would end or corrupt an
// escape sequence.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

// Cell is a table cell: its visible text, and how to print it (with a
// hyperlink, say). Show is Text when empty.
type Cell struct{ Text, Show string }

// Table writes rows as columns two spaces apart, aligned on the visible
// text, so hyperlinks and other escapes do not shift the columns as they
// would in text/tabwriter.
func Table(w io.Writer, rows [][]Cell) error {
	var widths []int
	for _, row := range rows {
		for i, c := range row {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], utf8.RuneCountInString(c.Text))
		}
	}
	var b strings.Builder
	for _, row := range rows {
		for i, c := range row {
			show := c.Show
			if show == "" {
				show = c.Text
			}
			b.WriteString(show)
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c.Text)+2))
			}
		}
		b.WriteByte('\n')
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// isTerminal reports whether f is a terminal. A character device is not
// enough: /dev/null is one too.
func isTerminal(f *os.File) bool { return xterm.IsTerminal(int(f.Fd())) }

// ForcedTerm returns a Term that writes to w as an interactive terminal of
// the given kind and TERM, whatever w is. For tests, and for callers that
// know better than the environment.
func ForcedTerm(w io.Writer, kind Kind, term string) Term {
	return Term{w: w, tty: true, kind: kind, term: term}
}
