package tui

import (
	"image"
	"image/color"
	"math"
	"time"
)

// The Amiga version of the header scene, for terminals with the Kitty
// graphics protocol: the same story as Frame, at four times the
// resolution, in the Amiga's 12-bit colours with ordered dithering.
// Copper bars change colour on every scanline; chrome letters drop in,
// bounce and wave; a moon glows over the horizon and everything above the
// water is reflected in it, rippling.

// Hi-res scene size: four pixels for each pixel of the chunky scene.
const (
	HiW = SceneW * hk
	HiH = SceneH * hk
	hk  = 4 // hi-res pixels per chunky pixel

	hiWater = waterY * hk // the waterline
	hiSky   = skyY * hk   // below the word
	hiQuay  = jettyX*hk + 12
	hiHouse = shoreX * hk // the canal houses start here
)

type rgb struct{ r, g, b float64 }

func mix(a, b rgb, t float64) rgb {
	t = min(max(t, 0), 1)
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

// canvas holds the scene in floating point until the last step, which
// quantizes it to the Amiga's 4 bits per channel with ordered dithering.
type canvas struct {
	px []rgb
}

func newCanvas() *canvas { return &canvas{px: make([]rgb, HiW*HiH)} }

func (c *canvas) in(x, y int) bool { return x >= 0 && x < HiW && y >= 0 && y < HiH }

func (c *canvas) get(x, y int) rgb {
	x, y = min(max(x, 0), HiW-1), min(max(y, 0), HiH-1)
	return c.px[y*HiW+x]
}

func (c *canvas) set(x, y int, v rgb) {
	if c.in(x, y) {
		c.px[y*HiW+x] = v
	}
}

func (c *canvas) blend(x, y int, v rgb, a float64) {
	if c.in(x, y) {
		c.px[y*HiW+x] = mix(c.px[y*HiW+x], v, a)
	}
}

// disc fills a circle, with a soft edge.
func (c *canvas) disc(cx, cy, r float64, v rgb, a float64) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			if d < r+0.5 {
				c.blend(x, y, v, a*min(1, r+0.5-d))
			}
		}
	}
}

// poly fills a polygon, row by row.
func (c *canvas) poly(pts [][2]float64, fill func(x, y int) rgb) {
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, p := range pts {
		minY, maxY = min(minY, p[1]), max(maxY, p[1])
	}
	for y := int(math.Floor(minY)); y <= int(math.Ceil(maxY)); y++ {
		fy := float64(y) + 0.5
		var xs []float64
		for i := range pts {
			a, b := pts[i], pts[(i+1)%len(pts)]
			if (a[1] <= fy) != (b[1] <= fy) {
				xs = append(xs, a[0]+(fy-a[1])/(b[1]-a[1])*(b[0]-a[0]))
			}
		}
		for i := 0; i+1 < len(xs); i += 2 {
			x0, x1 := min(xs[i], xs[i+1]), max(xs[i], xs[i+1])
			for x := int(math.Round(x0)); x < int(math.Round(x1)); x++ {
				c.set(x, y, fill(x, y))
			}
		}
	}
}

// bayer4 is the 4×4 ordered dither matrix.
var bayer4 = [4][4]float64{{0, 8, 2, 10}, {12, 4, 14, 6}, {3, 11, 1, 9}, {15, 7, 13, 5}}

// image quantizes to 12-bit colour with ordered dithering. Colours that
// are already 12-bit (multiples of 17) come out unchanged; gradients come
// out dithered, as on the Amiga.
func (c *canvas) image() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, HiW, HiH))
	q := func(v, d float64) uint8 {
		return uint8(min(15, max(0, math.Floor((v+d*17)/17))) * 17)
	}
	for y := 0; y < HiH; y++ {
		for x := 0; x < HiW; x++ {
			v := c.px[y*HiW+x]
			d := bayer4[y%4][x%4] / 16
			img.SetRGBA(x, y, color.RGBA{q(v.r, d), q(v.g, d), q(v.b, d), 0xff})
		}
	}
	return img
}

