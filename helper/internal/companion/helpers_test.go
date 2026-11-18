package companion

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Test doubles mirroring the Swift stubs (CompanionTests.swift:63-262). No network anywhere.

var errOffline = errors.New("offline")

var fixedNow = time.Unix(1_700_000_000, 0)

var bg = context.Background()

func node(id int, children ...Node) Node { return Node{ID: id, Children: children} }

func allIDs(n Node) []int {
	out := []int{n.ID}
	for _, c := range n.Children {
		out = append(out, allIDs(c)...)
	}
	return out
}

// makeLine mirrors Swift makeLine: names P<id> / 포<id> / ポ<id> for every node.
func makeLine(base int, tree Node, rarity string) *Line {
	l := &Line{BaseID: base, Tree: tree, Names: map[int]map[string]string{}}
	switch rarity {
	case rarityLegendary:
		l.CaptureRate, l.IsLegendary = 3, true
	case rarityRare:
		l.CaptureRate = 45
	case rarityUncommon:
		l.CaptureRate = 90
	default:
		l.CaptureRate = 255
	}
	for _, id := range allIDs(tree) {
		s := strconv.Itoa(id)
		l.Names[id] = map[string]string{"en": "P" + s, "ko": "포" + s, "ja": "ポ" + s}
	}
	return l
}

var (
	linear3     = func() *Line { return makeLine(1, node(1, node(2, node(3))), rarityCommon) }
	branch3     = func() *Line { return makeLine(10, node(10, node(11), node(12), node(13)), rarityCommon) }
	wurmpleLine = func() *Line {
		return makeLine(265, node(265, node(266, node(267)), node(268, node(269))), rarityCommon)
	}
	delayedBranchLine = func() *Line { return makeLine(43, node(43, node(44, node(45), node(182))), rarityCommon) }
	noEvo             = func() *Line { return makeLine(20, node(20), rarityCommon) }
)

// fakeData is a configurable DataSource. Nil funcs fail as offline, except where noted.
type fakeData struct {
	mu        sync.Mutex
	lineFn    func(base int) (*Line, error)
	index     []BaseEntry
	indexErr  error
	speciesFn func(id int) (*Species, error) // nil: derived from index like Swift's default baseSpecies
	detailsFn func(id int) (*Details, error)
	namesFn   func(kind, name string) (map[string]string, error)
	lineCalls int
	detailIDs []int
}

func (f *fakeData) BaseIndex(context.Context) ([]BaseEntry, error) {
	if f.indexErr != nil {
		return nil, f.indexErr
	}
	return f.index, nil
}

func (f *fakeData) Line(_ context.Context, base int) (*Line, error) {
	f.mu.Lock()
	f.lineCalls++
	f.mu.Unlock()
	if f.lineFn == nil {
		return nil, errOffline
	}
	return f.lineFn(base)
}

func (f *fakeData) Species(_ context.Context, id int) (*Species, error) {
	if f.speciesFn != nil {
		return f.speciesFn(id)
	}
	if f.indexErr != nil {
		return nil, f.indexErr
	}
	for _, e := range f.index {
		if e.ID == id {
			return &Species{ID: id, CaptureRate: e.CaptureRate}, nil
		}
	}
	return &Species{ID: id, EvolvesFrom: 1}, nil
}

func (f *fakeData) Details(_ context.Context, id int) (*Details, error) {
	f.mu.Lock()
	f.detailIDs = append(f.detailIDs, id)
	f.mu.Unlock()
	if f.detailsFn == nil {
		return nil, errOffline
	}
	return f.detailsFn(id)
}

func (f *fakeData) Names(_ context.Context, kind, name string) (map[string]string, error) {
	if f.namesFn == nil {
		return nil, errOffline
	}
	return f.namesFn(kind, name)
}

// stub mirrors Swift StubProvider: every base returns l; the index is l's base at capture rate 255.
func stub(l *Line) *fakeData {
	return &fakeData{
		lineFn: func(int) (*Line, error) { return l, nil },
		index:  []BaseEntry{{ID: l.BaseID, CaptureRate: 255}},
	}
}

// countingRng counts draws (Swift CountingRNG).
type countingRng struct {
	SplitMix64
	calls int
}

func (c *countingRng) Next() uint64 { c.calls++; return c.SplitMix64.Next() }

func draws(seed uint64, n int) []uint64 {
	r := SplitMix64{State: seed}
	out := make([]uint64, n)
	for i := range out {
		out[i] = r.Next()
	}
	return out
}

type storeOpt func(*Options)

func withNow(f func() time.Time) storeOpt { return func(o *Options) { o.Now = f } }
func withRng(r Rng) storeOpt              { return func(o *Options) { o.Rng = r } }
func withDitto() storeOpt                 { return func(o *Options) { o.DittoDisguiseEnabled = true } }

func openAt(t *testing.T, dir string, data DataSource, seed uint64, opts ...storeOpt) *Store {
	t.Helper()
	o := Options{Dir: dir, Data: data, Rng: &SplitMix64{State: seed}, Now: func() time.Time { return fixedNow }}
	for _, f := range opts {
		f(&o)
	}
	s, err := Open(o)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func newStore(t *testing.T, data DataSource, seed uint64, opts ...storeOpt) *Store {
	return openAt(t, t.TempDir(), data, seed, opts...)
}

// writeSave writes raw JSON as the save in a fresh directory and returns the directory.
func writeSave(t *testing.T, json string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(json), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeState encodes st as the save in a fresh directory.
func writeState(t *testing.T, st state) string {
	t.Helper()
	data, err := encodeState(&st)
	if err != nil {
		t.Fatal(err)
	}
	return writeSave(t, string(data))
}

func update(s *Store, m map[string]int, date string, has bool) []Event {
	return s.Update(bg, UsageInput{TodayByProvider: m, TodayDate: date, HasUsageData: has})
}

func use(s *Store, today int) []Event { return update(s, map[string]int{"test": today}, "d1", true) }

func phase(r string, k, i int) int { return phaseThreshold(r, k, i, 1) }

func entry(base, final int, chain []int, rarity string, opts ...func(*dexEntry)) dexEntry {
	at := fixedNow
	e := dexEntry{ID: newUUID(), BaseID: base, FinalID: final, ChainOrder: chain, Rarity: rarity, CaughtAt: &at}
	for _, f := range opts {
		f(&e)
	}
	e.Form = formForLine(base, e.Form)
	return e
}

func shiny(e *dexEntry)                 { e.IsShiny = true }
func withForm(f string) func(*dexEntry) { return func(e *dexEntry) { e.Form = &f } }
func withNames(n map[int]map[string]string) func(*dexEntry) {
	return func(e *dexEntry) { e.Names = n }
}

func mon(base int, path []int, stage int, rarity string, forms int) *monState {
	return &monState{BaseID: base, PathIDs: path, PlannedPathIDs: path, StageIndex: stage, Rarity: rarity, TotalForms: forms}
}

func eq[T comparable](t *testing.T, got, want T, msg string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", msg, got, want)
	}
}

func ints(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func deref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}
