package tui

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// cells counts terminal cells, with the boat as its declared width.
func cells(s style, line string) int {
	return utf8.RuneCountInString(strings.Replace(line, s.boat, strings.Repeat("x", s.boatWidth), 1))
}

func TestRenderKeepsWidth(t *testing.T) {
	for _, s := range []style{unicodeStyle, asciiStyle} {
		want := -1
		for pos := -1; pos <= water+1; pos++ {
			for frame := 0; frame < 10; frame++ {
				for _, st := range []state{sailing, berthed, stranded} {
					line := s.render(frame, pos, st, "")
					if !strings.Contains(line, s.boat) {
						t.Fatalf("no boat in %q", line)
					}
					if n := cells(s, line); want < 0 {
						want = n
					} else if n != want {
						t.Errorf("pos %d frame %d state %d: %d cells, want %d: %q", pos, frame, st, n, want, line)
					}
				}
			}
		}
	}
}

func TestRenderStates(t *testing.T) {
	s := unicodeStyle
	if got := s.render(0, 0, sailing, "starting"); !strings.HasPrefix(got, "  ⛵") || !strings.Contains(got, "┃  starting") {
		t.Errorf("start: %q", got)
	}
	if got := s.render(3, 4, berthed, "berthed"); got != "  "+strings.Repeat("▁", water-2)+"⛵⚓  berthed" {
		t.Errorf("berthed: %q", got)
	}
	// Waves move from frame to frame; the boat does not.
	a, b := s.render(0, 11, sailing, ""), s.render(1, 11, sailing, "")
	if a == b || strings.Index(a, "⛵") != strings.Index(b, "⛵") {
		t.Errorf("frames %q and %q", a, b)
	}
	s.color = true
	if got := s.render(0, 0, stranded, "no answer"); !strings.Contains(got, red+"no answer"+reset) {
		t.Errorf("stranded in colour: %q", got)
	}
}

func TestNilProgress(t *testing.T) {
	var p *Progress
	p.Step(0.5, "x")
	p.Done("y")
	p.Fail("z")
	if Start(&bytes.Buffer{}, "x") != nil {
		t.Error("Start drew on a buffer")
	}
	if f, err := os.CreateTemp(t.TempDir(), "out"); err == nil {
		defer f.Close()
		if Interactive(f) {
			t.Error("a regular file is interactive")
		}
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestQuickOperationDrawsNothing(t *testing.T) {
	var out syncBuffer
	p := start(&out, unicodeStyle, "starting")
	p.Done("berthed")
	if out.String() != "" {
		t.Errorf("drew %q", out.String())
	}
}

func TestSailAndBerth(t *testing.T) {
	var out syncBuffer
	p := start(&out, unicodeStyle, "starting proxy")
	time.Sleep(delay + 3*tick)
	p.Step(0.5, "waiting for the port")
	time.Sleep(2 * tick)
	p.Done("proxy up")
	got := out.String()
	for _, want := range []string{"\x1b[?25l", "starting proxy", "waiting for the port", "⛵⚓  proxy up in ", "\n\x1b[?25h", "\x1b[?2026h\r\x1b[2K"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	// The boat glides: its position never jumps more than a few cells
	// between frames, and the last stretch is sailed cell by cell.
	prev, frames := -1, 0
	for _, f := range strings.Split(got, "\x1b[?2026h")[1:] {
		pos := utf8.RuneCountInString(f[:strings.Index(f, "⛵")]) - len("\r\x1b[2K") - 2
		if prev >= 0 && (pos < prev || pos-prev > 4) {
			t.Errorf("boat went from cell %d to %d", prev, pos)
		}
		prev = pos
		frames++
	}
	if prev != water-2 || frames < water/2 {
		t.Errorf("%d frames, boat ends at cell %d", frames, prev)
	}
	if !strings.HasSuffix(got, "\n\x1b[?25h") {
		t.Errorf("output does not end with a new line and the cursor shown: %q", got[max(len(got)-40, 0):])
	}
}