// hash is a deterministic pseudo-random number in [0, 1) for i.
func hash(i float64) float64 {
	v := math.Sin(i*12.9898+78.233) * 43758.5453
	return v - math.Floor(v)
}

// FrameHi draws the Amiga scene at time t; from SceneLength on it is the
// last frame.
func FrameHi(t time.Duration) *image.RGBA {
	s := min(t, SceneLength).Seconds()
	c := newCanvas()
	drawHiSky(c, s)
	drawHiShore(c, s)
	drawHiOctopus(c, s)
	drawHiBoat(c, s)
	drawHiWater(c, s) // reflects what is above
	drawHiSplashes(c, s)
	drawHiWord(c, s)
	return c.image()
}

func drawHiSky(c *canvas, s float64) {
	top, horizon := rgb{0, 0, 34}, rgb{51, 17, 85}
	for y := 0; y < hiWater; y++ {
		v := mix(top, horizon, float64(y)/float64(hiWater))
		for x := 0; x < HiW; x++ {
			c.set(x, y, v)
		}
	}
	// Copper bars behind the word: a new colour on every scanline.
	bars := []rgb{{255, 85, 187}, {85, 221, 255}, {255, 204, 68}}
	for i, bc := range bars {
		yc := 17 + 11*math.Sin(s*1.6+float64(i)*2.1)
		for y := 0; y < hiSky-2; y++ {
			p := 1 - math.Abs(float64(y)+0.5-yc)/6
			if p <= 0 {
				continue
			}
			v := mix(c.get(0, y), bc, p)
			if p > 0.8 {
				v = mix(v, rgb{255, 255, 255}, (p-0.8)*4)
			}
			for x := 0; x < HiW; x++ {
				c.set(x, y, v)
			}
		}
	}
	// Stars over the water, twinkling.
	for i := 0; i < 34; i++ {
		x, y := int(hash(float64(i))*240), hiSky+1+int(hash(float64(i)+50)*float64(hiWater-hiSky-4))
		b := 0.5 + 0.5*math.Sin(s*4+float64(i)*1.7)
		c.blend(x, y, rgb{221, 221, 255}, 0.4+0.6*b)
	}
	// A moon over the horizon, with a glow.
	mx, my := 168.0, 45.0
	for r := 16.0; r > 5; r -= 1 {
		c.disc(mx, my, r, rgb{255, 238, 170}, 0.05)
	}
	c.disc(mx, my, 5, rgb{255, 255, 221}, 1)
	c.disc(mx+1.5, my-1, 1.2, rgb{221, 221, 187}, 1)
	c.disc(mx-1.5, my+1.5, 0.9, rgb{221, 221, 187}, 1)
	// Clouds drifting slowly across it.
	for k, cl := range [][2]float64{{150, 41}, {60, 39}, {250, 44}} {
		x := cl[0] - s*5 - float64(k)*3
		for j := 0; j < 4; j++ {
			c.disc(x+float64(j)*4, cl[1]+math.Sin(float64(j))*1.2, 3+hash(float64(k*7+j))*2, rgb{68, 51, 102}, 0.75)
		}
	}
}

// Canal houses on the quay, as in the chunky scene, with more detail.
var hiHouses = []struct {
	height, gable int
	wall          rgb
	kind          int // 0 step gable, 1 bell gable, 2 neck gable
}{
	{44, 10, rgb{153, 51, 34}, 0},
	{52, 10, rgb{102, 102, 119}, 1},
	{36, 9, rgb{170, 119, 51}, 2},
}

func gableWidth(kind, gy, gable int) float64 {
	u := float64(gy) / float64(gable)
	switch kind {
	case 0: // steps
		return math.Min(20, 6+4*math.Floor(float64(gy)/3))
	case 1: // bell
		return math.Max(6, 20*math.Sqrt(u))
	default: // neck with shoulders
		if u < 0.6 {
			return 8
		}
		return 8 + 12*math.Pow((u-0.6)/0.4, 2)
	}
}

