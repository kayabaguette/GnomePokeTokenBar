package limits

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// expirySkew is the safety margin: a token expiring within it counts as expired.
const expirySkew = 60 * time.Second

// Credentials is the subset of the Claude Code credentials file this package uses.
type Credentials struct {
	AccessToken      string
	ExpiresAt        time.Time // zero when the file carries no usable expiry (treated as not expired)
	SubscriptionType string
	RateLimitTier    string
}

// Expired reports whether the token expires at or before now + 60 s.
func (c Credentials) Expired(now time.Time) bool {
	return !c.ExpiresAt.IsZero() && !c.ExpiresAt.After(now.Add(expirySkew))
}

// DefaultCredentialsPath returns $CLAUDE_CONFIG_DIR/.credentials.json (first entry when the value is
// comma-separated, "~" expanded; an empty value counts as unset), else ~/.claude/.credentials.json.
func DefaultCredentialsPath() string {
	home, _ := os.UserHomeDir()
	return credentialsPath(os.Getenv("CLAUDE_CONFIG_DIR"), home)
}

func credentialsPath(configDir, home string) string {
	for _, part := range strings.Split(configDir, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "~" || strings.HasPrefix(part, "~/") {
			part = filepath.Join(home, part[1:])
		}
		return filepath.Join(part, ".credentials.json")
	}
	return filepath.Join(home, ".claude", ".credentials.json")
}

// ReadCredentials reads and validates the credentials file at path. It does not check expiry.
// Errors are *Error with CodeCredentialsMissing or CodeAccountOAuthMissing.
func ReadCredentials(path string) (Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		// Do not wrap err: *PathError is harmless but the cause adds nothing for the UI.
		if errors.Is(err, fs.ErrNotExist) {
			return Credentials{}, &Error{Code: CodeCredentialsMissing}
		}
		return Credentials{}, &Error{Code: CodeCredentialsMissing, Err: errors.New("unreadable credentials file")}
	}
	return parseCredentials(data)
}

func parseCredentials(data []byte) (Credentials, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return Credentials{}, &Error{Code: CodeCredentialsMissing, Err: errors.New("credentials file is not a JSON object")}
	}
	// A JSON null is not "present": claudeAiOauth must decode as an object (logged out otherwise).
	var oauth map[string]json.RawMessage
	if raw, ok := root["claudeAiOauth"]; !ok || json.Unmarshal(raw, &oauth) != nil || oauth == nil {
		return Credentials{}, &Error{Code: CodeAccountOAuthMissing}
	}
	var c Credentials
	_ = json.Unmarshal(oauth["accessToken"], &c.AccessToken)
	if c.AccessToken == "" {
		return Credentials{}, &Error{Code: CodeCredentialsMissing, Err: errors.New("no access token")}
	}
	_ = json.Unmarshal(oauth["subscriptionType"], &c.SubscriptionType)
	_ = json.Unmarshal(oauth["rateLimitTier"], &c.RateLimitTier)
	c.ExpiresAt = parseExpiresAt(oauth["expiresAt"])
	return c, nil
}

// parseExpiresAt accepts a number or a numeric string; values > 1e10 are milliseconds. A missing,
// malformed or non-positive value yields the zero time (Swift: expiresAt nil, never expired).
func parseExpiresAt(raw json.RawMessage) time.Time {
	if len(raw) == 0 {
		return time.Time{}
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return time.Time{}
		}
		if f, err = strconv.ParseFloat(s, 64); err != nil {
			return time.Time{}
		}
	}
	if !(f > 0) || f > 1e18 {
		return time.Time{}
	}
	if f > 1e10 {
		f /= 1000
	}
	sec, frac := int64(f), f-float64(int64(f))
	return time.Unix(sec, int64(frac*1e9))
}
