package sprites

import (
	"image"
	"math"
	"slices"
)

// visibleAlpha is the alpha above which a pixel counts as part of the sprite.
const visibleAlpha = 16

// openFrameTop is where the first opaque row of one frame sits as a share of its height. It scans
// the central columns [int(w*0.35), max(lo+1, int(w*0.65))) first, so raised arms or a tail on the
// sides do not decide the anchor, and the whole width only when that band is empty. A fully
// transparent frame gives 0 (SpriteVisibleTop.opaqueTopFraction, FloatingPetPettingTests.swift:188-235).
func openFrameTop(m *image.RGBA) float64 {
	w, h := m.Rect.Dx(), m.Rect.Dy()
	if w <= 0 || h <= 0 {
		return 0
	}
	lo := int(float64(w) * 0.35)
	hi := max(lo+1, int(float64(w)*0.65))
	firstRow := func(from, to int) int {
		for y := 0; y < h; y++ {
			for x := from; x < min(to, w); x++ {
				if m.Pix[y*m.Stride+x*4+3] > visibleAlpha {
					return y
				}
			}
		}
		return -1
	}
	row := firstRow(lo, hi)
	if row < 0 {
		row = firstRow(0, w)
	}
	if row < 0 {
		return 0
	}
	return float64(row) / float64(h)
}

// anchorFraction is the median frame, sorted(values)[count/2]: the upper median for an even count.
// Not the minimum: a few frames that reach the canvas top would drag the anchor away
// (SpriteVisibleTop.anchorFraction, FloatingPetPettingTests.swift:179-186). No frames give 0.
func anchorFraction(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	s := slices.Clone(values)
	slices.Sort(s)
	return s[len(s)/2]
}

// visibleTopFraction is the manifest's visibleTopFraction: the median frame's value rounded to
// 4 decimals (docs/protocol.md §8.2).
func visibleTopFraction(frames []frame) float64 {
	values := make([]float64, len(frames))
	for i, f := range frames {
		values[i] = openFrameTop(f.Image)
	}
	return math.Round(anchorFraction(values)*1e4) / 1e4
}
