package tui

import (
	"image"
	"image/png"
	"os"
	"testing"
	"time"
)

// TestWriteHiPreview writes frames of the Amiga scene to $HI_PREVIEW,
// doubled, between $HI_PREVIEW_FROM and $HI_PREVIEW_TO (default the whole
// scene). Skipped unless the variable is set.
func TestWriteHiPreview(t *testing.T) {
	out := os.Getenv("HI_PREVIEW")
	if out == "" {
		t.Skip("HI_PREVIEW not set")
	}
	const scale, n = 2, 7
	from, to := time.Duration(0), SceneLength
	if v, err := time.ParseDuration(os.Getenv("HI_PREVIEW_FROM")); err == nil {
		from = v
	}
	if v, err := time.ParseDuration(os.Getenv("HI_PREVIEW_TO")); err == nil {
		to = v
	}
	strip := image.NewRGBA(image.Rect(0, 0, HiW*scale, (HiH*scale+4)*n))
	for i := 0; i < n; i++ {
		f := FrameHi(from + time.Duration(i)*(to-from)/(n-1))
		for y := 0; y < HiH*scale; y++ {
			for x := 0; x < HiW*scale; x++ {
				strip.Set(x, i*(HiH*scale+4)+y, f.At(x/scale, y/scale))
			}
		}
	}
	w, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	png.Encode(w, strip)
}
