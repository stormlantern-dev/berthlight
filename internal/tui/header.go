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

	xterm "golang.org/x/term"
)

// The header (PRD 0001, R12) shows the scene at the top of a command's
// output on a terminal:
//   - animated in terminals with the Kitty graphics protocol (Kitty,
//     Ghostty), in its Amiga version (the chunky one over SSH), for as
//     long as the command is working and at most
//     SceneLength; the first output, or the end of the command, puts it on
//     its last frame, so it never holds a command up;
//   - as the last frame in half-block characters on other truecolor
//     terminals;
//   - as one line of text elsewhere.
//
// Nothing at all without a terminal, in CI, with TERM=dumb or with
// BERTHLIGHT_NO_ANIMATION.

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

// Header is a header on screen. A nil *Header is valid and does nothing.
type Header struct {
	w     io.Writer
	mode  headerMode
	id    uint32 // Kitty image id
	start time.Time
	// frame draws the scene at a time: the Amiga scene, or the chunky one
	// over SSH, where each frame costs bandwidth.
	frame func(time.Duration) *image.RGBA

	mu      sync.Mutex
	below   int // rows printed under the header by Print
	stopped bool
	stop    chan struct{}
	done    chan struct{}
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
	h := &Header{w: f, start: time.Now(), stop: make(chan struct{}), done: make(chan struct{})}
	kind := DetectKind(os.Getenv)
	switch {
	case err != nil || cols < SceneW:
		h.mode = modeText
	case (kind == Kitty || kind == Ghostty) && os.Getenv("TMUX") == "" && rows > HeaderRows+2:
		h.mode = modeKitty
	case truecolor(kind):
		h.mode = modeHalfBlocks
	default:
		h.mode = modeText
	}
	switch h.mode {
	case modeText:
		fmt.Fprintln(f, textBanner(version))
		h.stopped = true
		close(h.done)
	case modeHalfBlocks:
		fmt.Fprint(f, HalfBlocks(Frame(SceneLength)))
		h.stopped = true
		close(h.done)
	case modeKitty:
		h.id = rand.Uint32N(1<<31-2) + 1
		h.frame = FrameHi
		if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
			h.frame = Frame
		}
		// Make room, then draw the first frame into it.
		fmt.Fprint(f, strings.Repeat("\n", HeaderRows))
		h.mu.Lock()
		h.draw(h.frame(0))
		h.mu.Unlock()
		go h.animate()
	}
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

func (h *Header) animate() {
	defer close(h.done)
	t := time.NewTicker(frameInterval)
	defer t.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-t.C:
		}
		elapsed := time.Since(h.start)
		h.mu.Lock()
		if !h.stopped {
			h.draw(h.frame(elapsed))
		}
		h.mu.Unlock()
		if elapsed >= SceneLength {
			return
		}
	}
}

// draw places img over the header's rows, leaving the cursor where it was.
// Each frame is one synchronized update, so it never shows half drawn.
// Callers hold mu.
func (h *Header) draw(img *image.RGBA) {
	up := HeaderRows + h.below
	fmt.Fprintf(h.w, "\x1b[?2026h\x1b7\x1b[%dA\r%s\x1b8\x1b[?2026l", up, KittyImage(img, h.id))
}

// Stop puts the header on its last frame and stops animating. It returns
// at once if the header is already still.
func (h *Header) Stop() {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	h.stopped = true
	h.draw(h.frame(SceneLength))
	h.mu.Unlock()
	close(h.stop)
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

// Print writes text under the header without stopping it; the header keeps
// animating above it. The text's lines must fit the terminal's width.
func (h *Header) Print(text string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	io.WriteString(h.w, text)
	h.below += strings.Count(text, "\n")
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
