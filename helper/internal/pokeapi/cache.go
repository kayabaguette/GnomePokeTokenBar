package pokeapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TTL is how long a cached response is served without revalidation.
const TTL = 30 * 24 * time.Hour

// snapshot is the on-disk envelope of every cached response.
type snapshot[T any] struct {
	FetchedAt time.Time `json:"fetchedAt"`
	Data      T         `json:"data"`
}

// flight dedupes concurrent calls for the same key.
type flight struct {
	mu    sync.Mutex
	calls map[string]*flightCall
}

type flightCall struct {
	done chan struct{}
	val  any
	err  error
}

func (f *flight) do(key string, fn func() (any, error)) (any, error) {
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]*flightCall{}
	}
	if c, ok := f.calls[key]; ok {
		f.mu.Unlock()
		<-c.done
		return c.val, c.err
	}
	c := &flightCall{done: make(chan struct{})}
	f.calls[key] = c
	f.mu.Unlock()

	c.val, c.err = fn()

	f.mu.Lock()
	delete(f.calls, key)
	f.mu.Unlock()
	close(c.done)
	return c.val, c.err
}

// cached returns the value stored at rel (relative to the cache dir) when it
// is younger than TTL; otherwise it calls fetch, persists the result
// atomically, and returns it. When fetch fails, any stale value is returned
// instead. Concurrent calls for the same rel share one fetch.
func cached[T any](c *Client, rel string, fetch func() (T, error)) (T, error) {
	v, err := c.flight.do(rel, func() (any, error) {
		prev, havePrev := readSnapshot[T](c.path(rel))
		if havePrev && c.now().Sub(prev.FetchedAt) < TTL {
			return prev.Data, nil
		}
		data, err := fetch()
		if err != nil {
			if havePrev {
				return prev.Data, nil
			}
			return nil, err
		}
		// A failed write only costs a refetch next time.
		_ = writeJSONAtomic(c.path(rel), snapshot[T]{FetchedAt: c.now(), Data: data})
		return data, nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return v.(T), nil
}

func readSnapshot[T any](path string) (snapshot[T], bool) {
	var s snapshot[T]
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &s) != nil || s.FetchedAt.IsZero() {
		return s, false
	}
	return s, true
}

// writeJSONAtomic writes v to path via a temp file in the same directory and a rename.
func writeJSONAtomic(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(f.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		os.Remove(f.Name())
		return err
	}
	return nil
}
