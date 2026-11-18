package limits

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const usageJSON = `{"five_hour":{"utilization":7,"resets_at":"2026-08-21T17:20:00.186770+00:00"},
"seven_day":{"utilization":30,"resets_at":"2026-08-25T23:00:00.186790+00:00"},"extra_key":{"x":1}}`

func tokenCreds(token string, extra string) string {
	return `{"claudeAiOauth":{"accessToken":"` + token + `"` + extra + `}}`
}

// fixture builds a client against an httptest server running handler.
func fixture(t *testing.T, creds string, handler http.HandlerFunc) (*Client, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	path := filepath.Join(t.TempDir(), ".credentials.json")
	writeCreds(t, path, creds)
	return &Client{
		CredentialsPath: path, BaseURL: srv.URL, Version: "9.9.9",
		Now: func() time.Time { return time.Unix(1_800_000_000, 0) },
	}, &hits
}

func okHandler(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte(usageJSON))
}

func TestFetchSuccessHeadersAndPlan(t *testing.T) {
	var got http.Header
	var gotPath string
	c, hits := fixture(t,
		tokenCreds("tok-1", `,"expiresAt":1800009999000,"subscriptionType":"max","rateLimitTier":"default_claude_max_20x"`),
		func(w http.ResponseWriter, r *http.Request) {
			got, gotPath = r.Header.Clone(), r.URL.Path
			okHandler(w, r)
		})
	s, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 || gotPath != "/api/oauth/usage" {
		t.Fatalf("hits=%d path=%s", hits.Load(), gotPath)
	}
	for k, v := range map[string]string{
		"Authorization":  "Bearer tok-1",
		"Anthropic-Beta": "oauth-2025-04-20",
		"User-Agent":     "poketokenbar-gnome/9.9.9",
		"Accept":         "application/json",
	} {
		if got.Get(k) != v {
			t.Errorf("header %s = %q want %q", k, got.Get(k), v)
		}
	}
	if s.FiveHour == nil || *s.FiveHour.Utilization != 7 || s.Plan() != "Ultra" {
		t.Fatalf("status: %+v plan=%q", s.FiveHour, s.Plan())
	}
}

func TestFetchCredentialErrorsMakeNoRequest(t *testing.T) {
	cases := []struct{ name, creds, want string }{
		{"missing oauth", `{"mcpOAuth":{}}`, CodeAccountOAuthMissing},
		{"expired", tokenCreds("t", `,"expiresAt":1800000030`), CodeTokenExpired},
	}
	for _, tc := range cases {
		c, hits := fixture(t, tc.creds, okHandler)
		_, err := c.Fetch(context.Background())
		if CodeOf(err) != tc.want || hits.Load() != 0 {
			t.Errorf("%s: code=%q hits=%d err=%v", tc.name, CodeOf(err), hits.Load(), err)
		}
	}
	c, hits := fixture(t, `{}`, okHandler)
	c.CredentialsPath += ".absent"
	if _, err := c.Fetch(context.Background()); CodeOf(err) != CodeCredentialsMissing || hits.Load() != 0 {
		t.Fatalf("absent file: %v", err)
	}
}

func TestFetch401RetryOnlyWhenTokenChanged(t *testing.T) {
	// Swift: Sources/PokeTokenBar/Core/OAuthLimitsProvider.swift:58 (retry once only if the token changed)
	for _, status := range []int{401, 403} {
		t.Run(fmt.Sprintf("%d token changed -> retry succeeds", status), func(t *testing.T) {
			var path string
			c, hits := fixture(t, tokenCreds("old", ""), func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "Bearer old" {
					// Claude Code rewrites the credentials while we are being rejected.
					writeCreds(t, path, tokenCreds("new", `,"subscriptionType":"pro"`))
					w.WriteHeader(status)
					return
				}
				okHandler(w, r)
			})
			path = c.CredentialsPath
			s, err := c.Fetch(context.Background())
			if err != nil || hits.Load() != 2 {
				t.Fatalf("err=%v hits=%d", err, hits.Load())
			}
			if s.Plan() != "Pro" {
				t.Fatalf("plan from the new credentials expected, got %q", s.Plan())
			}
		})
		t.Run(fmt.Sprintf("%d same token -> no retry", status), func(t *testing.T) {
			c, hits := fixture(t, tokenCreds("same", ""), func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			})
			_, err := c.Fetch(context.Background())
			var e *Error
			if !errors.As(err, &e) || e.Code != CodeUnauthorized || e.HTTPStatus != status || hits.Load() != 1 {
				t.Fatalf("err=%v hits=%d", err, hits.Load())
			}
		})
	}
}

