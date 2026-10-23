// Package tui draws berthlight's progress in a terminal: a sailboat that
// crosses the water to its berth while the proxy or a route comes up.
//
// It only draws on an interactive terminal, on standard error, so standard
// output stays the same for scripts and agents. Without a terminal, with
// TERM=dumb, in CI or with BERTHLIGHT_NO_ANIMATION set, every method does
// nothing. NO_COLOR turns off colour but keeps the boat.
package tui

import (
	"fmt"
	"io"
	"os"
	goruntime "runtime"
	"strings"
	"sync"
	"time"
)

const (
	// water is the number of cells the boat crosses.
	water = 24
	// delay before the first frame: quick operations draw nothing.
	delay = 150 * time.Millisecond
	// tick is the time between frames while sailing.
	tick = 80 * time.Millisecond
	// glideTick is the time per cell on the last stretch to the berth,
	// sailed when the work is done so the eye can follow it: at most about
	// two thirds of a second.
	glideTick = 30 * time.Millisecond
)

// style is how a terminal can draw the boat.
type style struct {
	boat      string // the boat
	boatWidth int    // its width in cells
	waves     []string
	calm      string
	dock      string
	anchor    string
	color     bool
}

var (
	unicodeStyle = style{
		boat: "⛵", boatWidth: 2,
		waves: []string{"▁", "▁", "▂", "▃", "▂", "▁", "▁", "▁"},
		calm:  "▁", dock: "┃", anchor: "⚓",
	}
	asciiStyle = style{
		boat: `\_/`, boatWidth: 3,
		waves: []string{"_", "_", "~", "~", "_", "_", "_", "_"},
		calm:  "_", dock: "|", anchor: "#",
	}
)

const (
	blue  = "\x1b[34m"
	white = "\x1b[1;97m"
	grey  = "\x1b[90m"
	green = "\x1b[32m"
	red   = "\x1b[31m"
	reset = "\x1b[0m"
)

// state is where the boat is in its journey.
type state int

const (
	sailing state = iota
	berthed
	stranded
)

// last is the boat's position, in cells, at the berth.
func (s style) last() int { return water - s.boatWidth }

// render draws one frame: the water, the boat pos cells from the start,
// the dock, and msg.
func (s style) render(frame, pos int, st state, msg string) string {
	pos = min(max(pos, 0), s.last())
	if st == berthed {
		pos = s.last()
	}
	c := func(code, text string) string {
		if !s.color || text == "" {
			return text
		}
		return code + text + reset
	}
	var b strings.Builder
	b.WriteString("  ")
	var wave strings.Builder
	for i := 0; i < pos; i++ {
		if st == berthed {
			wave.WriteString(s.calm)
		} else {
			wave.WriteString(s.waves[(i+frame)%len(s.waves)])
		}
	}
	b.WriteString(c(blue, wave.String()))
	b.WriteString(c(white, s.boat))
	wave.Reset()
	for i := pos + s.boatWidth; i < water; i++ {
		wave.WriteString(s.waves[(i+frame)%len(s.waves)])
	}
	b.WriteString(c(blue, wave.String()))
	switch st {
	case sailing:
		b.WriteString(c(grey, s.dock))
	case berthed:
		b.WriteString(c(grey, s.anchor))
	case stranded:
		b.WriteString(c(red, s.dock))
	}
	if msg != "" {
		b.WriteString("  ")
		switch st {
		case berthed:
			b.WriteString(c(green, msg))
		case stranded:
			b.WriteString(c(red, msg))
		default:
			b.WriteString(msg)
		}
	}
	return b.String()
}

// Progress is a boat on its way. A nil *Progress is valid and draws
// nothing, so callers need not check whether the terminal is interactive.
type Progress struct {
	w     io.Writer
	style style
	start time.Time

	mu    sync.Mutex
	frac  float64 // where the boat is heading, 0 to 1
	pos   float64 // where it is drawn, in cells
	msg   string
	frame int
	drawn bool

	stop chan struct{}
	done chan struct{}
}

