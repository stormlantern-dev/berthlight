package tui

import (
	"image"
	"image/color"
	"math"
	"time"
)

// The header scene (PRD 0001, R12): a small demo in the spirit of the C64
// and Amiga. Copper bars roll behind the word berthlight, whose letters
// drop in and bounce; below, a submarine crosses the water, rises over a
// tentacle the octopus reaches up with, and moors at a berth whose light
// comes on. Original pixel art, drawn at the C64's chunky resolution.

// Scene size in pixels. On a terminal each pixel is a half cell (two per
// character row), so the header is SceneW columns by SceneH/2 rows.
const (
	SceneW = 80
	SceneH = 20
	// SceneLength is how long the scene runs before it rests on its last
	// frame.
	SceneLength = 4 * time.Second
)

// The C64 palette ("Pepto"), the colours the scene is drawn in.
var c64 = map[byte]color.RGBA{
	'k': {0x00, 0x00, 0x00, 0xff}, // black
	'w': {0xff, 0xff, 0xff, 0xff}, // white
	'r': {0x68, 0x37, 0x2b, 0xff}, // red
	'c': {0x70, 0xa4, 0xb2, 0xff}, // cyan
	'm': {0x6f, 0x3d, 0x86, 0xff}, // purple
	'g': {0x58, 0x8d, 0x43, 0xff}, // green
	'b': {0x35, 0x28, 0x79, 0xff}, // blue
	'y': {0xb8, 0xc7, 0x6f, 0xff}, // yellow
	'o': {0x6f, 0x4f, 0x25, 0xff}, // orange
	'n': {0x43, 0x39, 0x00, 0xff}, // brown
	'p': {0x9a, 0x67, 0x59, 0xff}, // light red
	'd': {0x44, 0x44, 0x44, 0xff}, // dark grey
	'e': {0x6c, 0x6c, 0x6c, 0xff}, // grey
	'l': {0x9a, 0xd2, 0x84, 0xff}, // light green
	'u': {0x6c, 0x5e, 0xb5, 0xff}, // light blue
	'i': {0x95, 0x95, 0x95, 0xff}, // light grey
}

// Sprites: one palette letter per pixel, '.' for transparent. The
// submarine faces right, its propeller at the back.
var (
	submarine = []string{
		"......oo.....",
		".....yyyy....",
		"..yyyyyyyyyy.",
		"eyycyycyycyyy",
		"..yyyyyyyyyy.",
	}
	octopus = []string{
		"..ppppp..",
		".ppppppp.",
		"ppwkpwkpp",
		"ppppppppp",
		".pprprpp.",
	}
)

// font is a 5×7 lowercase font for the letters of the name.
var font = map[rune][]string{
	'b': {"x....", "x....", "xxxx.", "x...x", "x...x", "x...x", "xxxx."},
	'e': {".....", ".....", ".xxx.", "x...x", "xxxxx", "x....", ".xxxx"},
	'r': {".....", ".....", "x.xx.", "xx..x", "x....", "x....", "x...."},
	't': {".x...", ".x...", "xxxx.", ".x...", ".x...", ".x..x", "..xx."},
	'h': {"x....", "x....", "x.xx.", "xx..x", "x...x", "x...x", "x...x"},
	'l': {".xx..", "..x..", "..x..", "..x..", "..x..", "..x..", ".xxx."},
	'i': {"..x..", ".....", ".xx..", "..x..", "..x..", "..x..", ".xxx."},
	'g': {".....", ".xxxx", "x...x", "x...x", ".xxxx", "....x", ".xxx."},
}

// Where things are.
const (
	word       = "berthlight"
	wordX      = 4  // first letter's column
	wordY      = 1  // resting row of the letters' top
	waterY     = 9  // first row of water
	seabedY    = 19 // the seabed
	cruiseY    = 13 // submarine's top row while cruising
	octopusX   = 30 // octopus head's left column
	berthX     = 70 // the pier's left end
	lampX      = 77 // the lamp post
	dockedX    = 57 // submarine's left column when moored
	arriveTime = 3.2
)

