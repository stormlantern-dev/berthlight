package tui

import (
	"context"
	"fmt"
	"image"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// slowWriter is a terminal on a slow link: each write takes delay, except
// the first buffered writes, which a buffer on the way takes at once.
type slowWriter struct {
	delay    time.Duration
	buffered int
	mu       sync.Mutex
	log      []write
}

type write struct {
	start, end time.Time
	s          string
}

func (w *slowWriter) Write(p []byte) (int, error) {
	start := time.Now()
	w.mu.Lock()
	n := len(w.log)
	w.mu.Unlock()
	if n >= w.buffered {
		time.Sleep(w.delay)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.log = append(w.log, write{start, time.Now(), string(p)})
	return len(p), nil
}

func (w *slowWriter) writes() []write {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]write(nil), w.log...)
}

func isFrame(s string) bool { return strings.Contains(s, "\x1b_Ga=T") }

// frames returns the frames written.
func (w *slowWriter) frames() []write {
	var fs []write
	for _, wr := range w.writes() {
		if isFrame(wr.s) {
			fs = append(fs, wr)
		}
	}
	return fs
}

// drawn is how h draws img with nothing printed under it.
func drawn(h *Header, img *image.RGBA) string {
	return fmt.Sprintf("\x1b[?2026h\x1b7\x1b[%dA\r%s\x1b8\x1b[?2026l", HeaderRows, KittyImage(img, h.id))
}

// slow is the time a write takes on a slow link. The time limits below
// leave one frame of slack for rendering under the race detector; a queue
// of frames would take many times longer.
const slow = 200 * time.Millisecond

// TestHeaderStopOnSlowLink: with a terminal that takes 200 ms per write,
// Stop waits for the frame on its way and the last frame, no more, and the
// last frame drawn is the scene's last.
func TestHeaderStopOnSlowLink(t *testing.T) {
	w := &slowWriter{delay: slow}
	h := startKitty(w, 0, 0, false, false, nil)
	time.Sleep(5*slow + slow/2)
	begin := time.Now()
	h.Stop()
	if took := time.Since(begin); took > 3*slow {
		t.Errorf("Stop took %v; want at most the frame on its way and the last one (%v)", took, 2*slow)
	}
	fs := w.frames()
	if len(fs) == 0 || fs[len(fs)-1].s != drawn(h, Frame(SceneLength)) {
		t.Error("the last frame drawn is not the scene's last")
	}
	h.Stop() // again: returns at once
}

// TestHeaderOutputWaitsForOneFrame: the command's first output waits for at
// most the frame on its way to the terminal, then the last frame; no other
// frame starts after the output is asked for, and none after it.
func TestHeaderOutputWaitsForOneFrame(t *testing.T) {
	w := &slowWriter{delay: slow}
	h := startKitty(w, 0, 0, false, false, nil)
	time.Sleep(3*slow + slow/3)
	asked := time.Now()
	io.WriteString(h.Wrap(w), "NAME  URL\n")
	all := w.writes()
	out := all[len(all)-1]
	if out.s != "NAME  URL\n" {
		t.Fatalf("the output is not the last write: %q", out.s[:min(40, len(out.s))])
	}
	if waited := out.start.Sub(asked); waited > 3*slow {
		t.Errorf("the output waited %v; want at most the frame on its way and the last one (%v)", waited, 2*slow)
	}
	started := 0
	for _, f := range all[:len(all)-1] {
		if isFrame(f.s) && f.start.After(asked) {
			started++
		}
	}
	if started != 1 {
		t.Errorf("%d frames started after the output was asked for; want only the last frame", started)
	}
	if prev := all[len(all)-2]; prev.s != drawn(h, Frame(SceneLength)) {
		t.Error("the frame right before the output is not the scene's last")
	}
}

// TestHeaderDropsFrames: on a slow terminal the scene still ends on time,
// with frames dropped rather than queued: far fewer than the 100 of a fast
// one, each later in the scene than the one before.
func TestHeaderDropsFrames(t *testing.T) {
	w := &slowWriter{delay: slow}
	begin := time.Now()
	h := startKitty(w, 0, 0, false, false, nil)
	h.Play(context.Background())
	if took := time.Since(begin); took > SceneLength+4*slow {
		t.Errorf("the scene took %v on a slow terminal; frames queue up", took)
	}
	n := len(w.frames())
	if max := int(SceneLength/slow) + 2; n > max || n < 3 {
		t.Errorf("%d frames written at %v a frame; want between 3 and %d", n, slow, max)
	}
	if n >= int(SceneLength/frameInterval) {
		t.Errorf("%d frames written; none were dropped", n)
	}
}

