package pokeapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var bg = context.Background()

func TestValidateChainURL(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:2173
	if _, ok := ValidateChainURL("https://pokeapi.co/api/v2/evolution-chain/1/"); !ok {
		t.Error("pokeapi.co over https must be accepted")
	}
	reject := []struct{ raw, why string }{
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:2177
		{"https://evil.example.com/x", "arbitrary host (SSRF)"},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:2178
		{"https://pokeapi.co.evil.com/x", "look-alike host"},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:2179
		{"http://pokeapi.co/x", "http"},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:2180
		{"", "empty"},
		{"https://evilpokeapi.co/x", "suffix look-alike"},
		// Go-side hardening beyond the Swift cases (host compared with its port, no userinfo).
		{"https://pokeapi.co@evil.com/x", "userinfo trick"},
		{"https://user@pokeapi.co/x", "userinfo"},
		{"https://pokeapi.co:8443/x", "port"},
		{"//pokeapi.co/x", "scheme-relative"},
	}
	for _, c := range reject {
		if _, ok := ValidateChainURL(c.raw); ok {
			t.Errorf("%q accepted (%s)", c.raw, c.why)
		}
	}
}

// ---- test harness ----------------------------------------------------------

type fake struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   map[string]int
	routes map[string]http.HandlerFunc // key: "METHOD path"
	lastUA string
	posted string
}

func newFake(t *testing.T) *fake {
	f := &fake{hits: map[string]int{}, routes: map[string]http.HandlerFunc{}}
	f.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		key := r.Method + " " + r.URL.Path
		f.hits[key]++
		f.lastUA = r.Header.Get("User-Agent")
		if r.Method == "POST" {
			f.posted = string(body)
		}
		h := f.routes[key]
		f.mu.Unlock()
		if h == nil {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	f.srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	f.srv.StartTLS()
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) json(key, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[key] = func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) }
}

func (f *fake) status(key string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[key] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func (f *fake) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[key]
}

// rewrite sends every request, whatever its host, to the test server. This
// lets https://pokeapi.co/... chain URLs reach the fake without a production
// escape hatch in the SSRF guard.
type rewrite struct{ f *fake }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.f.srv.URL)
	c := req.Clone(req.Context())
	c.URL.Scheme, c.URL.Host = "https", u.Host
	return r.f.srv.Client().Transport.RoundTrip(c)
}

func (f *fake) client(t *testing.T, dir string) *Client {
	c := New(Options{
		CacheDir: dir, Version: "9.9.9",
		RESTBase: f.srv.URL + "/api/v2", GraphQLURL: f.srv.URL + "/v1beta2",
		HTTPClient: &http.Client{Transport: rewrite{f}},
	})
	t.Cleanup(c.Close)
	return c
}

const speciesPichu = `{"id":172,"capture_rate":190,"is_legendary":false,"is_mythical":false,"gender_rate":4,
 "evolution_chain":{"url":"https://pokeapi.co/api/v2/evolution-chain/10/"},"evolves_from_species":null,
 "names":[{"name":"Pichu","language":{"name":"en"}},{"name":"ピチュー","language":{"name":"ja-Hrkt"}},{"name":"  ","language":{"name":"fr"}},{"name":"Pichu","language":{"name":"pt_BR"}}]}`

const speciesPikachu = `{"capture_rate":190,"is_legendary":false,"is_mythical":false,"gender_rate":4,
 "evolution_chain":{"url":"https://pokeapi.co/api/v2/evolution-chain/10/"},
 "evolves_from_species":{"name":"pichu","url":"https://pokeapi.co/api/v2/pokemon-species/172/"},
 "names":[{"name":"Pikachu","language":{"name":"en"}}]}`

const chain10 = `{"chain":{"species":{"name":"pichu","url":"https://pokeapi.co/api/v2/pokemon-species/172/"},
 "evolves_to":[{"species":{"name":"pikachu","url":"https://pokeapi.co/api/v2/pokemon-species/25/"},
   "evolves_to":[{"species":{"name":"raichu","url":"https://pokeapi.co/api/v2/pokemon-species/26/"},"evolves_to":[]}]}]}}`

