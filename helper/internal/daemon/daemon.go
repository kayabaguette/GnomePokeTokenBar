// Package daemon is ptb-helper's long-running process: it wires the usage, limits, companion,
// PokéAPI and sprite packages behind the NDJSON protocol of docs/protocol.md.
//
// Concurrency: one owner goroutine (loop) holds every piece of mutable state: usage, limits,
// alert tiers, settings, the companion Store and the menu sprite. The stdin reader, timers and
// workers only post messages to it. Workers do everything that blocks (scans, HTTP), and the
// companion Store only sees a memory-only data view, so the owner never waits on the network.
// Output goes through one writer goroutine (output.go).
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"poketokenbar/helper/internal/companion"
	"poketokenbar/helper/internal/ipc"
	"poketokenbar/helper/internal/limits"
	"poketokenbar/helper/internal/sprites"
	"poketokenbar/helper/internal/usage"
)

// Config wires a daemon. Zero durations take the protocol values.
type Config struct {
	Version   string
	StateDir  string
	CacheDir  string
	Providers []UsageSource
	Limits    []LimitsSpec
	// Data is the blocking Pokémon data source (PokeSource in production).
	Data    companion.DataSource
	Sprites *sprites.Store
	Rng     companion.Rng // nil: crypto/rand
	Now     func() time.Time
	Loc     *time.Location // calendar zone; nil: time.Local
	Logf    func(format string, args ...any)

	LockWait        time.Duration // 5 s
	SettingsWait    time.Duration // 2 s: the first scan waits this long for setSettings
	EmptyRetry      time.Duration // 20 s
	Tick            time.Duration // 60 s rebuild of time-derived fields
	StateInterval   time.Duration // 250 ms between state lines
	SnapshotEvery   time.Duration // 60 s between last-snapshot.json writes
	ExitBudget      time.Duration // 1.8 s for the flush on exit
	SpeciesDeadline time.Duration // 25 s for getSpecies
	SpriteDeadline  time.Duration // 25 s for one sprite load
	PreWarm         time.Duration // 300 ms to load the active line from disk before the first state
}

func (c Config) withDefaults() Config {
	def := func(v *time.Duration, d time.Duration) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&c.LockWait, 5*time.Second)
	def(&c.SettingsWait, 2*time.Second)
	def(&c.EmptyRetry, 20*time.Second)
	def(&c.Tick, 60*time.Second)
	def(&c.StateInterval, 250*time.Millisecond)
	def(&c.SnapshotEvery, 60*time.Second)
	def(&c.ExitBudget, 1800*time.Millisecond)
	def(&c.SpeciesDeadline, 25*time.Second)
	def(&c.SpriteDeadline, 25*time.Second)
	def(&c.PreWarm, 300*time.Millisecond)
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Loc == nil {
		c.Loc = time.Local
	}
	if c.Logf == nil {
		c.Logf = log.Printf
	}
	if c.Rng == nil {
		c.Rng = cryptoRng{}
	}
	return c
}

// Exit codes (protocol §2.6).
const (
	ExitOK          = 0
	ExitUsage       = 2
	ExitRunning     = 3
	ExitStateDir    = 4
	ExitInternal    = 70
	maxWarmPasses   = 12
	spriteRecoveryN = 5
)

// cycle is one refresh: usage, then limits, then candies (usage.md §6).
type cycle struct {
	manual        bool
	scheduleEmpty bool
	waiters       []int64
}

func (c *cycle) merge(o *cycle) {
	c.manual = c.manual || o.manual
	c.scheduleEmpty = c.scheduleEmpty || o.scheduleEmpty
	c.waiters = append(c.waiters, o.waiters...)
}

type daemon struct {
	cfg     Config
	loc     *time.Location
	logf    func(string, ...any)
	out     *output
	oneShot bool // snapshot --json: no events, no alert tiers, no sprite downloads

	ctx    context.Context
	cancel context.CancelFunc
	inbox  chan any
	quit   chan struct{}
	wg     sync.WaitGroup

	settings Settings
	store    *companion.Store
	data     *dataCache
	rng      *replayRng

	usage       map[string]*providerUsage
	ledger      usage.Ledger
	lastUpdated time.Time
	fromCache   bool
	refreshing  bool
	cacheErr    map[string]error
	persistErr  error
	lastFlush   time.Time
	scanned     bool // a scan was applied in this process

	limits     map[string]*providerLimits
	alertTiers map[string]map[string]int // provider id → window key → tier

	started bool
	cycle   *cycle
	pending *cycle

	timers       map[string]*time.Timer
	lastStateKey []byte
	snapshotAt   time.Time
	snapshotDue  bool

	warmBusy, warmAgain bool
	warmPasses          int
	pokeDown            bool

	menu menuSprite

	recoverMu  sync.Mutex
	recovering map[string]bool
}

