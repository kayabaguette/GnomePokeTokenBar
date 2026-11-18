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

// Protocol §1/§11.1: an over-long line is discarded through its "\n" and reading continues.
func TestOversizeLineIsSkipped(t *testing.T) {
	exact := strings.Repeat("e", MaxLine)
	in := "ok\n" + strings.Repeat("x", MaxLine+10) + "\n" + "after\n" + exact + "\n" +
		strings.Repeat("y", MaxLine+1) + "\n" + strings.Repeat("z", 3*MaxLine) + "\nlast"
	r := NewReader(strings.NewReader(in))
	want := []struct {
		line string
		err  error
	}{
		{"ok", nil}, {"", ErrLineTooLong}, {"after", nil}, {exact, nil}, {"", ErrLineTooLong},
		{"", ErrLineTooLong}, {"last", nil}, {"", io.EOF},
	}
	for i, w := range want {
		l, err := r.Next()
		if err != w.err || string(l) != w.line {
			t.Fatalf("read %d: got (%d bytes, %v), want (%d bytes, %v)", i, len(l), err, len(w.line), w.err)
		}
	}
}

func TestOversizeFinalLineWithoutTerminator(t *testing.T) {
	r := NewReader(strings.NewReader(strings.Repeat("x", MaxLine+1)))
	if _, err := r.Next(); err != ErrLineTooLong {
		t.Fatalf("want ErrLineTooLong, got %v", err)
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestReadErrorIsReturned(t *testing.T) {
	r := NewReader(bufio.NewReader(errReader{}))
	if _, err := r.Next(); err == nil || err == io.EOF {
		t.Fatalf("want a read error, got %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

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
