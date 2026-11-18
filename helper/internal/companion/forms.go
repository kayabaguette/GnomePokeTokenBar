package companion

import (
	"sort"
	"strconv"
	"strings"
)

// Form catalog (upstream PokemonForm.swift). A form belongs to the individual and is resolved per
// species of its line: species ids and evolution paths never change, only sprites, names and
// collection slots do.

const unownSpeciesID = 201

type formEntry struct {
	form      string
	formID    int // PokéAPI pokemon-form id holding the localized form names
	pokemonID int // PokéAPI variety whose static PNG holds this form; 0 = named like the GIF
}

func fe(form string, formID int, pokemonID ...int) formEntry {
	e := formEntry{form: form, formID: formID}
	if len(pokemonID) > 0 {
		e.pokemonID = pokemonID[0]
	}
	return e
}

// unownLetters lists Unown's 28 forms in catalog order.
var unownLetters = func() []string {
	out := make([]string, 0, 28)
	for c := 'a'; c <= 'z'; c++ {
		out = append(out, string(c))
	}
	return append(out, "exclamation", "question")
}()

// formCatalog lists every species with forms, default first (PF:45-89).
var formCatalog = func() map[int][]formEntry {
	unown := make([]formEntry, len(unownLetters))
	for i, l := range unownLetters {
		id := 10000 + i
		if i == 0 {
			id = unownSpeciesID
		}
		unown[i] = fe(l, id)
	}
	return map[int][]formEntry{
		unownSpeciesID: unown,
		351:            {fe("normal", 351), fe("sunny", 10028, 10013), fe("rainy", 10029, 10014), fe("snowy", 10030, 10015)},
		386:            {fe("normal", 386), fe("attack", 10031, 10001), fe("defense", 10032, 10002), fe("speed", 10033, 10003)},
		412:            {fe("plant", 412), fe("sandy", 10034), fe("trash", 10035)},
		413:            {fe("plant", 413), fe("sandy", 10036, 10004), fe("trash", 10037, 10005)},
		421:            {fe("overcast", 421), fe("sunshine", 10038)},
		422:            {fe("west", 422), fe("east", 10039)},
		423:            {fe("west", 423), fe("east", 10040)},
		479: {fe("normal", 479), fe("heat", 10058, 10008), fe("wash", 10059, 10009), fe("frost", 10060, 10010),
			fe("fan", 10061, 10011), fe("mow", 10062, 10012)},
		487: {fe("altered", 487), fe("origin", 10063, 10007)},
		492: {fe("land", 492), fe("sky", 10064, 10006)},
		493: {fe("normal", 493), fe("fighting", 10045), fe("flying", 10047), fe("poison", 10052), fe("ground", 10050),
			fe("rock", 10054), fe("bug", 10041), fe("ghost", 10048), fe("steel", 10055), fe("fire", 10046),
			fe("water", 10056), fe("grass", 10049), fe("electric", 10044), fe("psychic", 10053), fe("ice", 10051),
			fe("dragon", 10043), fe("dark", 10042), fe("fairy", 10085)},
		550: {fe("red-striped", 550), fe("blue-striped", 10066, 10016)},
		555: {fe("standard", 555), fe("zen", 10067, 10017)},
		585: {fe("spring", 585), fe("summer", 10068), fe("autumn", 10069), fe("winter", 10070)},
		586: {fe("spring", 586), fe("summer", 10071), fe("autumn", 10072), fe("winter", 10073)},
		641: {fe("incarnate", 641), fe("therian", 10079, 10019)},
		642: {fe("incarnate", 642), fe("therian", 10080, 10020)},
		645: {fe("incarnate", 645), fe("therian", 10081, 10021)},
		646: {fe("normal", 646), fe("black", 10082, 10022), fe("white", 10083, 10023)},
		647: {fe("ordinary", 647), fe("resolute", 10084, 10024)},
		648: {fe("aria", 648), fe("pirouette", 10074, 10018)},
		649: {fe("normal", 649), fe("douse", 10075), fe("shock", 10076), fe("burn", 10077), fe("chill", 10078)},
	}
}()

// lineBaseIDs maps catalogued species whose line starts elsewhere to that base (PF:93).
var lineBaseIDs = map[int]int{413: 412, 421: 420, 423: 422, 555: 554, 586: 585}

// unnamedDefaultSpecies have no official name for their usual look (PF:208).
var unnamedDefaultSpecies = map[int]bool{351: true, 479: true, 646: true, 649: true}

// formsOf returns a species' forms, default first; nil when it has a single appearance.
func formsOf(speciesID int) []string {
	entries := formCatalog[speciesID]
	if entries == nil {
		return nil
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.form
	}
	return out
}

func hasForms(speciesID int) bool { return formCatalog[speciesID] != nil }