// hiWindows lists every window as house, column and row, in reading order.
func hiWindows() [][3]int {
	var w [][3]int
	for k, h := range hiHouses {
		rows := (h.height - h.gable - 12) / 7
		for r := 0; r < rows; r++ {
			for col := 0; col < 2; col++ {
				w = append(w, [3]int{k, col, r})
			}
		}
	}
	return w
}

// hiWindowLightAt is when a window lights up: some are lit from the
// start, the rest one by one once the boat has moored.
func hiWindowLightAt(k, col, row int) float64 {
	n := 0
	for _, w := range hiWindows() {
		early := (w[0]*3+w[1]+w[2])%3 == 0
		if w == [3]int{k, col, row} {
			if early {
				return 0
			}
			return arriveAt + 0.6*float64(n)/12
		}
		if !early {
			n++
		}
	}
	return math.Inf(1)
}

func drawHiShore(c *canvas, s float64) {
	ground := hiWater - 1
	// Quay: a stone edge and a brick wall into the water.
	for x := hiQuay; x < HiW; x++ {
		c.set(x, ground, rgb{187, 187, 170})
		for y := hiWater; y < HiH; y++ {
			v := rgb{119, 51, 34}
			if (y%4 == 0) || ((x+(y/4)*3)%8 == 0) {
				v = rgb{68, 34, 34}
			}
			c.set(x, y, v)
		}
	}
	// The jetty, on posts.
	for x := jettyX * hk; x < hiQuay; x++ {
		c.set(x, ground-1, rgb{136, 85, 51})
		c.set(x, ground, rgb{85, 51, 34})
	}
	for _, px := range []int{jettyX*hk + 2, jettyX*hk + 9} {
		for y := hiWater; y < HiH; y++ {
			c.set(px, y, rgb{68, 51, 34})
		}
	}
	// The canal houses.
	for k, h := range hiHouses {
		x0 := hiHouse + k*20
		bodyTop := ground - h.height + h.gable
		for y := bodyTop; y < ground; y++ {
			for x := x0; x < x0+20; x++ {
				v := h.wall
				if x == x0 || ((y-bodyTop)%4 == 3 && h.kind != 1) {
					v = mix(h.wall, rgb{0, 0, 0}, 0.25)
				}
				c.set(x, y, v)
			}
		}
		for gy := 0; gy < h.gable; gy++ {
			w := gableWidth(h.kind, gy, h.gable)
			y := bodyTop - h.gable + gy
			x1, x2 := float64(x0)+10-w/2, float64(x0)+10+w/2
			for x := int(math.Round(x1)); x < int(math.Round(x2)); x++ {
				c.set(x, y, h.wall)
			}
			c.set(int(math.Round(x1)), y, rgb{238, 238, 238})
			c.set(int(math.Round(x2))-1, y, rgb{238, 238, 238})
		}
		for x := x0 + 7; x < x0+13; x++ { // the top of the gable
			if gableWidth(h.kind, 0, h.gable) >= float64(x-x0-7)*2-6 {
				c.set(x, bodyTop-h.gable, rgb{238, 238, 238})
			}
		}
		for x := x0; x < x0+20; x++ { // cornice
			c.set(x, bodyTop, rgb{238, 238, 238})
		}
		// Windows, lit or dark, with a glow when lit.
		rows := (h.height - h.gable - 12) / 7
		for r := 0; r < rows; r++ {
			for col := 0; col < 2; col++ {
				wx, wy := x0+3+col*9, bodyTop+3+r*7
				lit := s >= hiWindowLightAt(k, col, r)
				for y := wy; y < wy+5; y++ {
					for x := wx; x < wx+5; x++ {
						v := rgb{17, 17, 51}
						if lit {
							v = rgb{255, 187, 85}
							if x > wx && x < wx+4 && y > wy && y < wy+4 {
								v = rgb{255, 238, 153}
							}
						}
						c.set(x, y, v)
					}
				}
				if lit {
					for d := 1; d <= 2; d++ {
						for x := wx - d; x < wx+5+d; x++ {
							c.blend(x, wy-d, rgb{255, 187, 85}, 0.18/float64(d))
							c.blend(x, wy+4+d, rgb{255, 187, 85}, 0.18/float64(d))
						}
					}
				}
				for x := wx - 1; x < wx+6; x++ { // sill
					c.set(x, wy+5, rgb{221, 221, 221})
				}
			}
		}
		for y := ground - 8; y < ground; y++ { // door
			for x := x0 + 8; x < x0+12; x++ {
				c.set(x, y, rgb{51, 34, 17})
			}
		}
	}
}

