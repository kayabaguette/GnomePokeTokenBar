package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
)

var (
	transparent = color.RGBA{}
	blue        = color.RGBA{0, 0, 255, 255}
	red         = color.RGBA{255, 0, 0, 255}
	green       = color.RGBA{0, 255, 0, 255}
	yellow      = color.RGBA{255, 255, 0, 255}
	palette     = color.Palette{transparent, blue, red, green, yellow}
)

// paletted builds a frame over rect, filled with fill, then applies the
// per-pixel overrides.
func paletted(rect image.Rectangle, fill color.Color, px map[image.Point]color.Color) *image.Paletted {
	m := image.NewPaletted(rect, palette)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			m.Set(x, y, fill)
		}
	}
	for p, c := range px {
		m.Set(p.X, p.Y, c)
	}
	return m
}

// roundTrip encodes g and decodes it back, so the test goes through the real
// image/gif decoder (transparent index, disposal and delay fields).
func roundTrip(t *testing.T, g *gif.GIF) *gif.GIF {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := gif.DecodeAll(&buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// A 4-frame GIF: the last frame's disposal never shows in any emitted frame,
// so every disposal mode under test needs a successor frame to be observable.
//
//	F0 full canvas blue,        disposal None       -> kept for F1
//	F1 red 2x2 at (1,1),        disposal Background -> its rect is cleared for F2
//	F2 green (0,0),(1,1) with transparent (1,0),(0,1), disposal Previous
//	                                                -> F3 sees the canvas from before F2
//	F3 yellow 1x1 at (3,3),     disposal unspecified
func TestCompositeDisposalModes(t *testing.T) {
	g := roundTrip(t, &gif.GIF{
		Image: []*image.Paletted{
			paletted(image.Rect(0, 0, 4, 4), blue, nil),
			paletted(image.Rect(1, 1, 3, 3), red, nil),
			paletted(image.Rect(0, 0, 2, 2), green, map[image.Point]color.Color{
				{1, 0}: transparent,
				{0, 1}: transparent,
			}),
			paletted(image.Rect(3, 3, 4, 4), yellow, nil),
		},
		Delay:    []int{0, 1, 5, 10},
		Disposal: []byte{gif.DisposalNone, gif.DisposalBackground, gif.DisposalPrevious, 0},
		Config:   image.Config{ColorModel: palette, Width: 4, Height: 4},
	})

	frames := Composite(g)
	if len(frames) != 4 {
		t.Fatalf("frames = %d, want 4", len(frames))
	}

	// Delays: 0 cs and 1 cs (10 ms) fall under 20 ms and become 100 ms.
	wantDelays := []int{100, 100, 50, 100}
	for i, f := range frames {
		if f.DelayMs != wantDelays[i] {
			t.Errorf("frame %d delay = %d ms, want %d", i, f.DelayMs, wantDelays[i])
		}
		if b := f.Image.Bounds(); b != image.Rect(0, 0, 4, 4) {
			t.Errorf("frame %d bounds = %v, want the full 4x4 canvas", i, b)
		}
	}

	// '.' transparent, B blue, R red, G green, Y yellow; rows top to bottom.
	want := [][]string{
		{ // F0: full blue.
			"BBBB",
			"BBBB",
			"BBBB",
			"BBBB",
		},
		{ // F1: F0 kept (None), red drawn on top.
			"BBBB",
			"BRRB",
			"BRRB",
			"BBBB",
		},
		{ // F2: F1 rect cleared (Background); transparent index keeps the
			// blue under (1,0) and (0,1); green lands on the cleared (1,1).
			"GBBB",
			"BG.B",
			"B..B",
			"BBBB",
		},
		{ // F3: F2 undone (Previous): (0,0) is blue again, not green (keep)
			// nor transparent (clear); (1,1) is back to the cleared state.
			"BBBB",
			"B..B",
			"B..B",
			"BBBY",
		},
	}
	key := map[byte]color.RGBA{'.': transparent, 'B': blue, 'R': red, 'G': green, 'Y': yellow}
	for i, rows := range want {
		for y, row := range rows {
			for x := range len(row) {
				got := frames[i].Image.RGBAAt(x, y)
				if exp := key[row[x]]; got != exp {
					t.Errorf("frame %d pixel (%d,%d) = %v, want %v (%q)", i, x, y, got, exp, row[x])
				}
			}
		}
	}
}

func TestRunSingleFrameIsNotAnimated(t *testing.T) {
	g := &gif.GIF{
		Image:  []*image.Paletted{paletted(image.Rect(0, 0, 3, 2), red, nil)},
		Delay:  []int{0},
		Config: image.Config{ColorModel: palette, Width: 3, Height: 2},
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "one.gif")
	f, err := os.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := gif.EncodeAll(f, g); err != nil {
		t.Fatal(err)
	}
	f.Close()

	out := filepath.Join(dir, "frames")
	if err := run(in, out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.Animated || len(m.Frames) != 1 || m.Width != 3 || m.Height != 2 {
		t.Fatalf("manifest = %+v, want 3x2, 1 frame, animated=false", m)
	}
	if m.Frames[0].File != "frame-000.png" || m.Frames[0].DelayMs != 100 {
		t.Fatalf("frame = %+v, want frame-000.png at 100 ms", m.Frames[0])
	}
	if _, err := os.Stat(filepath.Join(out, "frame-000.png")); err != nil {
		t.Fatal(err)
	}
}
