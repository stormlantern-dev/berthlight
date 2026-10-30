package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	xterm "golang.org/x/term"
)

// The header (PRD 0001, R12) shows the scene at the top of a command's
// output on a terminal:
//   - animated in terminals with the Kitty graphics protocol (Kitty,
//     Ghostty), locally and over SSH, in its Amiga version, for as long as
//     the command is working and at most SceneLength; the first output, or
//     the end of the command, puts it on its last frame, so it never holds a
//     command up for more than the frame on its way to the terminal;
//   - as the last frame in half-block characters on other truecolor
//     terminals;
//   - as one line of text elsewhere.
//
// Nothing at all without a terminal, in CI, with TERM=dumb or with
// BERTHLIGHT_NO_ANIMATION. BERTHLIGHT_SCENE=amiga or =c64 forces the
// version the Kitty graphics protocol shows.

// HeaderRows is the header's height in terminal rows.
const HeaderRows = SceneH / 2

type headerMode int

const (
	modeText headerMode = iota
	modeHalfBlocks
	modeKitty
)

// frameInterval is the time between animation frames.
const frameInterval = 40 * time.Millisecond

// The Amiga scene falls back to the chunky one, for the rest of the run, as
// soon as fewer than fallbackFrames frames have reached the terminal in a
// fallbackAfter: the first one, or any later one. An Amiga frame is about
// 19 KB and a chunky one about 5 KB, so 5 frames a second is a link of
// about 95 KB/s (0.75 Mbit/s): below it the Amiga scene is a slide show of
// under 5 frames a second, where the chunky one still gets about 19 of the
// 25, and the last frame, which the command's output waits for, is a
// quarter of the size.
//
// "Reached" is as far as berthlight can see: the write to the terminal has
// returned and, over SSH where the socket can be seen, the link has carried
// it (drain). Buffers it cannot see, such as the pty, take the first frames
// at once and hide a slow link until they fill, which is why the check
// keeps looking after the first second.
const (
	fallbackAfter  = time.Second
	fallbackFrames = 5
)

// Header is a header on screen. A nil *Header is valid and does nothing.
//
// Frames reach the terminal through a writer goroutine and a one-slot
// mailbox: the animation puts the scene time of the frame it wants in the
// slot, replacing one the writer has not taken yet, and the writer draws
// the latest. So at most one frame is ever on its way to the terminal, and
// a slow terminal connection drops frames rather than queueing them.
type Header struct {
	w     io.Writer
	mode  headerMode
	id    uint32 // Kitty image id
	start time.Time

	// The terminal's size when the header started; 0 if not known.
	rows, cols int

	// mu serializes writes to w (frames and Print) and guards below.
	mu    sync.Mutex
	below int // rows printed under the header by Print

	// Owned by the writer goroutine.
	hi     bool                      // drawing the Amiga scene, not the chunky one
	auto   bool                      // may fall back to the chunky scene on a slow link
	drawn  int                       // frames written
	recent [fallbackFrames]time.Time // when the last frames were written
	// backlog reports the bytes on their way to the terminal, over SSH
	// where that can be seen (link.go); nil elsewhere.
	backlog func() (int, bool)

	// box guards the mailbox: the scene time of the next frame to draw.
	box     sync.Mutex
	next    time.Duration
	pending bool
	last    bool // next is the last frame; later ones are ignored
	wake    chan struct{}

	stop chan struct{} // closed by Stop: the animation ends
	done chan struct{} // closed when the last frame is drawn
}

// StartHeader shows the header on w if w is a terminal it may draw on, and
// starts animating it where the terminal can show that. version goes in
// the one-line form.
func StartHeader(w io.Writer, version string) *Header {
	f, ok := fileOf(w)
	if !ok || !Interactive(f) {
		return nil
	}
	cols, rows, err := xterm.GetSize(int(f.Fd()))
	mode := modeText
	kind := DetectKind(os.Getenv)
	switch {
	case err != nil || cols < SceneW:
	case (kind == Kitty || kind == Ghostty) && os.Getenv("TMUX") == "" && rows > HeaderRows+2:
		mode = modeKitty
	case truecolor(kind):
		mode = modeHalfBlocks
	}
	switch mode {
	case modeText:
		fmt.Fprintln(f, textBanner(version))
	case modeHalfBlocks:
		fmt.Fprint(f, HalfBlocks(Frame(SceneLength)))
	case modeKitty:
		hi, auto := sceneChoice(os.Getenv("BERTHLIGHT_SCENE"))
		var backlog func() (int, bool)
		if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
			// Over SSH the Amiga scene needs a view of the link; without
			// one, the chunky scene, which needs a quarter of it.
			if backlog = sshBacklog(os.Getenv); backlog == nil && auto {
				hi = false
			}
		}
		// Make room, then animate in it.
		fmt.Fprint(f, strings.Repeat("\n", HeaderRows))
		return startKitty(f, rows, cols, hi, auto, backlog)
	}
	// Already on its last frame.
	h := &Header{w: f, mode: mode, last: true, done: make(chan struct{})}
	close(h.done)
	return h
}