// open loads the persisted inputs and the save. The lock must be held.
func open(cfg Config, out *output, oneShot bool) (*daemon, error) {
	d := &daemon{cfg: cfg, loc: cfg.Loc, logf: cfg.Logf, out: out, oneShot: oneShot,
		inbox: make(chan any, 64), quit: make(chan struct{}),
		settings: DefaultSettings(), usage: map[string]*providerUsage{}, cacheErr: map[string]error{},
		limits: map[string]*providerLimits{}, alertTiers: map[string]map[string]int{},
		timers: map[string]*time.Timer{}, fromCache: true, recovering: map[string]bool{}}
	d.ctx, d.cancel = context.WithCancel(context.Background())
	if err := os.MkdirAll(cfg.CacheDir, 0o700); err != nil {
		d.logf("cache dir: %v", err)
	}
	d.data = newDataCache(cfg.Data, cfg.Now)
	d.rng = &replayRng{src: cfg.Rng}
	store, err := companion.Open(companion.Options{Dir: cfg.StateDir, Data: d.data.view(), Rng: d.rng,
		Now: cfg.Now, AppVersion: cfg.Version, Logf: cfg.Logf})
	if err != nil {
		return nil, fmt.Errorf("open companion save: %w", err)
	}
	d.store = store

	var s Settings
	if ok, err := readJSON(d.path(settingsFile), &s); err != nil {
		d.logf("%s unreadable, using defaults: %v", settingsFile, err)
	} else if ok {
		d.settings = s.normalized()
		d.store.SetLanguage(d.settings.Language)
	}
	// The save holds the last applied difficulty; only a received setSettings rescales.
	d.settings.GrowthDifficulty = d.store.GrowthDifficulty()

	for _, src := range cfg.Providers {
		d.usage[src.ID()] = newProviderUsage()
	}
	for _, spec := range cfg.Limits {
		d.limits[spec.ProviderID] = &providerLimits{}
	}
	d.loadSnapshot()
	if _, err := readJSON(d.path(usageLedgerFile), &d.ledger); err != nil {
		d.logf("%s unreadable, starting empty: %v", usageLedgerFile, err)
	}
	d.ledger.Sanitize()
	if _, err := readJSON(d.path(alertTiersFile), &d.alertTiers); err != nil || d.alertTiers == nil {
		d.alertTiers = map[string]map[string]int{}
	}
	return d, nil
}

func (d *daemon) path(name string) string { return filepath.Join(d.cfg.StateDir, name) }

// ---------------------------------------------------------------- persisted inputs

// snapshotDoc is last-snapshot.json: the inputs of the last fresh state (protocol §2.3).
type snapshotDoc struct {
	Version     int                        `json:"version"`
	LastUpdated *time.Time                 `json:"lastUpdated,omitempty"`
	Usage       map[string]*providerUsage  `json:"usage"`
	Limits      map[string]persistedLimits `json:"limits"`
}

const snapshotVersion = 1

func (d *daemon) loadSnapshot() {
	var doc snapshotDoc
	ok, err := readJSON(d.path(snapshotFile), &doc)
	if err != nil {
		d.logf("%s unreadable, starting empty: %v", snapshotFile, err)
	}
	if !ok || err != nil || doc.Version != snapshotVersion {
		return
	}
	if doc.LastUpdated != nil {
		d.lastUpdated = *doc.LastUpdated
	}
	for id, p := range doc.Usage {
		if _, known := d.usage[id]; known && p != nil {
			if p.Days == nil {
				p.Days = map[string]dayTotals{}
			}
			d.usage[id] = p
		}
	}
	now := d.cfg.Now()
	for id, p := range doc.Limits {
		if l := d.limits[id]; l != nil {
			l.restore(p, now)
		}
	}
}

