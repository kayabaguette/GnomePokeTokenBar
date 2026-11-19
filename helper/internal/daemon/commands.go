package daemon

import (
	"context"
	"fmt"
	"time"

	"poketokenbar/helper/internal/companion"
	"poketokenbar/helper/internal/sprites"
)

var (
	rarities    = []string{"common", "uncommon", "rare", "legendary"}
	regions     = []string{"kanto", "johto", "hoenn", "sinnoh", "unova"}
	dexSorts    = []string{"numberAsc", "numberDesc", "nameAsc", "nameDesc", "rarityDesc"}
	catchSorts  = []string{"recentFirst", "oldestFirst", "numberAsc", "numberDesc", "nameAsc", "nameDesc", "rarityDesc"}
	maxSpecies  = int64(649)
	emptyObject = struct{}{}
)

// command serves one command on the owner (protocol §4). Commands that wait on the network
// (getSpecies, getSprite) or on a refresh cycle reply later; everything else replies now.
func (d *daemon) command(r request) {
	switch r.Cmd {
	case "ping":
		d.reply(r.ID, emptyObject)
	case "setSettings":
		d.cmdSetSettings(r)
	case "refresh":
		a, err := newArgs(r.Args)
		manual := false
		if err == nil {
			manual = a.boolean("manual", false, false)
			err = a.err
		}
		if err != nil {
			d.replyErr(r.ID, "badArgs", err.Error())
			return
		}
		id := r.ID
		d.requestRefresh(manual, &id, true)
	case "wake":
		d.reply(r.ID, emptyObject)
		d.armMidnight()
		d.requestRefresh(false, nil, true)
	case "networkUp":
		d.reply(r.ID, emptyObject)
		d.data.clearFailures()
		d.pokeDown = false
		if d.menu.status != "ready" && !d.menu.inflight {
			d.menu.attempt = 0
			d.fetchMenuSprite()
		}
		d.warmPasses = 0
		d.companionUpdate()
		d.requestRefresh(false, nil, true)
	case "setRepresentative":
		d.cmdSetRepresentative(r)
	case "getDexPage":
		d.cmdDexPage(r)
	case "getCatchLog":
		d.cmdCatchLog(r)
	case "getSpecies":
		d.cmdSpecies(r)
	case "getRecap":
		d.cmdRecap(r)
	case "getSprite":
		d.cmdSprite(r)
	default:
		msg := "missing cmd"
		if r.Cmd != "" {
			msg = fmt.Sprintf("unknown command %q", r.Cmd)
		}
		d.replyErr(r.ID, "unknownCommand", msg)
	}
}

func (d *daemon) cmdSetSettings(r request) {
	s, err := parseSettings(r.Args)
	if err != nil {
		d.replyErr(r.ID, "badArgs", err.Error())
		return
	}
	old := d.settings
	// The rescale happens only when the received value differs from the persisted last-applied
	// one, which the save holds (protocol §2.1 step 6).
	if s.GrowthDifficulty != d.store.GrowthDifficulty() {
		d.store.SetGrowthDifficulty(s.GrowthDifficulty)
	}
	s.GrowthDifficulty = d.store.GrowthDifficulty()
	d.store.SetLanguage(s.Language)
	d.settings = s
	d.notePersist(writeJSONAtomic(d.path(settingsFile), s), settingsFile)
	if s.RefreshInterval != old.RefreshInterval {
		d.armInterval()
	}
	d.emitState(true)
	d.reply(r.ID, s)
	if !d.started {
		d.start()
	}
}

func (d *daemon) cmdSetRepresentative(r request) {
	a, err := newArgs(r.Args)
	if err != nil {
		d.replyErr(r.ID, "badArgs", err.Error())
		return
	}
	id := a.nullableInt("speciesId", true, 1, maxSpecies)
	form := a.nullableStr("form")
	if a.err != nil {
		d.replyErr(r.ID, "badArgs", a.err.Error())
		return
	}
	if !d.store.SetRepresentative(id, form) {
		d.replyErr(r.ID, "notOwned", "that species or form is not owned")
		return
	}
	d.emitState(true)
	v := d.store.BuildState(companion.MoodInput{Now: d.cfg.Now()})
	d.reply(r.ID, map[string]*companion.Representative{"representative": v.Representative})
}

