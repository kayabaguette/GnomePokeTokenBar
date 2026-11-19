package companion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Save transfer: the export/import envelope, the import rebase and the snapshot summaries
// (upstream SaveTransfer.swift, SaveSnapshots.swift and CompanionStore.applySave; protocol §7.7,
// docs/spec/save-transfer.md).

const (
	// MaxSaveBytes caps a save file before parsing (ST:68-70).
	MaxSaveBytes  = 8 * 1024 * 1024
	backupPrefix  = "companion-state.pre-import-"
	backupsToKeep = 5
)

// Summary is the progress a confirmation dialog compares (ST:33-41).
type Summary struct {
	DexCount       int `json:"dexCount"`
	LifetimeTokens int `json:"lifetimeTokens"`
}

func (st *state) summary() Summary {
	return Summary{DexCount: len(st.Dex), LifetimeTokens: st.UsedSinceInstall}
}

// SnapshotSummary is one row of listSnapshots (protocol §7.7, SS:4-13).
type SnapshotSummary struct {
	ID             string  `json:"id"`
	Date           string  `json:"date"`
	DexCount       int     `json:"dexCount"`
	LifetimeTokens int     `json:"lifetimeTokens"`
	SpeciesID      *int    `json:"speciesId"`
	Shiny          bool    `json:"shiny"`
	Form           *string `json:"form"`
}

// snapshotSummary describes a snapshot of st dated at. The species is the current one, the shiny
// flag hides a disguised Ditto (SST:128-156) and the form is resolved for that species (SS:78-82).
func snapshotSummary(id string, at time.Time, st *state) SnapshotSummary {
	sum := SnapshotSummary{ID: id, Date: *ts(at), DexCount: len(st.Dex), LifetimeTokens: st.UsedSinceInstall}
	if a := st.Active; a != nil {
		species := a.currentID()
		sum.SpeciesID, sum.Shiny, sum.Form = &species, a.displaysShiny(), formForSpecies(species, a.Form)
	}
	return sum
}

// Envelope is a validated, sanitized save file.
type Envelope struct {
	Schema       int
	AppVersion   string
	ExportedAt   time.Time
	SourceDevice string
	st           state
}

// Summary is the progress the file carries.
func (e *Envelope) Summary() Summary { return e.st.summary() }

// ---------------------------------------------------------------- decode

func transferError(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// DecodeEnvelope validates a save file in upstream's order (ST:113-132): the size cap before any
// parsing, then the {format, schema} header alone, so a newer file whose body does not parse still
// reports its schema, then the whole envelope with its six fields. The state is then read leniently
// with the legacy Unown keys and sanitized. Errors are *Error with the protocol §11.2 codes.
func DecodeEnvelope(data []byte) (*Envelope, error) {
	if len(data) > MaxSaveBytes {
		return nil, transferError("fileTooLarge", "the file is at least %d bytes; a save is at most %d", len(data), MaxSaveBytes)
	}
	notSave := transferError("notASaveFile", "not a PokeTokenBar save file")
	o, ok := parseObject(data)
	if !ok {
		return nil, notSave
	}
	format, ferr := required[string](o, "format")
	schema, serr := required[int](o, "schema")
	if ferr != nil || serr != nil || format != envelopeFormat {
		return nil, notSave
	}
	if schema > envelopeSchema {
		return nil, transferError("newerSchema", "the save has schema %d; this version reads up to %d", schema, envelopeSchema)
	}
	appVersion, err1 := required[string](o, "appVersion")
	exported, err2 := required[string](o, "exportedAt")
	device, err3 := required[string](o, "sourceDevice")
	at, err4 := time.Parse(time.RFC3339, exported)
	stateObj, ok := parseObject(o["state"])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || !ok {
		return nil, transferError("notASaveFile", "the save envelope is unreadable")
	}
	raw, err := json.Marshal(aliasLegacyForms(stateObj))
	if err != nil {
		return nil, notSave
	}
	st, ok := decodeState(raw)
	if !ok {
		return nil, notSave
	}
	st.sanitize()
	return &Envelope{Schema: schema, AppVersion: appVersion, ExportedAt: at, SourceDevice: device, st: st}, nil
}

// aliasLegacyForms reads the pre-form Unown keys of older macOS saves as forms: unownForm on the
// active Pokémon and on dex entries, pendingUnownForm and representativeUnownForm. A valid new key
// wins, as upstream tries the keys in order (PokemonForm.decoded, CM:548, :629, :721, :737). The
// objects stay raw JSON so the uint64 profile seed is never parsed as a float.
func aliasLegacyForms(o jsonObject) jsonObject {
	alias(o, "pendingForm", "pendingUnownForm")
	alias(o, "representativeForm", "representativeUnownForm")
	if a, ok := parseObject(o["active"]); ok {
		alias(a, "form", "unownForm")
		if b, err := json.Marshal(a); err == nil {
			o["active"] = b
		}
	}
	var dex []json.RawMessage
	if raw, ok := o["dex"]; ok && json.Unmarshal(raw, &dex) == nil {
		for i, e := range dex {
			if eo, ok := parseObject(e); ok {
				alias(eo, "form", "unownForm")
				if b, err := json.Marshal(eo); err == nil {
					dex[i] = b
				}
			}
		}
		if b, err := json.Marshal(dex); err == nil {
			o["dex"] = b
		}
	}
	return o
}

func alias(o jsonObject, key, legacy string) {
	if _, err := required[string](o, key); err == nil {
		return
	}
	if _, err := required[string](o, legacy); err == nil {
		o[key] = o[legacy]
	}
}

// ReadSave reads a save file (at most MaxSaveBytes + 1 bytes, so a huge file is never loaded) and
// validates it. It returns the bytes read, for the digest. File errors are returned unwrapped from
// the os package so the caller can classify them.
func ReadSave(path string) ([]byte, *Envelope, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxSaveBytes+1))
	if err != nil {
		return nil, nil, err
	}
	env, err := DecodeEnvelope(data)
	if err != nil {
		return nil, nil, err
	}
	return data, env, nil
}

