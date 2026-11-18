package limits

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCreds(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialsPath(t *testing.T) {
	cases := []struct{ name, dir, want string }{
		{"unset", "", "/h/.claude/.credentials.json"},
		{"single", "/cfg", "/cfg/.credentials.json"},
		{"first of comma list", " /a , /b", "/a/.credentials.json"},
		{"skips empty pieces", " , /b", "/b/.credentials.json"},
		{"only separators is unset", " , ,", "/h/.claude/.credentials.json"},
		{"tilde", "~/cfg", "/h/cfg/.credentials.json"},
	}
	for _, c := range cases {
		if got := credentialsPath(c.dir, "/h"); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestParseCredentials(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCode string
	}{
		{"valid", `{"claudeAiOauth":{"accessToken":"t","expiresAt":1800000000000}}`, ""},
		{"not json", `nope`, CodeCredentialsMissing},
		{"no claudeAiOauth (mcpOAuth only)", `{"mcpOAuth":{}}`, CodeAccountOAuthMissing},
		// Swift: Tests/PokeTokenBarTests/CredentialSwitchCacheTests.swift:72 (null is logged out, not "present")
		{"claudeAiOauth null", `{"claudeAiOauth":null}`, CodeAccountOAuthMissing},
		{"claudeAiOauth not object", `{"claudeAiOauth":"x"}`, CodeAccountOAuthMissing},
		{"empty token", `{"claudeAiOauth":{"accessToken":""}}`, CodeCredentialsMissing},
		{"token wrong type", `{"claudeAiOauth":{"accessToken":5}}`, CodeCredentialsMissing},
	}
	for _, c := range cases {
		_, err := parseCredentials([]byte(c.body))
		if got := CodeOf(err); got != c.wantCode {
			t.Errorf("%s: code %q want %q (%v)", c.name, got, c.wantCode, err)
		}
	}
}

func TestReadCredentialsMissingFile(t *testing.T) {
	_, err := ReadCredentials(filepath.Join(t.TempDir(), "nope.json"))
	if CodeOf(err) != CodeCredentialsMissing || AuthState(err) != "missing" {
		t.Fatalf("got %v", err)
	}
}

func TestExpiresAtAndExpired(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name        string
		raw         string
		wantUnix    int64 // 0 = zero time
		wantExpired bool
	}{
		{"seconds number", `1800001000`, 1800001000, false},
		{"milliseconds number", `1800001000000`, 1800001000, false},
		{"seconds string", `"1800001000"`, 1800001000, false},
		{"milliseconds string", `"1800001000000"`, 1800001000, false},
		{"fractional seconds", `1800001000.5`, 1800001000, false},
		{"in the past", `1700000000`, 1700000000, true},
		{"exactly now+60s is expired", `1800000060`, 1800000060, true},
		{"now+61s is valid", `1800000061`, 1800000061, false},
		{"missing means never expired", ``, 0, false},
		{"garbage string", `"abc"`, 0, false},
		{"zero", `0`, 0, false},
		{"negative", `-5`, 0, false},
		{"null", `null`, 0, false},
	}
	for _, c := range cases {
		got := parseExpiresAt([]byte(c.raw))
		if c.wantUnix == 0 {
			if !got.IsZero() {
				t.Errorf("%s: want zero time, got %v", c.name, got)
			}
		} else if got.Unix() != c.wantUnix {
			t.Errorf("%s: got %d want %d", c.name, got.Unix(), c.wantUnix)
		}
		if exp := (Credentials{ExpiresAt: got}).Expired(now); exp != c.wantExpired {
			t.Errorf("%s: expired %v want %v", c.name, exp, c.wantExpired)
		}
	}
}

// Swift: Tests/PokeTokenBarTests/CredentialSwitchCacheTests.swift:34 (in-place switch: the file is
// re-read on every call, so the new token and plan win).
func TestReadCredentialsRereadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	writeCreds(t, path, `{"claudeAiOauth":{"accessToken":"A","subscriptionType":"max"}}`)
	first, err := ReadCredentials(path)
	if err != nil || first.AccessToken != "A" || first.SubscriptionType != "max" {
		t.Fatalf("first: %+v %v", first, err)
	}
	writeCreds(t, path, `{"claudeAiOauth":{"accessToken":"B","subscriptionType":"team"}}`)
	second, err := ReadCredentials(path)
	if err != nil || second.AccessToken != "B" || second.SubscriptionType != "team" {
		t.Fatalf("second: %+v %v", second, err)
	}
}

func TestErrorMessageFormat(t *testing.T) {
	e := &Error{Code: CodeRateLimited, HTTPStatus: 429, Err: errors.New("cause")}
	if got := e.Error(); got != "limits: rateLimited (HTTP 429): cause" {
		t.Fatal(got)
	}
}

func TestAuthState(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, "ok"},
		{&Error{Code: CodeCredentialsMissing}, "missing"},
		{&Error{Code: CodeAccountOAuthMissing}, "missing"},
		{&Error{Code: CodeTokenExpired}, "expired"},
		{&Error{Code: CodeUnauthorized}, "expired"},
		{&Error{Code: CodeRateLimited}, "error"},
		{&Error{Code: CodeNetwork}, "error"},
		{&Error{Code: CodeHTTPStatus}, "error"},
		{&Error{Code: CodeDecode}, "error"},
		{errors.New("other"), "error"},
	}
	for _, c := range cases {
		if got := AuthState(c.err); got != c.want {
			t.Errorf("%v: got %s want %s", c.err, got, c.want)
		}
	}
}

func TestDefaultCredentialsPathReadsEnv(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/x/cfg,/y")
	if got := DefaultCredentialsPath(); got != "/x/cfg/.credentials.json" {
		t.Fatal(got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "/h")
	if got := DefaultCredentialsPath(); got != "/h/.claude/.credentials.json" {
		t.Fatal(got)
	}
}
