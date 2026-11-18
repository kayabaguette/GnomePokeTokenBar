// Package ipc implements the newline-delimited JSON transport used over stdio.
package ipc

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"
)

// MaxLine is the largest accepted input line (1 MiB).
const MaxLine = 1 << 20

// Reader reads one line at a time. A line longer than MaxLine ends the
// stream with bufio.ErrTooLong.
type Reader struct {
	sc *bufio.Scanner
}

func NewReader(r io.Reader) *Reader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), MaxLine)
	return &Reader{sc: sc}
}

// Next returns the next line (valid until the next call). It returns io.EOF
// at end of input.
func (r *Reader) Next() ([]byte, error) {
	if r.sc.Scan() {
		return r.sc.Bytes(), nil
	}
	if err := r.sc.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

// Writer serializes values as one JSON object per line. Safe for concurrent use.
type Writer struct {
	mu sync.Mutex
	w  io.Writer
}

func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

func (w *Writer) Write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.w.Write(b)
	return err
}