// saveSnapshot writes last-snapshot.json, at most once per SnapshotEvery unless forced.
func (d *daemon) saveSnapshot(force bool) {
	now := d.cfg.Now()
	if !force && !d.snapshotAt.IsZero() && now.Sub(d.snapshotAt) < d.cfg.SnapshotEvery {
		d.snapshotDue = true
		return
	}
	if force && !d.snapshotDue && !d.snapshotAt.IsZero() {
		return
	}
	doc := snapshotDoc{Version: snapshotVersion, Usage: d.usage, Limits: map[string]persistedLimits{}}
	if !d.lastUpdated.IsZero() {
		t := d.lastUpdated
		doc.LastUpdated = &t
	}
	for id, l := range d.limits {
		doc.Limits[id] = l.persist()
	}
	d.snapshotAt, d.snapshotDue = now, false
	d.notePersist(writeJSONAtomic(d.path(snapshotFile), doc), snapshotFile)
}

func (d *daemon) notePersist(err error, what string) {
	if err != nil && d.persistErr == nil {
		d.logf("write %s: %v", what, err)
	}
	d.persistErr = err
}

// ---------------------------------------------------------------- Serve

// Serve runs `ptb-helper serve` until stdin closes or a signal arrives, and returns the exit code.
func Serve(cfg Config, in io.Reader, out io.Writer, signals <-chan os.Signal) int {
	cfg = cfg.withDefaults()
	o := newOutput(out, cfg.StateInterval, cfg.Logf)
	fatal := func(code, msg string, exit int) int {
		cfg.Logf("fatal %s: %s", code, msg)
		o.send(fatalMsg{Type: "fatal", Code: code, Message: msg})
		o.close(time.Now().Add(time.Second))
		return exit
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return fatal("stateDirUnavailable", "cannot create the state dir: "+err.Error(), ExitStateDir)
	}
	lock, err := acquireLock(cfg.StateDir, cfg.LockWait)
	if err != nil {
		var running errAlreadyRunning
		if errors.As(err, &running) {
			return fatal("alreadyRunning", running.Error(), ExitRunning)
		}
		return fatal("stateDirUnavailable", "cannot lock the state dir: "+err.Error(), ExitStateDir)
	}
	defer lock.Close()
	d, err := open(cfg, o, false)
	if err != nil {
		return fatal("stateDirUnavailable", err.Error(), ExitStateDir)
	}
	return d.serve(in, signals)
}

func (d *daemon) serve(in io.Reader, signals <-chan os.Signal) (code int) {
	defer func() {
		if r := recover(); r != nil {
			code = d.die(r, debug.Stack())
		}
	}()
	d.out.send(helloMsg{Type: "hello", Protocol: Protocol, HelperVersion: d.cfg.Version, PID: os.Getpid()})
	events := d.preWarm()
	d.emitState(true)
	d.sendEvents(companionEvents(events))
	d.arm("settingsWait", d.cfg.SettingsWait, timerMsg("settingsWait"))
	d.arm("tick", d.cfg.Tick, timerMsg("tick"))
	d.armMidnight()
	go d.readInput(in)
	for {
		select {
		case m := <-d.inbox:
			if done, code := d.handle(m); done {
				return code
			}
		case <-signals:
			d.logf("signal received, exiting")
			return d.shutdown()
		}
	}
}

// readInput is the stdin reader. It is not joined on exit: it ends at EOF or with the process.
func (d *daemon) readInput(in io.Reader) {
	r := ipc.NewReader(in)
	for {
		line, err := r.Next()
		if errors.Is(err, ipc.ErrLineTooLong) {
			d.logf("ignored an input line longer than 1 MiB")
			continue
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				d.logf("stdin: %v", err)
			}
			d.post(eofMsg{})
			return
		}
		req, ok, why := parseRequest(line)
		if !ok {
			d.logf("ignored input line (%s): %q", why, head(line, 200))
			continue
		}
		d.post(cmdMsg{req})
	}
}

func head(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

// Owner messages.
type (
	cmdMsg   struct{ req request }
	eofMsg   struct{}
	timerMsg string
	panicMsg struct {
		v     any
		stack []byte
	}
	usageMsg struct {
		c       *cycle
		at      time.Time
		results []scanResult
	}
	limitsMsg struct {
		c       *cycle
		at      time.Time
		results []fetchResult
	}
	warmMsg    struct{ got, failed int }
	speciesMsg struct {
		job *speciesJob
		got int
	}
	spriteMsg struct {
		gen   int
		res   sprites.Result
		err   error
		front bool
	}
	spriteRetryMsg struct{ gen int }
)

type scanResult struct {
	enr      usage.Enrichment
	err      error
	flushed  bool
	flushErr error
}

type fetchResult struct {
	providerID string
	status     *limits.Status
	err        error
	skipped    bool
}

// post delivers a message to the owner unless the daemon is exiting.
func (d *daemon) post(m any) {
	select {
	case d.inbox <- m:
	case <-d.quit:
	}
}

// work runs f in a tracked goroutine; a panic there ends the daemon with `fatal internal`.
func (d *daemon) work(f func()) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				d.post(panicMsg{r, debug.Stack()})
			}
		}()
		f()
	}()
}

