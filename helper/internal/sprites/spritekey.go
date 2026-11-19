// Package sprites downloads Pokémon sprites from the PokéAPI sprites repo,
// caches them on disk, applies the fallback rules and converts animated GIFs
// into a raw RGBA blob plus a manifest.
package sprites

import (
	"strconv"
	"strings"
	"time"
)

const (
	// SpriteBase and ItemBase are the default download roots.
	SpriteBase = "https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon"
	ItemBase   = "https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/items"

	// MaxAnimatedID is the last species with a Gen-V animated sprite.
	MaxAnimatedID = 649

	// EggKey is the cache key of the (cropped) egg sprite.
	EggKey = "egg"
)

// Request identifies one sprite. The caller computes the form-aware assets.
type Request struct {
	ID int
	// Asset names the GIF and the cache key: "<id>" for the default form,
	// else "<id>-<form>". Empty means the plain id.
	Asset string
	// StaticAsset names the static PNG (a PokéAPI variety id such as
	// "10006", else Asset). Empty means Asset.
	StaticAsset           string
	Shiny, Back, Animated bool
}

func (r Request) asset() string {
	if r.Asset != "" {
		return r.Asset
	}
	return strconv.Itoa(r.ID)
}

func (r Request) staticAsset() string {
	if r.StaticAsset != "" {
		return r.StaticAsset
	}
	return r.asset()
}

// Key is the cache key: <asset>-[back-][sh](a|s). Statics use Asset, not
// StaticAsset.
func (r Request) Key() string {
	k := r.asset() + "-"
	if r.Back {
		k += "back-"
	}
	if r.Shiny {
		k += "sh"
	}
	if r.Animated {
		return k + "a"
	}
	return k + "s"
}

// URL is the download URL under SpriteBase.
func (r Request) URL() string { return r.url(SpriteBase) }

func (r Request) url(base string) string {
	var b strings.Builder
	b.WriteString(base)
	b.WriteByte('/')
	name, ext := r.staticAsset(), ".png"
	if r.Animated {
		b.WriteString("versions/generation-v/black-white/animated/")
		name, ext = r.asset(), ".gif"
	}
	if r.Back {
		b.WriteString("back/")
	}
	if r.Shiny {
		b.WriteString("shiny/")
	}
	return b.String() + name + ext
}

// ItemKey is the cache key of an item sprite.
func ItemKey(name string) string { return "item-" + name }

// RecoveryDelay is the wait before retry number attempt (0-based) of a
// failed animated load: 2, 4, 8, 16 s, then 30 s forever.
func RecoveryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= 4 {
		return 30 * time.Second
	}
	return time.Duration(2<<attempt) * time.Second
}

// ValidName reports whether s is a valid item sprite name (lowercase letters, digits, '-').
func ValidName(s string) bool { return validName(s) }

// validName reports whether s is safe as a path element and URL segment.
func validName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-') {
			return false
		}
	}
	return true
}
