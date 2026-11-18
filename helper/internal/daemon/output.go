package daemon

import (
	"encoding/json"
	"io"
	"sync"
	"time"

	"poketokenbar/helper/internal/ipc"
)

// output is the single stdout writer (protocol §2.4). hello, result, event and fatal lines go
// through an unbounded FIFO and are never dropped. state lines go through one slot: a newer state
// replaces an unsent one, and states are written at most once per interval. Queuing a non-state
// line first moves the pending state into the FIFO, so a result or event never overtakes the state
// it depends on. Producers never block on stdout.
type output struct {
	w        *ipc.Writer
	interval time.Duration
	logf     func(string, ...any)

	mu        sync.Mutex
	queue     [][]byte
	state     []byte
	lastState time.Time
	closing   bool
	broken    bool
	wake      chan struct{}
	done      chan struct{}
}

func newOutput(w io.Writer, interval time.Duration, logf func(string, ...any)) *output {
	o := &output{w: ipc.NewWriter(w), interval: interval, logf: logf,
		wake: make(chan struct{}, 1), done: make(chan struct{})}
	go o.run()
	return o
}

func (o *output) signal() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// send queues a hello, result, event or fatal message.
func (o *output) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		o.logf("encode %T: %v", v, err)
		return
	}
	o.mu.Lock()
	if o.state != nil {
		o.queue = append(o.queue, o.state)
		o.state = nil
	}
	o.queue = append(o.queue, b)
	o.mu.Unlock()
	o.signal()
}

// setState replaces the pending state.
func (o *output) setState(b []byte) {
	o.mu.Lock()
	o.state = b
	o.mu.Unlock()
	o.signal()
}

// close writes everything still pending and stops the writer, waiting at most until deadline.
func (o *output) close(deadline time.Time) {
	o.mu.Lock()
	o.closing = true
	o.mu.Unlock()
	o.signal()
	select {
	case <-o.done:
	case <-time.After(time.Until(deadline)):
		o.logf("stdout flush timed out")
	}
}

func (o *output) run() {
	defer close(o.done)
	var timer *time.Timer
	for {
		o.mu.Lock()
		var line []byte
		isState := false
		switch {
		case len(o.queue) > 0:
			line = o.queue[0]
			o.queue[0] = nil
			o.queue = o.queue[1:]
		case o.state != nil:
			if wait := o.interval - time.Since(o.lastState); wait > 0 && !o.closing {
				o.mu.Unlock()
				if timer == nil {
					timer = time.NewTimer(wait)
				} else {
					timer.Reset(wait)
				}
				select {
				case <-o.wake:
				case <-timer.C:
				}
				continue
			}
			line, o.state, isState = o.state, nil, true
		case o.closing:
			o.mu.Unlock()
			return
		}
		if line == nil {
			o.mu.Unlock()
			<-o.wake
			continue
		}
		broken := o.broken
		o.mu.Unlock()
		if isState || isStateLine(line) {
			o.mu.Lock()
			o.lastState = time.Now()
			o.mu.Unlock()
		}
		if broken {
			continue
		}
		if err := o.w.WriteLine(line); err != nil {
			o.mu.Lock()
			o.broken = true
			o.mu.Unlock()
			o.logf("stdout write failed, dropping further output: %v", err)
		}
	}
}

// isStateLine tells whether a queued line is a state (moved into the FIFO by send).
func isStateLine(b []byte) bool {
	const prefix = `{"type":"state"`
	return len(b) >= len(prefix) && string(b[:len(prefix)]) == prefix
}
