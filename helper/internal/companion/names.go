package companion

import (
	"strings"
	"unicode"
)

// languageCode normalizes a locale tag or PokéAPI language name: trimmed, lower case, "_" → "-".
func languageCode(raw string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "_", "-")
}

// nameCandidates is the resolution order of protocol §10 for a locale tag: the exact code, then the
// primary subtag with upstream's preferences (ja → ja-hrkt, ja; pt → pt-br, pt), then en. A bare
// "ja" or "pt" uses the preference list, as upstream's AppLanguage does.
func nameCandidates(tag string) []string {
	tag = languageCode(tag)
	primary, _, _ := strings.Cut(tag, "-")
	prefs := map[string][]string{"ja": {"ja-hrkt", "ja"}, "pt": {"pt-br", "pt"}}[primary]
	if prefs == nil {
		prefs = []string{primary}
	}
	out := prefs
	if tag != primary {
		out = append([]string{tag}, prefs...)
	}
	return append(out, "en")
}

// resolveName picks a localized name; ok is false when no candidate language is present.
func resolveName(names map[string]string, tag string) (string, bool) {
	if len(names) == 0 {
		return "", false
	}
	normalized := make(map[string]string, len(names))
	for k, v := range names {
		if v = strings.TrimSpace(v); v != "" {
			normalized[languageCode(k)] = v
		}
	}
	for _, c := range nameCandidates(tag) {
		if v, ok := normalized[c]; ok {
			return v, true
		}
	}
	return "", false
}

// appLanguage maps a locale tag to upstream's save enum (ko, en, ja, es, fr, pt, de, ru), so the
// persisted `language` stays a valid upstream value.
func appLanguage(tag string) string {
	primary, _, _ := strings.Cut(languageCode(tag), "-")
	switch primary {
	case "ko", "ja", "es", "fr", "pt", "de", "ru":
		return primary
	}
	return "en"
}

// foldTable maps the Latin letters with diacritics found in Pokémon names to their base letters.
var foldTable = func() map[rune]string {
	groups := map[string]string{
		"a": "àáâãäåāăą", "c": "çćĉċč", "d": "ďđ", "e": "èéêëēĕėęě", "g": "ĝğġģ",
		"i": "ìíîïĩīĭįı", "l": "ĺļľł", "n": "ñńņň", "o": "òóôõöøōŏő", "r": "ŕř",
		"s": "śŝşš", "t": "ţť", "u": "ùúûüũūŭůűų", "y": "ýÿŷ", "z": "źżž",
		"ss": "ß", "ae": "æ", "oe": "œ",
	}
	t := map[rune]string{}
	for base, letters := range groups {
		for _, r := range letters {
			t[r] = base
		}
	}
	return t
}()

// fold lowercases s and strips Latin diacritics, for case- and accent-insensitive search.
func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.Is(unicode.Mn, r) { // combining marks of decomposed input
			continue
		}
		if base, ok := foldTable[r]; ok {
			b.WriteString(base)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