// ---------------------------------------------------------------- encode

// encodeEnvelope writes st as an envelope with every key sorted and pretty-printed (ST:106-110).
// Every date is UTC whole seconds with Z (protocol §1.1): the macOS app decodes with .iso8601,
// which rejects fractional seconds, and would silently drop each dex entry (save-transfer.md §4.3).
// The live save keeps its nanosecond dates. An export leaves out growthDifficulty, a setting of
// this computer (protocol §7.7); snapshots and backups keep it, because the corrupt-save recovery
// restores a snapshot as the save and must not rescale it at the next setSettings.
func encodeEnvelope(st *state, now time.Time, appVersion, device string, export bool) ([]byte, error) {
	c := withWholeSecondDates(st)
	stateJSON, err := encodeState(&c)
	if err != nil {
		return nil, err
	}
	// Re-decode with json.Number (exact digits, so the uint64 seed survives) to sort every key.
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(stateJSON))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if export {
		delete(doc, "growthDifficulty")
	}
	return json.MarshalIndent(map[string]any{
		"appVersion": appVersion, "exportedAt": *ts(now), "format": envelopeFormat,
		"schema": envelopeSchema, "sourceDevice": device, "state": doc,
	}, "", "  ")
}

// withWholeSecondDates is a shallow copy of st whose dex dates are UTC whole seconds. The dex is
// copied, so the live entries keep their own times.
func withWholeSecondDates(st *state) state {
	c := *st
	c.Dex = make([]dexEntry, len(st.Dex))
	for i, e := range st.Dex {
		e.CaughtAt, e.ReleasedAt = wholeSecond(e.CaughtAt), wholeSecond(e.ReleasedAt)
		c.Dex[i] = e
	}
	return c
}

// hostName is the envelope's sourceDevice (protocol §7.7).
func hostName() string {
	h, _ := os.Hostname()
	return h
}

func wholeSecond(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC().Truncate(time.Second)
	return &v
}

// ---------------------------------------------------------------- store operations

// Summary is the progress of the save in use.
func (s *Store) Summary() Summary { return s.st.summary() }

// ExportSave writes the save to path as an envelope and returns its size. The write is atomic (a
// temp file renamed in the target directory); a directory that refuses the temp file, such as a
// document-portal mount, gets a direct write instead (save-transfer.md §5.1).
func (s *Store) ExportSave(path string) (int, error) {
	data, err := encodeEnvelope(&s.st, s.now(), s.appVersion, hostName(), true)
	if err != nil {
		return 0, err
	}
	if err := writeFile(path, data, true); err != nil {
		return 0, err
	}
	return len(data), nil
}

// ApplyImport replaces this computer's progress with the envelope's (CS:2020-2049). The current
// state is first written as an envelope backup; if that fails nothing changes and the error is
// backupFailed. The device ledger is re-seeded from this computer's usage today, the account
// ledger is merged, and growthDifficulty, schemaVersion and language are kept. Badges the save
// already earned become history: an import announces nothing.
func (s *Store) ApplyImport(env *Envelope, in UsageInput) error {
	if err := s.backupBeforeImport(); err != nil {
		s.logf("companion: import stopped, the backup failed: %v", err)
		return transferError("backupFailed", "the current save could not be backed up: %v", err)
	}
	s.st = rebase(env.st, &s.st, in)
	s.line, s.prefetchedLineID, s.hatchDelayed, s.warmPending = nil, 0, false, true
	s.window = levelUpWindow{}
	s.lastSeenDate, s.lastSeenTotal, s.haveLastSeen = "", 0, false
	s.ensureProfiles()
	s.seedDexCompletions()
	s.save()
	s.pending = nil
	s.logf("companion: save imported from %q: dex=%d lifetime=%d", env.SourceDevice, len(s.st.Dex), s.st.UsedSinceInstall)
	return nil
}

