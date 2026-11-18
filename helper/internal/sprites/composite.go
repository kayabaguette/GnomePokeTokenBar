package sprites

import (
	"image"
	"image/draw"
	"image/gif"
)

// frame is one fully composited frame of the logical GIF canvas.
type frame struct {
	Image   *image.RGBA
	DelayMs int
}

// minDelayMs and fallbackDelayMs follow the browser convention used by the
// macOS app (SpriteAnimation.swift): anything under 20 ms plays at 100 ms.
const (
	minDelayMs      = 20
	fallbackDelayMs = 100
)

// composite renders every GIF frame onto the logical canvas
// (Config.Width x Config.Height), honouring disposal and transparency, and
// returns one full RGBA image per frame.
//
// Disposal is applied after the frame is emitted:
//   - unspecified (0) / None (1): keep the canvas as drawn;
//   - Background (2): clear the frame's rectangle to transparent;
//   - Previous (3): restore the canvas as it was before the frame was drawn.
func composite(g *gif.GIF) []frame {
	bounds := image.Rect(0, 0, g.Config.Width, g.Config.Height)
	canvas := image.NewRGBA(bounds)
	frames := make([]frame, 0, len(g.Image))

	for i, src := range g.Image {
		disposal := byte(0)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}

		var saved *image.RGBA
		if disposal == gif.DisposalPrevious {
			saved = clone(canvas)
		}

		// Over: transparent palette entries (alpha 0) leave the canvas pixel
		// untouched; GIF palette colours are otherwise fully opaque.
		draw.Draw(canvas, src.Bounds(), src, src.Bounds().Min, draw.Over)

		delay := 0
		if i < len(g.Delay) {
			delay = g.Delay[i] * 10 // centiseconds -> ms
		}
		if delay < minDelayMs {
			delay = fallbackDelayMs
		}
		frames = append(frames, frame{Image: clone(canvas), DelayMs: delay})

		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, src.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			canvas = saved
		}
	}
	return frames
}

func clone(m *image.RGBA) *image.RGBA {
	c := image.NewRGBA(m.Bounds())
	copy(c.Pix, m.Pix)
	return c
}
