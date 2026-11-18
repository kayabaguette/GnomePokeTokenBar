package limits

import (
	"testing"
	"time"
)

func TestBackoffLadder(t *testing.T) {
	var b Backoff
	if b.Active(testNow) {
		t.Fatal("fresh backoff must be inactive")
	}
	want := []time.Duration{300, 600, 1200, 2400, 3600, 3600, 3600}
	now := testNow
	for i, w := range want {
		got := b.Next(now, 0)
		if got != w*time.Second {
			t.Fatalf("step %d: got %v want %vs", i, got, w)
		}
		if !b.Active(now.Add(got-time.Second)) || b.Active(now.Add(got)) {
			t.Fatalf("step %d: active window wrong", i)
		}
		if !b.Until().Equal(now.Add(got)) {
			t.Fatalf("step %d: until", i)
		}
		now = now.Add(got)
	}
}

func TestBackoffRetryAfterWinsButLadderAdvances(t *testing.T) {
	var b Backoff
	if got := b.Next(testNow, 120*time.Second); got != 120*time.Second {
		t.Fatalf("Retry-After must win: %v", got)
	}
	if got := b.Next(testNow, 0); got != 600*time.Second {
		t.Fatalf("ladder must have advanced past 300: %v", got)
	}
}

func TestBackoffReset(t *testing.T) {
	var b Backoff
	b.Next(testNow, 0)
	b.Next(testNow, 0)
	b.Reset()
	if b.Active(testNow) || !b.Until().IsZero() {
		t.Fatal("reset must clear")
	}
	if got := b.Next(testNow, 0); got != 300*time.Second {
		t.Fatalf("ladder must restart: %v", got)
	}
}