// arm (re)starts a named one-shot timer that posts m.
func (d *daemon) arm(name string, after time.Duration, m any) {
	if t := d.timers[name]; t != nil {
		t.Stop()
	}
	d.timers[name] = time.AfterFunc(max(after, 0), func() { d.post(m) })
}

func (d *daemon) disarm(name string) {
	if t := d.timers[name]; t != nil {
		t.Stop()
		delete(d.timers, name)
	}
}

// armMidnight schedules a refresh just after the next local midnight. wake re-arms it, since a
// timer armed before a suspend fires late.
func (d *daemon) armMidnight() {
	now := d.cfg.Now().In(d.loc)
	y, m, day := now.Date()
	next := usage.StartOfDay(time.Date(y, m, day+1, 12, 0, 0, 0, d.loc))
	d.arm("midnight", next.Sub(now)+time.Second, timerMsg("midnight"))
}

func (d *daemon) armInterval() {
	if iv := d.settings.interval(); iv > 0 && d.started {
		d.arm("interval", iv, timerMsg("interval"))
	} else {
		d.disarm("interval")
	}
}

func (d *daemon) handle(m any) (done bool, code int) {
	switch m := m.(type) {
	case cmdMsg:
		d.command(m.req)
	case eofMsg:
		return true, d.shutdown()
	case panicMsg:
		return true, d.die(m.v, m.stack)
	case usageMsg:
		d.onUsage(m)
	case limitsMsg:
		d.onLimits(m)
	case warmMsg:
		d.onWarm(m)
	case speciesMsg:
		d.onSpeciesWarm(m)
	case spriteMsg:
		d.onSprite(m)
	case spriteRetryMsg:
		if m.gen == d.menu.gen {
			d.fetchMenuSprite()
		}
	case timerMsg:
		d.onTimer(string(m))
	}
	return false, 0
}

func (d *daemon) onTimer(name string) {
	switch name {
	case "settingsWait":
		if !d.started {
			d.logf("no setSettings within %s; using the last applied settings", d.cfg.SettingsWait)
			d.start()
		}
	case "tick":
		d.emitState(false)
		d.arm("tick", d.cfg.Tick, timerMsg("tick"))
	case "midnight":
		d.emitState(false)
		d.requestRefresh(false, nil, true)
		d.armMidnight()
	case "interval":
		d.requestRefresh(false, nil, true)
		d.armInterval()
	case "emptyRetry":
		d.requestRefresh(false, nil, false)
	case "levelUp":
		d.emitState(false)
	}
}

// die reports a recovered panic as `fatal internal`.
func (d *daemon) die(v any, stack []byte) int {
	d.logf("internal error: %v\n%s", v, stack)
	d.cancel()
	d.closeQuit()
	d.out.send(fatalMsg{Type: "fatal", Code: "internal", Message: fmt.Sprintf("internal error: %v", v)})
	d.out.close(time.Now().Add(time.Second))
	return ExitInternal
}

func (d *daemon) closeQuit() {
	select {
	case <-d.quit:
	default:
		close(d.quit)
	}
}

// shutdown flushes the usage caches and the persisted inputs, then exits within ExitBudget
// (protocol §2.5). In-flight network work is cancelled and pending commands get no reply.
func (d *daemon) shutdown() int {
	deadline := time.Now().Add(d.cfg.ExitBudget)
	d.cancel()
	d.closeQuit()
	for name := range d.timers {
		d.disarm(name)
	}
	d.saveSnapshot(true)
	flushed := make(chan struct{})
	go func() {
		defer close(flushed)
		for _, src := range d.cfg.Providers {
			if err := src.Flush(); err != nil {
				d.logf("flush %s usage cache: %v", src.ID(), err)
			}
		}
	}()
	select {
	case <-flushed:
	case <-time.After(time.Until(deadline) - 300*time.Millisecond):
		d.logf("usage cache flush did not finish in time")
	}
	d.out.close(deadline)
	joined := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(joined)
	}()
	select {
	case <-joined:
	case <-time.After(time.Until(deadline)):
	}
	return ExitOK
}

