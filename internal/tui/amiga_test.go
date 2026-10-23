package tui

import (
	"bytes"
	"testing"
	"time"
)

func TestAmigaScene(t *testing.T) {
	last := FrameHi(SceneLength)
	if last.Bounds().Dx() != HiW || last.Bounds().Dy() != HiH {
		t.Fatalf("size %v", last.Bounds())
	}
	// Every channel is one of the Amiga's 16 levels.
	for i, v := range last.Pix {
		if v%17 != 0 {
			t.Fatalf("byte %d is %d, not a 12-bit colour", i, v)
		}
	}
	if !bytes.Equal(last.Pix, FrameHi(SceneLength+time.Hour).Pix) {
		t.Error("the scene keeps changing after its end")
	}
	if bytes.Equal(FrameHi(time.Second).Pix, FrameHi(1040*time.Millisecond).Pix) {
		t.Error("the scene does not move between frames")
	}
	// All windows lit at the end, some dark at the start.
	first := FrameHi(0)
	dark := 0
	for _, w := range hiWindows() {
		h := hiHouses[w[0]]
		x := hiHouse + w[0]*20 + 3 + w[1]*9 + 2
		y := hiWater - 1 - h.height + h.gable + 3 + w[2]*7 + 2
		if c := last.RGBAAt(x, y); c.R < 200 || c.B > 200 {
			t.Errorf("window %v is not lit at the end: %v", w, c)
		}
		if c := first.RGBAAt(x, y); c.R < 100 {
			dark++
		}
	}
	if dark == 0 || dark == len(hiWindows()) {
		t.Errorf("%d of %d windows dark at the start; want some", dark, len(hiWindows()))
	}
	// The moon is reflected: the water under it is brighter than beside it.
	bright := func(x int) int {
		sum := 0
		for y := hiWater + 3; y < hiWater+12; y++ {
			c := last.RGBAAt(x, y)
			sum += int(c.R) + int(c.G) + int(c.B)
		}
		return sum
	}
	if bright(168) <= bright(120) {
		t.Errorf("no reflection of the moon: %d under it, %d beside it", bright(168), bright(120))
	}
}