func TestFetch401ThenChangedTokenStillRejected(t *testing.T) {
	var path string
	var n atomic.Int32
	c, hits := fixture(t, tokenCreds("old", ""), func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			writeCreds(t, path, tokenCreds("new", ""))
		}
		w.WriteHeader(401)
	})
	path = c.CredentialsPath
	_, err := c.Fetch(context.Background())
	if CodeOf(err) != CodeUnauthorized || hits.Load() != 2 {
		t.Fatalf("err=%v hits=%d (retry must happen once, not loop)", err, hits.Load())
	}
}

func TestFetch401ThenCredentialsGone(t *testing.T) {
	var path string
	c, _ := fixture(t, tokenCreds("old", ""), func(w http.ResponseWriter, _ *http.Request) {
		writeCreds(t, path, `{"claudeAiOauth":null}`)
		w.WriteHeader(401)
	})
	path = c.CredentialsPath
	if _, err := c.Fetch(context.Background()); CodeOf(err) != CodeAccountOAuthMissing {
		t.Fatalf("got %v", err)
	}
}

func TestFetchHTTPErrors(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		retryAfter string
		body       string
		wantCode   string
		wantRetry  time.Duration
	}{
		{"429 with seconds", 429, "120", "", CodeRateLimited, 120 * time.Second},
		{"429 without header", 429, "", "", CodeRateLimited, 0},
		{"429 capped", 429, "99999", "", CodeRateLimited, time.Hour},
		{"429 HTTP-date ignored", 429, "Wed, 21 Oct 2026 07:28:00 GMT", "", CodeRateLimited, 0},
		{"429 zero ignored", 429, "0", "", CodeRateLimited, 0},
		{"500", 500, "", "", CodeHTTPStatus, 0},
		{"404", 404, "", "", CodeHTTPStatus, 0},
		{"200 bad body", 200, "", "<html>", CodeDecode, 0},
	}
	for _, tc := range cases {
		c, _ := fixture(t, tokenCreds("t", ""), func(w http.ResponseWriter, _ *http.Request) {
			if tc.retryAfter != "" {
				w.Header().Set("Retry-After", tc.retryAfter)
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		_, err := c.Fetch(context.Background())
		var e *Error
		if !errors.As(err, &e) || e.Code != tc.wantCode || e.RetryAfter != tc.wantRetry {
			t.Errorf("%s: %v (retry %v)", tc.name, err, e)
		}
		if tc.status != 200 && e != nil && e.HTTPStatus != tc.status {
			t.Errorf("%s: HTTPStatus %d", tc.name, e.HTTPStatus)
		}
	}
}

func TestFetchNetworkError(t *testing.T) {
	c, _ := fixture(t, tokenCreds("secret-token-value", ""), okHandler)
	c.BaseURL = "http://127.0.0.1:1" // nothing listens
	_, err := c.Fetch(context.Background())
	if CodeOf(err) != CodeNetwork {
		t.Fatalf("got %v", err)
	}
	if msg := err.Error(); len(msg) == 0 || strings.Contains(msg, "secret-token-value") {
		t.Fatalf("token leaked: %q", msg)
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"30", 30 * time.Second},
		{" 30 ", 30 * time.Second},
		{"1.5", 1500 * time.Millisecond},
		{"3600", time.Hour},
		{"3601", time.Hour},
		{"inf", time.Hour}, // Swift: TimeInterval("inf") > 0, capped
		{"", 0},
		{"0", 0},
		{"-4", 0},
		{"nan", 0},
		{"abc", 0},
	}
	for _, c := range cases {
		if got := ParseRetryAfter(c.in); got != c.want {
			t.Errorf("%q: got %v want %v", c.in, got, c.want)
		}
	}
}

func TestFetchCanceledContextIsNetworkError(t *testing.T) {
	c, hits := fixture(t, tokenCreds("t", ""), okHandler)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Fetch(ctx)
	if CodeOf(err) != CodeNetwork || hits.Load() != 0 || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("got %v hits=%d", err, hits.Load())
	}
}
