package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSceneEndsAtRest(t *testing.T) {
	if word[looseLetter] != 'e' {
		t.Error("the loose letter is not the e")
	}
	if sag, tilt := letterSag(sagFrom - 0.01); sag != 0 || tilt != 0 {
		t.Error("the e sags before the lights come on")
	}
	if sag, tilt := letterSag(SceneLength.Seconds()); sag != 2 || tilt != 1 {
		t.Errorf("at the end the e hangs %d down, tilted %d; want 2 down and crooked", sag, tilt)
	}
	if sagFrom < arriveAt || sagFrom+0.32 > SceneLength.Seconds() {
		t.Error("the e does not sag between the lights coming on and the end")
	}
	for i := range word {
		if off := letterOffset(SceneLength.Seconds(), i); off != 0 {
			t.Errorf("letter %d still %d px off at the end", i, off)
		}
		if off := letterOffset(0, i); off >= 0 {
			t.Errorf("letter %d already in at the start", i)
		}
	}
	if x := boatXAt(arriveAt); x != dockedX {
		t.Errorf("boat at %v when it should be moored at %d", x, dockedX)
	}
	if _, _, out := tentacleTip(0); out {
		t.Error("the tentacle is out at the start")
	}
	if _, _, out := tentacleTip(SceneLength.Seconds()); out {
		t.Error("the tentacle is out at the end")
	}
	last := Frame(SceneLength)
	dark, lit := 0, 0
	for k, house := range houses {
		x, y := houseAt(k)
		for sy, row := range house {
			for wx := range len(row) {
				if row[wx] != 'L' {
					continue
				}
				if last.RGBAAt(x+wx, y+sy) != c64['y'] {
					t.Errorf("house %d's window (%d, %d) is dark at the end", k, wx, sy)
				}
				if Frame(0).RGBAAt(x+wx, y+sy) == c64['y'] {
					lit++
				} else {
					dark++
				}
			}
		}
		if x+len(house[0]) > SceneW || y < 0 {
			t.Errorf("house %d does not fit", k)
		}
	}
	if lit == 0 || dark == 0 {
		t.Errorf("at the start %d windows are lit and %d dark; want some of each", lit, dark)
	}
	heights := map[int]bool{}
	for _, h := range houses {
		heights[len(h)] = true
	}
	if len(heights) != len(houses) {
		t.Error("the houses are not of different heights")
	}
	// After SceneLength the frame does not change, so a stopped header
	// and a static one look the same.
	if !bytes.Equal(Frame(SceneLength).Pix, Frame(SceneLength+time.Hour).Pix) {
		t.Error("the scene keeps changing after its end")
	}
	if bytes.Equal(Frame(time.Second).Pix, Frame(2*time.Second).Pix) {
		t.Error("the scene does not move")
	}
}

func TestHalfBlocks(t *testing.T) {
	out := HalfBlocks(Frame(SceneLength))
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != HeaderRows {
		t.Fatalf("%d rows, want %d", len(lines), HeaderRows)
	}
	plain := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	for i, l := range lines {
		if got := plain.ReplaceAllString(l, ""); got != strings.Repeat("▀", SceneW) {
			t.Errorf("row %d: %q", i, got)
		}
		if !strings.HasSuffix(l, "\x1b[0m") {
			t.Errorf("row %d does not reset its colours", i)
		}
	}
}

// TestKittyImage checks the command against the Kitty graphics protocol:
// the keys of the first chunk, chunks of at most 4096 bytes, m=1 on all but
// the last, and a PNG payload that is the scene, enlarged pixel for pixel,
// for both the chunky and the Amiga scene.
func TestKittyImage(t *testing.T) {
	for _, img := range []*image.RGBA{Frame(2 * time.Second), FrameHi(2 * time.Second)} {
		out := KittyImage(img, 7)
		chunks := regexp.MustCompile(`\x1b_G([^;]*);([^\x1b]*)\x1b\\`).FindAllStringSubmatch(out, -1)
		if len(chunks) < 2 {
			t.Fatalf("%d chunks", len(chunks))
		}
		if want := fmt.Sprintf("a=T,f=100,i=7,p=1,c=%d,r=%d,C=1,q=2,m=1", SceneW, HeaderRows); chunks[0][1] != want {
			t.Errorf("first chunk keys %q, want %q", chunks[0][1], want)
		}
		var data string
		for i, c := range chunks {
			if len(c[2]) > 4096 {
				t.Errorf("chunk %d is %d bytes", i, len(c[2]))
			}
			want := "m=1"
			if i == len(chunks)-1 {
				want = "m=0"
			}
			if !strings.HasSuffix(c[1], want) {
				t.Errorf("chunk %d keys %q, want %s", i, c[1], want)
			}
			data += c[2]
		}
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			t.Fatal(err)
		}
		got, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if got.Bounds().Dx() != kittyWidth || got.Bounds().Dy() != kittyWidth*SceneH/SceneW {
			t.Fatalf("PNG is %v, want %d wide", got.Bounds(), kittyWidth)
		}
		scale := kittyWidth / img.Bounds().Dx()
		for _, p := range [][2]int{{0, 0}, {img.Bounds().Dx() / 2, img.Bounds().Dy() / 2}, {img.Bounds().Dx() - 1, img.Bounds().Dy() - 1}} {
			r1, g1, b1, _ := got.At(p[0]*scale+scale/2, p[1]*scale+scale/2).RGBA()
			want := img.RGBAAt(p[0], p[1])
			if uint8(r1>>8) != want.R || uint8(g1>>8) != want.G || uint8(b1>>8) != want.B {
				t.Errorf("pixel %v: %v %v %v, want %v", p, r1>>8, g1>>8, b1>>8, want)
			}
		}
	}
}

