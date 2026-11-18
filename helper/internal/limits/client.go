package limits

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the Anthropic API origin.
const DefaultBaseURL = "https://api.anthropic.com"

const (
	usagePath      = "/api/oauth/usage"
	betaHeader     = "oauth-2025-04-20"
	requestTimeout = 15 * time.Second
	maxBody        = 4 << 20
	maxRetryAfter  = 3600
)

// Client fetches the official usage limits. The zero value is not usable: set CredentialsPath.
type Client struct {
	// CredentialsPath is the credentials file, re-read on every Fetch (see DefaultCredentialsPath).
	CredentialsPath string
	// BaseURL overrides DefaultBaseURL (tests).
	BaseURL string
	// Version goes into the User-Agent: poketokenbar-gnome/<Version>.
	Version string
	// HTTPClient overrides the default client (15 s timeout). Tests only.
	HTTPClient *http.Client
	// Now overrides time.Now (tests).
	Now func() time.Time
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Fetch reads the credentials, calls the usage endpoint and returns the decoded status with the
// plan fields filled in. It never refreshes the token. On 401/403 it re-reads the credentials file
// and retries once only if the token changed. Every error is an *Error. Fetch does not consult any
// backoff: callers skip automatic polls while Backoff.Active and call Fetch directly for a manual
// refresh.
func (c *Client) Fetch(ctx context.Context) (*Status, error) {
	cred, err := c.readValid()
	if err != nil {
		return nil, err
	}
	status, err := c.get(ctx, cred.AccessToken)
	if err != nil {
		var e *Error
		if !errors.As(err, &e) || e.Code != CodeUnauthorized {
			return nil, err
		}
		again, rerr := c.readValid()
		if rerr != nil {
			return nil, rerr
		}
		if again.AccessToken == cred.AccessToken {
			return nil, err
		}
		cred = again
		if status, err = c.get(ctx, cred.AccessToken); err != nil {
			return nil, err
		}
	}
	status.SubscriptionType = cred.SubscriptionType
	status.RateLimitTier = cred.RateLimitTier
	return status, nil
}

// readValid reads the credentials and rejects an expired token.
func (c *Client) readValid() (Credentials, error) {
	cred, err := ReadCredentials(c.CredentialsPath)
	if err != nil {
		return Credentials{}, err
	}
	if cred.Expired(c.now()) {
		return Credentials{}, &Error{Code: CodeTokenExpired}
	}
	return cred, nil
}

func (c *Client) get(ctx context.Context, token string) (*Status, error) {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+usagePath, nil)
	if err != nil {
		return nil, &Error{Code: CodeNetwork, Err: errors.New("bad request URL")}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", betaHeader)
	req.Header.Set("User-Agent", "poketokenbar-gnome/"+c.Version)
	req.Header.Set("Accept", "application/json")

	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: requestTimeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		// Keep only the cause class; the *url.Error text carries the URL, never headers, but be strict.
		var cause error = errors.New("request failed")
		if errors.Is(err, context.DeadlineExceeded) {
			cause = errors.New("timeout")
		} else if errors.Is(err, context.Canceled) {
			cause = errors.New("canceled")
		}
		return nil, &Error{Code: CodeNetwork, Err: cause}
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, &Error{Code: CodeUnauthorized, HTTPStatus: resp.StatusCode}
	case http.StatusTooManyRequests:
		return nil, &Error{Code: CodeRateLimited, HTTPStatus: resp.StatusCode,
			RetryAfter: ParseRetryAfter(resp.Header.Get("Retry-After"))}
	default:
		return nil, &Error{Code: CodeHTTPStatus, HTTPStatus: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, &Error{Code: CodeNetwork, Err: errors.New("reading body failed")}
	}
	return ParseStatus(body)
}

// ParseRetryAfter parses a Retry-After header in seconds only. It returns 0 for an empty,
// non-numeric, HTTP-date or non-positive value, and caps the result at 3600 s.
func ParseRetryAfter(h string) time.Duration {
	secs, err := strconv.ParseFloat(strings.Trim(h, " \t"), 64)
	if err != nil || !(secs > 0) {
		return 0
	}
	return time.Duration(min(secs, maxRetryAfter) * float64(time.Second))
}
