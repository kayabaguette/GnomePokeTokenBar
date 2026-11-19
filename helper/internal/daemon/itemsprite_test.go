package daemon

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"poketokenbar/helper/internal/sprites"
)

// getSprite {"item"} (protocol §8.1): ready static png, validated name, 404 -> unavailable, cache hit.
func TestGetSpriteItem(t *testing.T) {
	fs := newFakeSprites(t)
	h := newHarness(t, "")
	h.cfg.Sprites = fs.store(h.cfg.CacheDir)
	h.start()

	h.send(`{"id":1,"cmd":"getSprite","args":{"item":"rare-candy"}}`)
	r := h.expect("item", wait, isResult(1))
	p := r.str("data", "manifestPath")
	if r.str("data", "key") != "item-rare-candy" || r.str("data", "status") != "ready" ||
		filepath.Base(p) != "item-rare-candy.png" {
		t.Fatalf("item reply = %v", r)
	}
	hits := fs.hits.Load()
	h.send(`{"id":2,"cmd":"getSprite","args":{"item":"rare-candy"}}`)
	if r2 := h.expect("item again", wait, isResult(2)); r2.str("data", "manifestPath") != p {
		t.Fatalf("second reply = %v", r2)
	}
	if got := fs.hits.Load(); got != hits {
		t.Errorf("cache hit made %d extra request(s)", got-hits)
	}

	// egg wins over item.
	h.send(`{"id":3,"cmd":"getSprite","args":{"egg":true,"item":"rare-candy"}}`)
	if r3 := h.expect("egg", wait, isResult(3)); r3.str("data", "key") != "egg" {
		t.Fatalf("egg+item = %v", r3)
	}

	for i, args := range []string{`"../x"`, `""`, `"Rare-Candy"`, `"a/b"`, `5`, `null`} {
		h.send(`{"id":` + string(rune('1'+i)) + `0,"cmd":"getSprite","args":{"item":` + args + `}}`)
		r := h.expect("bad item", wait, isResult(int64(10*(i+1))))
		if e, _ := r["error"].(map[string]any); e == nil || e["code"] != "badArgs" {
			t.Errorf("item %s = %v", args, r)
		}
	}
	h.stop()
}

func TestGetSpriteItem404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	h := newHarness(t, "")
	h.cfg.Sprites = sprites.New(sprites.Options{CacheDir: h.cfg.CacheDir, SpriteBase: srv.URL + "/pokemon",
		ItemBase: srv.URL + "/items"})
	h.start()
	h.send(`{"id":1,"cmd":"getSprite","args":{"item":"mint"}}`)
	r := h.expect("mint", wait, isResult(1))
	if e, _ := r["error"].(map[string]any); e == nil || e["code"] != "unavailable" ||
		!strings.Contains(e["message"].(string), "item-mint") {
		t.Fatalf("mint 404 = %v", r)
	}
	h.stop()
}