func TestSpeciesChainLineAndRestartOffline(t *testing.T) {
	f := newFake(t)
	f.json("GET /api/v2/pokemon-species/172", speciesPichu)
	f.json("GET /api/v2/pokemon-species/25", speciesPikachu)
	f.json("GET /api/v2/pokemon-species/26", strings.Replace(speciesPikachu, "Pikachu", "Raichu", 1))
	f.json("GET /api/v2/evolution-chain/10/", chain10)
	dir := t.TempDir()
	c := f.client(t, dir)

	sp, err := c.Species(bg, 172)
	if err != nil {
		t.Fatal(err)
	}
	if sp.CaptureRate != 190 || sp.GenderRate != 4 || sp.EvolvesFrom != 0 || sp.IsLegendary ||
		sp.ChainURL != "https://pokeapi.co/api/v2/evolution-chain/10/" {
		t.Errorf("species = %+v", sp)
	}
	// Names: all languages, normalised codes, blank names dropped.
	wantNames := map[string]string{"en": "Pichu", "ja-hrkt": "ピチュー", "pt-br": "Pichu"}
	if !reflect.DeepEqual(sp.Names, wantNames) {
		t.Errorf("names = %v", sp.Names)
	}
	if f.lastUA != "poketokenbar-gnome/9.9.9" {
		t.Errorf("UA = %q", f.lastUA)
	}

	line, err := c.Line(bg, 172)
	if err != nil {
		t.Fatal(err)
	}
	want := EvoNode{ID: 172, Children: []EvoNode{{ID: 25, Children: []EvoNode{{ID: 26, Children: []EvoNode{}}}}}}
	if !reflect.DeepEqual(line.Tree, want) || len(line.Species) != 3 || line.Species[25].EvolvesFrom != 172 {
		t.Errorf("line = %+v", line)
	}

	// Offline restart: a new client over the same directory, server gone.
	f.srv.Close()
	c2 := New(Options{CacheDir: dir, RESTBase: "https://127.0.0.1:1/api/v2", GraphQLURL: "https://127.0.0.1:1/g"})
	defer c2.Close()
	line2, err := c2.Line(bg, 172)
	if err != nil {
		t.Fatalf("line must come from the disk cache offline: %v", err)
	}
	if !reflect.DeepEqual(line2, line) {
		t.Errorf("offline line differs:\n%+v\n%+v", line2, line)
	}
}

func TestChainSSRFGuardBlocksBeforeAnyRequest(t *testing.T) {
	f := newFake(t)
	evil := strings.Replace(speciesPichu, "https://pokeapi.co/api/v2/evolution-chain/10/", "https://evil.example.com/api/v2/evolution-chain/10/", 1)
	f.json("GET /api/v2/pokemon-species/172", evil)
	f.json("GET /api/v2/evolution-chain/10/", chain10)
	c := f.client(t, t.TempDir())
	if _, err := c.Line(bg, 172); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("err = %v, want a rejected-URL error", err)
	}
	if n := f.count("GET /api/v2/evolution-chain/10/"); n != 0 {
		t.Errorf("chain fetched %d times despite the guard", n)
	}
	if _, err := c.Chain(bg, "http://pokeapi.co/api/v2/evolution-chain/10/"); err == nil {
		t.Error("http chain URL accepted")
	}
}

func TestChainWithoutSpeciesIDIsRejected(t *testing.T) {
	f := newFake(t)
	f.json("GET /api/v2/evolution-chain/7/", `{"chain":{"species":{"name":"x","url":""},"evolves_to":[]}}`)
	c := f.client(t, t.TempDir())
	if _, err := c.Chain(bg, "https://pokeapi.co/api/v2/evolution-chain/7/"); err == nil {
		t.Error("node without id accepted")
	}
}

