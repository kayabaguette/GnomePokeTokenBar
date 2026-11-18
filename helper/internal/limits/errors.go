// Package limits implements the official Claude usage limits: a read-only client for the OAuth usage
// endpoint, plus the pure presentation, pace and alert logic built on its response.
//
// The package NEVER refreshes the OAuth token. Refreshing rotates the refresh token and logs Claude
// Code out, so the credentials file is only ever re-read. Token values never appear in errors.
package limits

import (
	"errors"
	"fmt"
	"time"
)

// Error codes, identical to the protocol's limits error codes (protocol.md §11.4).
const (
	CodeCredentialsMissing  = "credentialsMissing"  // no readable credentials file
	CodeAccountOAuthMissing = "accountOAuthMissing" // file present, claudeAiOauth absent or null
	CodeTokenExpired        = "tokenExpired"        // expiresAt <= now + 60 s
	CodeUnauthorized        = "unauthorized"        // 401/403 after the re-read retry
	CodeRateLimited         = "rateLimited"         // 429
	CodeNetwork             = "network"             // DNS, TLS, timeout
	CodeHTTPStatus          = "httpStatus"          // any other non-200
	CodeDecode              = "decode"              // unexpected response body
)

// Error is the single error type returned by this package's fetch path.
type Error struct {
	// Code is one of the Code* constants.
	Code string
	// HTTPStatus is the response status for unauthorized, rateLimited and httpStatus; otherwise 0.
	HTTPStatus int
	// RetryAfter is the server's Retry-After for rateLimited (seconds only, > 0, capped at 3600 s).
	// Zero means the header was absent or unusable.
	RetryAfter time.Duration
	// Err is an optional cause. It never contains a token.
	Err error
}

func (e *Error) Error() string {
	s := "limits: " + e.Code
	if e.HTTPStatus != 0 {
		s += fmt.Sprintf(" (HTTP %d)", e.HTTPStatus)
	}
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

// Unwrap returns the cause, if any.
func (e *Error) Unwrap() error { return e.Err }

// CodeOf returns the Code of err, or "" if err is not (or does not wrap) an *Error.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// AuthState maps an error code to the protocol AuthState ("ok" for a nil error).
func AuthState(err error) string {
	switch CodeOf(err) {
	case "":
		if err == nil {
			return "ok"
		}
		return "error"
	case CodeCredentialsMissing, CodeAccountOAuthMissing:
		return "missing"
	case CodeTokenExpired, CodeUnauthorized:
		return "expired"
	default:
		return "error"
	}
}
