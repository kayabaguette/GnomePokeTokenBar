package daemon

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"poketokenbar/helper/internal/companion"
	"poketokenbar/helper/internal/pokeapi"
)

// ---------------------------------------------------------------- PokéAPI adapter

// PokeSource adapts the PokéAPI client to companion.DataSource. Its calls block on the network;
// the daemon only calls it from warm goroutines, never from the owner.
type PokeSource struct{ C *pokeapi.Client }

var _ companion.DataSource = PokeSource{}

// BaseIndex implements companion.DataSource.
func (p PokeSource) BaseIndex(ctx context.Context) ([]companion.BaseEntry, error) {
	idx, err := p.C.BaseIndex(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]companion.BaseEntry, len(idx))
	for i, e := range idx {
		out[i] = companion.BaseEntry{ID: e.ID, CaptureRate: e.CaptureRate}
	}
	return out, nil
}

// Line implements companion.DataSource: the tree, the base species' rarity inputs and every node's names.
func (p PokeSource) Line(ctx context.Context, baseID int) (*companion.Line, error) {
	l, err := p.C.Line(ctx, baseID)
	if err != nil {
		return nil, err
	}
	base := l.Species[baseID]
	out := &companion.Line{BaseID: baseID, Tree: toNode(l.Tree), CaptureRate: base.CaptureRate,
		IsLegendary: base.IsLegendary, IsMythical: base.IsMythical, Names: map[int]map[string]string{}}
	for id, sp := range l.Species {
		out.Names[id] = sp.Names
	}
	return out, nil
}

func toNode(n pokeapi.EvoNode) companion.Node {
	out := companion.Node{ID: n.ID}
	for _, c := range n.Children {
		out.Children = append(out.Children, toNode(c))
	}
	return out
}

// Species implements companion.DataSource.
func (p PokeSource) Species(ctx context.Context, id int) (*companion.Species, error) {
	sp, err := p.C.Species(ctx, id)
	if err != nil {
		return nil, err
	}
	return &companion.Species{ID: sp.ID, CaptureRate: sp.CaptureRate, IsLegendary: sp.IsLegendary,
		IsMythical: sp.IsMythical, EvolvesFrom: sp.EvolvesFrom, Names: sp.Names}, nil
}

// Details implements companion.DataSource. Every black-2-white-2 learn method is kept: profile
// enrichment already keeps level-up moves only, and getSpecies shows the full learnset with its
// methods (protocol §7.2, ui.md §3.4).
func (p PokeSource) Details(ctx context.Context, speciesID int) (*companion.Details, error) {
	d, err := p.C.Details(ctx, speciesID)
	if err != nil {
		return nil, err
	}
	out := &companion.Details{SpeciesID: d.SpeciesID, Height: d.Height, Weight: d.Weight,
		GenderRate: d.GenderRate, Types: append([]string{}, d.Types...), BaseStats: d.BaseStats}
	for _, a := range d.Abilities {
		out.Abilities = append(out.Abilities, companion.Ability{Name: a.Name, Slot: a.Slot, Hidden: a.Hidden})
	}
	for _, m := range d.Moves {
		mv := companion.Move{Name: m.Name}
		for _, lm := range m.Methods {
			mv.Methods = append(mv.Methods, companion.LearnMethod{Method: lm.Method, Level: lm.Level})
		}
		out.Moves = append(out.Moves, mv)
	}
	return out, nil
}

// Names implements companion.DataSource.
func (p PokeSource) Names(ctx context.Context, kind, name string) (map[string]string, error) {
	return p.C.Names(ctx, pokeapi.NameKind(kind), name)
}

// ---------------------------------------------------------------- memory layer

// errNotLoaded is what the owner's view returns for data not in memory yet.
var errNotLoaded = errors.New("pokémon data not loaded yet")

// failCooldown keeps a failed key from being requested again by every pass of a warm chain.
const failCooldown = 60 * time.Second

