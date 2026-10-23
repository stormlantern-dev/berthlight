package tui

import (
	"image"
	"image/color"
	"math"
	"time"
)

// The header scene (PRD 0001, R12): a small demo in the spirit of the C64
// and Amiga. Copper bars roll behind the word berthlight, whose letters
// drop in and bounce. Below, at the waterline, a little sailing boat
// passes; a small octopus surfaces and swipes a tentacle at it, misses
// just behind the stern with a splash, and sinks back until only its eyes
// show; the boat moors at a quay with Amsterdam canal houses, whose dark
// windows then light up one by one. Original pixel
// art, drawn at the C64's chunky resolution.

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

// Sprites: one palette letter per pixel, '.' for transparent.
var (
	// A small sailing boat, bow to the right: mainsail and jib either side
	// of the mast, the hull's lower row in the water.
	boat = []string{
		".....i....",
		"....wiw...",
		"...wwiww..",
		"..wwwiwww.",
		"nooooooooo",
		".oooooooo.",
	}
	// Three Amsterdam canal houses of different heights on the quay: a step
	// gable in red brick, a tall bell gable in grey, a lower neck gable,
	// each with white trim. 'L' is a window, 'n' a door.
	houses = [][]string{
		{
			"..w..",
			".wrw.",
			"wrrrw",
			"rLrLr",
			"rrrrr",
			"rLrLr",
			"rrrrr",
			"rLrLr",
			"rrrrr",
			"rLnLr",
		},
		{
			".www.",
			"weeew",
			"eLeLe",
			"eeeee",
			"eLeLe",
			"eeeee",
			"eLeLe",
			"eeeee",
			"eLeLe",
			"eeeee",
			"eLnLe",
		},
		{
			"..w..",
			".wow.",
			"wooow",
			"oLoLo",
			"ooooo",
			"oLoLo",
			"ooooo",
			"oLnLo",
		},
	}
	// A small octopus's head; its tentacles are drawn apart, so they move.
	octopusHead = []string{
		"..mmm..",
		".mmmmm.",
		"mwkmwkm",
		"mmmmmmm",
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
	word   = "berthlight"
	wordX  = 2  // first letter's column
	wordY  = 1  // resting row of the letters' top
	skyY   = 9  // below the word: the night sky over the water
	waterY = 14 // the waterline
	// The boat's top row: its hull's lower row lies on the waterline.
	boatY    = waterY - 5
	dockedX  = 51 // boat's left column when moored
	jettyX   = 61 // the jetty's left end, reaching out from the shore
	shoreX   = 65 // land from here to the right edge
	octopusX = 20 // octopus head's left column

	arriveAt = 3.2 // moored
)

// The octopus's story, in seconds.
const (
	riseFrom  = 1.25 // surfaces just behind the boat as it passes
	riseTo    = 1.55 // head and eyes out of the water
	swipeFrom = 1.6  // the tentacle swings up and over at the boat
	swipeTo   = 1.95 // and slaps the water just behind its stern
	sinkFrom  = 2.3  // sinks back
	sinkTo    = 2.9  // until only its eyes show
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

	// Night sky, copper bars rolling behind the word, and the sea.
	for y := 0; y < SceneH; y++ {
		c := c64['k']
		if y > waterY {
			c = c64['b']
		}
		for x := 0; x < SceneW; x++ {
			set(x, y, c)
		}
	}
	bars := []byte{'b', 'm', 'u', 'm', 'b'}
	for _, phase := range []float64{0, math.Pi} {
		top := 2 + int(math.Round(2.5*math.Sin(s*2.2+phase)))
		for i, p := range bars {
			if y := top + i - 1; y >= 0 && y < skyY-1 {
				for x := 0; x < SceneW; x++ {
					set(x, y, c64[p])
				}
			}
		}
	}
	for i, st := range [][2]int{{8, 10}, {19, 12}, {37, 11}, {44, 10}, {53, 12}} {
		if int(s*3+float64(i))%4 != 0 { // twinkling
			set(st[0], st[1], c64['i'])
		}
	}

	// The octopus, of which only what is above the waterline shows: under
	// water it is out of sight.
	drawOctopus(func(x, y int, c color.RGBA) {
		if y < waterY {
			set(x, y, c)
		}
	}, s)
	for x := 0; x < shoreX; x++ {
		c := c64['u']
		if (x+int(s*8))%6 < 2 {
			c = c64['c']
		}
		set(x, waterY, c)
	}
	drawShore(set, s)
	drawSplashes(set, s)

	// The boat, sailing in level on the water and mooring.
	bx, by := boatPos(s)
	drawSprite(set, boat, bx, by)

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
					if ch == 'x' && y+gy+layer.dy < skyY {
						set(wordX+i*6+gx+layer.dx, y+gy+layer.dy, layer.c)
					}
				}
			}
		}
	}
	return img
}

