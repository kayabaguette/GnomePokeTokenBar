// Package ipc implements the newline-delimited JSON transport used over stdio.
package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// MaxLine is the largest accepted input line (1 MiB), excluding the terminator (protocol §1).
const MaxLine = 1 << 20

// ErrLineTooLong is returned by Next for a line longer than MaxLine. The line has been discarded
// through its terminator, so the next call reads the following line.
var ErrLineTooLong = errors.New("ipc: line longer than 1 MiB skipped")

// Reader reads one line at a time.
type Reader struct {
	br  *bufio.Reader
	buf []byte
}

// NewReader returns a Reader over r.
func NewReader(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReaderSize(r, 64*1024)}
}

// Next returns the next line without its "\n" (valid until the next call). A final line without a
// terminator is returned too. It returns ErrLineTooLong for an over-long line, which is skipped,
// and io.EOF at end of input.
func (r *Reader) Next() ([]byte, error) {
	r.buf = r.buf[:0]
	tooLong := false
	for {
		chunk, err := r.br.ReadSlice('\n')
		if !tooLong {
			content := len(r.buf) + len(chunk)
			if err == nil {
				content-- // the terminator does not count
			}
			if content > MaxLine {
				tooLong, r.buf = true, r.buf[:0]
			} else {
				r.buf = append(r.buf, chunk...)
			}
		}
		switch {
		case err == nil:
			if tooLong {
				return nil, ErrLineTooLong
			}
			return r.buf[:len(r.buf)-1], nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if tooLong {
				return nil, ErrLineTooLong
			}
			if len(r.buf) > 0 {
				return r.buf, nil
			}
			return nil, io.EOF
		default:
			return nil, err
		}
	}
}

// Writer serializes values as one JSON object per line. Safe for concurrent use.
type Writer struct {
	mu sync.Mutex
	w  io.Writer
}

// NewWriter returns a Writer over w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Write encodes v as one line.
func (w *Writer) Write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return w.WriteLine(b)
}

// WriteLine writes an already encoded JSON value followed by "\n".
func (w *Writer) WriteLine(b []byte) error {
	line := make([]byte, 0, len(b)+1)
	line = append(append(line, b...), '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.w.Write(line)
	return err
}
