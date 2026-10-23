// Command gif renders a terminal recording (asciicast v2) to an animated
// GIF: a window with the tab title, the screen in Go Mono, Kitty graphics
// images where the program placed them, and desktop notifications as
// cards in the corner, as the terminal and desktop would show them.
//
//	go run ./cmd/gif -in brag.cast -out brag.gif
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"log"
	"math"
	"os"
	"sort"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"stormlantern.nl/berthlight/tools/guides/cast"
	"stormlantern.nl/berthlight/tools/guides/term"
)

const (
	cellW, cellH = 9, 18 // pixels per character cell
	pad          = 14    // inside the window, around the screen
	titleH       = 30    // the title bar
	margin       = 18    // around the window
)

var (
	backdrop = color.RGBA{0x10, 0x10, 0x18, 0xff}
	titleBG  = color.RGBA{0x2a, 0x2b, 0x37, 0xff}
	titleFG  = color.RGBA{0xb4, 0xb8, 0xc8, 0xff}
	cardBG   = color.RGBA{0x32, 0x34, 0x44, 0xff}
	cardEdge = color.RGBA{0x50, 0x53, 0x6a, 0xff}
)

type note struct {
	text string
	at   float64
}

func main() {
	in := flag.String("in", "", "recording (asciicast v2)")
	out := flag.String("out", "", "GIF to write")
	fps := flag.Float64("fps", 12, "frames per second")
	hold := flag.Float64("hold", 3, "seconds to hold the last frame")
	flag.Parse()
	if *in == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	f, err := os.Open(*in)
	if err != nil {
		log.Fatal(err)
	}
	h, events, err := cast.Read(f)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}

	regular, bold := face(gomono.TTF), face(gomonobold.TTF)
	t := term.New(h.Width, h.Height)
	var notes []note
	now := 0.0
	t.Notified = func(n term.Notification) { notes = append(notes, note{n.Text, now}) }

	W := margin*2 + pad*2 + h.Width*cellW
	H := margin*2 + titleH + pad*2 + h.Height*cellH
	end := events[len(events)-1].Time + *hold
	var frames []*image.RGBA
	var delays []int
	step := 1 / *fps
	next := 0
	for now = 0; now <= end; now += step {
		for next < len(events) && events[next].Time <= now {
			t.Write([]byte(events[next].Data))
			next++
		}
		frames = append(frames, render(t, W, H, regular, bold, notes, now))
		delays = append(delays, int(math.Round(step*100)))
	}
	if err := writeGIF(*out, frames, delays); err != nil {
		log.Fatal(err)
	}
	st, _ := os.Stat(*out)
	fmt.Printf("%s: %d frames, %dx%d, %.1f MB\n", *out, len(frames), W, H, float64(st.Size())/1e6)
}

func face(ttf []byte) font.Face {
	ft, err := opentype.Parse(ttf)
	if err != nil {
		log.Fatal(err)
	}
	fc, err := opentype.NewFace(ft, &opentype.FaceOptions{Size: 14.5, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		log.Fatal(err)
	}
	return fc
}

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	draw.Draw(img, r, &image.Uniform{c}, image.Point{}, draw.Src)
}

