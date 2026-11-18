package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestProjectRoots(t *testing.T) {
	const home = "/home/testhome"
	cases := []struct {
		name string
		env  Env
		want []string
	}{
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:211 (commas, blanks, tilde, no bare /projects)
		{"config dir list", Env{Home: home, ConfigDir: " /a/one , ,~/two "},
			[]string{"/a/one/projects", home + "/two/projects", home + "/.config/claude/projects", home + "/.claude/projects"}},
		// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:278 (defaults present, unique)
		{"defaults", Env{Home: home},
			[]string{home + "/.config/claude/projects", home + "/.claude/projects"}},
		// Go-only: XDG_CONFIG_HOME adds a root (spec §1.2); the default path folds into it.
		{"xdg", Env{Home: home, XDGConfigHome: "/xdg"},
			[]string{"/xdg/claude/projects", home + "/.config/claude/projects", home + "/.claude/projects"}},
		{"xdg equal to default", Env{Home: home, XDGConfigHome: home + "/.config"},
			[]string{home + "/.config/claude/projects", home + "/.claude/projects"}},
		// Go-only: the XDG spec ignores relative paths.
		{"relative xdg", Env{Home: home, XDGConfigHome: "rel"},
			[]string{home + "/.config/claude/projects", home + "/.claude/projects"}},
		// Go-only: CLAUDE_CONFIG_DIR=~/.claude repeats the default root; it keeps its earlier rank.
		{"config dir is the default", Env{Home: home, ConfigDir: "~/.claude"},
			[]string{home + "/.claude/projects", home + "/.config/claude/projects"}},
		{"bare tilde", Env{Home: home, ConfigDir: "~"},
			[]string{home + "/projects", home + "/.config/claude/projects", home + "/.claude/projects"}},
	}
	for _, c := range cases {
		if got := ProjectRoots(c.env); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v; want %v", c.name, got, c.want)
		}
	}
}

func TestNormalizeRoots(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:240
	got := NormalizeRoots([]string{
		"/Users/x/.claude/projects",
		"/Users/x/.config/claude/projects",
		"/Users/x/.claude/projects",
		"/Users/x/.claude/projects/sub",
		"/Users/x/.claude/projects-other",
	})
	want := []string{"/Users/x/.claude/projects", "/Users/x/.config/claude/projects", "/Users/x/.claude/projects-other"}
	if !slices.Equal(got, want) {
		t.Errorf("nested and duplicate roots: %v; want %v", got, want)
	}

	// Go-only: Linux paths are case-sensitive, unlike upstream's APFS lowercasing.
	if got := NormalizeRoots([]string{"/x/.Claude/projects", "/x/.claude/projects"}); len(got) != 2 {
		t.Errorf("case-different roots folded: %v", got)
	}

	// Swift: Tests/PokeTokenBarTests/LocalUsageReaderTests.swift:227 (symlinked duplicate folds)
	base := t.TempDir()
	real := filepath.Join(base, "real", "projects")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "linked")); err != nil {
		t.Fatal(err)
	}
	if got := NormalizeRoots([]string{real, filepath.Join(base, "linked", "projects")}); len(got) != 1 {
		t.Errorf("symlinked duplicate kept: %v", got)
	}
	// Go-only: the XDG layout where ~/.config/claude links to ~/.claude.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".claude"), filepath.Join(home, ".config", "claude")); err != nil {
		t.Fatal(err)
	}
	if got := ProjectRoots(Env{Home: home}); len(got) != 1 {
		t.Errorf("XDG symlink not folded: %v", fmt.Sprint(got))
	}
}
