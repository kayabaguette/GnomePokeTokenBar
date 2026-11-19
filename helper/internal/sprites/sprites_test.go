package sprites

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const swiftBase = "https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon"

func TestURLsAndKeys(t *testing.T) {
	// Forms as the caller computes them (PokemonForm.assetName / staticAssetName).
	sky := Request{ID: 492, Asset: "492-sky", StaticAsset: "10006"}
	land := Request{ID: 492, Asset: "492", StaticAsset: "492"}
	sandy412 := Request{ID: 412, Asset: "412-sandy", StaticAsset: "412-sandy"}
	// Mothim (414): sandy is its default form, so the GIF is the plain "414".
	mothim := Request{ID: 414, Asset: "414", StaticAsset: "414"}
	with := func(r Request, shiny, animated bool) Request { r.Shiny, r.Animated = shiny, animated; return r }

	urls := []struct {
		name string
		req  Request
		want string
	}{
		// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:117
		{"sky static", with(sky, false, false), swiftBase + "/10006.png"},
		// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:119
		{"sky static shiny", with(sky, true, false), swiftBase + "/shiny/10006.png"},
		// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:121
		{"sky gif shiny", with(sky, true, true), swiftBase + "/versions/generation-v/black-white/animated/shiny/492-sky.gif"},
		// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:123
		{"412 sandy static", with(sandy412, false, false), swiftBase + "/412-sandy.png"},
		// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:125
		{"land static", with(land, false, false), swiftBase + "/492.png"},
		// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:127
		{"mothim default gif", with(mothim, false, true), swiftBase + "/versions/generation-v/black-white/animated/414.gif"},
		{"back shiny gif", Request{ID: 25, Animated: true, Back: true, Shiny: true},
			swiftBase + "/versions/generation-v/black-white/animated/back/shiny/25.gif"},
		{"back static", Request{ID: 25, Back: true}, swiftBase + "/back/25.png"},
	}
	for _, c := range urls {
		if got := c.req.URL(); got != c.want {
			t.Errorf("%s: URL = %s, want %s", c.name, got, c.want)
		}
	}

	keys := []struct {
		name string
		req  Request
		want string
	}{
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1950
		{"25-a", Request{ID: 25, Animated: true}, "25-a"},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1951
		{"25-s", Request{ID: 25}, "25-s"},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1952
		{"25-sha", Request{ID: 25, Animated: true, Shiny: true}, "25-sha"},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1953
		{"25-shs", Request{ID: 25, Shiny: true}, "25-shs"},
		// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:133
		{"492-sky-s (asset, not static asset)", with(sky, false, false), "492-sky-s"},
		// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:134
		{"492-sky-sha", with(sky, true, true), "492-sky-sha"},
		// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:135
		{"default form keeps plain key", with(land, false, false), "492-s"},
		// Swift: Tests/PokeTokenBarTests/PokemonFormTests.swift:137
		{"413-sandy-s", Request{ID: 413, Asset: "413-sandy", StaticAsset: "10004"}, "413-sandy-s"},
		{"back", Request{ID: 25, Back: true, Animated: true}, "25-back-a"},
		{"back shiny static", Request{ID: 25, Back: true, Shiny: true}, "25-back-shs"},
	}
	for _, c := range keys {
		if got := c.req.Key(); got != c.want {
			t.Errorf("%s: Key = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestRecoveryDelay(t *testing.T) {
	want := []time.Duration{2, 4, 8, 16, 30, 30, 30}
	for i, w := range want {
		if got := RecoveryDelay(i); got != w*time.Second {
			t.Errorf("RecoveryDelay(%d) = %v, want %v", i, got, w*time.Second)
		}
	}
	if RecoveryDelay(1000) != 30*time.Second || RecoveryDelay(-1) != 2*time.Second {
		t.Error("RecoveryDelay out of range")
	}
}

// ---- test server -----------------------------------------------------------

func gifBytes(t *testing.T, frames int) []byte {
	t.Helper()
	g := &gif.GIF{Config: image.Config{ColorModel: palette, Width: 4, Height: 3}}
	for i := 0; i < frames; i++ {
		fill := color.Color(red)
		if i%2 == 1 {
			fill = blue
		}
		g.Image = append(g.Image, paletted(image.Rect(0, 0, 4, 3), fill, nil))
		g.Delay = append(g.Delay, 5)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pngBytes(t *testing.T, w, h int, paint func(*image.NRGBA)) []byte {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	if paint != nil {
		paint(m)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeServer serves a path -> response table and counts hits per path.
type fakeServer struct {
	*httptest.Server
	mu     sync.Mutex
	routes map[string]func(w http.ResponseWriter)
	hits   map[string]int
}

func newFake(t *testing.T) *fakeServer {
	f := &fakeServer{routes: map[string]func(http.ResponseWriter){}, hits: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits[r.URL.Path]++
		h := f.routes[r.URL.Path]
		f.mu.Unlock()
		if h == nil {
			http.NotFound(w, r)
			return
		}
		h(w)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) serve(path string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[path] = func(w http.ResponseWriter) { w.Write(body) }
}

func (f *fakeServer) status(path string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[path] = func(w http.ResponseWriter) { w.WriteHeader(code) }
}

func (f *fakeServer) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func (f *fakeServer) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, v := range f.hits {
		n += v
	}
	return n
}

func newStore(t *testing.T, f *fakeServer) *Store {
	return New(Options{CacheDir: t.TempDir(), UserAgent: "test", SpriteBase: f.URL + "/pokemon", ItemBase: f.URL + "/items"})
}

const (
	gifPath  = "/pokemon/versions/generation-v/black-white/animated/"
	backGif  = gifPath + "back/"
	shinyGif = gifPath + "shiny/"
)

var bg = context.Background()

// ---- animated pipeline -----------------------------------------------------

func TestGetAnimatedWritesBlobAndManifest(t *testing.T) {
	f := newFake(t)
	f.serve(gifPath+"25.gif", gifBytes(t, 3))
	s := newStore(t, f)

	res, err := s.Get(bg, Request{ID: 25, Animated: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Key != "25-a" || res.Fallback || res.Retry || !res.Animated {
		t.Fatalf("result = %+v", res)
	}
	dir := filepath.Dir(res.Path)
	if filepath.Base(res.Path) != "manifest.json" || filepath.Base(dir) != "25-a" {
		t.Fatalf("path = %s", res.Path)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(filepath.Join(dir, m.RGBA))
	if err != nil {
		t.Fatal(err)
	}
	frameSize := m.Width * m.Height * 4
	if m.Width != 4 || m.Height != 3 || !m.Animated || len(m.Frames) != 3 {
		t.Fatalf("manifest = %+v", m)
	}
	// Offsets and sizes in the manifest must tile the blob exactly.
	if len(blob) != frameSize*len(m.Frames) {
		t.Fatalf("blob %d bytes, manifest says %d x %d", len(blob), len(m.Frames), frameSize)
	}
	for i, fr := range m.Frames {
		if fr.Offset != i*frameSize || fr.DelayMs != 50 {
			t.Errorf("frame %d = %+v, want offset %d delay 50", i, fr, i*frameSize)
		}
	}
	// Frame 0 is red, frame 1 blue, non-premultiplied RGBA8888 with stride w*4.
	if got := blob[m.Frames[0].Offset : m.Frames[0].Offset+4]; !bytes.Equal(got, []byte{255, 0, 0, 255}) {
		t.Errorf("frame 0 pixel 0 = %v", got)
	}
	last := m.Frames[1].Offset + (2*m.Width+3)*4 // last pixel of frame 1
	if got := blob[last : last+4]; !bytes.Equal(got, []byte{0, 0, 255, 255}) {
		t.Errorf("frame 1 last pixel = %v", got)
	}
	// frame-000.png equals frame 0.
	pf, err := os.Open(filepath.Join(dir, "frame-000.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer pf.Close()
	img, err := png.Decode(pf)
	if err != nil {
		t.Fatal(err)
	}
	if c := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA); c != (color.NRGBA{255, 0, 0, 255}) || img.Bounds().Dx() != 4 {
		t.Errorf("frame-000.png (0,0) = %v bounds %v", c, img.Bounds())
	}

	// Second call is served from disk.
	if _, err := s.Get(bg, Request{ID: 25, Animated: true}); err != nil || f.count(gifPath+"25.gif") != 1 {
		t.Fatalf("expected a cache hit: err=%v hits=%d", err, f.count(gifPath+"25.gif"))
	}
	assertNoTemp(t, filepath.Dir(dir))
}

func assertNoTemp(t *testing.T, spritesDir string) {
	t.Helper()
	ents, _ := os.ReadDir(spritesDir)
	for _, e := range ents {
		if len(e.Name()) > 4 && e.Name()[:5] == ".tmp-" {
			t.Errorf("leftover temp entry %s", e.Name())
		}
	}
}

func TestManifestJSONShape(t *testing.T) {
	f := newFake(t)
	f.serve(gifPath+"25.gif", gifBytes(t, 2))
	res, err := newStore(t, f).Get(bg, Request{ID: 25, Animated: true})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(res.Path)
	want := `{"width":4,"height":3,"animated":true,"rgba":"frames.rgba","frames":[{"offset":0,"delayMs":50},{"offset":48,"delayMs":50}],"visibleTopFraction":0}`
	if string(b) != want {
		t.Errorf("manifest = %s\nwant       %s", b, want)
	}
}

func TestSingleFrameGIFIsNotAnimated(t *testing.T) {
	f := newFake(t)
	f.serve(gifPath+"25.gif", gifBytes(t, 1))
	s := newStore(t, f)
	res, err := s.Get(bg, Request{ID: 25, Animated: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Animated || !res.Fallback || res.Retry || res.Key != "25-a" {
		t.Fatalf("result = %+v", res)
	}
	m, _ := readManifest(filepath.Dir(res.Path))
	if m.Animated || len(m.Frames) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestUnpremultiply(t *testing.T) {
	m := image.NewRGBA(image.Rect(0, 0, 3, 1))
	copy(m.Pix, []byte{10, 20, 30, 255, 0, 0, 0, 0, 50, 100, 25, 100})
	got := unpremultiply(m)
	if !bytes.Equal(got[:8], m.Pix[:8]) || got[8] != 127 || got[9] != 255 || got[10] != 63 || got[11] != 100 {
		t.Errorf("unpremultiply = %v", got)
	}
}

func TestFramesBlobHeaderless(t *testing.T) {
	// Guard the stride/offset contract independently of the writer.
	g := roundTrip(t, &gif.GIF{
		Image:  []*image.Paletted{paletted(image.Rect(0, 0, 2, 2), red, nil), paletted(image.Rect(0, 0, 2, 2), green, nil)},
		Delay:  []int{3, 4},
		Config: image.Config{ColorModel: palette, Width: 2, Height: 2},
	})
	blob, m, first, err := buildFrames(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(blob) != 2*2*2*4 || m.Frames[1].Offset != 16 || first.Stride != 8 {
		t.Fatalf("blob %d, frames %+v, stride %d", len(blob), m.Frames, first.Stride)
	}
	if binary.LittleEndian.Uint32(blob[16:20]) != 0xff00ff00 { // R=0 G=255 B=0 A=255 little endian
		t.Errorf("frame 1 first pixel = %x", blob[16:20])
	}
}

// ---- fallbacks -------------------------------------------------------------

func TestShinyFallsBackToPlain(t *testing.T) {
	f := newFake(t)
	f.serve(gifPath+"25.gif", gifBytes(t, 2))
	s := newStore(t, f)
	res, err := s.Get(bg, Request{ID: 25, Animated: true, Shiny: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Key != "25-a" || !res.Fallback || res.Retry || !res.Animated {
		t.Fatalf("result = %+v", res)
	}
	// The 404 is remembered: a repeat does not hit the shiny URL again.
	if _, err := s.Get(bg, Request{ID: 25, Animated: true, Shiny: true}); err != nil {
		t.Fatal(err)
	}
	if n := f.count(shinyGif + "25.gif"); n != 1 {
		t.Errorf("shiny GIF requested %d times, want 1", n)
	}
}

func TestAnimatedFallsBackToStatic(t *testing.T) {
	f := newFake(t)
	f.serve("/pokemon/25.png", pngBytes(t, 8, 8, nil))
	s := newStore(t, f)
	res, err := s.Get(bg, Request{ID: 25, Animated: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Key != "25-s" || !res.Fallback || res.Animated || res.Retry {
		t.Fatalf("result = %+v", res)
	}
	if filepath.Base(res.Path) != "25-s.png" {
		t.Errorf("static must be a plain png, got %s", res.Path)
	}
}

func TestTransientGIFFailureSetsRetry(t *testing.T) {
	f := newFake(t)
	f.status(gifPath+"25.gif", 500)
	f.serve("/pokemon/25.png", pngBytes(t, 8, 8, nil))
	s := newStore(t, f)
	res, err := s.Get(bg, Request{ID: 25, Animated: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Key != "25-s" || !res.Retry || !res.Fallback {
		t.Fatalf("result = %+v", res)
	}
	// The failure is not remembered: when the server recovers, Get serves the GIF.
	f.serve(gifPath+"25.gif", gifBytes(t, 2))
	res, err = s.Get(bg, Request{ID: 25, Animated: true})
	if err != nil || res.Key != "25-a" || !res.Animated || res.Retry {
		t.Fatalf("after recovery: %+v, %v", res, err)
	}
}

func TestEmptyBodyIsAFailure(t *testing.T) {
	f := newFake(t)
	f.serve(gifPath+"25.gif", nil) // 200 with an empty body
	f.serve("/pokemon/25.png", nil)
	s := newStore(t, f)
	_, err := s.Get(bg, Request{ID: 25, Animated: true})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "25-s.png")); err == nil {
		t.Error("empty body must not be cached")
	}
}

func TestNonPNGBodyIsNotCached(t *testing.T) {
	f := newFake(t)
	f.serve("/pokemon/25.png", []byte("<html>rate limited</html>"))
	s := newStore(t, f)
	if _, err := s.Get(bg, Request{ID: 25}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "25-s.png")); err == nil {
		t.Error("garbage must not be cached")
	}
}

func TestBackFallsBackToFrontOnlyAfter404(t *testing.T) {
	f := newFake(t)
	f.serve(gifPath+"25.gif", gifBytes(t, 2))
	s := newStore(t, f)

	// back GIF 404 (default route) -> the front sprite is served.
	res, err := s.Get(bg, Request{ID: 25, Animated: true, Back: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Key != "25-a" || !res.Fallback || res.Retry {
		t.Fatalf("after 404: %+v", res)
	}
	if f.count(backGif+"25.gif") != 1 {
		t.Fatal("back GIF should have been tried once")
	}
	// Remembered for the process lifetime.
	if _, err := s.Get(bg, Request{ID: 25, Animated: true, Back: true}); err != nil {
		t.Fatal(err)
	}
	if f.count(backGif+"25.gif") != 1 {
		t.Error("a confirmed 404 must not be requested again")
	}

	// A transient back failure must NOT settle on the front sprite.
	f2 := newFake(t)
	f2.status(backGif+"25.gif", 503)
	f2.serve(gifPath+"25.gif", gifBytes(t, 2))
	f2.serve("/pokemon/25.png", pngBytes(t, 8, 8, nil))
	f2.serve("/pokemon/back/25.png", pngBytes(t, 8, 8, nil))
	s2 := newStore(t, f2)
	res, err = s2.Get(bg, Request{ID: 25, Animated: true, Back: true})
	if !errors.Is(err, ErrUnavailable) {
		// The static back sprite exists, so the static stage may serve it, but never a front one.
		if err != nil {
			t.Fatal(err)
		}
		if res.Key != "25-back-s" || !res.Retry {
			t.Fatalf("transient back failure served %+v", res)
		}
	}
	if f2.count(gifPath+"25.gif") != 0 || f2.count("/pokemon/25.png") != 0 {
		t.Errorf("front sprite fetched after a non-404 back failure (gif %d, png %d)",
			f2.count(gifPath+"25.gif"), f2.count("/pokemon/25.png"))
	}
}

func TestSpeciesAbove649AreStaticOnly(t *testing.T) {
	f := newFake(t)
	f.serve("/pokemon/650.png", pngBytes(t, 8, 8, nil))
	s := newStore(t, f)
	res, err := s.Get(bg, Request{ID: 650, Animated: true})
	if err != nil || res.Key != "650-s" || res.Retry {
		t.Fatalf("%+v, %v", res, err)
	}
	if f.total() != 1 {
		t.Errorf("%d requests, want 1 (no GIF attempt)", f.total())
	}
}

func TestInvalidAssetRejected(t *testing.T) {
	s := newStore(t, newFake(t))
	for _, r := range []Request{
		{ID: 1, Asset: "../x"}, {ID: 1, StaticAsset: "a/b"}, {ID: 1, Asset: "A"}, {ID: 1, Asset: "1.png"},
	} {
		if _, err := s.Get(bg, r); err == nil || errors.Is(err, ErrUnavailable) {
			t.Errorf("%+v: err = %v, want a validation error", r, err)
		}
	}
	if _, err := s.Item(bg, "../../etc/passwd"); err == nil {
		t.Error("item name traversal accepted")
	}
}

func TestConcurrentGetsShareOneDownload(t *testing.T) {
	f := newFake(t)
	var hits atomic.Int32
	release := make(chan struct{})
	f.mu.Lock()
	f.routes[gifPath+"25.gif"] = func(w http.ResponseWriter) {
		hits.Add(1)
		<-release
		w.Write(gifBytes(t, 2))
	}
	f.mu.Unlock()
	s := newStore(t, f)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Get(bg, Request{ID: 25, Animated: true}); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if hits.Load() != 1 {
		t.Errorf("%d downloads, want 1", hits.Load())
	}
}

func TestItemAndEgg(t *testing.T) {
	f := newFake(t)
	f.serve("/items/rare-candy.png", pngBytes(t, 4, 4, nil))
	// 20x20 canvas with content at x 6..8, y 3..9 (3 wide, 7 tall).
	f.serve("/pokemon/egg.png", pngBytes(t, 20, 20, func(m *image.NRGBA) {
		for y := 3; y <= 9; y++ {
			for x := 6; x <= 8; x++ {
				m.SetNRGBA(x, y, color.NRGBA{200, 200, 200, 255})
			}
		}
	}))
	s := newStore(t, f)

	it, err := s.Item(bg, "rare-candy")
	if err != nil || it.Key != "item-rare-candy" || filepath.Base(it.Path) != "item-rare-candy.png" {
		t.Fatalf("%+v, %v", it, err)
	}
	if _, err := s.Item(bg, "mint"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("missing item: %v", err)
	}

	egg, err := s.Egg(bg)
	if err != nil || egg.Key != "egg" {
		t.Fatalf("%+v, %v", egg, err)
	}
	img := decodePNG(t, egg.Path)
	// side = max(3,7) = 7; sx = 6 - (7-3)/2 = 4; sy = 3.
	if b := img.Bounds(); b.Dx() != 7 || b.Dy() != 7 {
		t.Fatalf("egg crop = %v, want 7x7", b)
	}
	if a := alphaAt(img, 2, 0); a != 255 { // original (6,3)
		t.Errorf("crop origin misplaced, alpha(2,0) = %d", a)
	}
	if a := alphaAt(img, 1, 0); a != 0 {
		t.Errorf("padding should be transparent, alpha(1,0) = %d", a)
	}
	if _, err := s.Egg(bg); err != nil || f.count("/pokemon/egg.png") != 1 {
		t.Errorf("egg must be cached: %v", err)
	}
}

func TestEggCropClampsInsideImage(t *testing.T) {
	// Content hugging the left/top edge: the square is clamped to x,y >= 0.
	data := pngBytes(t, 10, 10, func(m *image.NRGBA) {
		for y := 0; y < 2; y++ {
			for x := 0; x < 6; x++ {
				m.SetNRGBA(x, y, color.NRGBA{1, 2, 3, 255})
			}
		}
	})
	out, err := cropEgg(data)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := png.Decode(bytes.NewReader(out))
	if b := img.Bounds(); b.Dx() != 6 || b.Dy() != 6 {
		t.Errorf("crop = %v, want 6x6", b)
	}
	if alphaAt(img, 0, 0) != 255 {
		t.Error("content should sit at the top-left of the clamped square")
	}
	// Fully transparent image: unchanged size.
	out, _ = cropEgg(pngBytes(t, 10, 10, nil))
	img, _ = png.Decode(bytes.NewReader(out))
	if img.Bounds().Dx() != 10 {
		t.Error("empty egg must not be cropped")
	}
}

func decodePNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func alphaAt(img image.Image, x, y int) uint8 {
	return color.NRGBAModel.Convert(img.At(img.Bounds().Min.X+x, img.Bounds().Min.Y+y)).(color.NRGBA).A
}

func TestNewRemovesTempLeftovers(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "sprites")
	os.MkdirAll(filepath.Join(sp, ".tmp-25-a-123"), 0o755)
	os.WriteFile(filepath.Join(sp, "25-s.png"), []byte("x"), 0o644)
	New(Options{CacheDir: dir})
	if _, err := os.Stat(filepath.Join(sp, ".tmp-25-a-123")); err == nil {
		t.Error("temp dir not cleaned")
	}
	if _, err := os.Stat(filepath.Join(sp, "25-s.png")); err != nil {
		t.Error("real entries must stay")
	}
}

func TestPublishOverLeftoverDirectory(t *testing.T) {
	f := newFake(t)
	f.serve(gifPath+"25.gif", gifBytes(t, 2))
	s := newStore(t, f)
	// A crashed older writer left a directory without manifest: it must be replaced.
	left := filepath.Join(s.dir, "25-a")
	os.MkdirAll(left, 0o755)
	os.WriteFile(filepath.Join(left, "frames.rgba"), []byte("partial"), 0o644)
	res, err := s.Get(bg, Request{ID: 25, Animated: true})
	if err != nil || !res.Animated {
		t.Fatalf("%+v, %v", res, err)
	}
	blob, _ := os.ReadFile(filepath.Join(left, "frames.rgba"))
	if len(blob) != 2*4*3*4 {
		t.Errorf("blob size %d, leftover not replaced", len(blob))
	}
	// A directory that already has a manifest is complete and left alone.
	data := gifBytes(t, 3)
	if err := writeAnimatedDir(s.dir, "25-a", data); err != nil {
		t.Fatal(err)
	}
	m, _ := readManifest(left)
	if len(m.Frames) != 2 {
		t.Errorf("complete directory was overwritten: %d frames", len(m.Frames))
	}
	assertNoTemp(t, s.dir)
}

func TestCancelledContextIsNotUnavailable(t *testing.T) {
	f := newFake(t)
	f.serve(gifPath+"25.gif", gifBytes(t, 2))
	ctx, cancel := context.WithCancel(bg)
	cancel()
	_, err := newStore(t, f).Get(ctx, Request{ID: 25, Animated: true})
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v", err)
	}
}
