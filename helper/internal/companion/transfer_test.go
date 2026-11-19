package companion

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// Save transfer tests. The Swift ones are SaveTransferTests.swift (STT) and SaveSnapshotTests.swift
// (SST); transferNow is their 1_700_000_000 clock (fixedNow) and the day key is 2026-08-03.

const transferDay = "2026-08-03"

func transferCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err != nil {
		return "other: " + err.Error()
	}
	return ""
}

// oldMacState is STT oldMacState: a computer that already credited 56.8M today.
func oldMacState() state {
	st := freshState()
	st.InstallBaselineSet, st.UsedSinceInstall, st.SpentTokens = true, 8_000_000_000, 3_500_000_000
	st.Claimed, st.LastDate = map[string]int{"test": 56_800_000}, transferDay
	st.Inventory = map[string]int{"rareCandy": 2}
	st.CollectedFinals = toSet([]string{"1-3"})
	st.Dex = []dexEntry{entry(1, 3, []int{1, 2, 3}, rarityCommon)}
	st.RepresentativeSpeciesID = intPtr(2)
	return st
}

func encodeFor(t *testing.T, st state, device string) []byte {
	t.Helper()
	data, err := encodeEnvelope(&st, fixedNow, "2.5.0", device, true)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeOK(t *testing.T, data []byte) *Envelope {
	t.Helper()
	env, err := DecodeEnvelope(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env
}

func usage(m map[string]int, has bool) UsageInput {
	return UsageInput{TodayByProvider: m, TodayDate: transferDay, HasUsageData: has}
}

func applyOK(t *testing.T, s *Store, data []byte, in UsageInput) {
	t.Helper()
	if err := s.ApplyImport(decodeOK(t, data), in); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

// patch rewrites top-level envelope keys; a nil value deletes the key.
func patch(t *testing.T, data []byte, keys map[string]any) []byte {
	t.Helper()
	var o map[string]json.RawMessage
	if err := json.Unmarshal(data, &o); err != nil {
		t.Fatal(err)
	}
	for k, v := range keys {
		if v == nil {
			delete(o, k)
			continue
		}
		b, _ := json.Marshal(v)
		o[k] = b
	}
	out, _ := json.Marshal(o)
	return out
}

// ---------------------------------------------------------------- envelope

func TestEnvelopeRoundTripPreservesProgress(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:99
	env := decodeOK(t, encodeFor(t, oldMacState(), "Old Mac"))
	st := env.st
	if env.SourceDevice != "Old Mac" || env.Schema != 2 || env.AppVersion != "2.5.0" || !env.ExportedAt.Equal(fixedNow) {
		t.Fatalf("header %+v", env)
	}
	if st.UsedSinceInstall != 8_000_000_000 || st.SpentTokens != 3_500_000_000 || !reflect.DeepEqual(st.Inventory, map[string]int{"rareCandy": 2}) ||
		len(st.Dex) != 1 || !reflect.DeepEqual(st.CollectedFinals, toSet([]string{"1-3"})) || st.RepresentativeSpeciesID == nil || *st.RepresentativeSpeciesID != 2 {
		t.Fatalf("%+v", st)
	}
}

func TestEnvelopeRoundTripKeepsRepeatGrowthBoost(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:138
	st := freshState()
	st.Active = &monState{BaseID: 1, PathIDs: []int{1}, PlannedPathIDs: []int{1, 2, 3}, UsedAtStage: 12_000_000,
		Rarity: rarityCommon, TotalForms: 3, HasGrowthBoost: true}
	a := decodeOK(t, encodeFor(t, st, "Old Mac")).st.Active
	if a == nil || !a.HasGrowthBoost || a.UsedAtStage != 12_000_000 || a.standardThreshold() != 62_500_000 {
		t.Fatalf("%+v", a)
	}
}

func TestForeignJSONIsRejectedRatherThanImportedAsEmptyState(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:155
	if _, err := DecodeEnvelope([]byte(`{"some":"other app","dex":123}`)); transferCode(err) != "notASaveFile" {
		t.Fatalf("foreign: %v", err)
	}
	st := oldMacState()
	bare, _ := encodeState(&st)
	if _, err := DecodeEnvelope(bare); transferCode(err) != "notASaveFile" {
		t.Fatalf("bare state: %v", err)
	}
}

func TestValidEnvelopeWithWrongFormatIDIsRejected(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:173 — the B-only branch of the header gate.
	data := encodeFor(t, oldMacState(), "Other App")
	// Premise: the same envelope with only the format rewritten (here to itself) is valid.
	decodeOK(t, patch(t, data, map[string]any{"format": envelopeFormat}))
	if _, err := DecodeEnvelope(patch(t, data, map[string]any{"format": "someotherapp.save"})); transferCode(err) != "notASaveFile" {
		t.Fatalf("wrong format: %v", err)
	}
}

func TestNewerSchemaIsRejected(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:191
	_, err := DecodeEnvelope(patch(t, encodeFor(t, freshState(), "Future Mac"), map[string]any{"schema": 3}))
	if transferCode(err) != "newerSchema" || !strings.Contains(err.Error(), "schema 3") || !strings.Contains(err.Error(), "up to 2") {
		t.Fatalf("%v", err)
	}
}

func TestNewerSchemaIsReportedEvenWhenTheBodyIsUnreadable(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:727
	_, err := DecodeEnvelope([]byte(`{"format":"poketokenbar.save","schema":99,"whatever":{"unknown":true}}`))
	if transferCode(err) != "newerSchema" || !strings.Contains(err.Error(), "schema 99") {
		t.Fatalf("%v", err)
	}
}

func TestOversizedFileIsRejectedBeforeParsing(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:717
	_, err := DecodeEnvelope(make([]byte, MaxSaveBytes+1))
	if transferCode(err) != "fileTooLarge" || !strings.Contains(err.Error(), "8388609") || !strings.Contains(err.Error(), "8388608") {
		t.Fatalf("%v", err)
	}
	// Exactly at the cap is parsed (and here is not a save).
	if _, err := DecodeEnvelope(make([]byte, MaxSaveBytes)); transferCode(err) != "notASaveFile" {
		t.Fatalf("at the cap: %v", err)
	}
	// ReadSave reads at most cap + 1 bytes of a larger file.
	path := filepath.Join(t.TempDir(), "big.json")
	if err := os.WriteFile(path, make([]byte, 3*MaxSaveBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadSave(path); transferCode(err) != "fileTooLarge" {
		t.Fatalf("ReadSave: %v", err)
	}
}

// New in the port: the six envelope fields are required, a bad header is not a save, schema 1 is accepted.
func TestEnvelopeHeaderAndFieldValidation(t *testing.T) {
	data := encodeFor(t, oldMacState(), "Old Mac")
	for _, c := range []struct {
		name  string
		patch map[string]any
		code  string
	}{
		{"no format", map[string]any{"format": nil}, "notASaveFile"},
		{"no schema", map[string]any{"schema": nil}, "notASaveFile"},
		{"string schema", map[string]any{"schema": "2"}, "notASaveFile"},
		{"no appVersion", map[string]any{"appVersion": nil}, "notASaveFile"},
		{"no exportedAt", map[string]any{"exportedAt": nil}, "notASaveFile"},
		{"bad exportedAt", map[string]any{"exportedAt": "yesterday"}, "notASaveFile"},
		{"no sourceDevice", map[string]any{"sourceDevice": nil}, "notASaveFile"},
		{"no state", map[string]any{"state": nil}, "notASaveFile"},
		{"array state", map[string]any{"state": []int{1}}, "notASaveFile"},
		{"schema 1", map[string]any{"schema": 1}, ""},
		{"fractional exportedAt", map[string]any{"exportedAt": "2026-08-03T12:00:00.5+02:00"}, ""},
	} {
		if _, err := DecodeEnvelope(patch(t, data, c.patch)); transferCode(err) != c.code {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	for _, raw := range []string{``, `[]`, `null`, `"x"`, `{"format":"poketokenbar.save"`} {
		if _, err := DecodeEnvelope([]byte(raw)); transferCode(err) != "notASaveFile" {
			t.Errorf("%q: %v", raw, err)
		}
	}
}

var wholeSecondUTC = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`)

// checkEnvelopeDates walks an encoded envelope and requires every date (exportedAt and each dex
// caughtAt and releasedAt) to be UTC whole seconds with Z (protocol §1.1). It returns how many
// dates it checked.
func checkEnvelopeDates(t *testing.T, data []byte) int {
	t.Helper()
	var env struct {
		ExportedAt string `json:"exportedAt"`
		State      struct {
			Dex []map[string]any `json:"dex"`
		} `json:"state"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	dates := []string{env.ExportedAt}
	for _, e := range env.State.Dex {
		for _, k := range []string{"caughtAt", "releasedAt"} {
			if v, ok := e[k]; ok {
				s, _ := v.(string)
				dates = append(dates, s)
			}
		}
	}
	for _, d := range dates {
		if !wholeSecondUTC.MatchString(d) {
			t.Errorf("date %q is not UTC whole seconds", d)
		}
	}
	return len(dates)
}

// New in the port: hazard 1 of save-transfer.md §4.3. The live save keeps nanoseconds and a local
// offset; the export, the snapshots and the backups carry whole seconds in UTC.
func TestExportedDatesAreUTCWholeSeconds(t *testing.T) {
	zone := time.FixedZone("CEST", 2*3600)
	at := time.Date(2026, 11, 18, 14, 20, 0, 123456789, zone)
	released := at.Add(90 * time.Minute)
	st := sampleState(1000, 2)
	st.GrowthDifficulty = 0.5
	st.Dex[0].CaughtAt, st.Dex[1].CaughtAt, st.Dex[1].ReleasedAt = &at, &at, &released
	st.Dex[0].Profile = generateProfile(math.MaxUint64, 5, st.Dex[0].ID)
	dir := writeState(t, st)
	now := time.Date(2026, 11, 18, 15, 0, 0, 987654321, zone)
	s := openAt(t, dir, &fakeData{}, 7, withNow(func() time.Time { return now }))
	path := filepath.Join(t.TempDir(), "out.json")
	n, err := s.ExportSave(path)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if n != len(data) || checkEnvelopeDates(t, data) != 4 {
		t.Fatalf("export %d bytes, file %d", n, len(data))
	}
	text := string(data)
	for _, want := range []string{`"exportedAt": "2026-11-18T13:00:00Z"`, `"caughtAt": "2026-11-18T12:20:00Z"`,
		`"releasedAt": "2026-11-18T13:50:00Z"`, `"seed": 18446744073709551615`} {
		if !strings.Contains(text, want) {
			t.Errorf("export lacks %s", want)
		}
	}
	// Sorted keys, pretty-printed (ST:108), and no growthDifficulty in an export (protocol §7.7).
	if !strings.HasPrefix(text, "{\n  \"appVersion\"") || strings.Index(text, `"active"`) > strings.Index(text, `"usedSinceInstall"`) {
		t.Error("keys are not sorted")
	}
	if strings.Contains(text, "growthDifficulty") {
		t.Error("an export must not carry growthDifficulty")
	}
	// The live entries were not rewritten.
	if s.st.Dex[0].CaughtAt.Nanosecond() != 123456789 || s.st.Dex[0].CaughtAt.Location() == time.UTC {
		t.Fatalf("live date changed: %v", s.st.Dex[0].CaughtAt)
	}
	// Snapshots and backups use the same encoder, and keep growthDifficulty.
	if _, err := s.CreateSnapshot(); err != nil {
		t.Fatal(err)
	}
	snap, _ := os.ReadFile(s.listSnapshots()[0].path)
	checkEnvelopeDates(t, snap)
	if !strings.Contains(string(snap), `"growthDifficulty": 0.5`) {
		t.Error("a snapshot must keep growthDifficulty for the corrupt-save recovery")
	}
	if err := s.backupBeforeImport(); err != nil {
		t.Fatal(err)
	}
	backup, _ := os.ReadFile(filepath.Join(s.dir, backupPrefix+now.Local().Format(snapshotStampLayout)+".json"))
	checkEnvelopeDates(t, backup)
}

// New in the port: the normaliser covers every date field of the save. A new time field must be
// added to withWholeSecondDates and to this list.
func TestEveryDateFieldIsNormalisedOnExport(t *testing.T) {
	timeType := reflect.TypeOf(time.Time{})
	var found []string
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type, string)
	walk = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if typ == timeType {
			found = append(found, path)
			return
		}
		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			walk(f.Type, typ.Name()+"."+f.Name)
		}
	}
	walk(reflect.TypeOf(state{}), "state")
	if want := []string{"dexEntry.CaughtAt", "dexEntry.ReleasedAt"}; !slices.Equal(found, want) {
		t.Fatalf("date fields %v, normalised %v", found, want)
	}
}

// New in the port: fractional dates and offsets (a snapshot written before the fix, a hand edit)
// import, and the next export normalises them.
func TestFractionalDatesImportAndExportWhole(t *testing.T) {
	raw := `{"appVersion":"0.2.0","exportedAt":"2026-11-18T13:20:00.5Z","format":"poketokenbar.save","schema":2,"sourceDevice":"fedora",
		"state":{"usedSinceInstall":42,"dex":[{"id":"x","baseID":1,"finalID":3,"chainOrder":[1,2,3],"rarity":"common",
		"caughtAt":"2026-11-18T13:20:00.123456789Z","releasedAt":"2026-11-18T15:20:00.75+02:00"}]}}`
	env := decodeOK(t, []byte(raw))
	e := env.st.Dex[0]
	if len(env.st.Dex) != 1 || e.CaughtAt.Nanosecond() != 123456789 || !e.ReleasedAt.Equal(time.Date(2026, 11, 18, 13, 20, 0, 750_000_000, time.UTC)) {
		t.Fatalf("%+v", env.st.Dex)
	}
	s := newStore(t, &fakeData{}, 7)
	applyOK(t, s, []byte(raw), UsageInput{})
	path := filepath.Join(t.TempDir(), "again.json")
	if _, err := s.ExportSave(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	checkEnvelopeDates(t, data)
	if !strings.Contains(string(data), `"releasedAt": "2026-11-18T13:20:00Z"`) {
		t.Errorf("releasedAt not normalised:\n%s", data)
	}
}

// New in the port: an export synthesized from the Swift encoder's format (SaveTransfer.swift:99-111:
// sorted keys, " : " separators, .iso8601 dates, nil optionals omitted), with the legacy Unown keys,
// no growthDifficulty or schemaVersion, Japanese, a macOS-only provider and a seed above 2^53. It
// imports onto a GNOME save, then exports with whole-second dates and imports again unchanged.
func TestMacOSExportImportsAndRoundTrips(t *testing.T) {
	fixture, err := os.ReadFile("testdata/macos-export.json")
	if err != nil {
		t.Fatal(err)
	}
	cur := freshState()
	cur.GrowthDifficulty, cur.Language = 0.5, "en"
	cur.InstallBaselineSet, cur.UsedSinceInstall = true, 99
	s := openAt(t, writeState(t, cur), stub(linear3()), 7)
	env := decodeOK(t, fixture)
	if env.SourceDevice != "Jane’s MacBook Pro" || env.AppVersion != "2.5.7" || env.Summary() != (Summary{3, 52_800_000_000}) {
		t.Fatalf("envelope %+v %+v", env, env.Summary())
	}
	if err := s.ApplyImport(env, UsageInput{TodayByProvider: map[string]int{"claude_code": 7000}, TodayDate: "2026-11-18", HasUsageData: true}); err != nil {
		t.Fatal(err)
	}
	st := &s.st
	a := st.Active
	if a == nil || a.BaseID != 201 || deref(a.Form) != "question" || !a.IsShiny || deref(a.Nature) != "modest" || a.UsedAtStage != 1_234_567 ||
		a.Profile == nil || a.Profile.Seed != 18_000_000_000_000_000_001 || a.Profile.Level != 12 || a.Profile.IVs != (ivSet{7, 12, 31, 30, 0, 19}) {
		t.Fatalf("active %+v profile %+v", a, a.Profile)
	}
	if len(st.Dex) != 3 || st.Dex[0].Form != nil || deref(st.Dex[1].Form) != "exclamation" || !st.Dex[1].IsShiny ||
		st.Dex[0].Names[1]["ja"] != "フシギダネ" || st.Dex[2].ReleasedAt == nil || !st.Dex[0].CaughtAt.Equal(time.Date(2026, 10, 2, 8, 15, 30, 0, time.UTC)) {
		t.Fatalf("dex %+v", st.Dex)
	}
	if st.RepresentativeSpeciesID == nil || *st.RepresentativeSpeciesID != 201 || deref(st.RepresentativeForm) != "exclamation" {
		t.Fatalf("representative %v %v", st.RepresentativeSpeciesID, deref(st.RepresentativeForm))
	}
	// Device fields: this computer's difficulty, language and usage; macOS-only providers are gone.
	if st.GrowthDifficulty != 0.5 || st.Language != "en" || s.lang != "en" || st.SchemaVersion != schemaVersion ||
		!reflect.DeepEqual(st.Claimed, map[string]int{"claude_code": 7000}) || st.LastDate != "2026-11-18" || !st.InstallBaselineSet {
		t.Fatalf("device fields %v %q %v %q", st.GrowthDifficulty, st.Language, st.Claimed, st.LastDate)
	}
	// Progress and the account ledger; the epoch string is opaque and stays byte for byte.
	if st.UsedSinceInstall != 52_800_000_000 || st.SpentTokens != 1_500_000_000 || st.Inventory["rareCandy"] != 3 ||
		st.CandyWindowEpoch["claude.fiveHour"] != "2026-11-17T20:00:00.512345+00:00" || st.CandyGrantTier["claude.fiveHour"] != 1 ||
		!st.CandyFeatureSeeded || !st.CollectedFinals["1:3"] {
		t.Fatalf("progress %+v", st)
	}
	if len(s.drain()) != 0 {
		t.Fatal("an import announces nothing")
	}
	// Re-export: whole-second dates, then a second import gives the same progress.
	path := filepath.Join(t.TempDir(), "PokeTokenBar-Save-2026-11-18.json")
	if _, err := s.ExportSave(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	checkEnvelopeDates(t, data)
	for _, gone := range []string{"unownForm", "growthDifficulty", "codex"} {
		if strings.Contains(string(data), gone) {
			t.Errorf("re-export carries %q", gone)
		}
	}
	checkKeepsMacKeys(t, fixture, data)
	back := decodeOK(t, data).st
	if !reflect.DeepEqual(back.Active, st.Active) || !reflect.DeepEqual(back.Dex, st.Dex) || !reflect.DeepEqual(back.CandyWindowEpoch, st.CandyWindowEpoch) ||
		back.UsedSinceInstall != st.UsedSinceInstall || deref(back.RepresentativeForm) != "exclamation" {
		t.Fatalf("round trip differs:\n%+v\n%+v", back.Active, st.Active)
	}
	other := newStore(t, stub(linear3()), 7)
	applyOK(t, other, data, UsageInput{})
	if other.Summary() != (Summary{3, 52_800_000_000}) || deref(other.st.Active.Form) != "question" {
		t.Fatalf("second import %+v", other.Summary())
	}
}

// checkKeepsMacKeys compares the re-export with the macOS fixture as JSON, so the check does not
// go through the Go structs: every key macOS wrote on the active Pokémon, its profile (with ivs and
// moves) and each dex entry comes back with the same value, unownForm as form, and the file holds
// no null (Swift's synthesized decoders would read a null required key as a failure).
func checkKeepsMacKeys(t *testing.T, fixture, export []byte) {
	t.Helper()
	parse := func(b []byte) map[string]any {
		var v map[string]any
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
		return v["state"].(map[string]any)
	}
	mac, gnome := parse(fixture), parse(export)
	compare := func(where string, want, got map[string]any) {
		for k, w := range want {
			key := k
			if k == "unownForm" {
				key = "form"
			}
			if g, ok := got[key]; !ok || !reflect.DeepEqual(g, w) {
				t.Errorf("%s.%s: macOS %v, re-export %v", where, key, w, g)
			}
		}
	}
	macActive, gnomeActive := mac["active"].(map[string]any), gnome["active"].(map[string]any)
	macProfile, gnomeProfile := macActive["profile"].(map[string]any), gnomeActive["profile"].(map[string]any)
	delete(macActive, "profile")
	compare("active", macActive, gnomeActive)
	// growthTokens is re-derived from usedAtStage under this computer's difficulty and only ever
	// grows (ensureProfiles, as upstream's migration after applySave); the level is kept.
	w, _ := macProfile["growthTokens"].(json.Number).Int64()
	g, _ := gnomeProfile["growthTokens"].(json.Number).Int64()
	if g < w {
		t.Errorf("active.profile.growthTokens: macOS %d, re-export %d", w, g)
	}
	delete(macProfile, "growthTokens")
	compare("active.profile", macProfile, gnomeProfile)
	macDex, gnomeDex := mac["dex"].([]any), gnome["dex"].([]any)
	if len(macDex) != len(gnomeDex) {
		t.Fatalf("dex %d → %d", len(macDex), len(gnomeDex))
	}
	for i := range macDex {
		compare(fmt.Sprintf("dex[%d]", i), macDex[i].(map[string]any), gnomeDex[i].(map[string]any))
	}
	if strings.Contains(string(export), "null") {
		t.Error("the re-export contains a null")
	}
}

// ---------------------------------------------------------------- rebase

func TestTransferDayTokensStillCountAfterRebase(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:212
	// Control: loading the old computer's state raw credits nothing (the defect).
	raw := openAt(t, writeState(t, oldMacState()), &fakeData{}, 7)
	before := raw.st.UsedSinceInstall
	update(raw, map[string]int{"test": 8_000_000}, transferDay, true)
	eq(t, raw.st.UsedSinceInstall-before, 0, "control must reproduce the defect")

	s := newStore(t, &fakeData{}, 7)
	applyOK(t, s, encodeFor(t, oldMacState(), "Old Mac"), usage(map[string]int{"test": 5_000_000}, true))
	eq(t, s.st.Claimed["test"], 5_000_000, "claimed")
	eq(t, s.st.LastDate, transferDay, "lastDate")
	eq(t, s.st.InstallBaselineSet, true, "baseline")
	before = s.st.UsedSinceInstall
	update(s, map[string]int{"test": 8_000_000}, transferDay, true)
	eq(t, s.st.UsedSinceInstall-before, 3_000_000, "credited on the transfer day")
}

func TestImportWithoutUsageDataDefersBaseline(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:249
	s := newStore(t, &fakeData{}, 7)
	applyOK(t, s, encodeFor(t, oldMacState(), "Old Mac"), usage(map[string]int{"test": 0}, false))
	eq(t, s.st.InstallBaselineSet, false, "baseline deferred")
	before := s.st.UsedSinceInstall
	update(s, map[string]int{"test": 40_000_000}, transferDay, true)
	eq(t, s.st.UsedSinceInstall, before, "the arrival day is not credited")
	eq(t, s.st.Claimed["test"], 40_000_000, "claimed")
	update(s, map[string]int{"test": 41_000_000}, transferDay, true)
	eq(t, s.st.UsedSinceInstall-before, 1_000_000, "then credited normally")
}

func TestImportWithStaleOnlyUsageDefersBaselineInsteadOfSeedingEmptyLedger(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:275
	s := newStore(t, &fakeData{}, 7)
	applyOK(t, s, encodeFor(t, oldMacState(), "Old Mac"), usage(map[string]int{}, true))
	if s.st.InstallBaselineSet || s.st.Claimed != nil || s.st.LastDate != "" {
		t.Fatalf("ledger %v %v %q", s.st.InstallBaselineSet, s.st.Claimed, s.st.LastDate)
	}
	before := s.st.UsedSinceInstall
	update(s, map[string]int{"test": 40_000_000}, transferDay, true)
	eq(t, s.st.UsedSinceInstall, before, "first snapshot is the baseline")
	eq(t, s.st.Claimed["test"], 40_000_000, "claimed")
	update(s, map[string]int{"test": 41_000_000}, transferDay, true)
	eq(t, s.st.UsedSinceInstall-before, 1_000_000, "then credited normally")
}

func TestImportKeepsProgressAndCandyGrantLedger(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:305
	imported := oldMacState()
	imported.CandyGrantTier = map[string]int{"five_hour|2026-08-03T00:00:00Z": 100}
	imported.CandyFeatureSeeded = true
	s := newStore(t, &fakeData{}, 7)
	applyOK(t, s, encodeFor(t, imported, "Old Mac"), usage(map[string]int{"test": 1}, true))
	st := s.st
	if st.UsedSinceInstall != 8_000_000_000 || st.SpentTokens != 3_500_000_000 || len(st.Dex) != 1 || st.Inventory["rareCandy"] != 2 ||
		st.RepresentativeSpeciesID == nil || *st.RepresentativeSpeciesID != 2 || st.CandyGrantTier["five_hour|2026-08-03T00:00:00Z"] != 100 ||
		!st.CandyFeatureSeeded {
		t.Fatalf("%+v", st)
	}
}

func TestImportKeepsThisComputersLanguage(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:575
	mine := freshState()
	mine.InstallBaselineSet, mine.Language = true, "en"
	s := openAt(t, writeState(t, mine), &fakeData{}, 7)
	imported := oldMacState()
	imported.Language = "ja"
	applyOK(t, s, encodeFor(t, imported, "JA Mac"), usage(map[string]int{"test": 1}, true))
	if s.st.Language != "en" || s.lang != "en" || len(s.st.Dex) != 1 {
		t.Fatalf("language %q/%q, dex %d", s.st.Language, s.lang, len(s.st.Dex))
	}
}

func TestCandyGrantLedgerMergesInsteadOfBeingReplacedByAnOlderSave(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:598
	mine := freshState()
	mine.InstallBaselineSet, mine.CandyGrantTier = true, map[string]int{"five_hour|A": 100, "weekly|B": 80}
	s := openAt(t, writeState(t, mine), &fakeData{}, 7)
	older := oldMacState()
	older.CandyGrantTier = map[string]int{"weekly|B": 50, "five_hour|C": 100}
	applyOK(t, s, encodeFor(t, older, "Old Mac"), usage(map[string]int{"test": 1}, true))
	if want := map[string]int{"five_hour|A": 100, "weekly|B": 80, "five_hour|C": 100}; !reflect.DeepEqual(s.st.CandyGrantTier, want) {
		t.Fatalf("%v", s.st.CandyGrantTier)
	}
}

func TestEveryStateFieldIsClassifiedForTransfer(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:551 — the port adds the keep-current
	// schemaVersion and growthDifficulty, and the json:"-" set and ledger fields keep their Go names.
	progress := []string{"UsedSinceInstall", "SpentTokens", "EggUsage", "EggTier", "PendingHatchID", "PendingForm", "EggPrayers",
		"Active", "RepresentativeSpeciesID", "RepresentativeForm", "Dex", "CollectedFinals", "Inventory",
		"NotifiedDexCompletions", "DexCompletionsSeeded"}
	deviceLedger := []string{"InstallBaselineSet", "Claimed", "LastDate"}
	accountLedger := []string{"CandyGrantTier", "CandyWindowEpoch", "CandyFeatureSeeded"}
	keepCurrent := []string{"Language", "SchemaVersion", "GrowthDifficulty"}
	classified := slices.Concat(progress, deviceLedger, accountLedger, keepCurrent)
	var actual []string
	typ := reflect.TypeOf(state{})
	for i := 0; i < typ.NumField(); i++ {
		actual = append(actual, typ.Field(i).Name)
	}
	slices.Sort(classified)
	slices.Sort(actual)
	if !slices.Equal(actual, classified) {
		t.Fatalf("state fields changed; classify the new one for transfer (rebase):\nactual     %v\nclassified %v", actual, classified)
	}
}

func TestGuaranteeTravelsWithSave(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:377
	imported := freshState()
	imported.EggTier, imported.UsedSinceInstall = sp(rarityUncommon), 1_000_000
	cur := freshState()
	if r := rebase(imported, &cur, UsageInput{TodayByProvider: map[string]int{"test": 0}, TodayDate: "d", HasUsageData: true}); deref(r.EggTier) != rarityUncommon {
		t.Fatalf("%v", deref(r.EggTier))
	}
}

// New in the port: upstream has no test for the epoch merge (ST:246-268).
func TestMergedWindowEpochsBranches(t *testing.T) {
	epochsA := map[string]string{"both": "2026-08-03T05:00:00Z", "bothRev": "2026-08-04T00:00:00Z", "onlyA": "a-older",
		"onlyB": "z-later", "neither": "2026-08-01", "grantedNoEpoch": "kept-by-max?"}
	tiersA := map[string]int{"both": 100, "bothRev": 80, "onlyA": 100, "onlyB": 0}
	epochsB := map[string]string{"both": "2026-08-03T10:00:00Z", "bothRev": "2026-08-03T00:00:00Z", "onlyA": "z-later",
		"onlyB": "a-older", "neither": "2026-08-02"}
	tiersB := map[string]int{"both": 80, "bothRev": 100, "onlyB": 100, "grantedNoEpoch": 80}
	got := mergedWindowEpochs(epochsA, tiersA, epochsB, tiersB)
	want := map[string]string{
		"both":    "2026-08-03T10:00:00Z", // both granted: the later epoch
		"bothRev": "2026-08-04T00:00:00Z",
		"onlyA":   "a-older", // one side granted: that side's epoch, even when older
		"onlyB":   "a-older",
		"neither": "2026-08-02", // neither granted: the later epoch
		// grantedNoEpoch: granted only on B, which has no epoch: no entry
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	// Through rebase: a window only this computer granted keeps its epoch, and the higher tier wins
	// from either side (ST:218-221).
	imported, cur := freshState(), freshState()
	imported.CandyGrantTier = map[string]int{"claude.sevenDay": 100}
	cur.CandyGrantTier, cur.CandyWindowEpoch = map[string]int{"claude.fiveHour": 80, "claude.sevenDay": 80}, map[string]string{"claude.fiveHour": "e-current"}
	r := rebase(imported, &cur, UsageInput{})
	if r.CandyWindowEpoch["claude.fiveHour"] != "e-current" || r.CandyGrantTier["claude.sevenDay"] != 100 || r.CandyGrantTier["claude.fiveHour"] != 80 {
		t.Fatalf("rebase merge %v %v", r.CandyWindowEpoch, r.CandyGrantTier)
	}
}

// New in the port: hazard 2 (save-transfer.md §4.2). An import keeps this computer's difficulty,
// whether the file has another one (a GNOME snapshot) or none (a macOS export), so the next
// setSettings with this computer's value does not rescale the imported progress.
func TestImportKeepsGrowthDifficultyAndDoesNotRescale(t *testing.T) {
	src := freshState()
	src.GrowthDifficulty, src.SchemaVersion = 2.0, 7
	src.Active = &monState{BaseID: 1, PathIDs: []int{1}, PlannedPathIDs: []int{1, 2, 3}, UsedAtStage: 1_000_000, Rarity: rarityCommon, TotalForms: 3}
	withDifficulty, _ := encodeEnvelope(&src, fixedNow, "0.3.0", "fedora", false)
	without, _ := encodeEnvelope(&src, fixedNow, "0.3.0", "fedora", true)
	if !strings.Contains(string(withDifficulty), `"growthDifficulty": 2`) || strings.Contains(string(without), "growthDifficulty") {
		t.Fatal("fixture premise")
	}
	for name, data := range map[string][]byte{"with": withDifficulty, "without": without} {
		cur := freshState()
		cur.GrowthDifficulty = 0.5
		s := openAt(t, writeState(t, cur), &fakeData{}, 7)
		applyOK(t, s, data, UsageInput{})
		eq(t, s.GrowthDifficulty(), 0.5, name+": difficulty kept")
		eq(t, s.st.SchemaVersion, schemaVersion, name+": schemaVersion kept")
		s.SetGrowthDifficulty(0.5)
		eq(t, s.st.Active.UsedAtStage, 1_000_000, name+": no rescale")
	}
}

// ---------------------------------------------------------------- backup and apply

func backupPath(s *Store, at time.Time) string {
	return filepath.Join(s.dir, backupPrefix+at.Local().Format(snapshotStampLayout)+".json")
}

func TestPreviousStateIsBackedUpBeforeOverwrite(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:329 — the port's backup is an envelope.
	mine := freshState()
	mine.InstallBaselineSet, mine.UsedSinceInstall = true, 123_456_789
	s := openAt(t, writeState(t, mine), &fakeData{}, 7)
	eq(t, s.st.UsedSinceInstall, 123_456_789, "premise")
	applyOK(t, s, encodeFor(t, oldMacState(), "Old Mac"), usage(map[string]int{"test": 0}, true))
	_, env, err := ReadSave(backupPath(s, fixedNow))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, env.st.UsedSinceInstall, 123_456_789, "backup holds the previous state")
	eq(t, s.st.UsedSinceInstall, 8_000_000_000, "imported")
}

func TestImportAbortsWhenBackupCannotBeWritten(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:624
	mine := freshState()
	mine.InstallBaselineSet, mine.UsedSinceInstall = true, 123_456_789
	dir := writeState(t, mine)
	s := openAt(t, dir, &fakeData{}, 7)
	if err := os.Mkdir(backupPath(s, fixedNow), 0o700); err != nil {
		t.Fatal(err)
	}
	err := s.ApplyImport(decodeOK(t, encodeFor(t, oldMacState(), "Old Mac")), usage(map[string]int{"test": 1}, true))
	if transferCode(err) != "backupFailed" || s.st.UsedSinceInstall != 123_456_789 || len(s.st.Dex) != 0 {
		t.Fatalf("%v, used %d, dex %d", err, s.st.UsedSinceInstall, len(s.st.Dex))
	}
	if r := openAt(t, dir, &fakeData{}, 7); r.st.UsedSinceInstall != 123_456_789 {
		t.Fatal("the save on disk changed")
	}
}

func TestSecondImportDoesNotDestroyTheOriginalBackup(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:651 — plus the keep-5 pruning (CS:2070-2077).
	original := freshState()
	original.InstallBaselineSet, original.UsedSinceInstall = true, 111
	now := fixedNow
	s := openAt(t, writeState(t, original), &fakeData{}, 7, withNow(func() time.Time { return now }))
	for i, used := range []int{222, 333} {
		now = fixedNow.Add(time.Duration(i) * time.Minute)
		st := oldMacState()
		st.UsedSinceInstall = used
		applyOK(t, s, encodeFor(t, st, "A"), usage(map[string]int{"test": 1}, true))
	}
	backups := func() []string {
		var out []string
		entries, _ := os.ReadDir(s.dir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), backupPrefix) {
				out = append(out, e.Name())
			}
		}
		return out
	}
	names := backups()
	if len(names) != 2 {
		t.Fatalf("backups %v", names)
	}
	_, env, err := ReadSave(filepath.Join(s.dir, names[0]))
	if err != nil || env.st.UsedSinceInstall != 111 {
		t.Fatalf("oldest backup: %v %+v", err, env)
	}
	for i := 2; i < 8; i++ {
		now = fixedNow.Add(time.Duration(i) * time.Minute)
		applyOK(t, s, encodeFor(t, oldMacState(), "A"), usage(map[string]int{"test": 1}, true))
	}
	names = backups()
	if len(names) != backupsToKeep || names[len(names)-1] != filepath.Base(backupPath(s, now)) {
		t.Fatalf("kept %v", names)
	}
}

func TestApplySetsPhaseFromWhetherACompanionCameIn(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:695
	withMon := oldMacState()
	withMon.Active = mon(403, []int{403}, 0, rarityCommon, 1)
	a := newStore(t, &fakeData{}, 7)
	applyOK(t, a, encodeFor(t, withMon, "A"), usage(map[string]int{"test": 1}, true))
	eq(t, a.BuildState(MoodInput{Now: fixedNow}).Phase, "active", "with a Pokémon")
	b := newStore(t, &fakeData{}, 7)
	applyOK(t, b, encodeFor(t, oldMacState(), "B"), usage(map[string]int{"test": 1}, true))
	eq(t, b.BuildState(MoodInput{Now: fixedNow}).Phase, "egg", "egg only")
}

func TestImportedCompanionIsNotShownAsEggBeforeUsageArrives(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:460
	imported := oldMacState()
	imported.Active = mon(403, []int{403}, 0, rarityCommon, 1)
	s := newStore(t, &fakeData{}, 7)
	applyOK(t, s, encodeFor(t, imported, "Old Mac"), usage(map[string]int{"test": 0}, false))
	eq(t, s.st.InstallBaselineSet, false, "premise: baseline deferred")
	update(s, map[string]int{"test": 0}, transferDay, false)
	if s.st.Active == nil || s.BuildState(MoodInput{Now: fixedNow}).Phase != "active" {
		t.Fatal("an imported companion shows as an egg")
	}
	e := newStore(t, &fakeData{}, 7)
	update(e, map[string]int{"test": 0}, transferDay, false)
	eq(t, e.BuildState(MoodInput{Now: fixedNow}).Phase, "egg", "an egg stays an egg")
}

func TestExtremeValuesAreClampedAtTheTrustBoundary(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveTransferTests.swift:489
	evil := freshState()
	evil.InstallBaselineSet, evil.UsedSinceInstall, evil.SpentTokens, evil.EggUsage = true, math.MaxInt, math.MinInt, math.MaxInt
	evil.Claimed = map[string]int{"test": -42}
	evil.Active = &monState{BaseID: 1, PathIDs: []int{1}, PlannedPathIDs: []int{1}, StageIndex: math.MaxInt, UsedAtStage: math.MaxInt,
		Rarity: rarityCommon, TotalForms: math.MaxInt}
	data := encodeFor(t, evil, "Corrupt")
	env := decodeOK(t, data)
	st := env.st
	if st.UsedSinceInstall != maxTokenValue || st.SpentTokens != 0 || st.EggUsage != maxTokenValue || st.Claimed["test"] != 0 ||
		st.Active.UsedAtStage != maxTokenValue || st.Active.TotalForms != 12 || st.Active.StageIndex != 0 {
		t.Fatalf("%+v %+v", st, st.Active)
	}
	s := newStore(t, &fakeData{}, 7)
	applyOK(t, s, data, usage(map[string]int{"test": 0}, true))
	if s.st.UsedSinceInstall-s.st.SpentTokens < 0 {
		t.Fatal("negative wallet")
	}
	update(s, map[string]int{"test": 1_000}, transferDay, true)
	if s.st.UsedSinceInstall > maxTokenValue+1_000 {
		t.Fatal("overflow")
	}
}

func TestLegacyImportSeedsExistingCompletionsBeforeANewHatch(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DexCompletionStoreTests.swift:110 and :114 (both variants of :118-160)
	for _, existingKanto := range []bool{true, false} {
		lower := 152
		var preexisting []string
		if existingKanto {
			lower, preexisting = 1, []string{"region.kanto"}
		}
		legacy := freshState()
		e := entry(lower, 250, nil, rarityCommon)
		for id := lower; id <= 250; id++ {
			e.ChainOrder = append(e.ChainOrder, id)
		}
		legacy.Dex = []dexEntry{e}
		legacy.EggUsage, legacy.PendingHatchID = eggHatchThreshold, intPtr(251)
		var doc map[string]json.RawMessage
		_ = json.Unmarshal(encodeFor(t, legacy, "Old Mac"), &doc)
		var stateDoc map[string]json.RawMessage
		_ = json.Unmarshal(doc["state"], &stateDoc)
		delete(stateDoc, "notifiedDexCompletions")
		delete(stateDoc, "dexCompletionsSeeded")
		doc["state"], _ = json.Marshal(stateDoc)
		data, _ := json.Marshal(doc)
		env := decodeOK(t, data)
		eq(t, env.st.DexCompletionsSeeded, false, "the export predates completion tracking")

		f := &fakeData{lineFn: func(b int) (*Line, error) { return makeLine(b, node(b), rarityCommon), nil }}
		s := newStore(t, f, 7)
		if err := s.ApplyImport(env, UsageInput{TodayByProvider: map[string]int{"test": 0}, TodayDate: "2026-11-05", HasUsageData: true}); err != nil {
			t.Fatal(err)
		}
		if !s.st.DexCompletionsSeeded || !slices.Equal(sortedKeys(s.st.NotifiedDexCompletions), preexisting) || len(s.drain()) != 0 {
			t.Fatalf("kanto=%v: seeded %v notified %v", existingKanto, s.st.DexCompletionsSeeded, sortedKeys(s.st.NotifiedDexCompletions))
		}
		raw, _ := os.ReadFile(filepath.Join(s.dir, stateFileName))
		if persisted, _ := decodeState(raw); !persisted.DexCompletionsSeeded {
			t.Fatalf("kanto=%v: even an empty seed must persist", existingKanto)
		}
		s.hatchIfNeeded(bg)
		if s.st.Active == nil || s.st.Active.BaseID != 251 {
			t.Fatalf("kanto=%v: hatched %+v", existingKanto, s.st.Active)
		}
		want := append(slices.Clone(preexisting), "region.johto")
		slices.Sort(want)
		if !slices.Equal(sortedKeys(s.st.NotifiedDexCompletions), want) {
			t.Fatalf("kanto=%v: notified %v", existingKanto, sortedKeys(s.st.NotifiedDexCompletions))
		}
		after := sortedKeys(s.st.NotifiedDexCompletions)
		s.checkDexCompletions()
		if !slices.Equal(sortedKeys(s.st.NotifiedDexCompletions), after) {
			t.Fatal("announced twice")
		}
	}
}

func TestImportOntoHarderDeviceKeepsEarnedIdentityAndLevel(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ProfileGrowthIntegrationTests.swift:128
	src := freshState()
	src.GrowthDifficulty = 0.1
	source := openAt(t, writeState(t, src), growthData(1), 7)
	source.hatch(bg, 1)
	source.applyUsage(37_500_000)
	original := *source.st.Active.Profile
	eq(t, original.Level, 52, "source level")
	dst := freshState()
	dst.GrowthDifficulty = 2
	destination := openAt(t, writeState(t, dst), growthData(1), 7)
	applyOK(t, destination, encodeFor(t, source.st, "source"), UsageInput{TodayDate: "day"})
	p := destination.st.Active.Profile
	if p.InstanceID != original.InstanceID || p.IVs != original.IVs || p.Level != 52 || destination.GrowthDifficulty() != 2 {
		t.Fatalf("%+v", p)
	}
	destination.loadCurrentLine(bg)
	a := destination.st.Active
	destination.applyUsage(destination.stageThreshold(a) - a.UsedAtStage)
	eq(t, destination.st.Dex[len(destination.st.Dex)-1].Profile.Level, 100, "graduated at 100")
}

func TestUnownFormsSurviveExportAndImport(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:504
	original := freshState()
	original.Active = mon(201, []int{201}, 0, rarityCommon, 1)
	original.Active.Form, original.Active.IsShiny = sp("question"), true
	original.Dex = []dexEntry{entry(201, 201, []int{201}, rarityCommon, withForm("a")), entry(201, 201, []int{201}, rarityCommon, withForm("exclamation"), shiny)}
	original.RepresentativeSpeciesID, original.RepresentativeForm = intPtr(201), sp("exclamation")
	source := openAt(t, writeState(t, original), &fakeData{}, 7)
	path := filepath.Join(t.TempDir(), "unown.json")
	if _, err := source.ExportSave(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	dst := newStore(t, &fakeData{}, 7)
	applyOK(t, dst, data, UsageInput{TodayDate: "2026-09-11"})
	r := openAt(t, dst.dir, &fakeData{}, 7)
	if deref(r.st.Active.Form) != "question" || !r.st.Active.IsShiny || deref(r.st.Dex[0].Form) != "a" || deref(r.st.Dex[1].Form) != "exclamation" ||
		r.st.Dex[0].IsShiny || !r.st.Dex[1].IsShiny || deref(r.st.RepresentativeForm) != "exclamation" || !r.st.ownsShinySpecies(201, sp("exclamation")) {
		t.Fatalf("%+v %+v", r.st.Active, r.st.Dex)
	}
}

func TestPendingEggLetterSurvivesExportAndImport(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:631
	original := freshState()
	original.PendingHatchID, original.PendingForm, original.EggUsage = intPtr(201), sp("exclamation"), 1_234
	source := openAt(t, writeState(t, original), &fakeData{}, 7)
	path := filepath.Join(t.TempDir(), "egg.json")
	if _, err := source.ExportSave(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	dst := newStore(t, &fakeData{}, 7)
	applyOK(t, dst, data, UsageInput{TodayDate: "2026-09-11"})
	r := openAt(t, dst.dir, &fakeData{}, 7)
	if r.st.Active != nil || r.st.PendingHatchID == nil || *r.st.PendingHatchID != 201 || deref(r.st.PendingForm) != "exclamation" || r.st.EggUsage != 1_234 {
		t.Fatalf("%+v", r.st)
	}
}

// New in the port: the legacy Unown keys (CM:548, :629, :721, :737); a valid new key wins.
func TestLegacyUnownKeysAreReadAsForms(t *testing.T) {
	raw := `{"appVersion":"2.3.0","exportedAt":"2026-08-03T12:00:00Z","format":"poketokenbar.save","schema":1,"sourceDevice":"Old Mac","state":{
		"pendingHatchID":201,"pendingUnownForm":"z","representativeSpeciesID":201,"representativeUnownForm":"b",
		"dex":[{"baseID":201,"finalID":201,"chainOrder":[201],"rarity":"common","unownForm":"b"},
		       {"baseID":201,"finalID":201,"chainOrder":[201],"rarity":"common","form":"c","unownForm":"d"},
		       {"baseID":201,"finalID":201,"chainOrder":[201],"rarity":"common","form":7,"unownForm":"e"}]}}`
	st := decodeOK(t, []byte(raw)).st
	if deref(st.PendingForm) != "z" || deref(st.RepresentativeForm) != "b" || deref(st.Dex[0].Form) != "b" || deref(st.Dex[1].Form) != "c" ||
		deref(st.Dex[2].Form) != "e" {
		t.Fatalf("pending %s rep %s dex %s %s %s", deref(st.PendingForm), deref(st.RepresentativeForm), deref(st.Dex[0].Form), deref(st.Dex[1].Form), deref(st.Dex[2].Form))
	}
	active := `{"appVersion":"2.3.0","exportedAt":"2026-08-03T12:00:00Z","format":"poketokenbar.save","schema":1,"sourceDevice":"Old Mac","state":{
		"active":{"baseID":201,"pathIDs":[201],"stageIndex":0,"usedAtStage":0,"rarity":"common","totalForms":1,"unownForm":"question"}}}`
	if a := decodeOK(t, []byte(active)).st.Active; a == nil || deref(a.Form) != "question" {
		t.Fatalf("active %+v", a)
	}
}

func TestImportClampsHostileProfiles(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:271-296, at the envelope boundary.
	hostile := generateProfile(5, 0, "")
	hostile.Level, hostile.GrowthTokens, hostile.IVs = 9999, math.MaxInt64, ivSet{999, -5, 31, 64, -1, 500}
	for i := 0; i < 400; i++ {
		hostile.Moves = append(hostile.Moves, knownMove{"move-" + strings.Repeat("x", 200), 900 + i})
	}
	low := generateProfile(5, 0, "below-hatch-level")
	low.Level = -99
	st := freshState()
	st.Active = mon(79, []int{79}, 0, rarityCommon, 1)
	st.Active.Profile = low
	st.Dex = []dexEntry{entry(79, 79, []int{79}, rarityCommon)}
	st.Dex[0].Profile = hostile
	got := decodeOK(t, encodeFor(t, st, "Evil")).st
	p := got.Dex[0].Profile
	if p.Level != 100 || p.GrowthTokens != maxTokenValue || p.IVs != (ivSet{31, 0, 31, 31, 0, 31}) || len(p.Moves) != 4 || p.InstanceID == "" ||
		len([]rune(p.Moves[0].Name)) > 80 || p.Moves[0].LearnedAtLevel > 100 {
		t.Fatalf("dex profile %+v", p)
	}
	eq(t, got.Active.Profile.Level, 5, "hatch minimum")
}

func TestEggPrayersAtTheTrustBoundary(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/EggPrayerTests.swift:130, :136 and :144
	withActive := freshState()
	withActive.Active = mon(4, []int{4}, 0, rarityCommon, 1)
	withActive.EggPrayers = &prayerRecord{Count: 9}
	if decodeOK(t, encodeFor(t, withActive, "d")).st.EggPrayers != nil {
		t.Fatal("an active Pokémon drops the egg prayers")
	}
	absurd := freshState()
	absurd.EggPrayers = &prayerRecord{Count: math.MaxInt}
	eq(t, decodeOK(t, encodeFor(t, absurd, "d")).st.EggPrayers.Count, maxTokenValue, "clamped")
	kept := freshState()
	kept.EggPrayers = &prayerRecord{Count: 5}
	kept.Dex = []dexEntry{entry(1, 1, []int{1}, rarityCommon)}
	kept.Dex[0].Prayers = &prayerRecord{Count: 2}
	got := decodeOK(t, encodeFor(t, kept, "d")).st
	if got.EggPrayers.Count != 5 || got.Dex[0].Prayers.Count != 2 {
		t.Fatalf("%+v %+v", got.EggPrayers, got.Dex[0].Prayers)
	}
	m := freshState()
	m.Active = mon(1, []int{1}, 0, rarityCommon, 1)
	m.Active.Prayers = &prayerRecord{Count: 2}
	eq(t, decodeOK(t, encodeFor(t, m, "d")).st.Active.Prayers.Count, 2, "active prayers")
}

// ---------------------------------------------------------------- snapshots (SaveSnapshotTests.swift)

func TestSnapshotCreationAndListing(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveSnapshotTests.swift:40
	s := openAt(t, writeState(t, sampleState(12_500, 3)), &fakeData{}, 7)
	if l := s.ListSnapshots(); l == nil || len(l) != 0 {
		t.Fatalf("listed %v", l)
	}
	snap, err := s.CreateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.DexCount != 3 || snap.LifetimeTokens != 12_500 || snap.SpeciesID == nil || *snap.SpeciesID != 1 || snap.Shiny || snap.Form != nil {
		t.Fatalf("%+v", snap)
	}
	if _, err := os.Stat(filepath.Join(s.snapshotDir(), snap.ID)); err != nil {
		t.Fatal(err)
	}
	listed := s.ListSnapshots()
	if len(listed) != 1 || !reflect.DeepEqual(listed[0], snap) {
		t.Fatalf("listed %+v, created %+v", listed, snap)
	}
}

func TestSnapshotKeepsNonDefaultFormInSummaryAndRestoredState(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveSnapshotTests.swift:61
	st := sampleState(1_000, 1)
	st.Active = &monState{BaseID: 492, PathIDs: []int{492}, PlannedPathIDs: []int{492}, UsedAtStage: 500, Rarity: rarityLegendary,
		TotalForms: 1, IsShiny: true, Form: sp("sky")}
	s := openAt(t, writeState(t, st), &fakeData{}, 7)
	created, err := s.CreateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, snap := range []SnapshotSummary{created, s.ListSnapshots()[0]} {
		if *snap.SpeciesID != 492 || !snap.Shiny || deref(snap.Form) != "sky" {
			t.Fatalf("%+v", snap)
		}
	}
	_, env, err := ReadSave(filepath.Join(s.snapshotDir(), created.ID))
	if err != nil || deref(env.st.Active.Form) != "sky" || !env.st.Active.IsShiny {
		t.Fatalf("%v %+v", err, env.st.Active)
	}
}

func TestSnapshotSummariesResolveCurrentAppearance(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveSnapshotTests.swift:84 — the legacy files are envelopes here
	// (the port never wrote bare snapshots); the Unown case uses the legacy unownForm key.
	cases := []struct {
		name    string
		species int
		stored  *string
		key     string
		want    *string
	}{
		{"sky", 492, sp("sky"), "form", sp("sky")},
		{"missing", 492, nil, "form", sp("land")},
		{"invalid", 492, sp("future"), "form", sp("land")},
		{"unown", 201, sp("b"), "unownForm", sp("b")},
		{"precursor", 420, sp("sunshine"), "form", nil},
		{"single", 25, sp("future"), "form", nil},
	}
	for _, c := range cases {
		now := fixedNow
		s := newStore(t, &fakeData{}, 7, withNow(func() time.Time { return now }))
		active := map[string]any{"baseID": c.species, "pathIDs": []int{c.species}, "stageIndex": 0, "usedAtStage": 0, "rarity": "common", "totalForms": 1}
		if c.stored != nil {
			active[c.key] = *c.stored
		}
		legacy, _ := json.Marshal(map[string]any{"appVersion": "2.0", "exportedAt": "2020-01-01T00:00:00Z", "format": envelopeFormat,
			"schema": 1, "sourceDevice": "Old Mac", "state": map[string]any{"active": active}})
		_ = os.MkdirAll(s.snapshotDir(), 0o700)
		if err := os.WriteFile(filepath.Join(s.snapshotDir(), snapshotPrefix+"2020-01-01-000000.json"), legacy, 0o600); err != nil {
			t.Fatal(err)
		}
		// Creation resolves a raw in-memory value too, rather than relying on the decoder.
		s.st.Active = &monState{BaseID: c.species, PathIDs: []int{c.species}, PlannedPathIDs: []int{c.species}, Rarity: rarityCommon, TotalForms: 1, Form: c.stored}
		created, err := s.CreateSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		listed := s.ListSnapshots()
		if len(listed) != 2 {
			t.Fatalf("%s: listed %d", c.name, len(listed))
		}
		for _, snap := range append([]SnapshotSummary{created}, listed...) {
			if snap.SpeciesID == nil || *snap.SpeciesID != c.species || deref(snap.Form) != deref(c.want) {
				t.Errorf("%s: %+v form %s", c.name, snap, deref(snap.Form))
			}
		}
	}
	egg := newStore(t, &fakeData{}, 7)
	snap, err := egg.CreateSnapshot()
	if err != nil || snap.SpeciesID != nil || snap.Form != nil || egg.ListSnapshots()[0].Form != nil {
		t.Fatalf("egg %v %+v", err, snap)
	}
}

func TestSnapshotDoesNotRevealShinyOfDisguisedDitto(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveSnapshotTests.swift:128
	now := fixedNow
	st := sampleState(1_000, 1)
	st.Active = &monState{BaseID: 412, PathIDs: []int{412}, PlannedPathIDs: []int{412}, UsedAtStage: 500, Rarity: rarityCommon,
		TotalForms: 2, IsShiny: true, DittoDisguise: intPtr(412), Form: sp("trash")}
	s := openAt(t, writeState(t, st), &fakeData{}, 7, withNow(func() time.Time { return now }))
	created, _ := s.CreateSnapshot()
	if created.Shiny || deref(created.Form) != "trash" {
		t.Fatalf("%+v", created)
	}
	if l := s.ListSnapshots(); len(l) != 1 || l[0].Shiny || deref(l[0].Form) != "trash" {
		t.Fatalf("%+v", l)
	}
	s.st.Active = &monState{BaseID: 132, PathIDs: []int{132}, PlannedPathIDs: []int{132}, Rarity: rarityRare, TotalForms: 1, IsShiny: true,
		DittoDisguise: intPtr(412), DittoRevealed: true}
	now = fixedNow.Add(time.Minute)
	revealed, _ := s.CreateSnapshot()
	if !revealed.Shiny || revealed.Form != nil {
		t.Fatalf("%+v", revealed)
	}
	if l := s.ListSnapshots(); len(l) != 2 || !l[0].Shiny || l[1].Shiny {
		t.Fatalf("%+v", l)
	}
}

// writeSnapshotOf writes st as a snapshot dated at into dir's snapshot folder (Swift
// SaveSnapshotManager.createSnapshot(state:for:date:)).
func writeSnapshotOf(t *testing.T, dir string, st state, at time.Time) string {
	t.Helper()
	w := openAt(t, dir, &fakeData{}, 7)
	saved := w.st
	w.st = st
	name, err := w.writeSnapshot(at)
	if err != nil {
		t.Fatal(err)
	}
	w.st = saved
	return name
}

func saveStateIn(t *testing.T, dir string, st state) {
	t.Helper()
	data, err := encodeState(&st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, stateFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreSnapshotRollsBackProgressAndPreservesCurrentAsSnapshot(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveSnapshotTests.swift:227
	now := fixedNow
	clock := withNow(func() time.Time { return now })
	dir := writeState(t, sampleState(10_000, 1))
	snapshot1, err := openAt(t, dir, &fakeData{}, 7, clock).CreateSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	now = fixedNow.Add(time.Hour)
	saveStateIn(t, dir, sampleState(90_000, 4))
	s := openAt(t, dir, &fakeData{}, 7, clock)
	if s.Summary() != (Summary{4, 90_000}) {
		t.Fatalf("premise %+v", s.Summary())
	}
	now = fixedNow.Add(2 * time.Hour)
	if err := s.RestoreSnapshot(snapshot1.ID, UsageInput{}); err != nil {
		t.Fatal(err)
	}
	if s.Summary() != (Summary{1, 10_000}) {
		t.Fatalf("restored %+v", s.Summary())
	}
	if !slices.ContainsFunc(s.ListSnapshots(), func(x SnapshotSummary) bool { return x.LifetimeTokens == 90_000 && x.DexCount == 4 }) {
		t.Fatal("no safety snapshot of the replaced state")
	}
}

// fillSnapshots writes the ten snapshots of SST:263-266 and the current 99,000 / 12 save.
func fillSnapshots(t *testing.T) string {
	dir := writeState(t, freshState())
	for i := 0; i < maxSnapshots; i++ {
		writeSnapshotOf(t, dir, sampleState(10_000+i*1_000, i+1), fixedNow.Add(time.Duration(i)*time.Hour))
	}
	saveStateIn(t, dir, sampleState(99_000, 12))
	return dir
}

func TestRestoreOldestSnapshotAtRetentionLimit(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveSnapshotTests.swift:258
	dir := fillSnapshots(t)
	s := openAt(t, dir, &fakeData{}, 7, withNow(func() time.Time { return fixedNow.Add(maxSnapshots * time.Hour) }))
	list := s.ListSnapshots()
	if len(list) != maxSnapshots || list[len(list)-1].LifetimeTokens != 10_000 {
		t.Fatalf("premise %+v", list)
	}
	if err := s.RestoreSnapshot(list[len(list)-1].ID, UsageInput{}); err != nil {
		t.Fatal(err)
	}
	after := s.ListSnapshots()
	if s.Summary() != (Summary{1, 10_000}) || len(after) != maxSnapshots ||
		!slices.ContainsFunc(after, func(x SnapshotSummary) bool { return x.LifetimeTokens == 99_000 && x.DexCount == 12 }) {
		t.Fatalf("restored %+v, snapshots %+v", s.Summary(), after)
	}
}

func TestRestoreUnreadableSnapshotLeavesStateAndSnapshotsUntouched(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/SaveSnapshotTests.swift:285 — the port lists before it reads,
	// so the corrupt selection is notFound; nothing is written or pruned either way.
	dir := fillSnapshots(t)
	s := openAt(t, dir, &fakeData{}, 7, withNow(func() time.Time { return fixedNow.Add(maxSnapshots * time.Hour) }))
	list := s.ListSnapshots()
	selected := list[0]
	if err := os.WriteFile(filepath.Join(s.snapshotDir(), selected.ID), []byte("corrupted invalid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := s.RestoreSnapshot(selected.ID, UsageInput{})
	if transferCode(err) != "notFound" || s.Summary() != (Summary{12, 99_000}) {
		t.Fatalf("%v, %+v", err, s.Summary())
	}
	var before, after []string
	for _, x := range list[1:] {
		before = append(before, x.ID)
	}
	for _, x := range s.ListSnapshots() {
		after = append(after, x.ID)
	}
	if !slices.Equal(before, after) {
		t.Fatalf("snapshots changed:\n%v\n%v", before, after)
	}
	if entries, _ := os.ReadDir(dir); slices.ContainsFunc(entries, func(e os.DirEntry) bool { return strings.HasPrefix(e.Name(), backupPrefix) }) {
		t.Fatal("a backup was written for a restore that never happened")
	}
}

// New in the port: an id that is not listed is notFound, and the safety snapshot failing does not
// stop a restore.
func TestRestoreUnknownIDAndSafetySnapshotFailure(t *testing.T) {
	dir := fillSnapshots(t)
	s := openAt(t, dir, &fakeData{}, 7, withNow(func() time.Time { return fixedNow.Add(maxSnapshots * time.Hour) }))
	if err := s.RestoreSnapshot("companion-snapshot-1999-01-01-000000.json", UsageInput{}); transferCode(err) != "notFound" {
		t.Fatalf("%v", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	oldest := s.ListSnapshots()[maxSnapshots-1].ID
	_ = os.Chmod(s.snapshotDir(), 0o500)
	defer os.Chmod(s.snapshotDir(), 0o700)
	if err := s.RestoreSnapshot(oldest, UsageInput{}); err != nil || s.Summary() != (Summary{1, 10_000}) {
		t.Fatalf("%v %+v", err, s.Summary())
	}
}

// New in the port: the export falls back to a direct write when the directory refuses a temp file
// (a document-portal mount), and reports the file errors otherwise.
func TestExportWriteFallbackAndErrors(t *testing.T) {
	s := openAt(t, writeState(t, sampleState(5, 1)), &fakeData{}, 7)
	if _, err := s.ExportSave(filepath.Join(t.TempDir(), "missing", "x.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing dir: %v", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.json")
	_ = os.WriteFile(path, []byte("old"), 0o600)
	_ = os.Chmod(dir, 0o500)
	defer os.Chmod(dir, 0o700)
	n, err := s.ExportSave(path)
	data, _ := os.ReadFile(path)
	if err != nil || n != len(data) || !strings.Contains(string(data), envelopeFormat) {
		t.Fatalf("direct write: %v %d", err, n)
	}
	if _, err := s.ExportSave(filepath.Join(dir, "new.json")); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("read-only dir: %v", err)
	}
}

// New in the port: an import resets the in-memory game state and announces nothing, even when the
// file claims seeded badges it never recorded (protocol §7.7: existing badges become history).
func TestImportResetsMemoryAndAnnouncesNothing(t *testing.T) {
	cur := freshState()
	cur.InstallBaselineSet, cur.Active = true, mon(1, []int{1}, 0, rarityCommon, 3)
	s := openAt(t, writeState(t, cur), stub(linear3()), 7)
	s.loadCurrentLine(bg)
	s.openWindow(time.Hour)
	s.prefetchedLineID, s.hatchDelayed = 4, true
	if s.line == nil {
		t.Fatal("premise: line loaded")
	}
	imported := freshState()
	imported.Active = mon(403, []int{403}, 0, rarityCommon, 1)
	e := entry(1, 151, nil, rarityCommon)
	for id := 1; id <= 151; id++ {
		e.ChainOrder = append(e.ChainOrder, id)
	}
	imported.Dex, imported.DexCompletionsSeeded = []dexEntry{e}, true
	applyOK(t, s, encodeFor(t, imported, "A"), UsageInput{})
	if s.line != nil || s.prefetchedLineID != 0 || s.hatchDelayed || !s.warmPending || !s.window.until.IsZero() {
		t.Fatalf("memory not reset: line=%v prefetched=%d delayed=%v warm=%v window=%v", s.line, s.prefetchedLineID, s.hatchDelayed, s.warmPending, s.window.until)
	}
	if evs := s.drain(); len(evs) != 0 || !s.st.NotifiedDexCompletions["region.kanto"] {
		t.Fatalf("events %+v, notified %v", evs, sortedKeys(s.st.NotifiedDexCompletions))
	}
}

// New in the port: listSnapshots returns at most ten even when more valid files are present (a
// copy into the folder, or a crash between a write and its pruning).
func TestListSnapshotsReturnsAtMostTen(t *testing.T) {
	dir := fillSnapshots(t)
	s := openAt(t, dir, &fakeData{}, 7)
	src := filepath.Join(s.snapshotDir(), s.ListSnapshots()[0].ID)
	data, _ := os.ReadFile(src)
	for _, name := range []string{"companion-snapshot-2030-01-01-000000.json", "companion-snapshot-2030-01-01-000001.json"} {
		_ = os.WriteFile(filepath.Join(s.snapshotDir(), name), data, 0o600)
	}
	eq(t, len(s.listSnapshots()), maxSnapshots+2, "premise")
	eq(t, len(s.ListSnapshots()), maxSnapshots, "listed")
}

// New in the port: the row shows the current stage, not the base species (SS:78, CM:486).
func TestSnapshotSummaryUsesTheCurrentStage(t *testing.T) {
	st := sampleState(1, 1)
	st.Active = mon(1, []int{1, 2}, 1, rarityCommon, 3)
	s := openAt(t, writeState(t, st), &fakeData{}, 7)
	snap, err := s.CreateSnapshot()
	if err != nil || snap.SpeciesID == nil || *snap.SpeciesID != 2 || *s.ListSnapshots()[0].SpeciesID != 2 {
		t.Fatalf("%v %+v", err, snap)
	}
}
