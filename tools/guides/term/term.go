// Package term is a small terminal emulator for rendering recordings of
// berthlight: enough of xterm for its output (text, colours, cursor
// movement, erasing, scrolling), the window title, desktop notifications,
// and the Kitty graphics protocol's transmit-and-display of images.
package term

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Cell is one character on the screen.
type Cell struct {
	R      rune
	FG, BG color.RGBA
	Bold   bool
	Link   bool
}

// Image is a placed Kitty image: its top left cell, size in cells, pixels.
type Image struct {
	X, Y, Cols, Rows int
	Img              image.Image
}

// Notification is a desktop notification the program sent.
type Notification struct{ Text string }

// Theme colours.
var (
	DefaultFG = color.RGBA{0xdc, 0xdf, 0xe4, 0xff}
	DefaultBG = color.RGBA{0x1e, 0x1f, 0x29, 0xff}
	// The 16 ANSI colours.
	ANSI = [16]color.RGBA{
		{0x1e, 0x1f, 0x29, 0xff}, {0xff, 0x55, 0x55, 0xff}, {0x50, 0xfa, 0x7b, 0xff}, {0xf1, 0xfa, 0x8c, 0xff},
		{0x6c, 0x9e, 0xf8, 0xff}, {0xff, 0x79, 0xc6, 0xff}, {0x8b, 0xe9, 0xfd, 0xff}, {0xdc, 0xdf, 0xe4, 0xff},
		{0x62, 0x72, 0xa4, 0xff}, {0xff, 0x6e, 0x6e, 0xff}, {0x69, 0xff, 0x94, 0xff}, {0xff, 0xff, 0xa5, 0xff},
		{0x9a, 0xbe, 0xff, 0xff}, {0xff, 0x92, 0xdf, 0xff}, {0xa4, 0xff, 0xff, 0xff}, {0xff, 0xff, 0xff, 0xff},
	}
)

// Term is the screen state.
type Term struct {
	Cols, Rows int
	Cells      [][]Cell
	Images     map[string]*Image // by "image id/placement id"
	Title      string
	// Notified receives each desktop notification as it is sent.
	Notified func(Notification)

	x, y         int
	sx, sy       int // saved cursor
	fg, bg       color.RGBA
	bold, link   bool
	titles       []string
	pending      []byte // an incomplete escape sequence or UTF-8 rune
	kittyData    strings.Builder
	kittyKeys    map[string]string
	kittyPending bool
}

// New returns a blank screen.
func New(cols, rows int) *Term {
	t := &Term{Cols: cols, Rows: rows, Images: map[string]*Image{}, fg: DefaultFG, bg: DefaultBG}
	t.Cells = make([][]Cell, rows)
	for i := range t.Cells {
		t.Cells[i] = t.blankRow()
	}
	return t
}

func (t *Term) blankRow() []Cell {
	row := make([]Cell, t.Cols)
	for i := range row {
		row[i] = Cell{R: ' ', FG: DefaultFG, BG: DefaultBG}
	}
	return row
}

// Write feeds output to the screen.
func (t *Term) Write(p []byte) (int, error) {
	data := append(t.pending, p...)
	t.pending = nil
	i := 0
	for i < len(data) {
		b := data[i]
		switch {
		case b == 0x1b:
			n, ok := t.escape(data[i:])
			if !ok {
				t.pending = append([]byte{}, data[i:]...)
				return len(p), nil
			}
			i += n
		case b == '\r':
			t.x = 0
			i++
		case b == '\n':
			t.lineFeed()
			i++
		case b == '\b':
			t.x = max(0, t.x-1)
			i++
		case b == '\t':
			t.x = min(t.Cols-1, (t.x/8+1)*8)
			i++
		case b < 0x20 || b == 0x7f:
			i++
		default:
			if !utf8.FullRune(data[i:]) {
				t.pending = append([]byte{}, data[i:]...)
				return len(p), nil
			}
			r, n := utf8.DecodeRune(data[i:])
			t.put(r)
			i += n
		}
	}
	return len(p), nil
}

func (t *Term) put(r rune) {
	if t.x >= t.Cols {
		t.x = 0
		t.lineFeed()
	}
	t.Cells[t.y][t.x] = Cell{R: r, FG: t.fg, BG: t.bg, Bold: t.bold, Link: t.link}
	t.x++
}

