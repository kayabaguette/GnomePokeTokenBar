// Package claude is the Claude Code usage provider: it reads the local JSONL transcripts under
// the Claude projects roots (docs/spec/usage.md §1-§2).
package claude

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Env carries the inputs of root discovery, so tests stay hermetic.
type Env struct {
	Home          string
	ConfigDir     string // CLAUDE_CONFIG_DIR; empty means unset
	XDGConfigHome string // XDG_CONFIG_HOME; empty or relative means unset
}

// EnvFromOS reads Env from the process environment. A helper spawned by gnome-shell inherits
// the systemd user environment, not the login shell's; a shell lookup fallback is not
// implemented in M1.
func EnvFromOS() Env {
	home, _ := os.UserHomeDir()
	return Env{
		Home:          home,
		ConfigDir:     os.Getenv("CLAUDE_CONFIG_DIR"),
		XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
	}
}

// ProjectRoots lists every directory that may hold Claude transcripts, in priority order,
// normalized (LocalUsageReader.swift:101-129):
//  1. each comma-separated CLAUDE_CONFIG_DIR entry + /projects (trimmed, ~ expanded);
//  2. $XDG_CONFIG_HOME/claude/projects, when set (not read upstream);
//  3. ~/.config/claude/projects;
//  4. ~/.claude/projects.
//
// New roots are added here only. Extension point (not in M1): multi-account ~/.claude-*
// roots go after 1, custom scan roots and Claude Desktop roots after 4.
func ProjectRoots(env Env) []string {
	var roots []string
	for _, part := range strings.Split(env.ConfigDir, ",") {
		p := strings.TrimFunc(part, isSwiftWhitespace)
		if p == "" {
			continue
		}
		roots = append(roots, filepath.Join(expandTilde(p, env.Home), "projects"))
	}
	if filepath.IsAbs(env.XDGConfigHome) {
		roots = append(roots, filepath.Join(env.XDGConfigHome, "claude", "projects"))
	}
	if env.Home != "" {
		roots = append(roots,
			filepath.Join(env.Home, ".config", "claude", "projects"),
			filepath.Join(env.Home, ".claude", "projects"))
	}
	return NormalizeRoots(roots)
}

// isSwiftWhitespace matches Foundation's CharacterSet.whitespaces: tab and Unicode Zs, not
// newlines.
func isSwiftWhitespace(r rune) bool { return r == '\t' || unicode.Is(unicode.Zs, r) }

func expandTilde(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// NormalizeRoots resolves symlinks (paths that do not exist are only cleaned), drops exact
// duplicates and roots nested in another root, and keeps the original order
// (LocalUsageReader.swift:266-289). Paths compare case-sensitively, unlike upstream's APFS
// lowercasing. A relative root stays relative to the working directory.
func NormalizeRoots(roots []string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, r := range roots {
		p := filepath.Clean(r)
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		if !seen[p] {
			seen[p] = true
			unique = append(unique, p)
		}
	}
	byLength := append([]string(nil), unique...)
	sort.SliceStable(byLength, func(i, j int) bool { return len(byLength[i]) < len(byLength[j]) })
	kept := map[string]bool{}
	var keptList []string
	for _, p := range byLength {
		nested := false
		for _, k := range keptList {
			if strings.HasPrefix(p, k+"/") {
				nested = true
				break
			}
		}
		if !nested {
			kept[p] = true
			keptList = append(keptList, p)
		}
	}
	var out []string
	for _, p := range unique {
		if kept[p] {
			out = append(out, p)
		}
	}
	return out
}
