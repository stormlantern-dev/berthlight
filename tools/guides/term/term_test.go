package term

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestTextAndColours(t *testing.T) {
	tm := New(20, 3)
	tm.Write([]byte("ab\x1b[1;31mC\x1b[0m\r\nde"))
	if got := string([]rune{tm.Cells[0][0].R, tm.Cells[0][1].R, tm.Cells[0][2].R}); got != "abC" {
		t.Errorf("row 0: %q", got)
	}
	if c := tm.Cells[0][2]; !c.Bold || c.FG != ANSI[1] {
		t.Errorf("C: %+v", c)
	}
	if tm.Cells[1][1].R != 'e' {
		t.Error("second line")
	}
	// Moving up a line and clearing from there, as status --watch redraws:
	// both lines go.
	tm.Write([]byte("\x1b[1F\x1b[J"))
	if tm.Cells[0][0].R != ' ' || tm.Cells[1][1].R != ' ' {
		t.Error("erase below")
	}
	// Scrolling.
	tm.Write([]byte("\n\n\nlast"))
	if tm.Cells[2][0].R != 'l' {
		t.Error("scroll")
	}
}

func TestTitleAndNotifications(t *testing.T) {
	tm := New(20, 3)
	var got []string
	tm.Notified = func(n Notification) { got = append(got, n.Text) }
	tm.Write([]byte("\x1b[22;0t\x1b]2;berthlight · 3/3 ok\x07\x1b]9;berthlight: shop.localhost is up\x07\x1b]9;4;3;0\x07"))
	if tm.Title != "berthlight · 3/3 ok" {
		t.Errorf("title %q", tm.Title)
	}
	tm.Write([]byte("\x1b[23;0t"))
	if tm.Title != "" {
		t.Errorf("title not restored: %q", tm.Title)
	}
	if len(got) != 1 || got[0] != "berthlight: shop.localhost is up" {
		t.Errorf("notifications %q (progress must not count)", got)
	}
}

func TestKittyImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	png.Encode(&b, img)
	data := base64.StdEncoding.EncodeToString(b.Bytes())
	tm := New(10, 5)
	tm.Write([]byte("\r\n\x1b7\x1b[1A"))
	half := len(data) / 2
	// Split across chunks and across writes, as a pty delivers it.
	seq := "\x1b_Ga=T,f=100,i=7,p=1,c=4,r=2,C=1,q=2,m=1;" + data[:half] + "\x1b\\\x1b_Gm=0;" + data[half:] + "\x1b\\\x1b8"
	tm.Write([]byte(seq[:20]))
	tm.Write([]byte(seq[20:]))
	im, ok := tm.Images["7/1"]
	if !ok || im.X != 0 || im.Y != 0 || im.Cols != 4 || im.Rows != 2 {
		t.Fatalf("image %+v", im)
	}
	if r, _, _, _ := im.Img.At(0, 0).RGBA(); r>>8 != 255 {
		t.Error("pixels")
	}
	// Placing again with the same ids replaces, not adds.
	tm.Write([]byte(strings.Replace(seq, "\x1b8", "", 1)))
	if len(tm.Images) != 1 {
		t.Errorf("%d images", len(tm.Images))
	}
}
