package tui

import (
	"image"
	"image/png"
	"os"
	"testing"
	"time"
)

// TestWritePreview writes a strip of frames to $SCENE_PREVIEW, scaled up,
// for looking at the scene. Skipped unless the variable is set.
func TestWritePreview(t *testing.T) {
	out := os.Getenv("SCENE_PREVIEW")
	if out == "" {
		t.Skip("SCENE_PREVIEW not set")
	}
	const scale, n = 6, 13
	from, to := time.Duration(0), SceneLength
	if v, err := time.ParseDuration(os.Getenv("SCENE_PREVIEW_FROM")); err == nil {
		from = v
	}
	if v, err := time.ParseDuration(os.Getenv("SCENE_PREVIEW_TO")); err == nil {
		to = v
	}
	strip := image.NewRGBA(image.Rect(0, 0, SceneW*scale, (SceneH*scale+4)*n))
	for i := 0; i < n; i++ {
		f := Frame(from + time.Duration(i)*(to-from)/(n-1))
		for y := 0; y < SceneH*scale; y++ {
			for x := 0; x < SceneW*scale; x++ {
				strip.Set(x, i*(SceneH*scale+4)+y, f.At(x/scale, y/scale))
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