// drawShore draws the quay on the right with its jetty and the canal
// houses, whose windows light up one after another once the boat has
// moored.
func drawShore(set func(int, int, color.RGBA), s float64) {
	// The quay: a stone edge over a brick wall down into the water.
	for x := shoreX; x < SceneW; x++ {
		set(x, waterY-1, c64['i'])
		for y := waterY; y < SceneH; y++ {
			c := c64['r']
			if (x+y)%3 == 0 {
				c = c64['n']
			}
			set(x, y, c)
		}
	}
	for x := jettyX; x < shoreX; x++ {
		set(x, waterY-1, c64['o'])
	}
	for y := waterY; y < SceneH; y++ {
		set(jettyX+1, y, c64['n'])
	}
	for k, house := range houses {
		x, y := houseAt(k)
		for sy, row := range house {
			for sx := 0; sx < len(row); sx++ {
				switch row[sx] {
				case '.':
				case 'L':
					c := c64['d']
					if s >= windowLightAt(k, sx, sy) {
						c = c64['y']
					}
					set(x+sx, y+sy, c)
				default:
					set(x+sx, y+sy, c64[row[sx]])
				}
			}
		}
	}
}

// houseAt is where house k's top left is: side by side on the quay.
func houseAt(k int) (int, int) {
	return shoreX + k*5, waterY - 1 - len(houses[k])
}

