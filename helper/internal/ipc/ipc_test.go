package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	in := map[string]any{"type": "result", "id": float64(7), "ok": true}
	if err := w.Write(in); err != nil {
		t.Fatal(err)
	}
	line, err := NewReader(&buf).Next()
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(line, &out); err != nil {
		t.Fatal(err)
	}
	if out["type"] != "result" || out["id"] != float64(7) || out["ok"] != true {
		t.Fatalf("got %v", out)
	}
	if _, err := NewReader(&buf).Next(); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestOversizeLine(t *testing.T) {
	r := NewReader(strings.NewReader("ok\n" + strings.Repeat("x", MaxLine+10) + "\n"))
	if l, err := r.Next(); err != nil || string(l) != "ok" {
		t.Fatalf("first line: %q %v", l, err)
	}
	if _, err := r.Next(); err != bufio.ErrTooLong {
		t.Fatalf("want ErrTooLong, got %v", err)
	}
}

func TestConcurrentWritesDoNotInterleave(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	pad := strings.Repeat("y", 4096)
	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := w.Write(map[string]any{"i": i, "pad": pad}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	r := NewReader(&buf)
	seen := map[int]bool{}
	for {
		line, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			I   int    `json:"i"`
			Pad string `json:"pad"`
		}
		if err := json.Unmarshal(line, &m); err != nil || m.Pad != pad {
			t.Fatalf("corrupt line: %v", err)
		}
		seen[m.I] = true
	}
	if len(seen) != n {
		t.Fatalf("got %d distinct lines, want %d", len(seen), n)
	}
}