// sceneChoice reads BERTHLIGHT_SCENE: whether to draw the Amiga scene, and
// whether it may fall back to the chunky one on a slow link. The Amiga
// scene is the default, also over SSH.
func sceneChoice(v string) (hi, auto bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "amiga":
		return true, false
	case "c64":
		return false, false
	}
	return true, true
}

// startKitty starts animating the scene in the HeaderRows rows above the
// cursor on w, a terminal of rows by cols cells (0 if not known): the
// Amiga one if hi, falling back to the chunky one on a slow link if auto.
// backlog, if not nil, reports the bytes on their way to the terminal.
func startKitty(w io.Writer, rows, cols int, hi, auto bool, backlog func() (int, bool)) *Header {
	h := &Header{
		w: w, mode: modeKitty, id: rand.Uint32N(1<<31-2) + 1, start: time.Now(), rows: rows, cols: cols,
		hi: hi, auto: hi && auto, backlog: backlog,
		wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
	}
	for i := range h.recent {
		h.recent[i] = h.start
	}
	h.post(0, false)
	go h.write()
	go h.animate()
	return h
}

func truecolor(k Kind) bool {
	switch os.Getenv("COLORTERM") {
	case "truecolor", "24bit":
		return true
	}
	return k != Unknown
}

func textBanner(version string) string {
	s := "⛵ berthlight " + version
	if os.Getenv("NO_COLOR") != "" {
		return s
	}
	return "\x1b[1;36m" + s + "\x1b[0m"
}

// post puts the frame at scene time t in the mailbox, replacing one not yet
// drawn. last marks the last frame: after it, posts are ignored. It never
// waits for the terminal. It reports whether the frame was taken.
func (h *Header) post(t time.Duration, last bool) bool {
	h.box.Lock()
	if h.last {
		h.box.Unlock()
		return false
	}
	h.next, h.pending, h.last = t, true, last
	h.box.Unlock()
	select {
	case h.wake <- struct{}{}:
	default: // the writer has been woken already
	}
	return true
}

// animate asks for a frame every frameInterval until SceneLength, or until
// Stop.
func (h *Header) animate() {
	t := time.NewTicker(frameInterval)
	defer t.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-t.C:
		}
		if elapsed := time.Since(h.start); elapsed < SceneLength {
			h.post(elapsed, false)
		} else {
			h.post(SceneLength, true)
			return
		}
	}
}

// write draws the frames from the mailbox, the latest each time, until it
// has drawn the last.
func (h *Header) write() {
	defer close(h.done)
	for range h.wake {
		h.box.Lock()
		t, ok, last := h.next, h.pending, h.last
		h.pending = false
		h.box.Unlock()
		if !ok {
			continue
		}
		img := Frame(t)
		if h.hi {
			img = FrameHi(t)
		}
		h.mu.Lock()
		h.draw(img)
		h.mu.Unlock()
		if last {
			return
		}
		h.drain()
		h.checkLink(time.Now())
	}
}

// Over SSH, the writer draws the next frame once at most linkSlack bytes
// are still on their way to the terminal: less than an Amiga frame, so the
// command's output never queues behind more than about two frames. It
// waits at most drainLimit for that, and not at all once the last frame is
// asked for, which then queues behind the backlog as the output would.
const (
	linkSlack  = 16 << 10
	drainLimit = time.Second
)

// drain waits for the link to carry what is on its way, as far as it can
// be seen.
func (h *Header) drain() {
	if h.backlog == nil {
		return
	}
	for deadline := time.Now().Add(drainLimit); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if n, ok := h.backlog(); !ok || n <= linkSlack {
			return
		}
		h.box.Lock()
		last := h.last
		h.box.Unlock()
		if last {
			return
		}
	}
}

// checkLink notes a frame written at now, and falls back to the chunky
// scene if fewer than fallbackFrames frames were written in the
// fallbackAfter up to now.
func (h *Header) checkLink(now time.Time) {
	h.drawn++
	h.recent[(h.drawn-1)%fallbackFrames] = now
	// The oldest of the last fallbackFrames frames, or the start.
	if oldest := h.recent[h.drawn%fallbackFrames]; h.hi && h.auto && now.Sub(oldest) >= fallbackAfter {
		h.hi = false
	}
}

// draw places img over the header's rows, leaving the cursor where it was.
// Each frame is one synchronized update, so it never shows half drawn.
// Callers hold mu.
func (h *Header) draw(img *image.RGBA) {
	up := HeaderRows + h.below
	fmt.Fprintf(h.w, "\x1b[?2026h\x1b7\x1b[%dA\r%s\x1b8\x1b[?2026l", up, KittyImage(img, h.id))
}

// Stop puts the header on its last frame and stops animating. It waits for
// the frame on its way to the terminal, if any, and the last frame, so at
// most two frames; and returns at once if the header is already still.
func (h *Header) Stop() {
	if h == nil {
		return
	}
	if h.post(SceneLength, true) {
		close(h.stop)
	}
	<-h.done
}