// Frame draws the scene at time t; from SceneLength on it is the last
// frame, which is also what terminals without animation show.
func Frame(t time.Duration) *image.RGBA {
	s := min(t, SceneLength).Seconds()
	img := image.NewRGBA(image.Rect(0, 0, SceneW, SceneH))
	set := func(x, y int, c color.RGBA) {
		if x >= 0 && x < SceneW && y >= 0 && y < SceneH {
			img.SetRGBA(x, y, c)
		}
	}

	// Sky with copper bars rolling behind the word, then the water.
	bars := []byte{'b', 'm', 'u', 'm', 'b'}
	for y := 0; y < SceneH; y++ {
		c := c64['k']
		if y >= waterY {
			c = c64['b']
		}
		for x := 0; x < SceneW; x++ {
			set(x, y, c)
		}
	}
	for _, phase := range []float64{0, math.Pi} {
		top := 2 + int(math.Round(2.5*math.Sin(s*2.2+phase)))
		for i, p := range bars {
			for x := 0; x < SceneW; x++ {
				if y := top + i - 1; y < waterY-1 {
					set(x, y, c64[p])
				}
			}
		}
	}
	// Seabed and weed swaying.
	for x := 0; x < SceneW; x++ {
		set(x, seabedY, c64['n'])
	}
	for _, wx := range []int{6, 13, 47, 64} {
		for h := 0; h < 3; h++ {
			sway := int(math.Round(math.Sin(s*3+float64(wx)+float64(h)) * 0.6))
			set(wx+sway*h/2, seabedY-1-h, c64['g'])
		}
	}

	// The berth: a pier on posts, and a lamp that comes on when the
	// submarine has moored.
	for x := berthX; x < SceneW; x++ {
		set(x, waterY+1, c64['o'])
	}
	for _, px := range []int{berthX + 1, SceneW - 2} {
		for y := waterY + 2; y < seabedY; y++ {
			set(px, y, c64['n'])
		}
	}
	for y := waterY - 4; y <= waterY; y++ {
		set(lampX, y, c64['e'])
	}
	lit := s >= arriveTime
	if lit {
		set(lampX, waterY-5, c64['w'])
		for _, d := range [][2]int{{-1, -5}, {1, -5}, {0, -6}} {
			set(lampX+d[0], waterY+d[1], c64['y'])
		}
	} else {
		set(lampX, waterY-5, c64['d'])
	}

	// The submarine: in from the left, over the tentacle, into the berth.
	subX, subY := submarinePos(s)
	// Bubbles from the propeller.
	for i := 0; i < 4; i++ {
		age := math.Mod(s*2+float64(i)*0.5, 2)
		if s >= arriveTime || age > s {
			continue
		}
		bx := int(math.Round(subXAt(s-age))) - 1
		by := cruiseY + 3 - int(age*3)
		if by > waterY {
			set(bx, by, c64['c'])
		}
	}
	drawSprite(set, submarine, int(math.Round(subX)), subY)

	// The octopus on the seabed, tentacles waving; one reaches up at the
	// submarine as it passes.
	ox, oy := octopusX, seabedY-len(octopus)-1
	drawSprite(set, octopus, ox, oy)
	for i := 0; i < 4; i++ {
		wig := int(math.Round(math.Sin(s*5+float64(i)) * 0.7))
		set(ox+1+i*2+wig, seabedY-1, c64['p'])
	}
	// The reaching tentacle comes up beside the head, just as the
	// submarine surfaces over it.
	if reach := tentacleReach(s); reach > 0 {
		for h := 0; h < reach; h++ {
			wig := int(math.Round(math.Sin(s*7+float64(h)*0.9) * 0.8))
			set(ox+len(octopus[0])+wig, oy+2-h, c64['p'])
		}
	}

	// The word, its letters dropping in one after another and bouncing.
	colours := []byte{'w', 'y', 'l', 'c', 'w', 'y', 'l', 'c', 'w', 'y'}
	for i, r := range word {
		y := wordY + letterOffset(s, i)
		c := c64[colours[(i+int(s*6))%len(colours)]]
		if s >= SceneLength.Seconds() {
			c = c64[colours[i%len(colours)]]
		}
		// A drop shadow first, so the letters stand out from the bars.
		for _, layer := range []struct {
			dx, dy int
			c      color.RGBA
		}{{1, 1, c64['k']}, {0, 0, c}} {
			for gy, row := range font[r] {
				for gx, ch := range row {
					if ch == 'x' {
						set(wordX+i*6+gx+layer.dx, y+gy+layer.dy, layer.c)
					}
				}
			}
		}
	}
	return img
}

func drawSprite(set func(int, int, color.RGBA), sprite []string, x, y int) {
	for sy, row := range sprite {
		for sx := 0; sx < len(row); sx++ {
			if row[sx] != '.' {
				set(x+sx, y+sy, c64[row[sx]])
			}
		}
	}
}

// subXAt is the submarine's left column at time s: in from off-screen,
// easing into its berth.
func subXAt(s float64) float64 {
	from, to := -float64(len(submarine[0])), float64(dockedX)
	if s >= arriveTime {
		return to
	}
	u := s / arriveTime
	u = 1 - (1-u)*(1-u) // ease out: slows down to moor
	return from + (to-from)*u
}

// submarinePos adds the dive: the submarine rises over the octopus and
// comes back down, bobbing a little on the way.
func submarinePos(s float64) (float64, int) {
	x := subXAt(s)
	mid := float64(octopusX) + float64(len(octopus[0]))/2 - float64(len(submarine[0]))/2
	d := (x - mid) / 9
	rise := 5 * math.Exp(-d*d)
	bob := 0.0
	if s < arriveTime {
		bob = 0.5 * math.Sin(s*6)
	}
	return x, cruiseY - int(math.Round(rise+bob))
}

// tentacleReach is how far up the reaching tentacle is, in pixels.
func tentacleReach(s float64) int {
	const start, end = 0.7, 1.7
	if s < start || s > end {
		return 0
	}
	u := (s - start) / (end - start)
	return int(math.Round(6 * math.Sin(u*math.Pi)))
}

// letterOffset is how far above its resting row letter i is at time s: it
// drops from above the scene, then bounces, each bounce lower.
func letterOffset(s float64, i int) int {
	const (
		delay   = 0.08 // between letters
		g       = 120  // pixels per second²
		height  = 9.0  // drop height
		damping = 0.45 // velocity kept per bounce
	)
	t := s - float64(i)*delay
	if t <= 0 {
		return -int(height) - 2 // not in yet: above the scene
	}
	// Free fall to the ground, then bounces of decreasing height.
	fall := math.Sqrt(2 * height / g)
	if t < fall {
		return -int(math.Round(height - g*t*t/2))
	}
	t -= fall
	v := g * fall * damping
	for v > 4 {
		flight := 2 * v / g
		if t < flight {
			return -int(math.Round(v*t - g*t*t/2))
		}
		t -= flight
		v *= damping
	}
	return 0
}