// ---------------------------------------------------------------- state emission

// emitState builds a state and queues it if anything but generatedAt changed, or always when
// forced (an explicit trigger of §2.4).
func (d *daemon) emitState(force bool) {
	now := d.cfg.Now()
	st := d.buildState(now)
	st.Meta.GeneratedAt = ""
	key, err := json.Marshal(st)
	if err != nil {
		d.logf("encode state: %v", err)
		return
	}
	if lu := st.Companion.LevelUpUntil; lu != nil && !d.oneShot {
		if t, err := time.Parse(time.RFC3339, *lu); err == nil {
			d.arm("levelUp", t.Sub(now)+20*time.Millisecond, timerMsg("levelUp"))
		}
	}
	if !force && bytes.Equal(key, d.lastStateKey) {
		return
	}
	d.lastStateKey = key
	st.Meta.GeneratedAt = ts(now.In(d.loc))
	b, err := json.Marshal(st)
	if err != nil {
		d.logf("encode state: %v", err)
		return
	}
	if !d.oneShot {
		d.out.setState(b)
	}
}

// sendEvents queues events after the state that reflects them.
func (d *daemon) sendEvents(events []any) {
	if d.oneShot || len(events) == 0 {
		return
	}
	d.emitState(false)
	for _, e := range events {
		d.out.send(e)
	}
}

func companionEvents(evs []companion.Event) []any {
	out := make([]any, len(evs))
	for i, e := range evs {
		out[i] = e
	}
	return out
}

func (d *daemon) reply(id int64, data any) {
	d.out.send(resultMsg{Type: "result", ID: id, OK: true, Data: data})
}

func (d *daemon) replyErr(id int64, code, msg string) {
	d.out.send(resultMsg{Type: "result", ID: id, OK: false, Error: &errorBody{Code: code, Message: msg}})
}

// ---------------------------------------------------------------- scheduler and refresh cycle

// start launches the first cycle, after setSettings or the settings wait (protocol §2.1 step 6).
func (d *daemon) start() {
	d.started = true
	d.disarm("settingsWait")
	c := d.pending
	if c == nil {
		c = &cycle{}
	}
	d.pending = nil
	c.scheduleEmpty = true
	d.startCycle(c)
	d.armInterval()
}

// requestRefresh starts a cycle, or sets the single pending rerun when one is running
// (usage.md §6.1). scheduleEmpty is false only for the empty retry itself.
func (d *daemon) requestRefresh(manual bool, waiter *int64, scheduleEmpty bool) {
	c := &cycle{manual: manual, scheduleEmpty: scheduleEmpty}
	if waiter != nil {
		c.waiters = []int64{*waiter}
	}
	if !d.started || d.cycle != nil {
		if d.pending == nil {
			d.pending = c
		} else {
			d.pending.merge(c)
		}
		return
	}
	d.startCycle(c)
}

func (d *daemon) startCycle(c *cycle) {
	d.cycle = c
	d.refreshing = true
	d.emitState(true)
	now := d.cfg.Now().In(d.loc)
	ws := d.settings.weekday()
	srcs := d.cfg.Providers
	// The usage-cache flush is due once per SnapshotEvery. It runs in the worker after the scan,
	// so a full cache write never lands on the owner; the result reports any cacheWrite error.
	flush := d.lastFlush.IsZero() || now.Sub(d.lastFlush) >= d.cfg.SnapshotEvery
	if flush {
		d.lastFlush = now
	}
	d.work(func() {
		results := make([]scanResult, len(srcs))
		for i, s := range srcs {
			results[i].enr, results[i].err = s.Scan(now, ws)
			if flush {
				results[i].flushed, results[i].flushErr = true, s.Flush()
			}
		}
		d.post(usageMsg{c: c, at: now, results: results})
	})
}

