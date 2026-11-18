package usage

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	cacheSaveInterval = 60 * time.Second
	cacheRetention    = 40 * 24 * time.Hour
)

// FileSig identifies one version of a file. Equal signatures mean "not changed".
type FileSig struct {
	MtimeNs int64  `json:"mtime"`
	Size    int64  `json:"size"`
	Inode   uint64 `json:"inode"`
}

func sigOf(fi fs.FileInfo) FileSig {
	s := FileSig{MtimeNs: fi.ModTime().UnixNano(), Size: fi.Size()}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		s.Inode = st.Ino
	}
	return s
}

// File is a log file selected for a scan.
type File struct {
	Path string
	Sig  FileSig
}

// ListFiles walks roots in order and returns the files whose name satisfies match and whose
// mtime is at or after since. Hidden files and directories are skipped, symlinked
// directories are not followed, and unreadable paths are ignored
// (LocalUsageCache.swift:386-404).
func ListFiles(roots []string, since time.Time, match func(name string) bool) []File {
	var out []File
	for _, root := range roots {
		// Cache keys are absolute paths, whatever the working directory.
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			hidden := path != root && strings.HasPrefix(d.Name(), ".")
			if d.IsDir() {
				if hidden {
					return filepath.SkipDir
				}
				return nil
			}
			if hidden || !match(d.Name()) {
				return nil
			}
			fi, err := os.Stat(path)
			if err != nil || fi.IsDir() || fi.ModTime().Before(since) {
				return nil
			}
			out = append(out, File{Path: path, Sig: sigOf(fi)})
			return nil
		})
	}
	return out
}

// ParseFunc parses one file's bytes, read from the cache's resume offset, which r yields up to
// the size the cache observed. It returns the entries found, deduplicated within r, and the
// number of bytes through the last newline: a trailing partial line is parsed but not
// consumed, so it is read again once complete.
type ParseFunc func(r io.Reader, path string) (entries []Entry, consumed int64, err error)

// MergeFunc combines cached entries with the entries of a newly appended tail.
type MergeFunc func(prior, tail []Entry) []Entry

// CacheOptions configures a FileCache.
type CacheOptions struct {
	// Dir holds the cache file; empty keeps the cache in memory only.
	Dir string
	// Name is the cache file name inside Dir.
	Name string
	// Version is the parser version: a cache written with another version is discarded. Bump
	// it when entry semantics change, not for price-only changes.
	Version int
	// Now is the clock for pruning and the save throttle; nil means time.Now.
	Now func() time.Time
	// Workers bounds parallel parsing; 0 means GOMAXPROCS, at most 8.
	Workers int
}

// ScanStats describes what one Collect did.
type ScanStats struct {
	Files     int   `json:"files"`
	Hits      int   `json:"hits"`      // unchanged, served from the cache
	Appended  int   `json:"appended"`  // grown, only the tail parsed
	Parsed    int   `json:"parsed"`    // parsed from the start
	Failed    int   `json:"failed"`    // unreadable; the previous entries are kept if any
	BytesRead int64 `json:"bytesRead"` // bytes handed to the parser
}

type blob struct {
	Sig     FileSig `json:"sig"`
	Offset  int64   `json:"offset"`
	Entries []Entry `json:"entries"`
}

type snapshot struct {
	Version int              `json:"version"`
	Files   map[string]*blob `json:"files"`
}

// FileCache memoizes per-file parse results across refreshes and restarts, so a refresh reads
// only what changed (LocalUsageCache.swift). A file is unchanged when its mtime, size and inode
// all match. A file that only grew, with the same inode, is parsed from the offset reached
// last time. Anything else is parsed from the start. Methods are safe for concurrent use;
// scans are serialized.
type FileCache struct {
	opts CacheOptions

	mu       sync.Mutex
	loaded   bool
	blobs    map[string]*blob
	dirty    bool
	lastSave time.Time
}

// NewFileCache returns a cache that loads its file lazily.
func NewFileCache(opts CacheOptions) *FileCache {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Workers <= 0 {
		opts.Workers = min(runtime.GOMAXPROCS(0), 8)
	}
	return &FileCache{opts: opts, blobs: map[string]*blob{}}
}

func (c *FileCache) path() string {
	if c.opts.Dir == "" {
		return ""
	}
	return filepath.Join(c.opts.Dir, c.opts.Name)
}

type result struct {
	blob     *blob // new blob on success, nil on a hit or a failure
	entries  []Entry
	read     int64
	hit      bool
	appended bool
}

