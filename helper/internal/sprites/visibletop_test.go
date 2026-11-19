package sprites

import (
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"math"
	"testing"
)

// side10 builds the 10x10 test image of the Swift tests from per-pixel opaque flags; row 0 is the top.
func side10(opaque func(row, column int) bool) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for row := 0; row < 10; row++ {
		for col := 0; col < 10; col++ {
			if opaque(row, col) {
				m.Pix[row*m.Stride+col*4], m.Pix[row*m.Stride+col*4+3] = 255, 255
			}
		}
	}
	return m
}

func near(t *testing.T, got, want float64, what string) {
	t.Helper()
	if math.Abs(got-want) > 0.001 {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// FloatingPetPettingTests.swift:179-186 testAnchorIsTheMedianFrame.
func TestAnchorIsTheMedianFrame(t *testing.T) {
	// Rotom-Heat-like: most frames start ~0.3 down the canvas, a few jump to the top.
	near(t, anchorFraction([]float64{0, 0, 0.3, 0.3, 0.3, 0.3, 0.3, 0.3, 0.3}), 0.3, "median of 9")
	near(t, anchorFraction([]float64{0.2}), 0.2, "single frame")
	near(t, anchorFraction(nil), 0, "no frames")
	// Derived: sorted(values)[count/2] is the UPPER median for an even count, never the minimum.
	near(t, anchorFraction([]float64{0.1, 0.5, 0.2, 0.4}), 0.4, "even count")
	in := []float64{0.9, 0.1}
	anchorFraction(in)
	if in[0] != 0.9 {
		t.Error("anchorFraction sorted its input")
	}
}

// FloatingPetPettingTests.swift:188-203 testOpaqueTopFractionScansTheFirstVisibleRow.
func TestOpaqueTopFractionScansTheFirstVisibleRow(t *testing.T) {
	// One opaque pixel on row 3, column 4 (inside the central band 3..5).
	m := side10(func(row, col int) bool { return row == 3 && col == 4 })
	near(t, openFrameTop(m), 0.3, "row 3")
}

// FloatingPetPettingTests.swift:205-220 testSideColumnsDoNotDecideTheAnchor.
func TestSideColumnsDoNotDecideTheAnchor(t *testing.T) {
	// Tall side columns (raised arms) from row 0; the centre (horn/head) only starts at row 5.
	m := side10(func(row, col int) bool {
		if col == 0 || col == 9 {
			return true
		}
		return col >= 4 && col <= 5 && row >= 5
	})
	near(t, openFrameTop(m), 0.5, "centre from row 5")
}

// FloatingPetPettingTests.swift:222-226 testEmptyCentralBandFallsBackToTheFullWidth.
func TestEmptyCentralBandFallsBackToTheFullWidth(t *testing.T) {
	m := side10(func(row, col int) bool { return (col == 0 || col == 9) && row >= 2 })
	near(t, openFrameTop(m), 0.2, "sides from row 2")
}

// Derived from the protocol §8.2 algorithm: alpha > 16 is opaque (16 is not), the band is
// [int(w*0.35), max(lo+1, int(w*0.65))), and a transparent frame gives 0.
func TestOpaqueTopEdges(t *testing.T) {
	faint := side10(func(row, col int) bool { return false })
	faint.Pix[4*faint.Stride+4*4+3] = 16 // not above the threshold
	near(t, openFrameTop(faint), 0, "alpha 16 is transparent")
	faint.Pix[4*faint.Stride+4*4+3] = 17
	near(t, openFrameTop(faint), 0.4, "alpha 17 is visible")

	// Band edges for w = 10: columns 3, 4, 5. Columns 2 and 6 are outside it, so the empty band falls
	// back to the full width, where the column-9 pixel on row 0 decides.
	for col, want := range map[int]float64{2: 0, 3: 0.2, 5: 0.2, 6: 0} {
		m := side10(func(row, c int) bool { return c == col && row >= 2 || c == 9 })
		near(t, openFrameTop(m), want, "column "+string(rune('0'+col)))
	}
	// w = 1: lo = 0, hi = max(1, 0) = 1, so the single column is scanned.
	one := image.NewRGBA(image.Rect(0, 0, 1, 4))
	one.Pix[2*one.Stride+3] = 255
	near(t, openFrameTop(one), 0.5, "one pixel wide")
	near(t, openFrameTop(image.NewRGBA(image.Rect(0, 0, 0, 0))), 0, "empty canvas")
}

// Derived: a real GIF through buildFrames. Frames 3x23 (w=4 would make the band 1..2): the sprite
// starts on row 3 in most frames, so the median is 3/23 = 0.1304 as in the protocol's example.
func TestManifestCarriesVisibleTop(t *testing.T) {
	g := &gif.GIF{Config: image.Config{ColorModel: palette, Width: 4, Height: 23}}
	// Disposal Background clears each frame, so the frames do not pile up on one canvas.
	for _, start := range []int{0, 3, 3} {
		f := paletted(image.Rect(0, 0, 4, 23), transparent, nil)
		for y := start; y < 23; y++ {
			f.Set(1, y, red)
		}
		g.Image = append(g.Image, f)
		g.Delay = append(g.Delay, 10)
		g.Disposal = append(g.Disposal, gif.DisposalBackground)
	}
	_, m, _, err := buildFrames(roundTrip(t, g))
	if err != nil {
		t.Fatal(err)
	}
	if m.VisibleTopFraction == nil {
		t.Fatal("visibleTopFraction missing from a new manifest")
	}
	if *m.VisibleTopFraction != 0.1304 {
		t.Fatalf("visibleTopFraction = %v, want 0.1304 (4 decimals)", *m.VisibleTopFraction)
	}
	b, _ := json.Marshal(m)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	if raw["visibleTopFraction"] != 0.1304 {
		t.Errorf("manifest JSON = %s", b)
	}
}

// Derived: a sprite that fills its canvas writes 0 (the key stays), and a manifest cached by an M1
// helper reads back without the field, so it is never migrated.
func TestVisibleTopZeroIsWrittenAndOldManifestsStayAbsent(t *testing.T) {
	full := paletted(image.Rect(0, 0, 4, 3), color.RGBA{255, 0, 0, 255}, nil)
	_, m, _, err := buildFrames(roundTrip(t, &gif.GIF{Image: []*image.Paletted{full}, Delay: []int{10},
		Config: image.Config{ColorModel: palette, Width: 4, Height: 3}}))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(m)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	if v, ok := raw["visibleTopFraction"]; !ok || v != 0.0 {
		t.Errorf("a full-canvas sprite must write visibleTopFraction 0, got %s", b)
	}
	var old Manifest
	if err := json.Unmarshal([]byte(`{"width":4,"height":3,"animated":false,"rgba":"frames.rgba","frames":[{"offset":0,"delayMs":100}]}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.VisibleTopFraction != nil {
		t.Errorf("an M1 manifest read back with visibleTopFraction %v", *old.VisibleTopFraction)
	}
	ob, _ := json.Marshal(old)
	if string(ob) != `{"width":4,"height":3,"animated":false,"rgba":"frames.rgba","frames":[{"offset":0,"delayMs":100}]}` {
		t.Errorf("an old manifest re-marshals with the field: %s", ob)
	}
}