func (t *Term) lineFeed() {
	if t.y < t.Rows-1 {
		t.y++
		return
	}
	t.Cells = append(t.Cells[1:], t.blankRow())
	for k, im := range t.Images {
		im.Y--
		if im.Y+im.Rows <= 0 {
			delete(t.Images, k)
		}
	}
}

// escape handles the escape sequence at the start of b and returns its
// length, or false if it is not complete yet.
func (t *Term) escape(b []byte) (int, bool) {
	if len(b) < 2 {
		return 0, false
	}
	switch b[1] {
	case '[':
		for i := 2; i < len(b); i++ {
			if b[i] >= 0x40 && b[i] <= 0x7e {
				t.csi(string(b[2:i]), b[i])
				return i + 1, true
			}
		}
		return 0, false
	case ']', '_':
		// OSC or APC, ended by BEL or ST (ESC \).
		for i := 2; i < len(b); i++ {
			if b[i] == 0x07 {
				t.string(b[1], string(b[2:i]))
				return i + 1, true
			}
			if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
				t.string(b[1], string(b[2:i]))
				return i + 2, true
			}
		}
		return 0, false
	case '7':
		t.sx, t.sy = t.x, t.y
		return 2, true
	case '8':
		t.x, t.y = t.sx, t.sy
		return 2, true
	}
	return 2, true
}

func params(s string, def int) []int {
	s = strings.TrimLeft(s, "?>")
	if s == "" {
		return []int{def}
	}
	var out []int
	for _, f := range strings.Split(s, ";") {
		n, err := strconv.Atoi(f)
		if err != nil {
			n = def
		}
		out = append(out, n)
	}
	return out
}

func (t *Term) csi(p string, final byte) {
	ps := params(p, 1)
	n := max(ps[0], 1)
	switch final {
	case 'A':
		t.y = max(0, t.y-n)
	case 'B':
		t.y = min(t.Rows-1, t.y+n)
	case 'C':
		t.x = min(t.Cols-1, t.x+n)
	case 'D':
		t.x = max(0, t.x-n)
	case 'E':
		t.y, t.x = min(t.Rows-1, t.y+n), 0
	case 'F':
		t.y, t.x = max(0, t.y-n), 0
	case 'G':
		t.x = min(t.Cols-1, n-1)
	case 'H', 'f':
		row, col := 1, 1
		if len(ps) > 0 && ps[0] > 0 {
			row = ps[0]
		}
		if len(ps) > 1 && ps[1] > 0 {
			col = ps[1]
		}
		t.y, t.x = min(t.Rows-1, row-1), min(t.Cols-1, col-1)
	case 'J':
		mode := params(p, 0)[0]
		if mode == 0 {
			t.clearLine(t.y, t.x, t.Cols)
			for y := t.y + 1; y < t.Rows; y++ {
				t.clearLine(y, 0, t.Cols)
			}
		} else if mode == 2 {
			for y := 0; y < t.Rows; y++ {
				t.clearLine(y, 0, t.Cols)
			}
		}
	case 'K':
		switch params(p, 0)[0] {
		case 0:
			t.clearLine(t.y, t.x, t.Cols)
		case 1:
			t.clearLine(t.y, 0, t.x+1)
		case 2:
			t.clearLine(t.y, 0, t.Cols)
		}
	case 'm':
		t.sgr(params(p, 0))
	case 't':
		switch ps[0] {
		case 22:
			t.titles = append(t.titles, t.Title)
		case 23:
			if len(t.titles) > 0 {
				t.Title = t.titles[len(t.titles)-1]
				t.titles = t.titles[:len(t.titles)-1]
			}
		}
	}
}

func (t *Term) clearLine(y, from, to int) {
	for x := max(0, from); x < min(to, t.Cols); x++ {
		t.Cells[y][x] = Cell{R: ' ', FG: DefaultFG, BG: DefaultBG}
	}
}