// windowLightAt is when the window at (sx, sy) of house k is lit: it is
// evening, so some are lit from the start; the others light up one by
// one, in reading order, once the boat has moored.
func windowLightAt(k, sx, sy int) float64 {
	n := 0 // windows not lit from the start, before this one
	for hk, house := range houses {
		for y, row := range house {
			for x := 0; x < len(row); x++ {
				if row[x] != 'L' {
					continue
				}
				litEarly := (hk*3+y+x)%3 == 0
				if hk == k && y == sy && x == sx {
					if litEarly {
						return 0
					}
					return arriveAt + 0.035*float64(n)
				}
				if !litEarly {
					n++
				}
			}
		}
	}
	return math.Inf(1)
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

// smooth eases u from 0 to 1 (smoothstep), clamped.
func smooth(u float64) float64 {
	u = min(max(u, 0), 1)
	return u * u * (3 - 2*u)
}

// boatXAt is the boat's left column at time s: in from off-screen,
// slowing down into its berth.
func boatXAt(s float64) float64 {
	from, to := -float64(len(boat[0])), float64(dockedX)
	if s >= arriveAt {
		return to
	}
	u := s / arriveAt
	u = 1 - (1-u)*(1-u) // ease out
	return from + (to-from)*u
}

// boatPos is where the boat is drawn: its column, and its top row, which
// stays level on the waterline.
func boatPos(s float64) (int, int) {
	return int(math.Round(boatXAt(s))), boatY
}

// octopusTop is the row of the octopus's head's top at time s: deep under
// water, surfacing, then sinking back until only its eyes show.
func octopusTop(s float64) int {
	deep := float64(waterY + 3)              // fully under water
	up := float64(waterY - len(octopusHead)) // head out, sitting on the waterline
	peek := float64(waterY - 3)              // eyes just above the water
	y := deep + (up-deep)*smooth((s-riseFrom)/(riseTo-riseFrom))
	if s >= sinkFrom {
		y = up + (peek-up)*smooth((s-sinkFrom)/(sinkTo-sinkFrom))
	}
	return int(math.Round(y))
}

// tentacleTip is where the swiping tentacle's tip is, and whether it is
// out: from the head's right side it swings up, over and down, slapping
// the water just behind the passing boat's stern.
func tentacleTip(s float64) (x, y float64, out bool) {
	if s < swipeFrom || s > sinkFrom {
		return 0, 0, false
	}
	bx, by := tentacleBase(s)
	u := smooth((s - swipeFrom) / (swipeTo - swipeFrom))
	// An arc from up and back to down and ahead: wide, not too tall, so it
	// stays under the word.
	angle := (-110 + 130*u) * math.Pi / 180
	return bx + 8*math.Cos(angle), by + 5*math.Sin(angle), true
}

func tentacleBase(s float64) (float64, float64) {
	return float64(octopusX + len(octopusHead[0]) - 1), float64(octopusTop(s) + 2)
}

func drawOctopus(set func(int, int, color.RGBA), s float64) {
	top := octopusTop(s)
	hx := octopusX
	// Small tentacles waving under the head.
	for k := 0; k < 4; k++ {
		for j := 0; j < 2; j++ {
			wig := int(math.Round(math.Sin(s*6+float64(k)+float64(j)) * 0.6))
			set(hx+k*2+wig, top+len(octopusHead)+j, c64['m'])
		}
	}
	// The swiping tentacle: a curve from the head to its tip, bending
	// with the swing, with a lighter tip.
	if tx, ty, out := tentacleTip(s); out {
		bx, by := tentacleBase(s)
		mx, my := (bx+tx)/2-1.5, (by+ty)/2-1.5 // the bend
		for i := 0; i <= 12; i++ {
			u := float64(i) / 12
			x := (1-u)*(1-u)*bx + 2*(1-u)*u*mx + u*u*tx
			y := (1-u)*(1-u)*by + 2*(1-u)*u*my + u*u*ty
			c := c64['m']
			if i >= 10 {
				c = c64['p']
			}
			set(int(math.Round(x)), int(math.Round(y)), c)
		}
	}
	// The head, outlined in black where it is out of the water, as C64
	// sprites were, so it stands out from the night.
	for sy := -1; sy <= len(octopusHead); sy++ {
		for sx := -1; sx <= len(octopusHead[0]); sx++ {
			if headAt(sx, sy) || !(headAt(sx-1, sy) || headAt(sx+1, sy) || headAt(sx, sy-1) || headAt(sx, sy+1)) {
				continue
			}
			if top+sy < waterY {
				set(hx+sx, top+sy, c64['k'])
			}
		}
	}
	drawSprite(set, octopusHead, hx, top)
}

func headAt(sx, sy int) bool {
	return sy >= 0 && sy < len(octopusHead) && sx >= 0 && sx < len(octopusHead[sy]) && octopusHead[sy][sx] != '.'
}

// drawSplashes throws up droplets where the octopus breaks the surface and
// where its tentacle slaps the water.
func drawSplashes(set func(int, int, color.RGBA), s float64) {
	splash := func(cx float64, since float64) {
		if since < 0 || since > 0.4 {
			return
		}
		h := since / 0.4
		for i, dx := range []float64{-3, -2, -1, 1, 2, 3} {
			// Droplets fly up and out, then fall back.
			x := cx + dx*(0.5+h)
			y := float64(waterY) - (3-math.Abs(dx)*0.6)*math.Sin(h*math.Pi) - float64(i%2)
			c := c64['w']
			if i%2 == 1 {
				c = c64['c']
			}
			set(int(math.Round(x)), int(math.Round(y)), c)
		}
	}
	splash(float64(octopusX)+3, s-(riseFrom+riseTo)/2)
	tx, _, _ := tentacleTip(swipeTo)
	splash(tx, s-swipeTo)
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
