package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"poketokenbar/helper/internal/companion"
	"poketokenbar/helper/internal/usage"
)

const macosExport = "../companion/testdata/macos-export.json"

// saveWorld is a save with an active line-16 Pokémon raised at growth difficulty 0.5, and no usage
// today, so no scan credits anything.
func saveWorld(t *testing.T) *harness {
	h := newHarness(t, "")
	h.usage.set(0)
	writeFile(t, filepath.Join(h.cfg.StateDir, "companion-state.json"), `{"installBaselineSet":true,"growthDifficulty":0.5,
		"lastDate":"2026-01-01","claimedTodayTokensByProvider":{},"usedSinceInstall":5000000,
		"active":{"baseID":16,"pathIDs":[16],"plannedPathIDs":[16,17,18],"stageIndex":0,"usedAtStage":1000,
		"rarity":"common","totalForms":3,"nature":"adamant"}}`)
	return h
}

func settingsAt(id int, difficulty float64) string {
	return fmt.Sprintf(`{"id":%d,"cmd":"setSettings","args":{"refreshInterval":0,"warnThreshold":80,"critThreshold":95,"growthDifficulty":%v,"weekStart":1,"language":"en","alwaysFacesBack":false}}`, id, difficulty)
}

func copyFixture(t *testing.T, dst string) []byte {
	t.Helper()
	b, err := os.ReadFile(macosExport)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dst, string(b))
	return b
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func cmdLine(id int, cmd string, args any) string {
	b, _ := json.Marshal(map[string]any{"id": id, "cmd": cmd, "args": args})
	return string(b)
}

func summaryOf(m msg, keys ...string) [2]float64 {
	return [2]float64{m.num(append(keys, "dexCount")...), m.num(append(keys, "lifetimeTokens")...)}
}