func (t *Term) sgr(ps []int) {
	for i := 0; i < len(ps); i++ {
		switch p := ps[i]; {
		case p == 0:
			t.fg, t.bg, t.bold = DefaultFG, DefaultBG, false
		case p == 1:
			t.bold = true
		case p == 22:
			t.bold = false
		case p >= 30 && p <= 37:
			t.fg = ANSI[p-30]
		case p >= 90 && p <= 97:
			t.fg = ANSI[p-90+8]
		case p == 39:
			t.fg = DefaultFG
		case p >= 40 && p <= 47:
			t.bg = ANSI[p-40]
		case p >= 100 && p <= 107:
			t.bg = ANSI[p-100+8]
		case p == 49:
			t.bg = DefaultBG
		case (p == 38 || p == 48) && i+4 < len(ps) && ps[i+1] == 2:
			c := color.RGBA{uint8(ps[i+2]), uint8(ps[i+3]), uint8(ps[i+4]), 0xff}
			if p == 38 {
				t.fg = c
			} else {
				t.bg = c
			}
			i += 4
		case (p == 38 || p == 48) && i+2 < len(ps) && ps[i+1] == 5:
			c := xterm256(ps[i+2])
			if p == 38 {
				t.fg = c
			} else {
				t.bg = c
			}
			i += 2
		}
	}
}

func xterm256(n int) color.RGBA {
	switch {
	case n < 16:
		return ANSI[n]
	case n < 232:
		n -= 16
		l := []uint8{0, 95, 135, 175, 215, 255}
		return color.RGBA{l[n/36], l[n/6%6], l[n%6], 0xff}
	}
	v := uint8(8 + (n-232)*10)
	return color.RGBA{v, v, v, 0xff}
}

// string handles an OSC (kind ']') or APC (kind '_') string.
func (t *Term) string(kind byte, s string) {
	if kind == '_' {
		if strings.HasPrefix(s, "G") {
			t.kitty(s[1:])
		}
		return
	}
	code, rest, _ := strings.Cut(s, ";")
	switch code {
	case "0", "2":
		t.Title = rest
	case "8":
		_, uri, _ := strings.Cut(rest, ";")
		t.link = uri != ""
	case "9":
		if !strings.HasPrefix(rest, "4;") && t.Notified != nil {
			t.Notified(Notification{Text: rest})
		}
	case "99":
		meta, text, _ := strings.Cut(rest, ";")
		if strings.Contains(meta, "p=body") && t.Notified != nil {
			t.Notified(Notification{Text: text})
		}
	}
}

// kitty handles a Kitty graphics command: transmit (and display), in
// chunks, PNG or zlib-compressed RGBA.
func (t *Term) kitty(s string) {
	ctrl, payload, _ := strings.Cut(s, ";")
	keys := map[string]string{}
	for _, kv := range strings.Split(ctrl, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			keys[k] = v
		}
	}
	if !t.kittyPending {
		t.kittyKeys = keys
		t.kittyData.Reset()
	}
	t.kittyData.WriteString(payload)
	if keys["m"] == "1" {
		t.kittyPending = true
		return
	}
	t.kittyPending = false
	k := t.kittyKeys
	if k["a"] != "T" {
		return
	}
	raw, err := base64.StdEncoding.DecodeString(t.kittyData.String())
	if err != nil {
		return
	}
	var img image.Image
	switch k["f"] {
	case "100":
		img, err = png.Decode(bytes.NewReader(raw))
	default:
		if k["o"] == "z" {
			zr, zerr := zlib.NewReader(bytes.NewReader(raw))
			if zerr != nil {
				return
			}
			raw, err = io.ReadAll(zr)
		}
		w, _ := strconv.Atoi(k["s"])
		h, _ := strconv.Atoi(k["v"])
		if err == nil && len(raw) >= w*h*4 {
			rgba := image.NewRGBA(image.Rect(0, 0, w, h))
			copy(rgba.Pix, raw)
			img = rgba
		}
	}
	if err != nil || img == nil {
		return
	}
	cols, _ := strconv.Atoi(k["c"])
	rows, _ := strconv.Atoi(k["r"])
	t.Images[k["i"]+"/"+k["p"]] = &Image{X: t.x, Y: t.y, Cols: cols, Rows: rows, Img: img}
	if k["C"] != "1" {
		t.y = min(t.Rows-1, t.y+rows)
	}
}
