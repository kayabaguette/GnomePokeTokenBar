package sprites

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrUnavailable means the sprite is neither cached nor downloadable (offline
// and uncached, or missing on the server after all fallbacks).
var ErrUnavailable = errors.New("sprites: unavailable")

var errMissing = errors.New("sprites: 404")

const (
	downloadTimeout = 30 * time.Second
	maxDownload     = 16 << 20
)

// Options configures a Store.
type Options struct {
	// CacheDir is the cache root; sprites live in <CacheDir>/sprites. Required.
	CacheDir string
	// UserAgent is sent on downloads.
	UserAgent string
	// HTTPClient overrides the transport (tests). Default: http.DefaultClient.
	HTTPClient *http.Client
	// SpriteBase and ItemBase override the download roots (tests).
	SpriteBase, ItemBase string
}

// Store downloads and caches sprites. It is safe for concurrent use.
type Store struct {
	dir, ua, spriteBase, itemBase string
	hc                            *http.Client

	mu      sync.Mutex
	missing map[string]bool // keys the server answered 404 for, this process
	flights map[string]*call
}

type call struct {
	done chan struct{}
	err  error
}

// Result describes a sprite that is ready on disk.
type Result struct {
	// Key is the cache key actually served.
	Key string
	// Path is manifest.json of an animated directory, or a plain .png file.
	Path string
	// Animated is true when Path is a manifest with more than one frame.
	Animated bool
	// Fallback is true when the sprite differs from the one requested: another
	// key, or a 1-frame GIF where an animation was asked for.
	Fallback bool
	// Retry is true when a transient failure produced this fallback; call Get
	// again after RecoveryDelay(attempt) until Retry is false.
	Retry bool
}

// New returns a Store and removes temp leftovers of a crashed run.
func New(o Options) *Store {
	s := &Store{
		dir: filepath.Join(o.CacheDir, "sprites"), ua: o.UserAgent,
		spriteBase: o.SpriteBase, itemBase: o.ItemBase, hc: o.HTTPClient,
		missing: map[string]bool{}, flights: map[string]*call{},
	}
	if s.spriteBase == "" {
		s.spriteBase = SpriteBase
	}
	if s.itemBase == "" {
		s.itemBase = ItemBase
	}
	if s.hc == nil {
		s.hc = http.DefaultClient
	}
	if ents, err := os.ReadDir(s.dir); err == nil {
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".tmp-") {
				os.RemoveAll(filepath.Join(s.dir, e.Name()))
			}
		}
	}
	return s
}

type kind int

const (
	kindGIF kind = iota
	kindPNG
	kindEgg
)

// Get returns the sprite for r, downloading it if needed.
//
// Order: the animated GIF when requested and the species has one (id <= 649),
// then the static PNG. Within a stage a shiny miss falls back to plain, and a
// back miss to the front sprite only after a 404; a transient back failure
// stops the stage so the caller retries rather than settling on the front.
func (s *Store) Get(ctx context.Context, r Request) (Result, error) {
	if !validName(r.asset()) || !validName(r.staticAsset()) {
		return Result{}, fmt.Errorf("sprites: invalid asset %q/%q", r.asset(), r.staticAsset())
	}
	requested := r.Key()
	stages := []bool{false}
	if r.Animated && r.ID >= 1 && r.ID <= MaxAnimatedID {
		stages = []bool{true, false}
	}
	retry := false
	for _, anim := range stages {
		q := r
		q.Animated = anim
		res, transient, err := s.stage(ctx, q)
		if err != nil {
			return Result{}, err
		}
		retry = retry || transient
		if res != nil {
			res.Fallback = res.Key != requested || (r.Animated && !res.Animated)
			res.Retry = retry
			return *res, nil
		}
	}
	return Result{}, fmt.Errorf("%w: %s", ErrUnavailable, requested)
}

// stage tries the candidates of one format. It reports whether any attempt
// failed transiently. A non-nil error is a cancelled context.
func (s *Store) stage(ctx context.Context, q Request) (res *Result, transient bool, err error) {
	backs := []bool{false}
	if q.Back {
		backs = []bool{true, false}
	}
	variants := []bool{false}
	if q.Shiny {
		variants = []bool{true, false}
	}
	backFailed := false
	for _, back := range backs {
		if !back && q.Back && backFailed {
			break
		}
		for _, shiny := range variants {
			c := q
			c.Back, c.Shiny = back, shiny
			k := kindPNG
			if c.Animated {
				k = kindGIF
			}
			r, lerr := s.load(ctx, c.Key(), c.url(s.spriteBase), k)
			switch {
			case lerr == nil:
				return &r, transient, nil
			case ctx.Err() != nil:
				return nil, transient, ctx.Err()
			case errors.Is(lerr, errMissing):
			default:
				transient = true
				if back {
					backFailed = true
				}
			}
		}
	}
	return nil, transient, nil
}