// TestHeaderFallsBack: the Amiga scene falls back to the chunky one on a
// very slow terminal, also when a buffer on the way hides that at first,
// but not on a fast one, nor when BERTHLIGHT_SCENE forces it.
func TestHeaderFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name     string
		delay    time.Duration
		buffered int
		auto     bool
		wantHi   bool
		lastWant string
	}{
		{"very slow", 300 * time.Millisecond, 0, true, false, "chunky"},
		{"very slow behind a buffer", 300 * time.Millisecond, 8, true, false, "chunky"},
		{"fast", 0, 0, true, true, "amiga"},
		{"very slow, forced", 300 * time.Millisecond, 0, false, true, "amiga"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &slowWriter{delay: tc.delay, buffered: tc.buffered}
			h := startKitty(w, 0, 0, true, tc.auto, nil)
			// Past the first second, and past the buffer by more than
			// fallbackFrames writes. Rendering an Amiga frame takes up to
			// 100 ms under the race detector on CI, so wait for writes,
			// not for a fixed time.
			begin := time.Now()
			for time.Since(begin) < fallbackAfter+500*time.Millisecond || len(w.writes()) < tc.buffered+fallbackFrames+1 {
				if time.Since(begin) > SceneLength {
					t.Fatalf("%d writes by the end of the scene", len(w.writes()))
				}
				time.Sleep(10 * time.Millisecond)
			}
			h.Stop()
			if h.hi != tc.wantHi {
				t.Errorf("drawing the Amiga scene: %v, want %v (%d frames written)", h.hi, tc.wantHi, h.drawn)
			}
			last := Frame(SceneLength)
			if tc.wantHi {
				last = FrameHi(SceneLength)
			}
			fs := w.frames()
			if fs[len(fs)-1].s != drawn(h, last) {
				t.Errorf("the last frame is not the %s scene's last", tc.lastWant)
			}
		})
	}
}

func TestSceneChoice(t *testing.T) {
	for v, want := range map[string][2]bool{
		"":        {true, true},
		"amiga":   {true, false},
		"AMIGA":   {true, false},
		"c64":     {false, false},
		" c64 ":   {false, false},
		"spectre": {true, true},
	} {
		if hi, auto := sceneChoice(v); hi != want[0] || auto != want[1] {
			t.Errorf("BERTHLIGHT_SCENE=%q: Amiga %v, fallback %v; want %v, %v", v, hi, auto, want[0], want[1])
		}
	}
}

// fakeLink is a terminal over SSH: writes return at once into a buffer as
// big as sshd's, which drains at rate bytes a second; backlog reports what
// is still in it, as the kernel's socket table does.
type fakeLink struct {
	rate     float64
	mu       sync.Mutex
	queued   float64
	at       time.Time
	frames   int
	atOutput int // the backlog when the command's output came
	output   bool
}

func (l *fakeLink) settle() {
	now := time.Now()
	if !l.at.IsZero() {
		l.queued = max(0, l.queued-l.rate*now.Sub(l.at).Seconds())
	}
	l.at = now
}

func (l *fakeLink) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.settle()
	if isFrame(string(p)) {
		l.frames++
	} else if !l.output {
		l.output, l.atOutput = true, int(l.queued)
	}
	l.queued += float64(len(p))
	return len(p), nil
}

func (l *fakeLink) backlog() (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.settle()
	return int(l.queued), true
}

// TestHeaderWatchesTheLink: over SSH, where writes return long before the
// frames reach the terminal, the header waits for the link: the command's
// output queues behind at most about two frames, on a slow link the scene
// falls back to the chunky one, and on a fast one it does not.
func TestHeaderWatchesTheLink(t *testing.T) {
	amigaFrame := len(drawn(&Header{}, FrameHi(SceneLength)))
	for _, tc := range []struct {
		name   string
		rate   float64
		wantHi bool
	}{
		{"50 KB/s", 50 << 10, false},
		{"10 MB/s", 10 << 20, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &fakeLink{rate: tc.rate}
			h := startKitty(l, 0, 0, true, true, l.backlog)
			time.Sleep(2500 * time.Millisecond)
			io.WriteString(h.Wrap(l), "NAME  URL\n")
			if most := linkSlack + 2*amigaFrame; l.atOutput > most {
				t.Errorf("the output queued behind %d bytes; want at most %d", l.atOutput, most)
			}
			if h.hi != tc.wantHi {
				t.Errorf("drawing the Amiga scene: %v, want %v (%d frames)", h.hi, tc.wantHi, l.frames)
			}
			// At 50 KB/s an Amiga frame takes 0.4 s; on the fast link the
			// frames come as fast as they render (10 a second or more,
			// under the race detector on CI).
			if tc.wantHi && l.frames < 15 {
				t.Errorf("%d frames on a fast link", l.frames)
			}
		})
	}
}

// TestHeaderStopDoesNotWaitForTheLink: on a link that carries nothing,
// Stop draws the last frame at once.
func TestHeaderStopDoesNotWaitForTheLink(t *testing.T) {
	l := &fakeLink{rate: 0}
	h := startKitty(l, 0, 0, true, true, l.backlog)
	time.Sleep(300 * time.Millisecond)
	begin := time.Now()
	h.Stop()
	if took := time.Since(begin); took > slow {
		t.Errorf("Stop took %v on a stalled link", took)
	}
}