// hiAbove keeps drawing above the waterline: what is under water is out
// of sight.
func hiAbove(c *canvas, x, y int, v rgb) {
	if y < hiWater {
		c.set(x, y, v)
	}
}

func drawHiOctopus(c *canvas, s float64) {
	top := octopusTopF(s) * hk
	cx, cy := float64(octopusX*hk)+14, top+9
	// Its eyes follow the boat.
	look := math.Max(-1, math.Min(1, (boatXAt(s)*hk+20-cx)/40))
	// Tentacles waving under the head.
	for k := 0; k < 4; k++ {
		for j := 0; j < 10; j++ {
			x := cx - 9 + float64(k)*6 + math.Sin(s*6+float64(k)+float64(j)*0.6)*1.5
			y := cy + 7 + float64(j)
			hiAbove(c, int(x), int(y), rgb{136, 51, 153})
			hiAbove(c, int(x)+1, int(y), rgb{136, 51, 153})
		}
	}
	// The swiping tentacle: thick at the base, thin at the tip, suckers
	// along it.
	if tx, ty, out := tentacleTip(s); out {
		bx, by := tentacleBase(s)
		bx, by, tx, ty = bx*hk, by*hk, tx*hk+2, ty*hk+2
		mx, my := (bx+tx)/2-6, (by+ty)/2-6
		for i := 0; i <= 60; i++ {
			u := float64(i) / 60
			x := (1-u)*(1-u)*bx + 2*(1-u)*u*mx + u*u*tx
			y := (1-u)*(1-u)*by + 2*(1-u)*u*my + u*u*ty
			r := 2.6 - 2*u
			for dy := -r; dy <= r; dy++ {
				for dx := -r; dx <= r; dx++ {
					if dx*dx+dy*dy <= r*r {
						hiAbove(c, int(x+dx), int(y+dy), rgb{153, 68, 170})
					}
				}
			}
			if i%6 == 3 && u < 0.85 {
				hiAbove(c, int(x+1), int(y+1), rgb{255, 170, 204})
			}
		}
	}
	// The head: purple with a highlight, outlined.
	for y := int(cy - 11); y <= int(cy+10); y++ {
		for x := int(cx - 13); x <= int(cx+13); x++ {
			dx, dy := (float64(x)+0.5-cx)/12, (float64(y)+0.5-cy)/9.5
			d := dx*dx + dy*dy
			switch {
			case d <= 1:
				shade := mix(rgb{187, 102, 204}, rgb{102, 34, 119}, (dx+dy+1)/2.4)
				if (dx+0.45)*(dx+0.45)+(dy+0.5)*(dy+0.5) < 0.05 {
					shade = rgb{238, 187, 255}
				}
				hiAbove(c, x, y, shade)
			case d <= 1.18:
				hiAbove(c, x, y, rgb{17, 0, 34})
			}
		}
	}
	for _, ex := range []float64{-5, 5} {
		ey := cy + 1
		for y := int(ey - 3); y <= int(ey+3); y++ {
			for x := int(cx + ex - 3); x <= int(cx+ex+3); x++ {
				dx, dy := float64(x)+0.5-(cx+ex), float64(y)+0.5-ey
				if dx*dx/7+dy*dy/9 <= 1 {
					hiAbove(c, x, y, rgb{255, 255, 255})
				}
			}
		}
		hiAbove(c, int(cx+ex+look*1.5), int(ey+1), rgb{0, 0, 0})
		hiAbove(c, int(cx+ex+look*1.5)+1, int(ey+1), rgb{0, 0, 0})
		hiAbove(c, int(cx+ex+look*1.5), int(ey), rgb{0, 0, 0})
	}
}