type wantKind int

const (
	wantBase wantKind = iota
	wantLine
	wantSpecies
	wantDetails
	wantNames
)

// want is one piece of data a Store call missed.
type want struct {
	kind wantKind
	id   int
	name string // names: "<kind>/<name>"
}

// dataCache holds the Pokémon data the companion Store reads. The Store is synchronous and owned
// by the daemon's owner goroutine, so it gets view(): a memory-only DataSource that never blocks.
// A miss is recorded as a want and fails at once; warm fetches wants through the blocking source
// in another goroutine, and the owner then repeats the Store call.
type dataCache struct {
	src companion.DataSource
	now func() time.Time

	mu      sync.Mutex
	base    []companion.BaseEntry
	lines   map[int]*companion.Line
	species map[int]*companion.Species
	details map[int]*companion.Details
	names   map[string]map[string]string
	failed  map[want]time.Time
	wants   map[want]bool
}

func newDataCache(src companion.DataSource, now func() time.Time) *dataCache {
	return &dataCache{src: src, now: now, lines: map[int]*companion.Line{}, species: map[int]*companion.Species{},
		details: map[int]*companion.Details{}, names: map[string]map[string]string{},
		failed: map[want]time.Time{}, wants: map[want]bool{}}
}

// miss records w unless it failed recently, and returns errNotLoaded.
func (c *dataCache) miss(w want) error {
	if at, ok := c.failed[w]; !ok || c.now().Sub(at) >= failCooldown {
		c.wants[w] = true
	}
	return errNotLoaded
}

// takeWants returns and clears the misses recorded since the last call.
func (c *dataCache) takeWants() []want {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]want, 0, len(c.wants))
	for w := range c.wants {
		out = append(out, w)
	}
	clear(c.wants)
	return out
}

// clearFailures lets every failed key be requested again (networkUp).
func (c *dataCache) clearFailures() {
	c.mu.Lock()
	clear(c.failed)
	c.mu.Unlock()
}