// Play lets the scene run to its end, unless ctx ends first, and then
// stops it on its last frame. For commands whose output is the show.
func (h *Header) Play(ctx context.Context) {
	if h == nil {
		return
	}
	select {
	case <-h.done:
	case <-ctx.Done():
	}
	h.Stop()
}

// Print writes text under the header; the header keeps animating above
// it. If the text would push the header's top row off the screen, the
// header stops on its last frame first: a frame drawn after that would
// land on the text, since the cursor cannot move above the screen.
func (h *Header) Print(text string) {
	if h == nil {
		return
	}
	n := textRows(text, h.cols)
	h.mu.Lock()
	// The cursor ends on the bottom row at worst, so the header's top row
	// stays on screen while the rows above the cursor hold it all.
	fits := h.rows == 0 || HeaderRows+h.below+n < h.rows
	h.mu.Unlock()
	if !fits {
		h.Stop()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	io.WriteString(h.w, text)
	h.below += n
}

// textRows is the number of rows text moves the cursor down on a terminal
// cols wide (0: lines never wrap). It counts runes, so wide characters and
// escape sequences are not measured.
func textRows(text string, cols int) int {
	lines := strings.Split(text, "\n")
	n := len(lines) - 1
	for _, l := range lines {
		if w := utf8.RuneCountInString(l); cols > 0 && w > cols {
			n += (w - 1) / cols
		}
	}
	return n
}

// Wrap returns a writer that stops the header before its first write, so a
// command's own output never competes with the animation.
func (h *Header) Wrap(w io.Writer) io.Writer {
	if h == nil {
		return w
	}
	return &stopWriter{h: h, w: w}
}

type stopWriter struct {
	h *Header
	w io.Writer
}

func (s *stopWriter) Write(p []byte) (int, error) {
	s.h.Stop()
	return s.w.Write(p)
}

// TerminalFile lets NewTerm and the boat see the terminal behind the
// wrapper.
func (s *stopWriter) TerminalFile() (*os.File, bool) { return fileOf(s.w) }

// fileOf finds the *os.File behind w, through writers that expose it.
func fileOf(w io.Writer) (*os.File, bool) {
	switch v := w.(type) {
	case *os.File:
		return v, true
	case interface{ TerminalFile() (*os.File, bool) }:
		return v.TerminalFile()
	}
	return nil, false
}

// HalfBlocks renders img in upper half block characters, two pixels per
// cell, in 24-bit colour: the top pixel as foreground, the bottom as
// background.
func HalfBlocks(img *image.RGBA) string {
	var b strings.Builder
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 2 {
		var lastFG, lastBG [3]uint8
		first := true
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			top := img.RGBAAt(x, y)
			bot := top
			if y+1 < bounds.Max.Y {
				bot = img.RGBAAt(x, y+1)
			}
			fg, bg := [3]uint8{top.R, top.G, top.B}, [3]uint8{bot.R, bot.G, bot.B}
			if first || fg != lastFG {
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm", fg[0], fg[1], fg[2])
			}
			if first || bg != lastBG {
				fmt.Fprintf(&b, "\x1b[48;2;%d;%d;%dm", bg[0], bg[1], bg[2])
			}
			lastFG, lastBG, first = fg, bg, false
			b.WriteString("▀")
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String()
}

// kittyWidth is the width, in pixels, frames are sent at. The scene is
// enlarged to it pixel for pixel (×8 for the chunky scene, ×2 for the
// Amiga one), so terminals scale it up crisply rather than blurring it.
const kittyWidth = SceneW * 8

// KittyImage returns the Kitty graphics commands that show img over
// SceneW×HeaderRows cells at the cursor: transmit-and-display (a=T) of a
// PNG (f=100) with a fixed image and placement id, so each frame replaces
// the last; the cursor does not move (C=1); no replies (q=2), which would
// otherwise arrive as input; in chunks of at most 4096 bytes.
// https://sw.kovidgoyal.net/kitty/graphics-protocol/
func KittyImage(img *image.RGBA, id uint32) string {
	scale := kittyWidth / img.Bounds().Dx()
	w, h := img.Bounds().Dx()*scale, img.Bounds().Dy()*scale
	big := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			big.SetRGBA(x, y, img.RGBAAt(x/scale, y/scale))
		}
	}
	var buf bytes.Buffer
	(&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, big)
	data := base64.StdEncoding.EncodeToString(buf.Bytes())

	var b strings.Builder
	for i := 0; i < len(data); i += 4096 {
		chunk := data[i:min(i+4096, len(data))]
		more := 0
		if i+4096 < len(data) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(&b, "\x1b_Ga=T,f=100,i=%d,p=1,c=%d,r=%d,C=1,q=2,m=%d;%s\x1b\\",
				id, SceneW, HeaderRows, more, chunk)
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
	return b.String()
}