func drawHiBoat(c *canvas, s float64) {
	bx := boatXAt(s) * hk
	by := float64(hiWater - 22)
	// Mast and a pennant waving at its top.
	for y := by; y < by+17; y++ {
		c.set(int(bx+19), int(y), rgb{204, 204, 204})
	}
	for i := 0; i < 6; i++ {
		y := by + 1 + math.Sin(s*12-float64(i)*0.9)*0.9
		c.set(int(bx+20)+i, int(math.Round(y)), rgb{238, 51, 51})
		if i < 3 {
			c.set(int(bx+20)+i, int(math.Round(y))+1, rgb{238, 51, 51})
		}
	}
	// Mainsail, bellied, shaded toward the mast; and the jib.
	billow := 1.5 + 0.5*math.Sin(s*3)
	c.poly([][2]float64{{bx + 18, by + 2}, {bx + 18, by + 16}, {bx + 3, by + 16}, {bx + 10 - billow, by + 9}}, func(x, y int) rgb {
		return mix(rgb{170, 187, 204}, rgb{255, 255, 255}, (float64(x)-bx-3)/15)
	})
	c.poly([][2]float64{{bx + 20, by + 4}, {bx + 20, by + 16}, {bx + 33, by + 16}, {bx + 26 + billow, by + 10}}, func(x, y int) rgb {
		return mix(rgb{255, 255, 255}, rgb{187, 204, 221}, (float64(x)-bx-20)/13)
	})
	// Hull, with a rail and a plank line.
	c.poly([][2]float64{{bx, by + 17}, {bx + 40, by + 17}, {bx + 35, by + 24}, {bx + 5, by + 24}}, func(x, y int) rgb {
		switch {
		case y == int(by)+17:
			return rgb{221, 187, 136}
		case y == int(by)+20:
			return rgb{85, 51, 17}
		}
		return rgb{136, 85, 51}
	})
	// A lantern at the stern.
	c.disc(bx+3, by+15, 3, rgb{255, 204, 102}, 0.3)
	c.set(int(bx+3), int(by+15), rgb{255, 238, 170})
}

func waterAt(y int) rgb {
	return mix(rgb{17, 51, 119}, rgb{0, 17, 51}, float64(y-hiWater)/float64(HiH-hiWater))
}

func drawHiWater(c *canvas, s float64) {
	// Everything above the waterline is reflected below it, rippling and
	// fading with depth.
	src := make([]rgb, len(c.px))
	copy(src, c.px)
	at := func(x, y int) rgb {
		x, y = min(max(x, 0), HiW-1), min(max(y, 0), HiH-1)
		return src[y*HiW+x]
	}
	for y := hiWater; y < HiH; y++ {
		d := y - hiWater
		for x := 0; x < hiQuay; x++ {
			ripple := math.Sin(float64(d)*0.9+s*5+float64(x)*0.04) * (0.6 + float64(d)*0.12)
			v := at(x+int(math.Round(ripple)), hiWater-1-d)
			c.set(x, y, mix(waterAt(y), v, 0.55*(1-float64(d)/30)))
		}
	}
	// Crests along the waterline.
	for x := 0; x < hiQuay; x++ {
		if math.Sin(float64(x)*0.21+s*4) > 0.55 {
			c.set(x, hiWater, rgb{136, 187, 238})
		} else {
			c.blend(x, hiWater, rgb{68, 119, 204}, 0.7)
		}
	}
}