// roundRect fills a rectangle with rounded corners.
func roundRect(img *image.RGBA, r image.Rectangle, rad int, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			dx := max(r.Min.X+rad-x, x-(r.Max.X-1-rad), 0)
			dy := max(r.Min.Y+rad-y, y-(r.Max.Y-1-rad), 0)
			if dx*dx+dy*dy <= rad*rad {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func text(img *image.RGBA, fc font.Face, x, y int, s string, c color.RGBA) {
	d := font.Drawer{Dst: img, Src: &image.Uniform{c}, Face: fc, Dot: fixed.P(x, y)}
	d.DrawString(s)
}

func render(t *term.Term, W, H int, regular, bold font.Face, notes []note, now float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	fill(img, img.Bounds(), backdrop)
	win := image.Rect(margin, margin, W-margin, H-margin)
	roundRect(img, win, 10, term.DefaultBG)
	roundRect(img, image.Rect(win.Min.X, win.Min.Y, win.Max.X, win.Min.Y+titleH+10), 10, titleBG)
	fill(img, image.Rect(win.Min.X, win.Min.Y+titleH, win.Max.X, win.Min.Y+titleH+10), term.DefaultBG)
	for i, c := range []color.RGBA{{0xff, 0x5f, 0x57, 0xff}, {0xfe, 0xbc, 0x2e, 0xff}, {0x28, 0xc8, 0x40, 0xff}} {
		cx, cy := win.Min.X+18+i*20, win.Min.Y+titleH/2
		for y := -6; y <= 6; y++ {
			for x := -6; x <= 6; x++ {
				if x*x+y*y <= 30 {
					img.SetRGBA(cx+x, cy+y, c)
				}
			}
		}
	}
	title := t.Title
	if title == "" {
		title = "~/projects"
	}
	tw := font.MeasureString(regular, title).Round()
	text(img, regular, win.Min.X+(win.Dx()-tw)/2, win.Min.Y+titleH/2+5, title, titleFG)

	ox, oy := win.Min.X+pad, win.Min.Y+titleH+pad
	ascent := regular.Metrics().Ascent.Round()
	for y, row := range t.Cells {
		for x, c := range row {
			r := image.Rect(ox+x*cellW, oy+y*cellH, ox+(x+1)*cellW, oy+(y+1)*cellH)
			if c.BG != term.DefaultBG {
				fill(img, r, c.BG)
			}
			if c.R != ' ' && c.R != 0 {
				fc := regular
				if c.Bold {
					fc = bold
				}
				text(img, fc, r.Min.X, r.Min.Y+ascent+1, string(c.R), c.FG)
			}
		}
	}
	// Images, scaled to their cells as the terminal does, nearest
	// neighbour, so the pixel art stays sharp.
	keys := make([]string, 0, len(t.Images))
	for k := range t.Images {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		im := t.Images[k]
		dst := image.Rect(ox+im.X*cellW, oy+im.Y*cellH, ox+(im.X+im.Cols)*cellW, oy+(im.Y+im.Rows)*cellH)
		xdraw.NearestNeighbor.Scale(img, dst, im.Img, im.Img.Bounds(), xdraw.Src, nil)
	}
	// Desktop notifications: cards in the top right corner, the newest on
	// top, each shown for three seconds and sliding in.
	shown := 0
	for i := len(notes) - 1; i >= 0 && shown < 3; i-- {
		n := notes[i]
		age := now - n.at
		if age < 0 || age > 3 {
			continue
		}
		slide := 1.0
		if age < 0.25 {
			slide = age / 0.25
		}
		cw, ch := 330, 44
		x := W - margin - 12 - int(float64(cw)*slide)
		y := win.Min.Y + titleH + 10 + shown*(ch+8)
		roundRect(img, image.Rect(x-1, y-1, x+cw+1, y+ch+1), 8, cardEdge)
		roundRect(img, image.Rect(x, y, x+cw, y+ch), 8, cardBG)
		head, body := "berthlight", n.text
		if len(body) > 12 && body[:12] == "berthlight: " {
			body = body[12:]
		}
		if len([]rune(body)) > 40 {
			body = string([]rune(body)[:39]) + "…"
		}
		text(img, bold, x+12, y+18, head, color.RGBA{0xff, 0xff, 0xff, 0xff})
		text(img, regular, x+12, y+36, body, titleFG)
		shown++
	}
	return img
}

// writeGIF writes the frames with one palette, built from the colours
// they use most, and each frame after the first as the rectangle that
// changed.
func writeGIF(path string, frames []*image.RGBA, delays []int) error {
	counts := map[color.RGBA]int{}
	for i, f := range frames {
		if i%3 != 0 && i != len(frames)-1 {
			continue
		}
		for p := 0; p < len(f.Pix); p += 4 * 3 {
			counts[color.RGBA{f.Pix[p], f.Pix[p+1], f.Pix[p+2], 0xff}]++
		}
	}
	type cc struct {
		c color.RGBA
		n int
	}
	var all []cc
	for c, n := range counts {
		all = append(all, cc{c, n})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].n > all[j].n })
	// Colours that must come out exactly: the window's and the theme's.
	pal := color.Palette{backdrop, titleBG, titleFG, cardBG, cardEdge, term.DefaultBG, term.DefaultFG,
		color.RGBA{0xff, 0x5f, 0x57, 0xff}, color.RGBA{0xfe, 0xbc, 0x2e, 0xff}, color.RGBA{0x28, 0xc8, 0x40, 0xff}, color.RGBA{0xff, 0xff, 0xff, 0xff}}
	for _, c := range term.ANSI {
		pal = append(pal, c)
	}
	seen := map[color.RGBA]bool{}
	for _, c := range pal {
		seen[c.(color.RGBA)] = true
	}
	for _, e := range all {
		if len(pal) == 256 {
			break
		}
		if !seen[e.c] {
			pal = append(pal, e.c)
		}
	}
	cache := map[color.RGBA]uint8{}
	index := func(c color.RGBA) uint8 {
		if i, ok := cache[c]; ok {
			return i
		}
		i := uint8(pal.Index(c))
		cache[c] = i
		return i
	}

	out := &gif.GIF{}
	var prev *image.RGBA
	for i, f := range frames {
		r := f.Bounds()
		if prev != nil {
			r = changed(prev, f)
			if r.Empty() { // nothing changed: lengthen the last frame
				out.Delay[len(out.Delay)-1] += delays[i]
				continue
			}
		}
		pm := image.NewPaletted(r, pal)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				pm.SetColorIndex(x, y, index(f.RGBAAt(x, y)))
			}
		}
		out.Image = append(out.Image, pm)
		out.Delay = append(out.Delay, delays[i])
		out.Disposal = append(out.Disposal, gif.DisposalNone)
		prev = f
	}
	out.Config = image.Config{ColorModel: pal, Width: frames[0].Bounds().Dx(), Height: frames[0].Bounds().Dy()}
	w, err := os.Create(path)
	if err != nil {
		return err
	}
	defer w.Close()
	return gif.EncodeAll(w, out)
}

func changed(a, b *image.RGBA) image.Rectangle {
	r := image.Rectangle{}
	bounds := a.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		i := a.PixOffset(bounds.Min.X, y)
		ra, rb := a.Pix[i:i+bounds.Dx()*4], b.Pix[i:i+bounds.Dx()*4]
		for x := 0; x < bounds.Dx(); x++ {
			if ra[x*4] != rb[x*4] || ra[x*4+1] != rb[x*4+1] || ra[x*4+2] != rb[x*4+2] {
				r = r.Union(image.Rect(bounds.Min.X+x, y, bounds.Min.X+x+1, y+1))
			}
		}
	}
	return r
}
