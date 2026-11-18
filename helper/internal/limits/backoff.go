package limits

import "time"

// Backoff ladder bounds for 429 responses.
const (
	backoffFirst = 300 * time.Second
	backoffMax   = 3600 * time.Second
)

// Backoff tracks the 429 backoff: 300, 600, 1200, 2400, then 3600 s. The delay is the server's
// Retry-After when given, otherwise the ladder value; the ladder advances either way. A manual
// refresh bypasses the backoff: the caller simply skips Active and still calls Next or Reset with the
// result. Not safe for concurrent use.
type Backoff struct {
	interval time.Duration
	until    time.Time
}

// Active reports whether automatic polls should be skipped at now.
func (b *Backoff) Active(now time.Time) bool { return now.Before(b.until) }

// Until is the end of the current backoff (zero when none), for protocol backoffUntil.
func (b *Backoff) Until() time.Time { return b.until }

// Next records a 429 at now and returns the delay applied. retryAfter <= 0 means no Retry-After.
func (b *Backoff) Next(now time.Time, retryAfter time.Duration) time.Duration {
	if b.interval == 0 {
		b.interval = backoffFirst
	} else {
		b.interval = min(b.interval*2, backoffMax)
	}
	delay := b.interval
	if retryAfter > 0 {
		delay = retryAfter
	}
	b.until = now.Add(delay)
	return delay
}

// Reset clears the backoff after a success.
func (b *Backoff) Reset() { *b = Backoff{} }