// Protocol §7.7: the six save commands, their replies, the state-before-result order of the
// mutating ones, and their error codes.
func TestSaveCommands(t *testing.T) {
	h := saveWorld(t)
	h.start()
	h.send(settingsAt(1, 0.5))
	st := h.expect("fresh state", wait, isFreshState)
	if st.num("companion", "active", "usedAtStage") != 1000 {
		t.Fatalf("premise: %v", st.path("companion", "active"))
	}
	files := t.TempDir()

	h.send(`{"id":2,"cmd":"createSnapshot"}`)
	created := h.expect("createSnapshot", wait, isResult(2))
	snap, _ := created.path("data", "snapshot").(map[string]any)
	if snap == nil || !regexp.MustCompile(`^companion-snapshot-[0-9-]+\.json$`).MatchString(fmt.Sprint(snap["id"])) ||
		snap["dexCount"] != 0.0 || snap["lifetimeTokens"] != 5_000_000.0 || snap["speciesId"] != 16.0 || snap["shiny"] != false {
		t.Fatalf("createSnapshot = %v", created)
	}
	h.send(`{"id":3,"cmd":"listSnapshots"}`)
	list, _ := h.expect("listSnapshots", wait, isResult(3)).path("data", "snapshots").([]any)
	listed := false
	for _, x := range list {
		listed = listed || reflect.DeepEqual(x, any(snap))
	}
	if !listed { // an automatic snapshot may share the second, so the created one need not be first
		t.Fatalf("listSnapshots %v, created %v", list, snap)
	}

	out := filepath.Join(files, "PokeTokenBar-Save-2026-11-18.json")
	h.send(cmdLine(4, "exportSave", map[string]any{"path": out}))
	r := h.expect("exportSave", wait, isResult(4))
	data, err := os.ReadFile(out)
	if err != nil || r.str("data", "path") != out || r.num("data", "bytes") != float64(len(data)) || summaryOf(r, "data", "summary") != [2]float64{0, 5_000_000} {
		t.Fatalf("exportSave = %v (%v)", r, err)
	}
	if strings.Contains(string(data), "growthDifficulty") || !regexp.MustCompile(`"exportedAt": "\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ"`).Match(data) {
		t.Fatalf("export:\n%s", data)
	}

	imp := filepath.Join(files, "from-mac.json")
	raw := copyFixture(t, imp)
	h.send(cmdLine(5, "inspectSave", map[string]any{"path": imp}))
	r = h.expect("inspectSave", wait, isResult(5))
	if r.str("data", "digest") != sha(raw) || r.num("data", "schema") != 2 || r.str("data", "appVersion") != "2.5.7" ||
		r.str("data", "exportedAt") != "2026-11-17T21:04:12Z" || r.str("data", "sourceDevice") != "Jane’s MacBook Pro" ||
		summaryOf(r, "data", "incoming") != [2]float64{3, 52_800_000_000} || summaryOf(r, "data", "current") != [2]float64{0, 5_000_000} {
		t.Fatalf("inspectSave = %v", r)
	}

	h.send(cmdLine(6, "importSave", map[string]any{"path": imp, "digest": strings.Repeat("0", 64)}))
	if r := h.expect("importSave stale digest", wait, isResult(6)); errorCode(r) != "fileChanged" {
		t.Fatalf("stale digest = %v", r)
	}

	from := len(h.seen)
	h.send(cmdLine(7, "importSave", map[string]any{"path": imp, "digest": sha(raw)}))
	r = h.expect("importSave", wait, isResult(7))
	if !r.flag("ok") || summaryOf(r, "data", "summary") != [2]float64{3, 52_800_000_000} {
		t.Fatalf("importSave = %v", r)
	}
	resIdx := len(h.seen) - 1
	stIdx := h.indexOf(from, func(m msg) bool {
		return m.typ() == "state" && m.num("companion", "active", "speciesId") == 201 && m.num("companion", "wallet") == 51_300_000_000
	})
	if stIdx < 0 || stIdx > resIdx {
		t.Fatalf("no imported state before the result:\n%s", h.tail(6))
	}
	if i := h.indexOf(from, func(m msg) bool { return m.typ() == "event" }); i >= 0 && i < resIdx {
		t.Fatalf("an import emitted an event: %v", h.seen[i])
	}
	if a := h.seen[stIdx].path("companion", "active").(map[string]any); a["usedAtStage"] != 1_234_567.0 || a["isShiny"] != true || a["form"] != "question" {
		t.Fatalf("imported active %v", a)
	}

	// The import kept this computer's difficulty: setSettings at 0.5 rescales nothing (hazard 2).
	h.send(settingsAt(8, 0.5))
	h.expect("setSettings", wait, isResult(8))
	if st := h.lastState(); st.num("companion", "active", "usedAtStage") != 1_234_567 || st.num("settingsApplied", "growthDifficulty") != 0.5 {
		t.Fatalf("after setSettings: %v", st.path("companion", "active", "usedAtStage"))
	}

	// Restore the snapshot taken before the import: the safety snapshot keeps the imported state.
	from = len(h.seen)
	h.send(cmdLine(9, "restoreSnapshot", map[string]any{"id": snap["id"]}))
	r = h.expect("restoreSnapshot", wait, isResult(9))
	if summaryOf(r, "data", "summary") != [2]float64{0, 5_000_000} {
		t.Fatalf("restoreSnapshot = %v", r)
	}
	if i := h.indexOf(from, func(m msg) bool { return m.typ() == "state" && m.num("companion", "active", "speciesId") == 16 }); i < 0 || i > len(h.seen)-2 {
		t.Fatalf("no restored state before the result:\n%s", h.tail(4))
	}
	h.send(`{"id":10,"cmd":"listSnapshots"}`)
	list, _ = h.expect("listSnapshots", wait, isResult(10)).path("data", "snapshots").([]any)
	found := false
	for _, x := range list {
		s := x.(map[string]any)
		found = found || (s["lifetimeTokens"] == 52_800_000_000.0 && s["dexCount"] == 3.0 && s["speciesId"] == 201.0)
	}
	if !found || len(list) > 10 {
		t.Fatalf("no safety snapshot of the imported state: %v", list)
	}
	if entries, _ := os.ReadDir(h.cfg.StateDir); !strings.Contains(fmt.Sprint(entries), "companion-state.pre-import-") {
		t.Fatalf("no pre-import backup in %v", entries)
	}

	// Errors.
	writeFile(t, filepath.Join(files, "garbage.json"), "not json")
	writeFile(t, filepath.Join(files, "newer.json"), `{"format":"poketokenbar.save","schema":3}`)
	if err := os.WriteFile(filepath.Join(files, "big.json"), make([]byte, companion.MaxSaveBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	changed := filepath.Join(files, "changed.json")
	changedRaw := copyFixture(t, changed)
	errs := []struct {
		line, code string
	}{
		{cmdLine(20, "exportSave", map[string]any{"path": "relative.json"}), "badArgs"},
		{cmdLine(21, "exportSave", map[string]any{}), "badArgs"},
		{cmdLine(22, "exportSave", map[string]any{"path": 5}), "badArgs"},
		{cmdLine(23, "exportSave", map[string]any{"path": filepath.Join(files, "nope", "x.json")}), "fileNotFound"},
		{cmdLine(24, "inspectSave", map[string]any{"path": filepath.Join(files, "missing.json")}), "fileNotFound"},
		{cmdLine(36, "inspectSave", map[string]any{"path": "./from-mac.json"}), "badArgs"},
		{cmdLine(25, "inspectSave", map[string]any{"path": files}), "ioError"},
		{cmdLine(26, "inspectSave", map[string]any{"path": filepath.Join(files, "garbage.json")}), "notASaveFile"},
		{cmdLine(27, "inspectSave", map[string]any{"path": filepath.Join(files, "newer.json")}), "newerSchema"},
		{cmdLine(28, "inspectSave", map[string]any{"path": filepath.Join(files, "big.json")}), "fileTooLarge"},
		{cmdLine(29, "importSave", map[string]any{"path": imp}), "badArgs"},
		{cmdLine(30, "importSave", map[string]any{"path": "x.json", "digest": sha(raw)}), "badArgs"},
		{cmdLine(31, "restoreSnapshot", map[string]any{"id": "../companion-state.json"}), "badArgs"},
		{cmdLine(32, "restoreSnapshot", map[string]any{"id": 3}), "badArgs"},
		{cmdLine(33, "restoreSnapshot", map[string]any{}), "badArgs"},
		{cmdLine(34, "restoreSnapshot", map[string]any{"id": "companion-snapshot-1999-01-01-000000.json"}), "notFound"},
		{`{"id":35,"cmd":"inspectSave","args":[1]}`, "badArgs"},
	}
	for _, c := range errs {
		h.send(c.line)
	}
	for _, c := range errs {
		var head struct{ ID int64 }
		_ = json.Unmarshal([]byte(c.line), &head)
		if r := h.expect(c.line, wait, isResult(head.ID)); errorCode(r) != c.code {
			t.Errorf("%s → %v, want %s", c.line, r, c.code)
		}
	}
	// A file that changed into something else after the preview: re-validation runs first, then the
	// digest, so garbage is notASaveFile and another valid save is fileChanged.
	writeFile(t, changed, "garbage")
	h.send(cmdLine(40, "importSave", map[string]any{"path": changed, "digest": sha(changedRaw)}))
	if r := h.expect("changed to garbage", wait, isResult(40)); errorCode(r) != "notASaveFile" {
		t.Fatalf("changed to garbage = %v", r)
	}
	writeFile(t, changed, strings.Replace(string(changedRaw), "52800000000", "52800000001", 1))
	h.send(cmdLine(41, "importSave", map[string]any{"path": changed, "digest": sha(changedRaw)}))
	if r := h.expect("changed save", wait, isResult(41)); errorCode(r) != "fileChanged" {
		t.Fatalf("changed save = %v", r)
	}
	if os.Geteuid() != 0 {
		locked := filepath.Join(files, "locked.json")
		copyFixture(t, locked)
		_ = os.Chmod(locked, 0)
		h.send(cmdLine(42, "inspectSave", map[string]any{"path": locked}))
		if r := h.expect("unreadable", wait, isResult(42)); errorCode(r) != "permissionDenied" {
			t.Fatalf("unreadable = %v", r)
		}
	}
	if os.Geteuid() != 0 {
		snaps := filepath.Join(h.cfg.StateDir, ".snapshots", "companion-state.json")
		_ = os.Chmod(snaps, 0o500)
		h.send(`{"id":43,"cmd":"createSnapshot"}`)
		r := h.expect("read-only snapshots", wait, isResult(43))
		_ = os.Chmod(snaps, 0o700)
		if errorCode(r) != "permissionDenied" {
			t.Fatalf("createSnapshot in a read-only folder = %v", r)
		}
	}
	if st := h.lastState(); st.num("companion", "active", "speciesId") != 16 {
		t.Fatal("a failed command changed the save")
	}
}

// Protocol §7.7 error table: the file error kinds, and a Store code wins over the error under it.
func TestSaveErrCode(t *testing.T) {
	pathErr := func(errno syscall.Errno) error { return &fs.PathError{Op: "open", Path: "/x", Err: errno} }
	for _, c := range []struct {
		err  error
		code string
	}{
		{pathErr(syscall.ENOENT), "fileNotFound"},
		{pathErr(syscall.EACCES), "permissionDenied"},
		{pathErr(syscall.EPERM), "permissionDenied"},
		{pathErr(syscall.ENOSPC), "noSpace"},
		{fmt.Errorf("write: %w", pathErr(syscall.EDQUOT)), "noSpace"},
		{pathErr(syscall.EIO), "ioError"},
		{pathErr(syscall.EISDIR), "ioError"},
		{errors.New("short write"), "ioError"},
		{&companion.Error{Code: "backupFailed", Message: "no space left on device"}, "backupFailed"},
		{fmt.Errorf("restore: %w", &companion.Error{Code: "notFound"}), "notFound"},
	} {
		if got := saveErrCode(c.err); got != c.code {
			t.Errorf("%v → %s, want %s", c.err, got, c.code)
		}
	}
}

// save-transfer.md §5.1: draws requeued for the egg being replaced never roll the imported one.
func TestImportForgetsReplayedDraws(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "companion-state.json"), `{"installBaselineSet":true,"eggUsage":10,"lastDate":"2026-01-01","claimedTodayTokensByProvider":{}}`)
	cfg := Config{StateDir: dir, CacheDir: filepath.Join(dir, "cache"), Data: newFakePoke(1), Rng: &companion.SplitMix64{State: 3},
		Now: func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.Local) }, Logf: t.Logf}.withDefaults()
	d, err := open(cfg, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	d.out = newOutput(io.Discard, time.Hour, t.Logf)
	defer d.out.close(time.Now().Add(time.Second))
	imp := filepath.Join(t.TempDir(), "in.json")
	raw := copyFixture(t, imp)
	d.rng.queued = []uint64{1, 2, 3}
	args, _ := json.Marshal(map[string]string{"path": imp, "digest": sha(raw)})
	d.command(request{ID: 1, Cmd: "importSave", Args: args})
	if len(d.rng.queued) != 0 || d.store.Summary() != (companion.Summary{DexCount: 3, LifetimeTokens: 52_800_000_000}) {
		t.Fatalf("queued %v, summary %+v", d.rng.queued, d.store.Summary())
	}
}

