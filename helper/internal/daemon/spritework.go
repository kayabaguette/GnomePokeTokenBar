package daemon

import (
	"context"
	"errors"
	"os"
	"time"

	"poketokenbar/helper/internal/companion"
	"poketokenbar/helper/internal/sprites"
)

// spriteWant is a sprite the helper should have on disk.
type spriteWant struct {
	key  string // the requested key
	egg  bool
	item string // item sprite name (key item-<name>), "" when not an item
	req  sprites.Request
}

// menuSprite tracks the animated sprite kept ready for state.companion.menuSubject (protocol §5.4).
type menuSprite struct {
	want     spriteWant
	gen      int    // bumped when the wanted sprite changes; stale results are dropped
	key      string // served key, "" when nothing is on disk
	path     string
	status   string // ready, fallback, loading
	inflight bool
	attempt  int
}

// wantFor maps the menu subject to its sprite: the egg, else the animated sprite of the species,
// form, shininess and facing.
func wantFor(ms companion.MenuSubject) spriteWant {
	if ms.Egg || ms.SpeciesID == nil {
		return spriteWant{key: sprites.EggKey, egg: true}
	}
	req := sprites.Request{ID: *ms.SpeciesID, Asset: ms.Asset, StaticAsset: ms.StaticAsset, Shiny: ms.Shiny,
		Back: ms.Back, Animated: true}
	return spriteWant{key: req.Key(), req: req}
}

// menuSpriteRef syncs the tracker with the subject of the state being built and returns its
// SpriteRef. A new subject starts a load; a file that vanished from disk is loaded again.
func (d *daemon) menuSpriteRef(ms companion.MenuSubject) companion.SpriteRef {
	w := wantFor(ms)
	m := &d.menu
	if w.key != m.want.key || m.status == "" {
		d.disarm("spriteRetry")
		d.menu = menuSprite{want: w, gen: m.gen + 1, status: "loading"}
		d.fetchMenuSprite()
	} else if m.path != "" {
		if _, err := os.Stat(m.path); err != nil {
			m.key, m.path, m.status = "", "", "loading"
			m.attempt = 0
			d.fetchMenuSprite()
		}
	}
	ref := companion.SpriteRef{Key: d.menu.want.key, Status: d.menu.status}
	if d.menu.path != "" {
		p := d.menu.path
		ref.Key, ref.ManifestPath = d.menu.key, &p
	}
	return ref
}

// fetchMenuSprite loads the wanted menu sprite in the background. In snapshot mode it only looks
// on disk: a request with a cancelled context is served from the cache or fails at once.
func (d *daemon) fetchMenuSprite() {
	m := &d.menu
	if d.cfg.Sprites == nil || m.inflight {
		return
	}
	if d.oneShot {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		res, _, err := loadSprite(ctx, d.cfg.Sprites, m.want)
		if err == nil {
			d.setMenuResult(res)
		}
		return
	}
	m.inflight = true
	w, gen := m.want, m.gen
	d.work(func() {
		ctx, cancel := context.WithTimeout(d.ctx, d.cfg.SpriteDeadline)
		defer cancel()
		res, front, err := loadSprite(ctx, d.cfg.Sprites, w)
		d.post(spriteMsg{gen: gen, res: res, err: err, front: front})
	})
}

// loadSprite gets a sprite. When a back sprite is unavailable (a transient failure stops the
// sprite package from settling on the front), the front is served instead and front is true, so
// the caller schedules a recovery of the back view (companion.md §8.3).
func loadSprite(ctx context.Context, s *sprites.Store, w spriteWant) (res sprites.Result, front bool, err error) {
	if w.egg {
		res, err = s.Egg(ctx)
		return res, false, err
	}
	if w.item != "" {
		res, err = s.Item(ctx, w.item)
		return res, false, err
	}
	res, err = s.Get(ctx, w.req)
	if err != nil && w.req.Back && errors.Is(err, sprites.ErrUnavailable) && ctx.Err() == nil {
		f := w.req
		f.Back = false
		if res, err = s.Get(ctx, f); err == nil {
			res.Fallback, front = true, true
		}
	}
	return res, front, err
}

