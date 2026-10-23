package tui

import (
	"image"
	"image/color"
	"math"
	"time"
)

// The header scene (PRD 0001, R12): a small demo in the spirit of the C64
// and Amiga. Copper bars roll behind the word berthlight, whose letters
// drop in and bounce. Below the waterline a submarine cruises over an
// octopus, swerves away from the tentacle it reaches up with, surfaces and
// moors alongside a pier whose lamp then comes on. Original pixel art,
// drawn at the C64's chunky resolution.

// Scene size in pixels. On a terminal each pixel is a half cell (two per
// character row), so the header is SceneW columns by SceneH/2 rows.
const (
	SceneW = 80
	SceneH = 24
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
	// The submarine faces right: conning tower and periscope on top,
	// portholes along the hull, the propeller at the back. Rows 0-1 are the
	// tower; when it has surfaced, the hull (rows 2-4) lies in the water.
	submarine = []string{
		"......o.o....",
		".....yyyy....",
		"..yyyyyyyyyy.",
		"eyycyycyycyyy",
		"..yyyyyyyyy..",
	}
	// The octopus's head (mantle) with its eyes; the tentacles are drawn
	// apart, so they can move.
	octopusHead = []string{
		"...mmmmm...",
		"..mmmmmmm..",
		".mmmmmmmmm.",
		".mwkmmmwkm.",
		"..mmmmmmm..",
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
	word    = "berthlight"
	wordX   = 2  // first letter's column
	wordY   = 1  // resting row of the letters' top
	waterY  = 9  // the waterline; water below it
	seabedY = 23 // the seabed
	cruiseY = 10 // submarine's top row while cruising, at periscope depth
	// The octopus's head, on the seabed.
	octopusX = 26
	octopusY = seabedY - 2 - len(".....") // head, then a row of tentacles
	pierX    = 71                         // the pier's left end
	lampX    = 77                         // the lamp post
	dockedX  = 58                         // submarine's left column when moored
	// surfacedY puts the hull's top row on the waterline.
	surfacedY = waterY - 2

	surfaceAt = 2.4 // the submarine starts to surface
	arriveAt  = 3.2 // moored
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

	// Night sky with copper bars rolling behind the word; the water.
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
			if y := top + i - 1; y >= 0 && y < waterY-1 {
				for x := 0; x < SceneW; x++ {
					set(x, y, c64[p])
				}
			}
		}
	}
	// The waterline: small waves rolling to the right.
	for x := 0; x < SceneW; x++ {
		c := c64['u']
		if (x+int(s*8))%6 < 2 {
			c = c64['c']
		}
		set(x, waterY, c)
	}
	// Seabed, and weed swaying.
	for x := 0; x < SceneW; x++ {
		set(x, seabedY, c64['n'])
	}
	for _, wx := range []int{5, 12, 46, 52, 65} {
		for h := 0; h < 4; h++ {
			sway := int(math.Round(math.Sin(s*3+float64(wx)) * float64(h) / 3))
			set(wx+sway, seabedY-1-h, c64['g'])
		}
	}

	// The pier: a deck above the water on posts down to the seabed, and a
	// lamp that comes on when the submarine has moored.
	for y := waterY - 1; y < seabedY; y++ {
		set(pierX+1, y, c64['n'])
		set(SceneW-2, y, c64['n'])
	}
	for x := pierX; x < SceneW; x++ {
		set(x, waterY-1, c64['o'])
	}
	for y := waterY - 6; y < waterY-1; y++ {
		set(lampX, y, c64['e'])
	}
	if s >= arriveAt {
		set(lampX, waterY-7, c64['w'])
		for _, d := range [][2]int{{-1, -7}, {1, -7}, {0, -8}, {-2, -7}, {2, -7}} {
			set(lampX+d[0], waterY+d[1], c64['y'])
		}
	} else {
		set(lampX, waterY-7, c64['d'])
	}

	// The octopus: its head on the seabed, six tentacles curling out over
	// it, and one reaching up at the submarine.
	drawOctopus(set, s)

	// The submarine, and bubbles from its propeller while it is under.
	subX, subY := submarinePos(s)
	for i := 0; i < 4; i++ {
		age := math.Mod(s*2+float64(i)*0.5, 2)
		if s >= surfaceAt || age > s {
			continue
		}
		_, y0 := submarinePos(s - age)
		bx := int(math.Round(subXAt(s-age))) - 1
		by := y0 + 3 - int(age*4)
		if by > waterY {
			set(bx, by, c64['c'])
		}
	}
	drawSprite(set, submarine, int(math.Round(subX)), subY)
	// Water over whatever lies below the waterline when it has surfaced:
	// the waterline stays in front of the hull.
	if subY < waterY {
		for x := int(math.Round(subX)) + 1; x < int(math.Round(subX))+len(submarine[0])-1; x++ {
			if (x+int(s*8))%6 < 2 {
				set(x, waterY, c64['c'])
			}
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
					if ch == 'x' && y+gy+layer.dy < waterY {
						set(wordX+i*6+gx+layer.dx, y+gy+layer.dy, layer.c)
					}
				}
			}
		}
	}
	return img
}