// Collect returns the entries of files, in file order, parsing only what changed. A file that
// cannot be read keeps its previous entries under its old signature, so it is retried on the
// next scan. It saves the cache when due.
func (c *FileCache) Collect(files []File, parse ParseFunc, merge MergeFunc) ([]Entry, ScanStats) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLoaded()

	results := make([]result, len(files))
	var jobs []int
	for i, f := range files {
		if b := c.blobs[f.Path]; b != nil && b.Sig == f.Sig {
			results[i] = result{entries: b.Entries, hit: true}
			continue
		}
		jobs = append(jobs, i)
	}

	// Workers only read c.blobs; results are written back after they finish.
	queue := make(chan int)
	var wg sync.WaitGroup
	for range min(c.opts.Workers, len(jobs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				results[i] = parseOne(files[i].Path, c.blobs[files[i].Path], parse, merge)
			}
		}()
	}
	for _, i := range jobs {
		queue <- i
	}
	close(queue)
	wg.Wait()

	stats := ScanStats{Files: len(files)}
	var all []Entry
	for i, r := range results {
		stats.BytesRead += r.read
		switch {
		case r.hit:
			stats.Hits++
		case r.blob == nil:
			stats.Failed++
		case r.appended:
			stats.Appended++
		default:
			stats.Parsed++
		}
		if r.blob != nil {
			c.blobs[files[i].Path] = r.blob
			c.dirty = true
		}
		all = append(all, r.entries...)
	}
	c.saveIfDue()
	return all, stats
}

// parseOne reads one changed file. It re-reads the signature from the open handle and bounds
// the read to that size, so the recorded offset always matches the recorded signature.
func parseOne(path string, prior *blob, parse ParseFunc, merge MergeFunc) result {
	failed := result{}
	if prior != nil {
		failed.entries = prior.Entries
	}
	f, sig, err := openWithSig(path)
	if err != nil {
		return failed
	}
	defer f.Close()
	var from int64
	if prior != nil && sig.Size > prior.Sig.Size && sig.Inode == prior.Sig.Inode {
		from = prior.Offset
	}
	entries, consumed, err := parse(io.NewSectionReader(f, from, sig.Size-from), path)
	if err != nil {
		return failed
	}
	if from > 0 {
		entries = merge(prior.Entries, entries)
	}
	b := &blob{Sig: sig, Offset: from + consumed, Entries: entries}
	return result{blob: b, entries: entries, read: sig.Size - from, appended: from > 0}
}

func openWithSig(path string) (*os.File, FileSig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, FileSig{}, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, FileSig{}, err
	}
	return f, sigOf(fi), nil
}

// Flush saves pending changes now, ignoring the save throttle. Call it before exiting.
func (c *FileCache) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty || c.path() == "" {
		return nil
	}
	return c.save()
}

func (c *FileCache) saveIfDue() {
	if !c.dirty {
		return
	}
	if c.path() == "" {
		c.dirty = false
		return
	}
	now := c.opts.Now()
	if !c.lastSave.IsZero() && now.Sub(c.lastSave) < cacheSaveInterval {
		return
	}
	if err := c.save(); err != nil {
		log.Printf("usage cache: save failed: %v", err)
	}
}

// save prunes files not modified in the last 40 days, then writes zlib-compressed JSON
// atomically (temporary file, then rename). Pruned files are only parsed again if they come
// back into a scan window.
func (c *FileCache) save() error {
	now := c.opts.Now()
	c.lastSave = now
	cutoff := now.Add(-cacheRetention).UnixNano()
	for p, b := range c.blobs {
		if b.Sig.MtimeNs < cutoff {
			delete(c.blobs, p)
		}
	}
	// Encoding cannot fail: the writer is a bytes.Buffer and the snapshot holds only strings,
	// integers and times.
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	_ = json.NewEncoder(zw).Encode(snapshot{Version: c.opts.Version, Files: c.blobs})
	_ = zw.Close()
	if err := writeAtomic(c.path(), buf.Bytes()); err != nil {
		return err
	}
	c.dirty = false
	return nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// ensureLoaded reads the cache file once. A missing, unreadable or foreign file starts empty;
// a version mismatch discards every blob.
func (c *FileCache) ensureLoaded() {
	if c.loaded {
		return
	}
	c.loaded = true
	if c.path() == "" {
		return
	}
	snap, err := readSnapshot(c.path())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("usage cache: ignoring unreadable cache: %v", err)
		}
		return
	}
	if snap.Version != c.opts.Version {
		c.dirty = true
		return
	}
	for p, b := range snap.Files {
		if b != nil {
			c.blobs[p] = b
		}
	}
}

func readSnapshot(path string) (snapshot, error) {
	var snap snapshot
	f, err := os.Open(path)
	if err != nil {
		return snap, err
	}
	defer f.Close()
	zr, err := zlib.NewReader(f)
	if err != nil {
		return snap, err
	}
	defer zr.Close()
	err = json.NewDecoder(zr).Decode(&snap)
	return snap, err
}