// Item returns the item sprite items/<name>.png (key "item-<name>").
func (s *Store) Item(ctx context.Context, name string) (Result, error) {
	if !validName(name) {
		return Result{}, fmt.Errorf("sprites: invalid item name %q", name)
	}
	return s.single(ctx, ItemKey(name), s.itemBase+"/"+name+".png", kindPNG)
}

// Egg returns the egg sprite cropped to a square around its content (key "egg").
func (s *Store) Egg(ctx context.Context) (Result, error) {
	return s.single(ctx, EggKey, s.spriteBase+"/egg.png", kindEgg)
}

func (s *Store) single(ctx context.Context, key, url string, k kind) (Result, error) {
	r, err := s.load(ctx, key, url, k)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, fmt.Errorf("%w: %s: %v", ErrUnavailable, key, err)
	}
	return r, nil
}

// load returns the cached sprite for key or downloads it (deduped per key).
func (s *Store) load(ctx context.Context, key, url string, k kind) (Result, error) {
	if r, ok := s.cachedResult(key, k); ok {
		return r, nil
	}
	s.mu.Lock()
	if s.missing[key] {
		s.mu.Unlock()
		return Result{}, errMissing
	}
	c, running := s.flights[key]
	if !running {
		c = &call{done: make(chan struct{})}
		s.flights[key] = c
	}
	s.mu.Unlock()

	if running {
		select {
		case <-c.done:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	} else {
		c.err = s.download(ctx, key, url, k)
		s.mu.Lock()
		delete(s.flights, key)
		s.mu.Unlock()
		close(c.done)
	}
	if c.err != nil {
		return Result{}, c.err
	}
	if r, ok := s.cachedResult(key, k); ok {
		return r, nil
	}
	return Result{}, errors.New("sprites: cache entry missing after download")
}

func (s *Store) cachedResult(key string, k kind) (Result, bool) {
	if k == kindGIF {
		dir := filepath.Join(s.dir, key)
		m, err := readManifest(dir)
		if err != nil {
			return Result{}, false
		}
		return Result{Key: key, Path: filepath.Join(dir, ManifestFile), Animated: m.Animated}, true
	}
	p := filepath.Join(s.dir, key+".png")
	if st, err := os.Stat(p); err != nil || st.Size() == 0 {
		return Result{}, false
	}
	return Result{Key: key, Path: p}, true
}

func (s *Store) download(ctx context.Context, key, url string, k kind) error {
	data, status, err := s.fetch(ctx, url)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		s.mu.Lock()
		s.missing[key] = true
		s.mu.Unlock()
		return errMissing
	}
	if status != http.StatusOK {
		return fmt.Errorf("sprites: GET %s: HTTP %d", url, status)
	}
	if len(data) == 0 {
		return fmt.Errorf("sprites: GET %s: empty body", url)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	switch k {
	case kindGIF:
		return writeAnimatedDir(s.dir, key, data)
	case kindEgg:
		data, err = cropEgg(data)
		if err != nil {
			return err
		}
	default:
		// Never cache an error page or truncated file as a sprite.
		if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
			return fmt.Errorf("sprites: GET %s: not a PNG: %w", url, err)
		}
	}
	return writeFileAtomic(filepath.Join(s.dir, key+".png"), data)
}

func (s *Store) fetch(ctx context.Context, url string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if s.ua != "" {
		req.Header.Set("User-Agent", s.ua)
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload))
	return b, resp.StatusCode, err
}

// cropEgg crops a PNG to the square around its non-transparent content
// (alpha > 0.01), the long side of the content bounding box capped at the
// image size, then clamped inside the image (SpriteLoader.swift cropToContent).
func cropEgg(data []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("sprites: decode egg: %w", err)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	nr := image.NewNRGBA(image.Rect(0, 0, w, h))
	minX, minY, maxX, maxY := w, h, -1, -1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			nr.SetNRGBA(x, y, c)
			if float64(c.A)/255 > 0.01 {
				minX, maxX = min(minX, x), max(maxX, x)
				minY, maxY = min(minY, y), max(maxY, y)
			}
		}
	}
	out := image.Image(nr)
	if maxX >= minX && maxY >= minY {
		bw, bh := maxX-minX+1, maxY-minY+1
		side := min(max(bw, bh), min(w, h))
		sx := max(0, min(minX-(side-bw)/2, w-side))
		sy := max(0, min(minY-(side-bh)/2, h-side))
		out = nr.SubImage(image.Rect(sx, sy, sx+side, sy+side))
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