// save-transfer.md §5.1: the import re-seeds the device ledger from today's usage once this process
// has scanned (STT:212 through the daemon), and not from a restored last-snapshot.json before.
func TestImportReseedsTheLedgerFromThisComputersUsage(t *testing.T) {
	h := newHarness(t, "")
	today := usage.DayKey(time.Now())
	writeFile(t, filepath.Join(h.cfg.StateDir, snapshotFile), `{"version":1,"usage":{"claude_code":{"days":{"`+today+`":{"cr":100}},"scanned":true}},"limits":{}}`)
	h.usage.set(125)
	imp := filepath.Join(t.TempDir(), "in.json")
	raw := copyFixture(t, imp)
	const wallet = 52_800_000_000 - 1_500_000_000
	h.start()
	h.send(cmdLine(1, "importSave", map[string]any{"path": imp, "digest": sha(raw)}))
	if r := h.expect("import before the first scan", wait, isResult(1)); !r.flag("ok") {
		t.Fatalf("%v", r)
	}
	h.sendSettings(2, false)
	if st := h.expect("fresh", wait, isFreshState); st.num("companion", "wallet") != wallet {
		t.Fatalf("wallet %v: the stale restored usage was used as the baseline", st.num("companion", "wallet"))
	}
	h.send(cmdLine(3, "importSave", map[string]any{"path": imp, "digest": sha(raw)}))
	h.expect("import after the scan", wait, isResult(3))
	h.usage.set(625)
	h.send(`{"id":4,"cmd":"refresh","args":{"manual":true}}`)
	h.expect("refresh", wait, isResult(4))
	if got := h.lastState().num("companion", "wallet"); got != wallet+500 {
		t.Fatalf("wallet %v, want the usage since the import (+500)", got)
	}
}

