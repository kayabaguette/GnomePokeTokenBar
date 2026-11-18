package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func credPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".credentials.json")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude", ".credentials.json")
}

func main() {
	b, err := os.ReadFile(credPath())
	if err != nil {
		fmt.Println("cannot read credentials file")
		os.Exit(1)
	}
	var c struct {
		O struct {
			AccessToken string          `json:"accessToken"`
			ExpiresAt   json.RawMessage `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(b, &c) != nil || c.O.AccessToken == "" {
		fmt.Println("no claudeAiOauth.accessToken")
		os.Exit(1)
	}
	exp, ok := parseExpiresAt(c.O.ExpiresAt)
	if !ok {
		fmt.Println("unparseable expiresAt")
		os.Exit(1)
	}
	if !exp.After(time.Now().Add(60 * time.Second)) {
		fmt.Println("token expired — skipping request")
		return
	}
	token := c.O.AccessToken

	req, _ := http.NewRequest("GET", "https://api.anthropic.com/api/oauth/usage", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "poketokenbar-gnome/0.0.1")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		fmt.Println("request failed (network error)")
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	fmt.Println("HTTP status:", resp.StatusCode)
	fmt.Println("Content-Type:", resp.Header.Get("Content-Type"))
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		fmt.Println("Retry-After:", ra)
	}
	if resp.StatusCode != 200 {
		if strings.Contains(string(body), token) {
			fmt.Println("[body withheld]")
		} else {
			s := string(body)
			if len(s) > 200 {
				s = s[:200]
			}
			fmt.Println(s)
		}
		return
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		fmt.Println("body not JSON object")
		return
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println("keys:", strings.Join(keys, ", "))
	var ls LimitStatus
	if err := json.Unmarshal(body, &ls); err != nil {
		fmt.Println("decode error:", err)
		return
	}
	for _, l := range describe(ls) {
		fmt.Println(l)
	}
}