func (d *daemon) cmdDexPage(r request) {
	a, err := newArgs(r.Args)
	if err != nil {
		d.replyErr(r.ID, "badArgs", err.Error())
		return
	}
	q := companion.DexQuery{
		Page:            int(a.int("page", false, 1, 1, maxSafeInt)),
		PageSize:        int(a.int("pageSize", false, 16, 1, 64)),
		Query:           a.str("query", false, ""),
		Rarity:          a.nullableEnum("rarity", rarities...),
		Region:          a.nullableEnum("region", regions...),
		ShinyOnly:       a.boolean("shinyOnly", false, false),
		Sort:            a.enum("sort", "numberAsc", dexSorts...),
		AnchorSpeciesID: a.nullableInt("anchorSpeciesId", false, 1, maxSpecies),
	}
	if a.err != nil {
		d.replyErr(r.ID, "badArgs", a.err.Error())
		return
	}
	page := d.store.DexPage(q)
	for i := range page.Cells {
		c := &page.Cells[i]
		c.Sprite.Back, c.Sprite.Animated = false, false
		c.SpriteKey = sprites.Request{ID: c.Sprite.SpeciesID, Asset: c.Sprite.Asset,
			StaticAsset: c.Sprite.StaticAsset, Shiny: c.Sprite.Shiny}.Key()
	}
	d.reply(r.ID, page)
}

func (d *daemon) cmdCatchLog(r request) {
	a, err := newArgs(r.Args)
	if err != nil {
		d.replyErr(r.ID, "badArgs", err.Error())
		return
	}
	q := companion.CatchLogQuery{
		Offset:    int(a.int("offset", false, 0, 0, maxSafeInt)),
		Limit:     int(a.int("limit", false, 20, 1, 100)),
		Query:     a.str("query", false, ""),
		Rarity:    a.nullableEnum("rarity", rarities...),
		ShinyOnly: a.boolean("shinyOnly", false, false),
		Sort:      a.enum("sort", "recentFirst", catchSorts...),
	}
	if a.err != nil {
		d.replyErr(r.ID, "badArgs", a.err.Error())
		return
	}
	d.reply(r.ID, d.store.CatchLog(q))
}

// speciesJob is one getSpecies being served: the owner builds the page from memory, warms what it
// missed in the background, and builds again, until nothing is missing or the deadline passes.
type speciesJob struct {
	id       int64
	species  int
	deadline time.Time
	passes   int
	final    bool
}

func (d *daemon) cmdSpecies(r request) {
	a, err := newArgs(r.Args)
	if err != nil {
		d.replyErr(r.ID, "badArgs", err.Error())
		return
	}
	id := a.int("speciesId", true, 0, 1, maxSpecies)
	if a.err != nil {
		d.replyErr(r.ID, "badArgs", a.err.Error())
		return
	}
	d.runSpecies(&speciesJob{id: r.ID, species: int(id), deadline: time.Now().Add(d.cfg.SpeciesDeadline)})
}

func (d *daemon) runSpecies(j *speciesJob) {
	v := d.store.Species(d.ctx, j.species)
	wants := d.data.takeWants()
	if len(wants) == 0 || j.final || j.passes >= 4 || !time.Now().Before(j.deadline) {
		d.emitState(false) // details may have enriched the active Pokémon's card
		d.reply(j.id, v)
		return
	}
	j.passes++
	ctx, cancel := context.WithDeadline(d.ctx, j.deadline)
	d.work(func() {
		defer cancel()
		got, _ := d.data.warm(ctx, wants)
		d.post(speciesMsg{job: j, got: got})
	})
}

func (d *daemon) onSpeciesWarm(m speciesMsg) {
	if m.got == 0 {
		m.job.final = true
	}
	d.runSpecies(m.job)
}