func TestNon200IsNeverDecodedOrCached(t *testing.T) {
	f := newFake(t)
	f.mu.Lock()
	f.routes["GET /api/v2/pokemon-species/1"] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, speciesPichu) // a valid-looking body behind a 500
	}
	f.mu.Unlock()
	dir := t.TempDir()
	c := f.client(t, dir)
	if _, err := c.Species(bg, 1); err == nil {
		t.Fatal("HTTP 500 accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "species", "1.json")); err == nil {
		t.Error("error response cached")
	}
}

func TestBaseIndexGraphQL(t *testing.T) {
	f := newFake(t)
	f.json("POST /v1beta2", `{"data":{"pokemonspecies":[{"id":1,"capture_rate":45},{"id":4,"capture_rate":45}]}}`)
	dir := t.TempDir()
	c := f.client(t, dir)
	got, err := c.BaseIndex(bg)
	if err != nil || !reflect.DeepEqual(got, []BaseSpecies{{1, 45}, {4, 45}}) {
		t.Fatalf("%v, %v", got, err)
	}
	var posted map[string]string
	json.Unmarshal([]byte(f.posted), &posted)
	wantQ := "{ pokemonspecies(where: {evolves_from_species_id: {_is_null: true}, id: {_lte: 649, _neq: 132}}, order_by: {id: asc}) { id capture_rate } }"
	if posted["query"] != wantQ || f.lastUA != "poketokenbar-gnome/9.9.9" {
		t.Errorf("query = %q, ua = %q", posted["query"], f.lastUA)
	}
	if _, err := c.BaseIndex(bg); err != nil || f.count("POST /v1beta2") != 1 {
		t.Errorf("second call must hit the cache (hits %d, err %v)", f.count("POST /v1beta2"), err)
	}
	if _, err := os.Stat(filepath.Join(dir, "base-index.json")); err != nil {
		t.Error(err)
	}
}

func TestBaseIndexTTLAndStaleFallback(t *testing.T) {
	f := newFake(t)
	f.json("POST /v1beta2", `{"data":{"pokemonspecies":[{"id":1,"capture_rate":45}]}}`)
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := New(Options{CacheDir: dir, GraphQLURL: f.srv.URL + "/v1beta2", RESTBase: f.srv.URL + "/api/v2",
		HTTPClient: f.srv.Client(), Now: func() time.Time { return now }})
	defer c.Close()
	if _, err := c.BaseIndex(bg); err != nil {
		t.Fatal(err)
	}
	now = now.Add(29 * 24 * time.Hour)
	c.BaseIndex(bg)
	if f.count("POST /v1beta2") != 1 {
		t.Fatal("29 days old must still be fresh")
	}
	now = now.Add(2 * 24 * time.Hour) // 31 days
	f.json("POST /v1beta2", `{"data":{"pokemonspecies":[{"id":1,"capture_rate":45},{"id":7,"capture_rate":45}]}}`)
	got, _ := c.BaseIndex(bg)
	if len(got) != 2 || f.count("POST /v1beta2") != 2 {
		t.Fatalf("expired cache must refetch: %v", got)
	}
	now = now.Add(31 * 24 * time.Hour)
	f.status("POST /v1beta2", 503)
	got, err := c.BaseIndex(bg)
	if err != nil || len(got) != 2 {
		t.Fatalf("stale fallback failed: %v, %v", got, err)
	}
}

