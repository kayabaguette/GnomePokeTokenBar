package companion

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Persistence (upstream CompanionStore load/save, SaveSnapshots.swift, SaveTransfer envelope).

const (
	stateFileName       = "companion-state.json"
	snapshotPrefix      = "companion-snapshot-"
	maxSnapshots        = 10
	autoSnapshotEvery   = 12 * time.Hour
	envelopeFormat      = "poketokenbar.save"
	envelopeSchema      = 2
	snapshotStampLayout = "2006-01-02-150405" // local time, as upstream's DateFormatter
)

// writeFileAtomic writes data to a temp file in the same directory, fsyncs it and renames it over
// path, so a crash never leaves a partial file.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if d, derr := os.Open(dir); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// loadState reads the save. A missing file is a fresh install. A file that is not a JSON object is
// moved to .corrupt and the newest valid snapshot is restored (sanitized and saved); without one
// the state starts fresh and nothing is written (CS:2278-2305).
func (s *Store) loadState() error {
	path := filepath.Join(s.dir, stateFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		s.st = freshState()
		return nil
	}
	if err != nil {
		return err
	}
	if st, ok := decodeState(data); ok {
		st.sanitize()
		s.st = st
		return nil
	}
	backup := path + ".corrupt"
	_ = os.Remove(backup)
	if err := os.Rename(path, backup); err != nil {
		s.logf("companion: cannot move corrupt save aside: %v", err)
	} else {
		s.logf("companion: save did not decode; original kept as %s", filepath.Base(backup))
	}
	if st, ok := s.latestValidSnapshot(); ok {
		st.sanitize()
		s.st = st
		s.logf("companion: recovered state from the newest snapshot")
		s.save()
		return nil
	}
	s.st = freshState()
	return nil
}

// save writes the save atomically after announcing new dex badges (CS:2306-2311).
func (s *Store) save() {
	s.checkDexCompletions()
	data, err := encodeState(&s.st)
	if err == nil {
		err = writeFileAtomic(filepath.Join(s.dir, stateFileName), data)
	}
	if err != nil && s.saveErr == nil {
		s.logf("companion: save failed: %v", err)
	}
	s.saveErr = err
}

// ---------------------------------------------------------------- snapshots

type envelope struct {
	AppVersion   string          `json:"appVersion"`
	ExportedAt   string          `json:"exportedAt"`
	Format       string          `json:"format"`
	Schema       int             `json:"schema"`
	SourceDevice string          `json:"sourceDevice"`
	State        json.RawMessage `json:"state"`
}

type snapshotInfo struct {
	path string
	id   string // file name
	date time.Time
}

func (s *Store) snapshotDir() string { return filepath.Join(s.dir, ".snapshots", stateFileName) }

// listSnapshots returns the readable snapshots, newest first (SS:62-103). Unreadable files are skipped.
func (s *Store) listSnapshots() []snapshotInfo {
	entries, err := os.ReadDir(s.snapshotDir())
	if err != nil {
		return nil
	}
	var out []snapshotInfo
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, snapshotPrefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(s.snapshotDir(), name)
		env, ok := readEnvelope(path)
		if !ok {
			continue
		}
		date, err := time.Parse(time.RFC3339, env.ExportedAt)
		if err != nil {
			continue
		}
		out = append(out, snapshotInfo{path: path, id: name, date: date})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].date.Equal(out[j].date) {
			return out[i].date.After(out[j].date)
		}
		return out[i].id > out[j].id
	})
	return out
}

func readEnvelope(path string) (envelope, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return envelope{}, false
	}
	var env envelope
	if json.Unmarshal(data, &env) != nil || env.Format != envelopeFormat || env.Schema > envelopeSchema {
		return envelope{}, false
	}
	if _, ok := parseObject(env.State); !ok {
		return envelope{}, false
	}
	return env, true
}

func (s *Store) latestValidSnapshot() (state, bool) {
	for _, snap := range s.listSnapshots() {
		if env, ok := readEnvelope(snap.path); ok {
			if st, ok := decodeState(env.State); ok {
				return st, true
			}
		}
	}
	return state{}, false
}

// createSnapshot writes an envelope snapshot named after the local time, with a -N suffix on a
// clash, then prunes to the newest ten (SS:134-174).
func (s *Store) createSnapshot(now time.Time) error {
	dir := s.snapshotDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	base := snapshotPrefix + now.Local().Format(snapshotStampLayout)
	name := base + ".json"
	for n := 1; ; n++ {
		if _, err := os.Stat(filepath.Join(dir, name)); errors.Is(err, fs.ErrNotExist) {
			break
		}
		name = fmt.Sprintf("%s-%d.json", base, n)
	}
	stateJSON, err := encodeState(&s.st)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	data, err := json.MarshalIndent(envelope{
		AppVersion: s.appVersion, ExportedAt: now.UTC().Format(time.RFC3339), Format: envelopeFormat,
		Schema: envelopeSchema, SourceDevice: host, State: stateJSON,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, name), data); err != nil {
		return err
	}
	s.newestSnapshot = now
	if snaps := s.listSnapshots(); len(snaps) > maxSnapshots {
		for _, old := range snaps[maxSnapshots:] {
			_ = os.Remove(old.path)
		}
	}
	return nil
}

// autoSnapshotIfNeeded snapshots a non-trivial state when the newest snapshot is ≥ 12 h old
// (CS:2104-2117).
func (s *Store) autoSnapshotIfNeeded(now time.Time) {
	if !s.newestSnapshot.IsZero() && now.Sub(s.newestSnapshot) < autoSnapshotEvery {
		return
	}
	if s.st.UsedSinceInstall <= 0 && len(s.st.Dex) == 0 && s.st.Active == nil {
		return
	}
	if err := s.createSnapshot(now); err != nil {
		s.logf("companion: snapshot failed: %v", err)
	}
}
