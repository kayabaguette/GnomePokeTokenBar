// Package usage is the provider-generic usage core: entries, cost, time bucketing, the active
// 5-hour block, the daily history ledger and an incremental per-file parse cache.
//
// Ported from the Swift app (Sources/PokeTokenBar/Core/LocalUsageReader.swift and friends);
// docs/spec/usage.md is the porting spec.
package usage

import "time"

// Entry is one model request read from a usage log, after deduplication.
type Entry struct {
	// ID identifies the request across files (Claude: message.id + "|" + requestId).
	ID string `json:"id"`
	// Date is the request time.
	Date time.Time `json:"date"`
	// LocalDay is Date as yyyy-MM-dd in the zone used at parse time.
	LocalDay   string `json:"day"`
	Model      string `json:"model"`
	Input      int64  `json:"in"`
	Output     int64  `json:"out"`
	CacheWrite int64  `json:"cw"`
	CacheRead  int64  `json:"cr"`
	// Sessions lists every session that logged this request, sorted. Treat it as read-only:
	// entries from one file may share the slice.
	Sessions []string `json:"sessions,omitempty"`
}

// Total is input + output + cache write + cache read.
func (e Entry) Total() int64 { return e.Input + e.Output + e.CacheWrite + e.CacheRead }