// save-transfer.md §7 Q4 (protocol decision 46): an import on a day with no usage yet defers the
// baseline, because usageInput drops zero providers. The first non-zero scan becomes the baseline
// and is not credited; only what follows is.
func TestImportOnAZeroUsageDayDefersTheBaseline(t *testing.T) {
	h := newHarness(t, "")
	h.usage.set(0)
	h.usage.history[1] = 5_000 // usage data exists (hasUsageData), none of it today
	imp := filepath.Join(t.TempDir(), "in.json")
	raw := copyFixture(t, imp)
	const wallet = 52_800_000_000 - 1_500_000_000
	h.start()
	h.sendSettings(1, false)
	h.expect("fresh", wait, isFreshState)
	h.send(cmdLine(2, "importSave", map[string]any{"path": imp, "digest": sha(raw)}))
	h.expect("import", wait, isResult(2))
	for i, c := range []struct {
		today, wallet int64
	}{{300, wallet}, {800, wallet + 500}} {
		h.usage.set(c.today)
		h.send(fmt.Sprintf(`{"id":%d,"cmd":"refresh","args":{"manual":true}}`, 3+i))
		h.expect("refresh", wait, isResult(int64(3+i)))
		if got := h.lastState().num("companion", "wallet"); got != float64(c.wallet) {
			t.Fatalf("usage %d: wallet %v, want %d", c.today, got, c.wallet)
		}
	}
}