// restServer answers pokemon-species for 1..649: ids divisible by 3 evolve
// from something, the rest are bases. fail(id) makes an id return HTTP 500.
func restServer(f *fake, fail func(id int) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := 1; id <= 649; id++ {
		evolves := "null"
		if id%3 == 0 {
			evolves = `{"name":"x","url":"https://pokeapi.co/api/v2/pokemon-species/1/"}`
		}
		body := fmt.Sprintf(`{"capture_rate":%d,"is_legendary":false,"is_mythical":false,"gender_rate":1,
		  "evolution_chain":{"url":"https://pokeapi.co/api/v2/evolution-chain/%d/"},"evolves_from_species":%s,"names":[]}`, id%255+1, id, evolves)
		bad := fail != nil && fail(id)
		f.routes[fmt.Sprintf("GET /api/v2/pokemon-species/%d", id)] = func(w http.ResponseWriter, _ *http.Request) {
			if bad {
				w.WriteHeader(500)
				return
			}
			io.WriteString(w, body)
		}
	}
}

func TestBaseIndexRESTSelfHeal(t *testing.T) {
	f := newFake(t)
	f.status("POST /v1beta2", 500)
	restServer(f, nil)
	dir := t.TempDir()
	c := f.client(t, dir)
	if _, err := c.BaseIndex(bg); err == nil {
		t.Fatal("GraphQL failure with no cache must return an error")
	}
	if _, err := c.BaseIndex(bg); err == nil { // second failure must not start a second build
		t.Fatal("still no cache")
	}
	c.wg.Wait() // the background build runs to completion
	snap, ok := readSnapshot[[]BaseSpecies](filepath.Join(dir, "base-index.json"))
	if !ok {
		t.Fatal("REST build did not persist the index")
	}
	// 649 ids, every third evolves (216), Ditto (132 = 3*44, already excluded) -> 433 bases.
	if len(snap.Data) != 433 || snap.Data[0].ID != 1 || snap.Data[1].ID != 2 || snap.Data[2].ID != 4 {
		t.Errorf("persisted %d bases, first %v", len(snap.Data), snap.Data[:3])
	}
	if n := f.count("GET /api/v2/pokemon-species/1"); n != 1 {
		t.Errorf("species 1 fetched %d times; the build must run once", n)
	}
	// A new client now has an offline-capable index.
	f.srv.Close()
	c2 := New(Options{CacheDir: dir, GraphQLURL: "https://127.0.0.1:1/g"})
	defer c2.Close()
	if got, err := c2.BaseIndex(bg); err != nil || len(got) != 433 {
		t.Errorf("offline index: %d, %v", len(got), err)
	}
}

func TestBaseIndexRESTThinResultNotPersisted(t *testing.T) {
	f := newFake(t)
	f.status("POST /v1beta2", 500)
	restServer(f, func(id int) bool { return id > 100 }) // only ids <= 100 answer: 67 bases
	dir := t.TempDir()
	c := f.client(t, dir)
	if err := c.BuildBaseIndexViaREST(bg); err == nil {
		t.Fatal("thin index must be an error")
	}
	if _, err := os.Stat(filepath.Join(dir, "base-index.json")); err == nil {
		t.Error("index with < 150 bases was persisted")
	}
	// Exactly at the threshold boundary: ids <= 225 answer -> 150 bases (225 - 75).
	f2 := newFake(t)
	restServer(f2, func(id int) bool { return id > 225 })
	dir2 := t.TempDir()
	c2 := f2.client(t, dir2)
	if err := c2.BuildBaseIndexViaREST(bg); err != nil {
		t.Fatalf("150 bases must be accepted: %v", err)
	}
}

func TestBaseSpeciesSkipsDittoAndEvolved(t *testing.T) {
	f := newFake(t)
	restServer(f, nil)
	c := f.client(t, t.TempDir())
	if b, err := c.BaseSpecies(bg, 132); b != nil || err != nil {
		t.Errorf("ditto: %v, %v", b, err)
	}
	if b, err := c.BaseSpecies(bg, 3); b != nil || err != nil {
		t.Errorf("evolved: %v, %v", b, err)
	}
	if f.count("GET /api/v2/pokemon-species/132") != 0 {
		t.Error("ditto must not be fetched")
	}
	if b, err := c.BaseSpecies(bg, 4); err != nil || b == nil || b.ID != 4 {
		t.Errorf("base: %v, %v", b, err)
	}
}