// backupBeforeImport writes companion-state.pre-import-<local stamp>.json in the state dir and keeps
// the newest five. The stamp sorts by time, so name order is age order (CS:2055-2077).
func (s *Store) backupBeforeImport() error {
	now := s.now()
	data, err := encodeEnvelope(&s.st, now, s.appVersion, hostName(), false)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(s.dir, backupPrefix+now.Local().Format(snapshotStampLayout)+".json"), data); err != nil {
		return err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), backupPrefix) {
			backups = append(backups, e.Name())
		}
	}
	sort.Strings(backups)
	for _, stale := range backups[:max(0, len(backups)-backupsToKeep)] {
		_ = os.Remove(filepath.Join(s.dir, stale))
	}
	return nil
}

// rebase re-seats an imported state on this computer (ST:211-239). Progress is copied; the device
// ledger (baseline, per-provider claimed, lastDate) restarts from today's usage, or is deferred to
// the fresh-install path when today's usage is unknown; the account ledger is merged; the device
// settings stay. growthDifficulty is kept because replacing it would make the next setSettings
// rescale the imported progress (save-transfer.md §4.2 hazard 2).
func rebase(imported state, current *state, in UsageInput) state {
	st := imported
	st.SchemaVersion, st.GrowthDifficulty, st.Language = current.SchemaVersion, current.GrowthDifficulty, current.Language
	st.CandyGrantTier = mergedGrantTier(imported.CandyGrantTier, current.CandyGrantTier)
	st.CandyWindowEpoch = mergedWindowEpochs(imported.CandyWindowEpoch, imported.CandyGrantTier,
		current.CandyWindowEpoch, current.CandyGrantTier)
	st.CandyFeatureSeeded = imported.CandyFeatureSeeded || current.CandyFeatureSeeded
	if in.HasUsageData && len(in.TodayByProvider) > 0 {
		st.InstallBaselineSet, st.Claimed, st.LastDate = true, copyLedger(in.TodayByProvider), in.TodayDate
	} else {
		st.InstallBaselineSet, st.Claimed, st.LastDate = false, nil, ""
	}
	return st
}

// mergedGrantTier keeps the higher tier per window key (ST:241-244).
func mergedGrantTier(a, b map[string]int) map[string]int {
	out := make(map[string]int, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = max(out[k], v)
	}
	return out
}

// mergedWindowEpochs (ST:246-268): a window granted on both sides keeps the later epoch, so it is
// not granted again; granted on one side, that side's epoch; granted on neither, the later epoch.
// Epochs compare as strings, as upstream's max() does.
func mergedWindowEpochs(epochsA map[string]string, tiersA map[string]int, epochsB map[string]string, tiersB map[string]int) map[string]string {
	keys := map[string]bool{}
	for _, m := range []map[string]string{epochsA, epochsB} {
		for k := range m {
			keys[k] = true
		}
	}
	for _, m := range []map[string]int{tiersA, tiersB} {
		for k := range m {
			keys[k] = true
		}
	}
	out := map[string]string{}
	for k := range keys {
		ea, okA := epochsA[k]
		eb, okB := epochsB[k]
		grantedA, grantedB := tiersA[k] > 0, tiersB[k] > 0
		switch {
		case grantedA && !grantedB:
			okB = false
		case grantedB && !grantedA:
			okA = false
		}
		switch {
		case okA && okB:
			out[k] = max(ea, eb)
		case okA:
			out[k] = ea
		case okB:
			out[k] = eb
		}
	}
	return out
}

// ---------------------------------------------------------------- snapshots

// ListSnapshots returns the readable snapshots, newest first, at most ten.
func (s *Store) ListSnapshots() []SnapshotSummary {
	out := []SnapshotSummary{}
	for _, snap := range s.listSnapshots() {
		if len(out) == maxSnapshots {
			break
		}
		out = append(out, snap.summary)
	}
	return out
}

// CreateSnapshot writes a snapshot now, with no interval check (SV:1140-1148).
func (s *Store) CreateSnapshot() (SnapshotSummary, error) {
	now := s.now()
	name, err := s.writeSnapshot(now)
	if err != nil {
		return SnapshotSummary{}, err
	}
	return snapshotSummary(name, now, &s.st), nil
}

// RestoreSnapshot imports a listed snapshot (CS:2119-2136). The chosen file is read and validated
// before the safety snapshot of the current state is taken: at ten files that snapshot prunes the
// oldest, which may be the chosen one (DL:1282-1289). A safety snapshot failure is only logged.
func (s *Store) RestoreSnapshot(id string, in UsageInput) error {
	path := ""
	for _, snap := range s.listSnapshots() {
		if snap.id == id {
			path = snap.path
		}
	}
	if path == "" {
		return transferError("notFound", "no snapshot %q", id)
	}
	_, env, err := ReadSave(path)
	if err != nil {
		return err
	}
	if _, err := s.writeSnapshot(s.now()); err != nil {
		s.logf("companion: safety snapshot before the restore failed: %v", err)
	}
	return s.ApplyImport(env, in)
}