// Start sets sail with msg if w is an interactive terminal, and returns
// nil otherwise.
func Start(w io.Writer, msg string) *Progress {
	f, ok := w.(*os.File)
	if !ok || !Interactive(f) {
		return nil
	}
	return start(w, detectStyle(), msg)
}

func start(w io.Writer, s style, msg string) *Progress {
	p := &Progress{w: w, style: s, start: time.Now(), msg: msg,
		stop: make(chan struct{}), done: make(chan struct{})}
	go p.run()
	return p
}

// Interactive reports whether f is a terminal the boat may be drawn on.
func Interactive(f *os.File) bool {
	if os.Getenv("TERM") == "dumb" || os.Getenv("CI") != "" || os.Getenv("BERTHLIGHT_NO_ANIMATION") != "" {
		return false
	}
	// The classic Windows console does not understand escape sequences;
	// Windows Terminal and terminals that set TERM_PROGRAM do.
	if goruntime.GOOS == "windows" && os.Getenv("WT_SESSION") == "" && os.Getenv("TERM_PROGRAM") == "" {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func detectStyle() style {
	s := unicodeStyle
	if os.Getenv("TERM") == "linux" { // the Linux console has no emoji
		s = asciiStyle
	}
	s.color = os.Getenv("NO_COLOR") == ""
	return s
}

// Step moves the boat to frac (0 to 1) of the crossing and sets the message.
func (p *Progress) Step(frac float64, msg string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.frac = max(p.frac, frac)
	if msg != "" {
		p.msg = msg
	}
	p.mu.Unlock()
}

// Done berths the boat and leaves the last frame, with msg, on screen. If
// nothing was drawn yet, it draws nothing: a quick operation needs no boat.
func (p *Progress) Done(msg string) { p.finish(berthed, msg) }

// Fail leaves the boat where it is, with msg in red.
func (p *Progress) Fail(msg string) { p.finish(stranded, msg) }

func (p *Progress) finish(st state, msg string) {
	if p == nil {
		return
	}
	took := time.Since(p.start)
	close(p.stop)
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.drawn {
		return
	}
	if st == berthed {
		// Sail the rest of the way where the eye can follow.
		for int(p.pos) < p.style.last() {
			p.pos = float64(int(p.pos) + 1)
			p.frame++
			p.draw(p.style.render(p.frame, int(p.pos), sailing, p.msg))
			time.Sleep(glideTick)
		}
		if msg != "" {
			msg = fmt.Sprintf("%s in %.1fs", msg, took.Seconds())
		}
	}
	p.draw(p.style.render(p.frame, int(p.pos), st, msg))
	fmt.Fprint(p.w, "\n\x1b[?25h") // new line, show the cursor
}

func (p *Progress) run() {
	defer close(p.done)
	select {
	case <-p.stop:
		return
	case <-time.After(delay):
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		p.mu.Lock()
		if !p.drawn {
			fmt.Fprint(p.w, "\x1b[?25l") // hide the cursor
			p.drawn = true
		}
		p.frame++
		p.advance()
		p.draw(p.style.render(p.frame, int(p.pos), sailing, p.msg))
		p.mu.Unlock()
		select {
		case <-p.stop:
			return
		case <-t.C:
		}
	}
}

// advance moves the boat part of the way to where it is heading, so it
// glides rather than jumps.
func (p *Progress) advance() {
	target := p.frac * float64(p.style.last())
	if p.pos < target {
		p.pos = min(p.pos+max((target-p.pos)*0.3, 0.5), target)
	}
}

// draw replaces the current line with line, as one synchronized update
// (mode 2026) so terminals that support it never show half a frame.
func (p *Progress) draw(line string) {
	fmt.Fprint(p.w, "\x1b[?2026h\r\x1b[2K"+line+"\x1b[?2026l")
}