// drawOutlined draws a sprite with a black outline, as C64 sprites often
// had, so it stands out from the water.
func drawOutlined(set func(int, int, color.RGBA), sprite []string, x, y int) {
	filled := func(sx, sy int) bool {
		return sy >= 0 && sy < len(sprite) && sx >= 0 && sx < len(sprite[sy]) && sprite[sy][sx] != '.'
	}
	for sy := -1; sy <= len(sprite); sy++ {
		for sx := -1; sx <= len(sprite[0]); sx++ {
			if filled(sx, sy) {
				continue
			}
			if filled(sx-1, sy) || filled(sx+1, sy) || filled(sx, sy-1) || filled(sx, sy+1) {
				set(x+sx, y+sy, c64['k'])
			}
		}
	}
	drawSprite(set, sprite, x, y)
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

// drawOctopus draws the head with six tentacles curling out to both sides
// along the seabed, each waving, and the reaching tentacle.
func drawOctopus(set func(int, int, color.RGBA), s float64) {
	hx, hy := octopusX, octopusY
	w := len(octopusHead[0])
	bottom := hy + len(octopusHead)
	// Each tentacle: a list of pixels, drawn over a black shadow so it
	// stands out from the water like the outlined head.
	var pixels [][2]int
	for k := 0; k < 6; k++ {
		dir, n := -1, k // n counts from the middle out
		if k >= 3 {
			dir, n = 1, 5-k
		}
		// Down from under the head, out along the seabed, and the tip
		// curling back up beside the head; outer ones reach further.
		x := hx + w/2 + dir*(1+n)
		y := bottom
		steps := 6 + 2*(2-n)
		for j := 0; j < steps; j++ {
			wave := int(math.Round(math.Sin(s*4+float64(k)*1.3+float64(j)*0.7) * 0.5))
			pixels = append(pixels, [2]int{x, min(y+wave, seabedY-1)})
			switch {
			case j < 1:
				y++
			case j >= steps-3:
				y--
			}
			x += dir
		}
	}
	// The reaching tentacle rises from the head's right side, curling,
	// where the submarine just was.
	reach := tentacleReach(s)
	for h := 0; h < reach; h++ {
		wig := int(math.Round(math.Sin(s*7+float64(h)*0.9) * 0.8))
		pixels = append(pixels, [2]int{hx + w - 3 + wig + h/2, hy - 1 - h})
		if h == reach-1 {
			pixels = append(pixels, [2]int{hx + w - 2 + wig + h/2, hy - 1 - h})
		}
	}
	for _, p := range pixels {
		for _, d := range [][2]int{{0, 1}, {1, 0}, {-1, 0}} {
			if p[1]+d[1] < seabedY {
				set(p[0]+d[0], p[1]+d[1], c64['k'])
			}
		}
	}
	for _, p := range pixels {
		set(p[0], p[1], c64['m'])
	}
	drawOutlined(set, octopusHead, hx, hy)
}

// subXAt is the submarine's left column at time s: in from off-screen,
// easing into its berth.
func subXAt(s float64) float64 {
	from, to := -float64(len(submarine[0])), float64(dockedX)
	if s >= arriveAt {
		return to
	}
	u := s / arriveAt
	u = 1 - (1-u)*(1-u) // ease out: slows down to moor
	return from + (to-from)*u
}

// submarinePos adds depth: cruising, a smooth swerve up and away from the
// reaching tentacle, then surfacing alongside the pier.
func submarinePos(s float64) (float64, int) {
	x := subXAt(s)
	y := float64(cruiseY)
	// A short lift as the tentacle snaps up just behind it.
	if r := tentacleReachF(s); r > 0 {
		y -= r / tentacleMax
	}
	if s < surfaceAt {
		y += 0.4 * math.Sin(s*5) // a slight bob
	} else {
		u := min((s-surfaceAt)/(arriveAt-surfaceAt), 1)
		u = u * u * (3 - 2*u) // smoothstep
		y = float64(cruiseY) + (float64(surfacedY)-float64(cruiseY))*u
	}
	return x, int(math.Round(y))
}

// The reaching tentacle: when, and how far.
const (
	reachFrom   = 1.45
	reachTo     = 2.35
	tentacleMax = 5
)

func tentacleReachF(s float64) float64 {
	if s < reachFrom || s > reachTo {
		return 0
	}
	return tentacleMax * math.Sin((s-reachFrom)/(reachTo-reachFrom)*math.Pi)
}

// tentacleReach is how far up the reaching tentacle is, in pixels.
func tentacleReach(s float64) int { return int(math.Round(tentacleReachF(s))) }

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