// applyUsage records one usage phase: per-provider data, lastUpdated, the history ledger and the
// usage-cache flush. It reports whether a provider failed and whether any usage is displayable.
func (d *daemon) applyUsage(at time.Time, results []scanResult) (anyErr, hasUsage bool) {
	for i, src := range d.cfg.Providers {
		p := d.usage[src.ID()]
		if err := results[i].err; err != nil {
			// Keep the previous data; today only counts while its day key is today.
			p.scanErr = err
			anyErr = true
			d.logf("%s usage scan failed: %v", src.ID(), err)
			continue
		}
		p.apply(results[i].enr)
	}
	hasUsage = d.hasUsageData(at)
	if !anyErr || (d.lastUpdated.IsZero() && hasUsage) {
		d.lastUpdated = at
	}
	if d.ledger.Record(d.combinedMonthDaily(at), at) {
		d.notePersist(writeJSONAtomic(d.path(usageLedgerFile), d.ledger), usageLedgerFile)
	}
	for i, src := range d.cfg.Providers {
		if results[i].flushed {
			d.cacheErr[src.ID()] = results[i].flushErr
		}
	}
	d.scanned = true
	return anyErr, hasUsage
}

func (d *daemon) onUsage(m usageMsg) {
	anyErr, hasUsage := d.applyUsage(m.at, m.results)
	d.disarm("emptyRetry")
	if m.c.scheduleEmpty && !anyErr && !hasUsage {
		d.logf("no usage found; retrying once in %s", d.cfg.EmptyRetry)
		d.arm("emptyRetry", d.cfg.EmptyRetry, timerMsg("emptyRetry"))
	}
	d.warmPasses = 0
	d.companionUpdate()
	now := d.cfg.Now()
	var specs []LimitsSpec
	results := make([]fetchResult, 0, len(d.cfg.Limits))
	for _, spec := range d.cfg.Limits {
		if !m.c.manual && d.limits[spec.ProviderID].backoff.Active(now) {
			results = append(results, fetchResult{providerID: spec.ProviderID, skipped: true})
			continue
		}
		specs = append(specs, spec)
	}
	if len(specs) == 0 {
		d.onLimits(limitsMsg{c: m.c, at: now, results: results})
		return
	}
	d.work(func() {
		for _, spec := range specs {
			st, err := spec.Fetcher.Fetch(d.ctx)
			results = append(results, fetchResult{providerID: spec.ProviderID, status: st, err: err})
		}
		d.post(limitsMsg{c: m.c, at: d.cfg.Now(), results: results})
	})
}