// warm fetches wants through the blocking source, eight at a time, and reports how many it got.
// The base index goes first: when it loads, species wanted by the REST fallback of the same pass
// are dropped, as the next pass draws from the index.
func (c *dataCache) warm(ctx context.Context, wants []want) (got, failed int) {
	var rest []want
	hasBase := false
	for _, w := range wants {
		if w.kind == wantBase {
			hasBase = true
		} else {
			rest = append(rest, w)
		}
	}
	if hasBase {
		if c.fetch(ctx, want{kind: wantBase}) {
			got++
			kept := rest[:0]
			for _, w := range rest {
				if w.kind != wantSpecies {
					kept = append(kept, w)
				}
			}
			rest = kept
		} else {
			failed++
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, w := range rest {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			ok := c.fetch(ctx, w)
			<-sem
			mu.Lock()
			if ok {
				got++
			} else {
				failed++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return got, failed
}

// fetch loads one want into memory.
func (c *dataCache) fetch(ctx context.Context, w want) bool {
	var err error
	switch w.kind {
	case wantBase:
		var v []companion.BaseEntry
		if v, err = c.src.BaseIndex(ctx); err == nil {
			c.mu.Lock()
			c.base = v
			c.mu.Unlock()
		}
	case wantLine:
		var v *companion.Line
		if v, err = c.src.Line(ctx, w.id); err == nil {
			c.mu.Lock()
			c.lines[w.id] = v
			c.mu.Unlock()
		}
	case wantSpecies:
		var v *companion.Species
		if v, err = c.src.Species(ctx, w.id); err == nil {
			c.mu.Lock()
			c.species[w.id] = v
			c.mu.Unlock()
		}
	case wantDetails:
		var v *companion.Details
		if v, err = c.src.Details(ctx, w.id); err == nil {
			c.mu.Lock()
			c.details[w.id] = v
			c.mu.Unlock()
		}
	case wantNames:
		kind, name := splitNameKey(w.name)
		var v map[string]string
		if v, err = c.src.Names(ctx, kind, name); err == nil {
			if v == nil {
				v = map[string]string{}
			}
			c.mu.Lock()
			c.names[w.name] = v
			c.mu.Unlock()
		}
	}
	if err != nil {
		// Only a real failure cools down: a fetch cut short by its deadline (the pre-warm) or by
		// exiting is wanted again by the next pass.
		if ctx.Err() == nil {
			c.mu.Lock()
			c.failed[w] = c.now()
			c.mu.Unlock()
		}
		return false
	}
	c.mu.Lock()
	delete(c.failed, w)
	c.mu.Unlock()
	return true
}

func splitNameKey(key string) (kind, name string) {
	for i := 0; i < len(key); i++ {
		if key[i] == '/' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

// view returns the owner's memory-only DataSource.
func (c *dataCache) view() companion.DataSource { return cacheView{c} }

type cacheView struct{ c *dataCache }

func (v cacheView) BaseIndex(context.Context) ([]companion.BaseEntry, error) {
	v.c.mu.Lock()
	defer v.c.mu.Unlock()
	if v.c.base != nil {
		return v.c.base, nil
	}
	return nil, v.c.miss(want{kind: wantBase})
}

func (v cacheView) Line(_ context.Context, baseID int) (*companion.Line, error) {
	v.c.mu.Lock()
	defer v.c.mu.Unlock()
	if l := v.c.lines[baseID]; l != nil {
		return l, nil
	}
	return nil, v.c.miss(want{kind: wantLine, id: baseID})
}

func (v cacheView) Species(_ context.Context, id int) (*companion.Species, error) {
	v.c.mu.Lock()
	defer v.c.mu.Unlock()
	if s := v.c.species[id]; s != nil {
		return s, nil
	}
	return nil, v.c.miss(want{kind: wantSpecies, id: id})
}

func (v cacheView) Details(_ context.Context, id int) (*companion.Details, error) {
	v.c.mu.Lock()
	defer v.c.mu.Unlock()
	if d := v.c.details[id]; d != nil {
		return d, nil
	}
	return nil, v.c.miss(want{kind: wantDetails, id: id})
}

func (v cacheView) Names(_ context.Context, kind, name string) (map[string]string, error) {
	key := kind + "/" + name
	v.c.mu.Lock()
	defer v.c.mu.Unlock()
	if m, ok := v.c.names[key]; ok {
		return m, nil
	}
	return nil, v.c.miss(want{kind: wantNames, name: key})
}

// ---------------------------------------------------------------- RNG

// cryptoRng is the production random source.
type cryptoRng struct{}

func (cryptoRng) Next() uint64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint64(b[:])
}

// replayRng records the draws of one Store call so they can be replayed. A ready egg with no
// prefetched species rolls its species before loading the species' line; when that line is not in
// memory yet, the hatch is delayed and the next pass would roll another species, so the warm
// chain would never converge. Requeuing the call's draws makes the next pass roll the same
// species, as upstream gets by awaiting the fetch inside the hatch. Owner-only.
type replayRng struct {
	src    companion.Rng
	drawn  []uint64
	queued []uint64
}

func (r *replayRng) Next() uint64 {
	var v uint64
	if len(r.queued) > 0 {
		v, r.queued = r.queued[0], r.queued[1:]
	} else {
		v = r.src.Next()
	}
	r.drawn = append(r.drawn, v)
	return v
}

// commit forgets the draws since the last commit or requeue.
func (r *replayRng) commit() { r.drawn = r.drawn[:0] }

// requeue makes the draws since the last commit come out again, in order, before fresh ones.
func (r *replayRng) requeue() {
	r.queued = append(append([]uint64{}, r.drawn...), r.queued...)
	r.drawn = r.drawn[:0]
}