const pokemon25 = `{"name":"pikachu","height":4,"weight":60,"base_experience":112,
 "types":[{"slot":2,"type":{"name":"flying"}},{"slot":1,"type":{"name":"electric"}}],
 "abilities":[{"is_hidden":true,"slot":3,"ability":{"name":"lightning-rod"}},{"is_hidden":false,"slot":1,"ability":{"name":"static"}}],
 "stats":[{"base_stat":35,"stat":{"name":"hp"}},{"base_stat":90,"stat":{"name":"speed"}}],
 "moves":[
  {"move":{"name":"thunder-shock"},"version_group_details":[
    {"level_learned_at":1,"move_learn_method":{"name":"level-up"},"version_group":{"name":"black-2-white-2"}},
    {"level_learned_at":5,"move_learn_method":{"name":"level-up"},"version_group":{"name":"red-blue"}}]},
  {"move":{"name":"agility"},"version_group_details":[
    {"level_learned_at":18,"move_learn_method":{"name":"level-up"},"version_group":{"name":"black-2-white-2"}},
    {"level_learned_at":0,"move_learn_method":{"name":"machine"},"version_group":{"name":"black-2-white-2"}}]},
  {"move":{"name":"surf"},"version_group_details":[
    {"level_learned_at":0,"move_learn_method":{"name":"machine"},"version_group":{"name":"x-y"}}]}]}`

func TestDetails(t *testing.T) {
	f := newFake(t)
	f.json("GET /api/v2/pokemon/25", pokemon25)
	f.json("GET /api/v2/pokemon-species/25", speciesPikachu)
	c := f.client(t, t.TempDir())
	d, err := c.Details(bg, 25)
	if err != nil {
		t.Fatal(err)
	}
	exp := 112
	want := Details{
		SpeciesID: 25, Name: "pikachu", Height: 4, Weight: 60, BaseExperience: &exp, GenderRate: 4,
		Types:     []string{"electric", "flying"},
		BaseStats: map[string]int{"hp": 35, "speed": 90},
		Abilities: []Ability{{"static", 1, false}, {"lightning-rod", 3, true}},
		Moves: []Move{
			{"agility", []LearnMethod{{"level-up", 18}, {"machine", 0}}},
			{"thunder-shock", []LearnMethod{{"level-up", 1}}},
		},
	}
	if !reflect.DeepEqual(d, want) {
		t.Errorf("details = %+v\nwant     %+v", d, want)
	}
	if _, err := c.Details(bg, 25); err != nil || f.count("GET /api/v2/pokemon/25") != 1 {
		t.Error("details must be cached")
	}
}

func TestNames(t *testing.T) {
	f := newFake(t)
	f.json("GET /api/v2/move/thunder-shock/", `{"names":[{"name":"Thunder Shock","language":{"name":"en"}},{"name":"でんきショック","language":{"name":"ja"}}]}`)
	f.json("GET /api/v2/pokemon-form/10064/", `{"names":[{"name":"Shaymin","language":{"name":"en"}}],"form_names":[{"name":"Sky Forme","language":{"name":"en"}}]}`)
	dir := t.TempDir()
	c := f.client(t, dir)

	got, err := c.Names(bg, KindMove, "thunder-shock")
	if err != nil || got["en"] != "Thunder Shock" || got["ja"] != "でんきショック" {
		t.Fatalf("%v, %v", got, err)
	}
	form, err := c.Names(bg, KindForm, "10064")
	if err != nil || !reflect.DeepEqual(form, map[string]string{"en": "Sky Forme"}) {
		t.Fatalf("form_names expected: %v, %v", form, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pokemon-names-v1", "move-thunder-shock.json")); err != nil {
		t.Error(err)
	}

	before := f.count("GET /api/v2/move/Thunder/")
	for _, bad := range []string{"", "Thunder", "../x", "a/b", "a b", "é", "a_b"} {
		if _, err := c.Names(bg, KindMove, bad); err == nil {
			t.Errorf("name %q accepted", bad)
		}
	}
	if _, err := c.Names(bg, "item", "x"); err == nil {
		t.Error("unknown kind accepted")
	}
	if f.count("GET /api/v2/move/Thunder/") != before {
		t.Error("an invalid name reached the network")
	}
}

func TestInFlightDedupe(t *testing.T) {
	f := newFake(t)
	release := make(chan struct{})
	var hits atomic.Int32
	f.mu.Lock()
	f.routes["GET /api/v2/ability/static/"] = func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		<-release
		io.WriteString(w, `{"names":[{"name":"Static","language":{"name":"en"}}]}`)
	}
	f.mu.Unlock()
	c := f.client(t, t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if n, err := c.Names(bg, KindAbility, "static"); err != nil || n["en"] != "Static" {
				t.Errorf("%v %v", n, err)
			}
		}()
	}
	time.Sleep(150 * time.Millisecond)
	close(release)
	wg.Wait()
	if hits.Load() != 1 {
		t.Errorf("%d requests, want 1", hits.Load())
	}
}

