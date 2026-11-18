package daemon

import (
	"encoding/json"
	"errors"
	"time"

	"poketokenbar/helper/internal/limits"
)

// providerLimits is the official-limits state of one provider (protocol §5.3).
type providerLimits struct {
	status    *limits.Status // last successful fetch, kept across failures
	updatedAt time.Time
	errCode   string // last failure, "" after a success or before any attempt
	httpCode  int
	backoff   limits.Backoff
}

// authState maps the last failure to the protocol AuthState.
func (l *providerLimits) authState() string {
	if l.errCode == "" {
		return "ok"
	}
	return limits.AuthState(&limits.Error{Code: l.errCode})
}

// applyResult records one fetch (usage.md §6.4): a success replaces the status and clears the
// error and the backoff; a failure keeps the previous status and starts or extends the 429 backoff.
func (l *providerLimits) applyResult(st *limits.Status, err error, now time.Time) {
	if err == nil {
		l.status, l.updatedAt, l.errCode, l.httpCode = st, now, "", 0
		l.backoff.Reset()
		return
	}
	var e *limits.Error
	if errors.As(err, &e) {
		l.errCode, l.httpCode = e.Code, e.HTTPStatus
		if e.Code == limits.CodeRateLimited {
			l.backoff.Next(now, e.RetryAfter)
		}
		return
	}
	l.errCode, l.httpCode = limits.CodeNetwork, 0
}

// persistedLimits is a provider's limits inputs in last-snapshot.json. Status re-encodes through its
// JSON tags; the plan fields come from the credentials and are stored beside it.
type persistedLimits struct {
	Status           json.RawMessage `json:"status,omitempty"`
	SubscriptionType string          `json:"subscriptionType,omitempty"`
	RateLimitTier    string          `json:"rateLimitTier,omitempty"`
	UpdatedAt        *time.Time      `json:"updatedAt,omitempty"`
	ErrorCode        string          `json:"errorCode,omitempty"`
	HTTPStatus       int             `json:"httpStatus,omitempty"`
	BackoffUntil     *time.Time      `json:"backoffUntil,omitempty"`
}

func (l *providerLimits) persist() persistedLimits {
	p := persistedLimits{ErrorCode: l.errCode, HTTPStatus: l.httpCode}
	if l.status != nil {
		if b, err := json.Marshal(l.status); err == nil {
			p.Status = b
		}
		p.SubscriptionType, p.RateLimitTier = l.status.SubscriptionType, l.status.RateLimitTier
	}
	if !l.updatedAt.IsZero() {
		t := l.updatedAt
		p.UpdatedAt = &t
	}
	if u := l.backoff.Until(); !u.IsZero() {
		p.BackoffUntil = &u
	}
	return p
}

// restore loads persisted inputs. A backoff still running is restored with its end, so a restart
// on every screen unlock does not hit a rate-limited endpoint again.
func (l *providerLimits) restore(p persistedLimits, now time.Time) {
	if len(p.Status) > 0 {
		if st, err := limits.ParseStatus(p.Status); err == nil {
			st.SubscriptionType, st.RateLimitTier = p.SubscriptionType, p.RateLimitTier
			l.status = st
		}
	}
	if p.UpdatedAt != nil {
		l.updatedAt = *p.UpdatedAt
	}
	l.errCode, l.httpCode = p.ErrorCode, p.HTTPStatus
	if p.BackoffUntil != nil && p.BackoffUntil.After(now) {
		l.backoff.Next(now, p.BackoffUntil.Sub(now))
	}
}
