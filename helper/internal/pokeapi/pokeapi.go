// Package pokeapi is a small PokéAPI client with a persistent on-disk cache.
//
// Every response (base index, species, evolution chains, details, names) is
// cached under the directory given in Options for 30 days, with a stale
// fallback when the network fails and in-flight request dedupe. Types are
// plain structs so callers can adapt them to their own interfaces.
package pokeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultREST    = "https://pokeapi.co/api/v2"
	defaultGraphQL = "https://graphql.pokeapi.co/v1beta2"
	chainHost      = "pokeapi.co"

	// MaxSpeciesID is the last hatchable species (Gen V animated sprites).
	MaxSpeciesID = 649
	dittoID      = 132
	// PreferredVersionGroup selects the learnset kept in Details.Moves.
	PreferredVersionGroup = "black-2-white-2"

	requestTimeout = 15 * time.Second
	maxBody        = 16 << 20
	restBatch      = 6
	restMinBases   = 150
)

// Options configures a Client.
type Options struct {
	// CacheDir is where cached responses are stored. Required.
	CacheDir string
	// Version is put in the User-Agent: poketokenbar-gnome/<Version>.
	Version string
	// RESTBase and GraphQLURL override the endpoints (tests).
	RESTBase, GraphQLURL string
	// HTTPClient overrides the transport (tests). Default: http.DefaultClient.
	HTTPClient *http.Client
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Client talks to PokéAPI. Create it with New and release it with Close.
type Client struct {
	dir, ua, rest, graphql string
	hc                     *http.Client
	nowFn                  func() time.Time
	flight                 flight

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu           sync.Mutex
	restBuildRan bool
}

// New returns a Client.
func New(o Options) *Client {
	c := &Client{
		dir:     o.CacheDir,
		ua:      "poketokenbar-gnome/" + o.Version,
		rest:    strings.TrimRight(o.RESTBase, "/"),
		graphql: o.GraphQLURL,
		hc:      o.HTTPClient,
		nowFn:   o.Now,
	}
	if c.rest == "" {
		c.rest = defaultREST
	}
	if c.graphql == "" {
		c.graphql = defaultGraphQL
	}
	if c.hc == nil {
		c.hc = http.DefaultClient
	}
	if c.nowFn == nil {
		c.nowFn = time.Now
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c
}

// Close cancels background work (the REST base-index build) and waits for it.
func (c *Client) Close() {
	c.cancel()
	c.wg.Wait()
}

func (c *Client) now() time.Time         { return c.nowFn() }
func (c *Client) path(rel string) string { return c.dir + "/" + rel }

// get fetches url and returns the body. Unlike upstream's REST calls, it
// requires HTTP 200 everywhere, so an error page is never decoded or cached.
func (c *Client) get(ctx context.Context, method, rawURL string, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("pokeapi: %s %s: HTTP %d", method, rawURL, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

func (c *Client) getJSON(ctx context.Context, rawURL string, out any) error {
	b, err := c.get(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// ---------------------------------------------------------------- base index

// BaseSpecies is a hatch candidate: the first species of an evolution line.
type BaseSpecies struct {
	ID          int `json:"id"`
	CaptureRate int `json:"captureRate"`
}

const baseIndexFile = "base-index.json"

// BaseIndex returns every base species with id <= 649 except Ditto, from the
// GraphQL endpoint (cached 30 days, stale fallback). When GraphQL fails and
// nothing is cached, it starts a one-off background REST build (see
// BuildBaseIndexViaREST) and returns the GraphQL error.
func (c *Client) BaseIndex(ctx context.Context) ([]BaseSpecies, error) {
	entries, err := cached(c, baseIndexFile, func() ([]BaseSpecies, error) {
		return c.fetchBaseIndex(ctx)
	})
	if err == nil {
		return entries, nil
	}
	c.mu.Lock()
	start := !c.restBuildRan
	c.restBuildRan = true
	c.mu.Unlock()
	if start && c.ctx.Err() == nil {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			_ = c.BuildBaseIndexViaREST(c.ctx)
		}()
	}
	return nil, err
}

type graphqlResp struct {
	Data struct {
		Species []struct {
			ID          int `json:"id"`
			CaptureRate int `json:"capture_rate"`
		} `json:"pokemonspecies"`
	} `json:"data"`
}

func (c *Client) fetchBaseIndex(ctx context.Context) ([]BaseSpecies, error) {
	q := fmt.Sprintf("{ pokemonspecies(where: {evolves_from_species_id: {_is_null: true}, id: {_lte: %d, _neq: %d}}, order_by: {id: asc}) { id capture_rate } }",
		MaxSpeciesID, dittoID)
	body, _ := json.Marshal(map[string]string{"query": q})
	b, err := c.get(ctx, http.MethodPost, c.graphql, body)
	if err != nil {
		return nil, err
	}
	var r graphqlResp
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	out := make([]BaseSpecies, 0, len(r.Data.Species))
	for _, s := range r.Data.Species {
		out = append(out, BaseSpecies{ID: s.ID, CaptureRate: s.CaptureRate})
	}
	if len(out) == 0 {
		return nil, errors.New("pokeapi: empty base index")
	}
	return out, nil
}

// BuildBaseIndexViaREST rebuilds the base index from pokemon-species ids
// 1..649 (batches of 6) and persists it only when at least 150 bases were
// found, so a flaky network never stores a thin index.
func (c *Client) BuildBaseIndexViaREST(ctx context.Context) error {
	var bases []BaseSpecies
	for start := 1; start <= MaxSpeciesID; start += restBatch {
		end := min(start+restBatch-1, MaxSpeciesID)
		found := make([]*BaseSpecies, end-start+1)
		var wg sync.WaitGroup
		for id := start; id <= end; id++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if b, err := c.BaseSpecies(ctx, id); err == nil {
					found[id-start] = b
				}
			}()
		}
		wg.Wait()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		for _, b := range found {
			if b != nil {
				bases = append(bases, *b)
			}
		}
	}
	if len(bases) < restMinBases {
		return fmt.Errorf("pokeapi: REST base index incomplete (%d bases), not cached", len(bases))
	}
	sort.Slice(bases, func(i, j int) bool { return bases[i].ID < bases[j].ID })
	return writeJSONAtomic(c.path(baseIndexFile), snapshot[[]BaseSpecies]{FetchedAt: c.now(), Data: bases})
}

// BaseSpecies returns the species when it starts an evolution line, or nil
// when it evolves from another one or is Ditto.
func (c *Client) BaseSpecies(ctx context.Context, id int) (*BaseSpecies, error) {
	if id == dittoID {
		return nil, nil
	}
	sp, err := c.Species(ctx, id)
	if err != nil {
		return nil, err
	}
	if sp.EvolvesFrom != 0 {
		return nil, nil
	}
	return &BaseSpecies{ID: id, CaptureRate: sp.CaptureRate}, nil
}

// ------------------------------------------------------------------- species

// Species is the part of pokemon-species the game uses.
type Species struct {
	ID          int  `json:"id"`
	CaptureRate int  `json:"captureRate"`
	IsLegendary bool `json:"isLegendary"`
	IsMythical  bool `json:"isMythical"`
	// GenderRate is the female share in eighths; -1 means genderless.
	GenderRate int `json:"genderRate"`
	// EvolvesFrom is the id of the previous species, 0 for a base species.
	EvolvesFrom int `json:"evolvesFrom"`
	// ChainURL is the validated evolution_chain URL.
	ChainURL string `json:"chainURL"`
	// Names maps a normalised language code (lower case, "-") to the name,
	// for every language PokéAPI returns.
	Names map[string]string `json:"names"`
}

type speciesDTO struct {
	CaptureRate    int      `json:"capture_rate"`
	IsLegendary    bool     `json:"is_legendary"`
	IsMythical     bool     `json:"is_mythical"`
	Names          []nameDT `json:"names"`
	EvolutionChain struct {
		URL string `json:"url"`
	} `json:"evolution_chain"`
	EvolvesFrom *namedRef `json:"evolves_from_species"`
	GenderRate  *int      `json:"gender_rate"`
}

type nameDT struct {
	Name     string   `json:"name"`
	Language namedRef `json:"language"`
}

type namedRef struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Species returns pokemon-species/{id}.
func (c *Client) Species(ctx context.Context, id int) (Species, error) {
	return cached(c, fmt.Sprintf("species/%d.json", id), func() (Species, error) {
		var d speciesDTO
		if err := c.getJSON(ctx, fmt.Sprintf("%s/pokemon-species/%d", c.rest, id), &d); err != nil {
			return Species{}, err
		}
		s := Species{
			ID:          id,
			CaptureRate: d.CaptureRate,
			IsLegendary: d.IsLegendary,
			IsMythical:  d.IsMythical,
			GenderRate:  -1,
			ChainURL:    d.EvolutionChain.URL,
			Names:       collectNames(d.Names),
		}
		if d.GenderRate != nil {
			s.GenderRate = *d.GenderRate
		}
		if d.EvolvesFrom != nil {
			s.EvolvesFrom = idFromURL(d.EvolvesFrom.URL)
			if s.EvolvesFrom == 0 {
				return Species{}, errors.New("pokeapi: evolves_from_species without id")
			}
		}
		return s, nil
	})
}

// ------------------------------------------------------------------ chain

// EvoNode is one node of an evolution tree.
type EvoNode struct {
	ID       int       `json:"id"`
	Children []EvoNode `json:"children"`
}

type chainLink struct {
	Species   namedRef    `json:"species"`
	EvolvesTo []chainLink `json:"evolves_to"`
}

func (l chainLink) node() EvoNode {
	n := EvoNode{ID: idFromURL(l.Species.URL), Children: []EvoNode{}}
	for _, ch := range l.EvolvesTo {
		n.Children = append(n.Children, ch.node())
	}
	return n
}

func (n EvoNode) valid() bool {
	if n.ID == 0 {
		return false
	}
	for _, ch := range n.Children {
		if !ch.valid() {
			return false
		}
	}
	return true
}

// ValidateChainURL is the SSRF guard for server-supplied evolution_chain
// URLs: https, host exactly pokeapi.co, no userinfo. It returns the URL when
// acceptable.
func ValidateChainURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != chainHost || u.User != nil {
		return "", false
	}
	return u.String(), true
}

// Chain returns the evolution tree behind chainURL, which must pass
// ValidateChainURL.
func (c *Client) Chain(ctx context.Context, chainURL string) (EvoNode, error) {
	valid, ok := ValidateChainURL(chainURL)
	if !ok {
		return EvoNode{}, fmt.Errorf("pokeapi: rejected evolution chain URL %q", chainURL)
	}
	cid := idFromURL(valid)
	if cid == 0 {
		return EvoNode{}, fmt.Errorf("pokeapi: evolution chain URL without id %q", valid)
	}
	return cached(c, fmt.Sprintf("chains/%d.json", cid), func() (EvoNode, error) {
		var d struct {
			Chain chainLink `json:"chain"`
		}
		if err := c.getJSON(ctx, valid, &d); err != nil {
			return EvoNode{}, err
		}
		tree := d.Chain.node()
		if !tree.valid() {
			return EvoNode{}, errors.New("pokeapi: evolution chain node without species id")
		}
		return tree, nil
	})
}

// Line is an evolution line with the data of every species in it.
type Line struct {
	BaseID  int
	Tree    EvoNode
	Species map[int]Species
}

// Line loads the species of baseID, its evolution tree and the species of
// every node (names in all languages, capture rate, flags).
func (c *Client) Line(ctx context.Context, baseID int) (Line, error) {
	base, err := c.Species(ctx, baseID)
	if err != nil {
		return Line{}, err
	}
	tree, err := c.Chain(ctx, base.ChainURL)
	if err != nil {
		return Line{}, err
	}
	l := Line{BaseID: baseID, Tree: tree, Species: map[int]Species{baseID: base}}
	var walk func(n EvoNode) error
	walk = func(n EvoNode) error {
		if _, ok := l.Species[n.ID]; !ok {
			sp, err := c.Species(ctx, n.ID)
			if err != nil {
				return err
			}
			l.Species[n.ID] = sp
		}
		for _, ch := range n.Children {
			if err := walk(ch); err != nil {
				return err
			}
		}
		return nil
	}
	return l, walk(tree)
}

// ------------------------------------------------------------------ details

// Details is the default-form battle metadata of a species.
type Details struct {
	SpeciesID      int            `json:"speciesID"`
	Name           string         `json:"name"`
	Height         int            `json:"height"` // decimetres
	Weight         int            `json:"weight"` // hectograms
	BaseExperience *int           `json:"baseExperience"`
	GenderRate     int            `json:"genderRate"` // female eighths, -1 genderless
	Types          []string       `json:"types"`      // by slot
	BaseStats      map[string]int `json:"baseStats"`
	Abilities      []Ability      `json:"abilities"` // by slot
	Moves          []Move         `json:"moves"`     // by name
}

// Ability is one possible ability.
type Ability struct {
	Name   string `json:"name"`
	Slot   int    `json:"slot"`
	Hidden bool   `json:"hidden"`
}

// Move is a move learnable in PreferredVersionGroup, with every method.
type Move struct {
	Name    string        `json:"name"`
	Methods []LearnMethod `json:"methods"`
}

// LearnMethod is a move-learn-method name and the level (0 unless level-up).
type LearnMethod struct {
	Method string `json:"method"`
	Level  int    `json:"level"`
}

type pokemonDTO struct {
	Name           string `json:"name"`
	Height         int    `json:"height"`
	Weight         int    `json:"weight"`
	BaseExperience *int   `json:"base_experience"`
	Types          []struct {
		Slot int      `json:"slot"`
		Type namedRef `json:"type"`
	} `json:"types"`
	Abilities []struct {
		IsHidden bool     `json:"is_hidden"`
		Slot     int      `json:"slot"`
		Ability  namedRef `json:"ability"`
	} `json:"abilities"`
	Stats []struct {
		BaseStat int      `json:"base_stat"`
		Stat     namedRef `json:"stat"`
	} `json:"stats"`
	Moves []struct {
		Move    namedRef `json:"move"`
		Details []struct {
			Level      int      `json:"level_learned_at"`
			Method     namedRef `json:"move_learn_method"`
			VersionGrp namedRef `json:"version_group"`
		} `json:"version_group_details"`
	} `json:"moves"`
}

// Details returns pokemon/{id} reduced to the fields the game uses.
func (c *Client) Details(ctx context.Context, speciesID int) (Details, error) {
	return cached(c, fmt.Sprintf("pokemon-details-v1/%d.json", speciesID), func() (Details, error) {
		var d pokemonDTO
		if err := c.getJSON(ctx, fmt.Sprintf("%s/pokemon/%d", c.rest, speciesID), &d); err != nil {
			return Details{}, err
		}
		sp, err := c.Species(ctx, speciesID)
		if err != nil {
			return Details{}, err
		}
		out := Details{
			SpeciesID: speciesID, Name: d.Name, Height: d.Height, Weight: d.Weight,
			BaseExperience: d.BaseExperience, GenderRate: sp.GenderRate,
			Types: []string{}, BaseStats: map[string]int{}, Abilities: []Ability{}, Moves: []Move{},
		}
		sort.SliceStable(d.Types, func(i, j int) bool { return d.Types[i].Slot < d.Types[j].Slot })
		for _, t := range d.Types {
			out.Types = append(out.Types, t.Type.Name)
		}
		for _, s := range d.Stats {
			out.BaseStats[s.Stat.Name] = s.BaseStat
		}
		for _, a := range d.Abilities {
			out.Abilities = append(out.Abilities, Ability{Name: a.Ability.Name, Slot: a.Slot, Hidden: a.IsHidden})
		}
		sort.SliceStable(out.Abilities, func(i, j int) bool { return out.Abilities[i].Slot < out.Abilities[j].Slot })
		for _, m := range d.Moves {
			mv := Move{Name: m.Move.Name}
			for _, v := range m.Details {
				if v.VersionGrp.Name == PreferredVersionGroup {
					mv.Methods = append(mv.Methods, LearnMethod{Method: v.Method.Name, Level: v.Level})
				}
			}
			if len(mv.Methods) > 0 {
				out.Moves = append(out.Moves, mv)
			}
		}
		sort.Slice(out.Moves, func(i, j int) bool { return out.Moves[i].Name < out.Moves[j].Name })
		return out, nil
	})
}

// -------------------------------------------------------------------- names

// NameKind is a PokéAPI resource type that carries localized names.
type NameKind string

// Name resource kinds.
const (
	KindType    NameKind = "type"
	KindAbility NameKind = "ability"
	KindMove    NameKind = "move"
	KindForm    NameKind = "pokemon-form"
)

// Names returns the localized names of a resource: language code -> name.
// For KindForm, name is the numeric pokemon-form id and the bare form names
// (form_names) are returned. name must match [a-z0-9-]+.
func (c *Client) Names(ctx context.Context, kind NameKind, name string) (map[string]string, error) {
	switch kind {
	case KindType, KindAbility, KindMove, KindForm:
	default:
		return nil, fmt.Errorf("pokeapi: unknown name kind %q", kind)
	}
	if !validResourceName(name) {
		return nil, fmt.Errorf("pokeapi: invalid resource name %q", name)
	}
	return cached(c, fmt.Sprintf("pokemon-names-v1/%s-%s.json", kind, name), func() (map[string]string, error) {
		var d struct {
			Names     []nameDT `json:"names"`
			FormNames []nameDT `json:"form_names"`
		}
		if err := c.getJSON(ctx, fmt.Sprintf("%s/%s/%s/", c.rest, kind, name), &d); err != nil {
			return nil, err
		}
		if kind == KindForm {
			return collectNames(d.FormNames), nil
		}
		return collectNames(d.Names), nil
	})
}

func validResourceName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-') {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- languages

// LanguageCode normalises a language code: trimmed, lower case, "_" -> "-".
func LanguageCode(raw string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "_", "-")
}

func collectNames(entries []nameDT) map[string]string {
	out := map[string]string{}
	for _, e := range entries {
		code, name := LanguageCode(e.Language.Name), strings.TrimSpace(e.Name)
		if code != "" && name != "" {
			out[code] = name
		}
	}
	return out
}

// ResolveName picks the first available name among preferred codes, then
// "en". The bool is false when none exists (callers show "#id").
func ResolveName(names map[string]string, preferred ...string) (string, bool) {
	norm := make(map[string]string, len(names))
	for k, v := range names {
		if v = strings.TrimSpace(v); v != "" {
			norm[LanguageCode(k)] = v
		}
	}
	for _, code := range append(append([]string{}, preferred...), "en") {
		if n, ok := norm[LanguageCode(code)]; ok {
			return n, true
		}
	}
	return "", false
}

// idFromURL extracts the trailing numeric id of ".../{id}/", or 0.
func idFromURL(raw string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(path.Base(strings.TrimRight(u.Path, "/")))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
