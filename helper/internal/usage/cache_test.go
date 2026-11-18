package usage

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// lineParser reads "id output" lines; it fails while *fail is true.
func lineParser(fail *bool) ParseFunc {
	return func(r io.Reader, path string) ([]Entry, int64, error) {
		if *fail {
			return nil, 0, errors.New("injected read error")
		}
		var out []Entry
		var consumed int64
		br := bufio.NewReader(r)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return out, consumed, nil
			}
			consumed += int64(len(line))
			id, n, _ := strings.Cut(strings.TrimSpace(line), " ")
			v, _ := strconv.ParseInt(n, 10, 64)
			out = append(out, Entry{ID: id, Output: v})
		}
	}
}

func concat(prior, tail []Entry) []Entry { return append(append([]Entry(nil), prior...), tail...) }

func outs(es []Entry) []int64 {
	var o []int64
	for _, e := range es {
		o = append(o, e.Output)
	}
	return o
}

func quietLog(t *testing.T) {
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
}

func TestCollectKeepsPriorEntriesOnParseError(t *testing.T) {
	// Go-only: a read error mid-file keeps the old entries under the old signature, then the
	// next scan resumes from the old offset (LocalUsageCache.swift:425-429).
	dir := t.TempDir()
	path := filepath.Join(dir, "a.jsonl")
	os.WriteFile(path, []byte("a 10\n"), 0o644)
	c := NewFileCache(CacheOptions{Version: 1})
	fail := false
	list := func() []File { return ListFiles([]string{dir}, time.Time{}, func(string) bool { return true }) }
	if got, _ := c.Collect(list(), lineParser(&fail), concat); !slices.Equal(outs(got), []int64{10}) {
		t.Fatalf("first %v", outs(got))
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("b 20\n")
	f.Close()
	fail = true
	if got, st := c.Collect(list(), lineParser(&fail), concat); !slices.Equal(outs(got), []int64{10}) || st.Failed != 1 {
		t.Errorf("during failure %v %+v", outs(got), st)
	}
	fail = false
	if got, st := c.Collect(list(), lineParser(&fail), concat); !slices.Equal(outs(got), []int64{10, 20}) || st.Appended != 1 {
		t.Errorf("after recovery %v %+v", outs(got), st)
	}
}

func TestListFilesSkipsWhatItCannotRead(t *testing.T) {
	// Go-only: a missing root or an unreadable directory is skipped, not fatal.
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "locked"), 0o755)
	os.WriteFile(filepath.Join(root, "locked", "x.jsonl"), nil, 0o644)
	os.WriteFile(filepath.Join(root, "ok.jsonl"), nil, 0o644)
	os.Chmod(filepath.Join(root, "locked"), 0)
	t.Cleanup(func() { os.Chmod(filepath.Join(root, "locked"), 0o755) })
	files := ListFiles([]string{filepath.Join(root, "missing"), root}, time.Time{}, func(n string) bool { return filepath.Ext(n) == ".jsonl" })
	if len(files) != 1 || filepath.Base(files[0].Path) != "ok.jsonl" {
		t.Errorf("files %+v", files)
	}
}

func TestCacheSaveFailures(t *testing.T) {
	// Go-only: a failed save keeps the changes pending, logs, and never touches the results.
	quietLog(t)
	fail := false
	scan := func(c *FileCache, dir string) []Entry {
		got, _ := c.Collect(ListFiles([]string{dir}, time.Time{}, func(string) bool { return true }), lineParser(&fail), concat)
		return got
	}
	data := t.TempDir()
	os.WriteFile(filepath.Join(data, "a.jsonl"), []byte("a 1\n"), 0o644)

	notADir := filepath.Join(t.TempDir(), "file")
	os.WriteFile(notADir, nil, 0o644)
	readOnly := t.TempDir()
	os.Chmod(readOnly, 0o500)
	t.Cleanup(func() { os.Chmod(readOnly, 0o700) })
	nameIsDir := t.TempDir()
	os.MkdirAll(filepath.Join(nameIsDir, "cache"), 0o700)

	cases := []struct {
		name string
		dir  string
	}{
		{"cache dir is a file", notADir},
		{"cache dir is read-only", readOnly},
		{"cache name is a directory", nameIsDir},
	}
	for _, c := range cases {
		if c.dir == readOnly && os.Geteuid() == 0 {
			continue
		}
		fc := NewFileCache(CacheOptions{Dir: c.dir, Name: "cache", Version: 1})
		if got := scan(fc, data); !slices.Equal(outs(got), []int64{1}) {
			t.Errorf("%s: results %v", c.name, outs(got))
		}
		if err := fc.Flush(); err == nil {
			t.Errorf("%s: Flush succeeded", c.name)
		}
		if leftovers, _ := filepath.Glob(filepath.Join(c.dir, "cache.tmp-*")); len(leftovers) != 0 {
			t.Errorf("%s: temporary files left: %v", c.name, leftovers)
		}
	}
	// The pending changes are written once the directory works again.
	os.RemoveAll(filepath.Join(nameIsDir, "cache"))
	fc := NewFileCache(CacheOptions{Dir: nameIsDir, Name: "cache", Version: 1})
	fc.dirty = true
	if err := fc.Flush(); err != nil {
		t.Errorf("Flush after repair: %v", err)
	}
}

func TestCacheFileEdgeCases(t *testing.T) {
	quietLog(t)
	fail := false
	data := t.TempDir()
	os.WriteFile(filepath.Join(data, "a.jsonl"), []byte("a 1\n"), 0o644)
	files := ListFiles([]string{data}, time.Time{}, func(string) bool { return true })

	// Go-only: an unreadable or foreign cache file starts empty instead of failing.
	var jsonGarbage bytes.Buffer
	zw := zlib.NewWriter(&jsonGarbage)
	zw.Write([]byte("{not json"))
	zw.Close()
	for name, content := range map[string][]byte{"not zlib": []byte("garbage"), "not JSON": jsonGarbage.Bytes()} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "cache"), content, 0o600)
		c := NewFileCache(CacheOptions{Dir: dir, Name: "cache", Version: 1})
		if got, st := c.Collect(files, lineParser(&fail), concat); !slices.Equal(outs(got), []int64{1}) || st.Parsed != 1 {
			t.Errorf("%s: %v %+v", name, outs(got), st)
		}
	}

	// Go-only: Flush without changes writes nothing.
	dir := t.TempDir()
	if err := NewFileCache(CacheOptions{Dir: dir, Name: "cache", Version: 1}).Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cache")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Flush without changes wrote a file: %v", err)
	}
}

func TestCostJSON(t *testing.T) {
	// Go-only: the wire shape of protocol §1.1, {usd, coverage}.
	b, err := json.Marshal(Cost{USD: 1.5, Coverage: Coverage{Estimated: true, Unknown: true}})
	if err != nil || string(b) != `{"usd":1.5,"coverage":"partial"}` {
		t.Errorf("Cost JSON = %s, %v", b, err)
	}
}