func TestStaleFallbackWhenRefreshFails(t *testing.T) {
	f := newFake(t)
	f.json("GET /api/v2/type/electric/", `{"names":[{"name":"Electric","language":{"name":"en"}}]}`)
	now := time.Now()
	c := New(Options{CacheDir: t.TempDir(), RESTBase: f.srv.URL + "/api/v2", HTTPClient: f.srv.Client(), Now: func() time.Time { return now }})
	defer c.Close()
	if _, err := c.Names(bg, KindType, "electric"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(40 * 24 * time.Hour)
	f.status("GET /api/v2/type/electric/", 500)
	got, err := c.Names(bg, KindType, "electric")
	if err != nil || got["en"] != "Electric" {
		t.Fatalf("stale fallback: %v, %v", got, err)
	}
	if f.count("GET /api/v2/type/electric/") != 2 {
		t.Error("expired entry must be revalidated first")
	}
	// No cache and a failure: the error surfaces.
	if _, err := c.Names(bg, KindType, "fire"); err == nil {
		t.Error("expected an error with nothing cached")
	}
}

func TestAtomicWritesLeaveNoTemp(t *testing.T) {
	f := newFake(t)
	f.json("GET /api/v2/type/fire/", `{"names":[]}`)
	dir := t.TempDir()
	c := f.client(t, dir)
	c.Names(bg, KindType, "fire")
	ents, _ := os.ReadDir(filepath.Join(dir, "pokemon-names-v1"))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover %s", e.Name())
		}
	}
	if len(ents) != 1 {
		t.Errorf("entries = %d", len(ents))
	}
	// A corrupt cache file is treated as a miss, not an error.
	os.WriteFile(filepath.Join(dir, "pokemon-names-v1", "type-fire.json"), []byte("{trunc"), 0o644)
	if _, err := c.Names(bg, KindType, "fire"); err != nil {
		t.Errorf("corrupt cache: %v", err)
	}
}

func TestResolveName(t *testing.T) {
	names := map[string]string{"en": "Pichu", "ja-Hrkt": "ピチュー", "pt_BR": "Pichu BR", "fr": "  "}
	cases := []struct {
		pref []string
		want string
		ok   bool
	}{
		{[]string{"ja-hrkt", "ja"}, "ピチュー", true},
		{[]string{"pt-br", "pt"}, "Pichu BR", true},
		{[]string{"fr"}, "Pichu", true}, // blank French falls to en
		{[]string{"de"}, "Pichu", true},
	}
	for _, c := range cases {
		got, ok := ResolveName(names, c.pref...)
		if got != c.want || ok != c.ok {
			t.Errorf("%v = %q,%v want %q", c.pref, got, ok, c.want)
		}
	}
	if _, ok := ResolveName(map[string]string{"fr": "x"}, "de"); ok {
		t.Error("no en and no preferred: want false")
	}
}