// drawHiSplashes throws droplets up where the octopus breaks the surface
// and where its tentacle slaps the water, and spreads rings on the water.
func drawHiSplashes(c *canvas, s float64) {
	tx, _, _ := tentacleTip(swipeTo)
	for _, sp := range []struct{ x, at float64 }{
		{float64(octopusX*hk) + 14, (riseFrom + riseTo) / 2},
		{tx*hk + 2, swipeTo},
	} {
		t := s - sp.at
		if t < 0 {
			continue
		}
		if t < 0.9 {
			for i := 0; i < 22; i++ {
				vx := (hash(sp.at*10+float64(i)) - 0.5) * 50
				vy := 30 + hash(sp.at*10+float64(i)+30)*40
				x := sp.x + vx*t
				y := float64(hiWater) - (vy*t - 70*t*t)
				if y <= float64(hiWater) {
					v := rgb{255, 255, 255}
					if i%3 == 0 {
						v = rgb{136, 204, 255}
					}
					c.set(int(x), int(y), v)
				}
			}
		}
		if t < 1.4 { // rings, wide and flat on the water
			r := t * 28
			for a := 0.0; a < math.Pi; a += 0.02 {
				x := sp.x + math.Cos(a)*r
				y := float64(hiWater) + 1 + math.Sin(a)*r*0.12
				if int(x) < hiQuay {
					c.blend(int(x), int(y), rgb{153, 204, 255}, 0.6*(1-t/1.4))
				}
			}
		}
	}
}

// The chrome gradient of the letters, top to bottom, with a horizon line.
func chrome(u float64) rgb {
	switch {
	case u < 0.48:
		return mix(rgb{238, 255, 255}, rgb{102, 170, 238}, u/0.48)
	case u < 0.56:
		return rgb{51, 34, 102}
	}
	return mix(rgb{187, 119, 204}, rgb{255, 221, 238}, (u-0.56)/0.44)
}

func drawHiWord(c *canvas, s float64) {
	const scale, pitch = 3, 18
	// After the bounce, the word waves for a moment, like a sine scroller.
	wave := smooth((s-1.4)/0.4) * (1 - smooth((s-2.8)/0.4))
	for i, r := range word {
		glyph := font[r]
		off := letterOffsetF(s, i)*hk + 3*wave*math.Sin(s*6+float64(i)*0.7)
		sag, tilt := 0.0, 0.0
		if i == looseLetter {
			sg, tl := letterSag(s)
			sag, tilt = float64(sg*hk), float64(tl*hk)
		}
		x0 := float64(wordX*hk + i*pitch)
		y0 := float64(wordY*hk) + off + sag
		filled := func(gx, gy int) bool {
			if gx < 0 || gy < 0 || gx >= 5*scale || gy >= 7*scale {
				return false
			}
			return glyph[gy/scale][gx/scale] == 'x'
		}
		pos := func(gx, gy int) (int, int) {
			shear := tilt * float64(gx) / float64(5*scale)
			return int(x0) + gx, int(math.Round(y0 + float64(gy) + shear))
		}
		for _, layer := range []int{0, 1, 2} { // shadow, outline, fill
			for gy := -1; gy <= 7*scale; gy++ {
				for gx := -1; gx <= 5*scale; gx++ {
					x, y := pos(gx, gy)
					switch layer {
					case 0:
						if filled(gx, gy) {
							c.blend(x+2, y+2, rgb{0, 0, 17}, 0.8)
						}
					case 1:
						if !filled(gx, gy) && (filled(gx-1, gy) || filled(gx+1, gy) || filled(gx, gy-1) || filled(gx, gy+1)) {
							c.set(x, y, rgb{0, 0, 0})
						}
					case 2:
						if filled(gx, gy) {
							c.set(x, y, chrome(float64(gy)/float64(7*scale)))
						}
					}
				}
			}
		}
	}
}
