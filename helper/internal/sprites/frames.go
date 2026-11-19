package sprites

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
)

// File names inside an animated sprite directory.
const (
	ManifestFile = "manifest.json"
	BlobFile     = "frames.rgba"
	FirstFrame   = "frame-000.png"
)

// Manifest describes an animated sprite directory, see docs/protocol.md §8.2.
type Manifest struct {
	Width    int             `json:"width"`
	Height   int             `json:"height"`
	Animated bool            `json:"animated"`
	RGBA     string          `json:"rgba"`
	Frames   []ManifestFrame `json:"frames"`
	// VisibleTopFraction (M2) is where the visible sprite starts, as a share of Height. A pointer
	// so 0 is written for a sprite that fills its canvas, while a manifest cached by an M1 helper,
	// which never had the field, reads back absent. Old manifests are immutable and not migrated.
	VisibleTopFraction *float64 `json:"visibleTopFraction,omitempty"`
}

// ManifestFrame locates one frame in the RGBA blob.
type ManifestFrame struct {
	// Offset is the byte offset of the frame; each frame is Width*Height*4 bytes.
	Offset  int `json:"offset"`
	DelayMs int `json:"delayMs"`
}

// buildFrames composites a decoded GIF into the blob, manifest and first frame.
// The blob is non-premultiplied RGBA8888 with a row stride of Width*4.
func buildFrames(g *gif.GIF) (blob []byte, m Manifest, first *image.NRGBA, err error) {
	w, h := g.Config.Width, g.Config.Height
	if w <= 0 || h <= 0 || len(g.Image) == 0 {
		return nil, m, nil, errors.New("sprites: empty GIF")
	}
	frames := composite(g)
	size := w * h * 4
	blob = make([]byte, 0, size*len(frames))
	m = Manifest{Width: w, Height: h, Animated: len(frames) > 1, RGBA: BlobFile}
	for _, f := range frames {
		m.Frames = append(m.Frames, ManifestFrame{Offset: len(blob), DelayMs: f.DelayMs})
		blob = append(blob, unpremultiply(f.Image)...)
	}
	top := visibleTopFraction(frames)
	m.VisibleTopFraction = &top
	first = &image.NRGBA{Pix: blob[:size:size], Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
	return blob, m, first, nil
}

// unpremultiply converts image.RGBA pixels to non-premultiplied RGBA bytes.
// GIF alpha is 0 or 255, so this is a plain copy for real sprites.
func unpremultiply(m *image.RGBA) []byte {
	out := make([]byte, len(m.Pix))
	copy(out, m.Pix)
	for i := 0; i < len(out); i += 4 {
		a := uint32(out[i+3])
		if a == 0 || a == 255 {
			continue
		}
		for c := 0; c < 3; c++ {
			out[i+c] = byte(min(uint32(out[i+c])*255/a, 255))
		}
	}
	return out
}

// writeAnimatedDir decodes gifData and publishes sprites/<key>/ atomically:
// everything is written into a temp directory (manifest last) which is then
// renamed into place.
func writeAnimatedDir(spritesDir, key string, gifData []byte) error {
	g, err := gif.DecodeAll(bytes.NewReader(gifData))
	if err != nil {
		return fmt.Errorf("sprites: decode GIF %s: %w", key, err)
	}
	blob, m, first, err := buildFrames(g)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(spritesDir, ".tmp-"+key+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) // no-op after a successful rename
	if err := os.WriteFile(filepath.Join(tmp, BlobFile), blob, 0o644); err != nil {
		return err
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, first); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, FirstFrame), pngBuf.Bytes(), 0o644); err != nil {
		return err
	}
	mb, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(tmp, ManifestFile), mb); err != nil {
		return err
	}
	final := filepath.Join(spritesDir, key)
	if err := os.Rename(tmp, final); err != nil {
		// A directory without manifest is a leftover; one with a manifest is complete.
		if _, serr := os.Stat(filepath.Join(final, ManifestFile)); serr == nil {
			return nil
		}
		if err := os.RemoveAll(final); err != nil {
			return err
		}
		return os.Rename(tmp, final)
	}
	return nil
}

func readManifest(dir string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

// writeFileAtomic writes data to path through a temp file and a rename.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(f.Name())
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		os.Remove(f.Name())
		return err
	}
	return nil
}