// applyLimits records fetch results and returns the alert and auth events they cause. Alert tiers
// are evaluated after a successful fetch only, and persisted (protocol §6, §14.1).
func (d *daemon) applyLimits(at time.Time, results []fetchResult) []any {
	var events []any
	warn, crit := float64(d.settings.WarnThreshold), float64(d.settings.CritThreshold)
	for _, r := range results {
		l := d.limits[r.providerID]
		if r.skipped || l == nil {
			continue
		}
		before := l.authState()
		l.applyResult(r.status, r.err, at)
		if r.err != nil {
			d.logf("%s limits fetch failed: %v", r.providerID, r.err)
		}
		if after := l.authState(); after == "expired" && before != "expired" {
			events = append(events, authExpiredEvent{Type: "event", Kind: "authExpired", ProviderID: r.providerID, ErrorCode: l.errCode})
		}
		if r.err != nil || d.oneShot {
			continue
		}
		next, alerts := limits.EvaluateAlerts(d.alertTiers[r.providerID], l.status.Windows(at, warn, crit), warn, crit)
		if !mapsEqual(next, d.alertTiers[r.providerID]) {
			d.alertTiers[r.providerID] = next
			d.notePersist(writeJSONAtomic(d.path(alertTiersFile), d.alertTiers), alertTiersFile)
		}
		for _, a := range alerts {
			events = append(events, limitAlertEvent{Type: "event", Kind: "limitAlert", ProviderID: r.providerID,
				WindowKey: a.WindowKey, WindowKind: a.WindowKind, Label: a.Label, Tier: a.Tier, Utilization: a.Utilization})
		}
	}
	return events
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// grantCandies runs the Rare Candy rule over every provider's held limits (usage.md §6.8).
func (d *daemon) grantCandies() []companion.Event {
	var windows []companion.CandyWindow
	ready := false
	for _, spec := range d.cfg.Limits {
		st := d.limits[spec.ProviderID].status
		if st == nil {
			continue
		}
		ready = true
		for _, w := range limits.CandyWindows(st, limits.KeyPrefix) {
			windows = append(windows, companion.CandyWindow{Key: w.Key, Kind: w.Kind, Utilization: w.Utilization,
				Epoch: w.Epoch, ProviderID: spec.ProviderID})
		}
	}
	return d.store.GrantCandies(windows, ready)
}

func (d *daemon) onLimits(m limitsMsg) {
	events := d.applyLimits(m.at, m.results)
	events = append(events, companionEvents(d.grantCandies())...)
	d.emitState(false)
	d.sendEvents(events)
	d.finishCycle(m.c)
}

func (d *daemon) finishCycle(c *cycle) {
	d.cycle = nil
	d.refreshing = false
	d.fromCache = false
	d.saveSnapshot(false)
	d.emitState(true)
	for _, id := range c.waiters {
		d.reply(id, struct{}{})
	}
	if p := d.pending; p != nil {
		d.pending = nil
		d.startCycle(p)
	}
}

// ---------------------------------------------------------------- companion

// updateCompanion runs one Store.Update on the memory-only data view and returns its events and
// the data it missed. The draws of a pass that left a ready egg unhatched are requeued, so the
// pass after the warm rolls the same species (see replayRng).
func (d *daemon) updateCompanion() ([]companion.Event, []want) {
	d.rng.commit()
	// Until this process has scanned, the usage restored from last-snapshot.json may be older than
	// the save's ledger; crediting it would rebase the ledger down and credit the gap twice.
	in := companion.UsageInput{}
	if d.scanned {
		in = d.usageInput(d.cfg.Now())
	}
	events := d.store.Update(d.ctx, in)
	wants := d.data.takeWants()
	if len(wants) > 0 && len(events) == 0 && d.eggReady() {
		d.rng.requeue()
	} else {
		d.rng.commit()
	}
	return events, wants
}

func (d *daemon) eggReady() bool {
	v := d.store.BuildState(companion.MoodInput{Now: d.cfg.Now()})
	return v.Egg != nil && v.Egg.Used >= v.Egg.Threshold
}

// companionUpdate is Update after a usage phase, a warm or networkUp: events go out after the
// state, and missing data is warmed in the background.
func (d *daemon) companionUpdate() {
	events, wants := d.updateCompanion()
	d.emitState(false)
	d.sendEvents(companionEvents(events))
	if len(wants) > 0 {
		d.startWarm(wants)
	}
}

func (d *daemon) startWarm(wants []want) {
	if d.warmBusy {
		d.warmAgain = true
		return
	}
	d.warmBusy = true
	d.work(func() {
		got, failed := d.data.warm(d.ctx, wants)
		d.post(warmMsg{got, failed})
	})
}

func (d *daemon) onWarm(m warmMsg) {
	d.warmBusy = false
	again := d.warmAgain
	d.warmAgain = false
	if m.failed > 0 && m.got == 0 {
		if !d.pokeDown {
			d.pokeDown = true
			d.logf("PokéAPI unreachable; companion data retries on the next refresh")
			d.sendEvents([]any{errorEvent{Type: "event", Kind: "error", Code: "pokeapiUnreachable",
				Message: "PokéAPI request failed; the companion retries on the next refresh"}})
		}
	} else if m.got > 0 {
		d.pokeDown = false
	}
	if (m.got > 0 || again) && d.warmPasses < maxWarmPasses {
		d.warmPasses++
		d.companionUpdate()
	}
}

// preWarm loads the active Pokémon's line, details and names before the first state, so it does
// not show "#id" after every restart. It is bounded by PreWarm; the pokeapi disk cache answers in
// milliseconds, and a cancelled request falls back to a stale copy. The base index is never warmed
// here: a cancelled base-index request would start pokeapi's REST rebuild.
func (d *daemon) preWarm() (events []companion.Event) {
	if v := d.store.BuildState(companion.MoodInput{Now: d.cfg.Now()}); v.Active == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(d.ctx, d.cfg.PreWarm)
	defer cancel()
	for pass := 0; pass < 4 && ctx.Err() == nil; pass++ {
		d.rng.commit()
		// No usage input: this only loads the line, evaluates banked growth and warms names.
		events = append(events, d.store.Update(ctx, companion.UsageInput{})...)
		d.rng.commit()
		var wants []want
		for _, w := range d.data.takeWants() {
			if w.kind != wantBase {
				wants = append(wants, w)
			}
		}
		if len(wants) == 0 {
			break
		}
		if got, _ := d.data.warm(ctx, wants); got == 0 {
			break
		}
	}
	return events
}