func (d *daemon) setMenuResult(res sprites.Result) {
	m := &d.menu
	m.key, m.path = res.Key, res.Path
	m.status = "ready"
	if res.Fallback {
		m.status = "fallback"
	}
}

func (d *daemon) onSprite(msg spriteMsg) {
	m := &d.menu
	if msg.gen != m.gen {
		return
	}
	m.inflight = false
	retry := false
	if msg.err != nil {
		if !errors.Is(msg.err, context.Canceled) {
			d.logf("menu sprite %s unavailable: %v", m.want.key, msg.err)
		}
		m.key, m.path, m.status = "", "", "loading"
		retry = true
	} else {
		d.setMenuResult(msg.res)
		retry = msg.res.Retry || msg.front
	}
	if retry {
		d.arm("spriteRetry", sprites.RecoveryDelay(m.attempt), spriteRetryMsg{gen: m.gen})
		m.attempt++
	} else {
		m.attempt = 0
	}
	d.emitState(false)
}

// cmdSprite serves getSprite off the owner (protocol §8.1).
func (d *daemon) cmdSprite(r request) {
	a, err := newArgs(r.Args)
	if err != nil {
		d.replyErr(r.ID, "badArgs", err.Error())
		return
	}
	var w spriteWant
	if a.boolean("egg", false, false) {
		w = spriteWant{key: sprites.EggKey, egg: true}
	} else if _, ok := a.m["item"]; ok {
		name := a.str("item", true, "")
		if a.err == nil && !sprites.ValidName(name) {
			a.fail(badArgs("item must be lowercase letters, digits and '-'"))
		}
		w = spriteWant{key: sprites.ItemKey(name), item: name}
	} else {
		id := int(a.int("speciesId", true, 0, 1, maxSpecies))
		form := a.nullableStr("form")
		shiny := a.boolean("shiny", false, false)
		back := a.boolean("back", false, false)
		animated := a.boolean("animated", false, true)
		asset, static := companion.SpriteAssets(id, form)
		req := sprites.Request{ID: id, Asset: asset, StaticAsset: static, Shiny: shiny, Back: back, Animated: animated}
		w = spriteWant{key: req.Key(), req: req}
	}
	if a.err != nil {
		d.replyErr(r.ID, "badArgs", a.err.Error())
		return
	}
	if d.cfg.Sprites == nil {
		d.replyErr(r.ID, "unavailable", "sprites are not configured")
		return
	}
	id := r.ID
	d.work(func() {
		ctx, cancel := context.WithTimeout(d.ctx, d.cfg.SpriteDeadline)
		defer cancel()
		res, front, err := loadSprite(ctx, d.cfg.Sprites, w)
		if err != nil {
			if d.ctx.Err() != nil {
				return // exiting: pending commands get no reply
			}
			code := "unavailable"
			if !errors.Is(err, sprites.ErrUnavailable) && ctx.Err() == nil {
				code = "internal"
			}
			d.replyErr(id, code, "sprite "+w.key+" unavailable")
			return
		}
		status := "ready"
		if res.Fallback {
			status = "fallback"
		}
		d.reply(id, spriteReply{Key: res.Key, Status: status, ManifestPath: res.Path})
		if front {
			d.recoverSprite(w)
		}
	})
}

// recoverSprite retries a back sprite that fell back to the front, so a later getSprite finds it.
// It stops on success, on a permanent miss (404, served as the front) or after a few attempts.
func (d *daemon) recoverSprite(w spriteWant) {
	d.recoverMu.Lock()
	if d.recovering[w.key] {
		d.recoverMu.Unlock()
		return
	}
	d.recovering[w.key] = true
	d.recoverMu.Unlock()
	d.work(func() {
		defer func() {
			d.recoverMu.Lock()
			delete(d.recovering, w.key)
			d.recoverMu.Unlock()
		}()
		for attempt := 0; attempt < spriteRecoveryN; attempt++ {
			select {
			case <-d.ctx.Done():
				return
			case <-time.After(sprites.RecoveryDelay(attempt)):
			}
			ctx, cancel := context.WithTimeout(d.ctx, d.cfg.SpriteDeadline)
			res, err := d.cfg.Sprites.Get(ctx, w.req)
			cancel()
			if err == nil && !res.Retry {
				return
			}
		}
	})
}