// formSpecies lists the species of a line that change appearance with the form, in id order.
func formSpecies(baseID int) []int {
	var out []int
	for id := range formCatalog {
		b, ok := lineBaseIDs[id]
		if !ok {
			b = id
		}
		if b == baseID {
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

// lineForms lists every form an individual of this line can carry, default first.
func lineForms(baseID int) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range formSpecies(baseID) {
		for _, f := range formsOf(id) {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

func resolveAmong(forms []string, form *string) *string {
	if len(forms) == 0 {
		return nil
	}
	if form != nil {
		for _, f := range forms {
			if f == *form {
				v := f
				return &v
			}
		}
	}
	v := forms[0]
	return &v
}

// formForSpecies is the appearance of an individual at one species: its own form when that species
// has it, otherwise the species default; nil for species without forms (PF:115-119).
func formForSpecies(speciesID int, form *string) *string {
	return resolveAmong(formsOf(speciesID), form)
}

// formForLine normalizes the form an individual stores: nil for lines without forms, the line
// default for an unknown value (PF:123-127).
func formForLine(baseID int, form *string) *string { return resolveAmong(lineForms(baseID), form) }

func sameForm(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// isDefaultForm reports whether a species with forms shows its default form.
func isDefaultForm(form *string, speciesID int) bool {
	r := formForSpecies(speciesID, form)
	return r != nil && *r == formsOf(speciesID)[0]
}

// rollForm picks a form: uncollected forms weigh 2, collected forms 1 (PF:139-147).
func rollForm(roll uint64, forms []string, collected map[string]bool) string {
	weights := make([]int, len(forms))
	total := 0
	for i, f := range forms {
		weights[i] = collectionWeight(2, collected[f])
		total += weights[i]
	}
	remaining := int(roll % uint64(total))
	for i := 0; i < len(forms)-1; i++ {
		remaining -= weights[i]
		if remaining < 0 {
			return forms[i]
		}
	}
	return forms[len(forms)-1]
}

// assetName is the sprite asset of a species in a form: "<id>" for the default, "<id>-<form>"
// otherwise (PF:153-158).
func assetName(speciesID int, form *string) string {
	r := formForSpecies(speciesID, form)
	if r == nil || isDefaultForm(r, speciesID) {
		return strconv.Itoa(speciesID)
	}
	return strconv.Itoa(speciesID) + "-" + *r
}

// staticAssetName is the static PNG asset: the PokéAPI variety id when the form is a separate
// variety, else assetName (PF:161-167).
func staticAssetName(speciesID int, form *string) string {
	r := formForSpecies(speciesID, form)
	for _, e := range formCatalog[speciesID] {
		if r != nil && e.form == *r && e.pokemonID != 0 {
			return strconv.Itoa(e.pokemonID)
		}
	}
	return assetName(speciesID, form)
}

// SpriteAssets returns the form-aware sprite asset names of a species in a form (nil: the default
// form): the animated/cache asset and the static PNG asset, for the daemon's getSprite (protocol
// §8.1). An unknown form resolves to the default, as everywhere else.
func SpriteAssets(speciesID int, form *string) (asset, staticAsset string) {
	return assetName(speciesID, form), staticAssetName(speciesID, form)
}

// formNameResource is the pokemon-form resource holding this form's localized name, or "" when the
// form needs none: Unown shows its letter and the unnamed usual looks use "Normal Form" (PF:216-220).
func formNameResource(speciesID int, form *string) string {
	if speciesID == unownSpeciesID || (unnamedDefaultSpecies[speciesID] && isDefaultForm(form, speciesID)) {
		return ""
	}
	r := formForSpecies(speciesID, form)
	for _, e := range formCatalog[speciesID] {
		if r != nil && e.form == *r {
			return strconv.Itoa(e.formID)
		}
	}
	return ""
}

func formSymbol(form string) string {
	switch form {
	case "exclamation":
		return "!"
	case "question":
		return "?"
	}
	return strings.ToUpper(form)
}

// normalFormNames localizes the label of an unnamed usual look (upstream L.normalForm).
var normalFormNames = map[string]string{
	"ko": "노말폼", "en": "Normal Form", "ja": "ノーマルフォルム", "es": "Forma Normal",
	"fr": "Forme Normale", "pt": "Forma Normal", "de": "Normalform", "ru": "Обычная форма",
}

// identifierLabel title-cases an API identifier: "blue-striped" → "Blue Striped" (upstream
// PokemonNameLocalization.identifier), the last resort for a form label.
func identifierLabel(raw string) string {
	parts := strings.Split(raw, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// formDisplayName adds a non-default form's label to a species name; Unown always shows its letter
// (PF:176-181).
func formDisplayName(name string, speciesID int, form *string, label string) string {
	r := formForSpecies(speciesID, form)
	if r == nil {
		return name
	}
	if speciesID == unownSpeciesID {
		return name + " [" + formSymbol(*r) + "]"
	}
	if isDefaultForm(r, speciesID) || label == "" {
		return name
	}
	return name + " [" + label + "]"
}