// testHeader is an animating header writing to a buffer, as StartHeader
// makes one for a Kitty terminal.
func testHeader(buf io.Writer) *Header {
	h := &Header{w: buf, mode: modeKitty, id: 9, start: time.Now(), frame: Frame, stop: make(chan struct{}), done: make(chan struct{})}
	go h.animate()
	return h
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestHeaderStopsAtFirstOutput(t *testing.T) {
	var buf lockedBuffer
	h := testHeader(&buf)
	time.Sleep(3 * frameInterval)
	out := h.Wrap(&buf)
	io.WriteString(out, "NAME  URL\n")
	frames := strings.Count(buf.String(), "\x1b_Ga=T")
	if frames < 2 {
		t.Errorf("%d frames before the output", frames)
	}
	// The last frame comes right before the output, and nothing after.
	s := buf.String()
	if !strings.HasSuffix(s, "\x1b8\x1b[?2026lNAME  URL\n") {
		t.Errorf("output does not follow the last frame: %q", s[max(0, len(s)-80):])
	}
	time.Sleep(3 * frameInterval)
	if strings.Count(buf.String(), "\x1b_Ga=T") != frames {
		t.Error("the header kept drawing after the output")
	}
	h.Stop() // again: nothing happens
	if strings.Count(buf.String(), "\x1b_Ga=T") != frames {
		t.Error("a second Stop drew again")
	}
	var nilHeader *Header
	nilHeader.Stop()
	nilHeader.Print("x")
	if nilHeader.Wrap(&buf) != io.Writer(&buf) {
		t.Error("a nil header wraps")
	}
}

func TestHeaderPrintKeepsAnimating(t *testing.T) {
	var buf lockedBuffer
	h := testHeader(&buf)
	h.Print("usage line 1\nusage line 2\n")
	time.Sleep(3 * frameInterval)
	h.Stop()
	// Frames drawn after the text move up past it: header rows plus two.
	if !strings.Contains(buf.String(), fmt.Sprintf("\x1b7\x1b[%dA\r\x1b_G", HeaderRows+2)) {
		t.Errorf("no frame drawn above the printed text")
	}
}

// TestStory checks the scene tells its story, every 10 ms: the octopus
// stays under water until the boat has passed it, then surfaces; its
// tentacle never touches the boat; it ends with only its eyes above the
// water, and the boat moored on the waterline short of the pier.
func TestStory(t *testing.T) {
	surfaced := false
	for ms := 0; ms <= int(SceneLength/time.Millisecond); ms += 10 {
		s := float64(ms) / 1000
		bx, by := boatPos(s)
		if by != boatY {
			t.Fatalf("at %.2fs the boat is at row %d; it should stay level at %d", s, by, boatY)
		}
		top := octopusTop(s)
		if top < waterY {
			if !surfaced && bx < octopusX+len(octopusHead[0]) {
				t.Fatalf("at %.2fs the octopus surfaces before the boat (column %d) has passed it", s, bx)
			}
			surfaced = true
		}
		if tx, ty, out := tentacleTip(s); out {
			x, y := int(math.Round(tx)), int(math.Round(ty))
			if x >= bx && x < bx+len(boat[0]) && y >= by && y < by+len(boat) {
				t.Fatalf("at %.2fs the tentacle hits the boat", s)
			}
		}
	}
	if !surfaced {
		t.Error("the octopus never surfaces")
	}
	end := SceneLength.Seconds()
	if top := octopusTop(end); top+2 >= waterY || top+3 < waterY {
		t.Errorf("at the end the octopus's top is row %d; only its eyes (row 2) should be above the waterline %d", top, waterY)
	}
	bx, by := boatPos(end)
	if bx != dockedX || by+len(boat)-1 != waterY || bx+len(boat[0]) > jettyX {
		t.Errorf("the boat ends at (%d, %d), not moored on the waterline before the pier", bx, by)
	}
}
