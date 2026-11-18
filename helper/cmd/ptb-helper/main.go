// Command ptb-helper is the PokeTokenBar-GNOME backend process.
//
//	ptb-helper --version
//	ptb-helper serve           NDJSON protocol on stdin/stdout (docs/protocol.md)
//	ptb-helper snapshot --json one full refresh, then one state line on stdout
//
// PTB_STATE_DIR overrides the state and cache directories. For snapshot only,
// PTB_SNAPSHOT_MAX_REQUESTS caps the PokéAPI requests (0 or unset: no cap); request counts per
// host are logged to stderr.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"poketokenbar/helper/internal/claude"
	"poketokenbar/helper/internal/daemon"
	"poketokenbar/helper/internal/limits"
	"poketokenbar/helper/internal/pokeapi"
	"poketokenbar/helper/internal/sprites"
)

// version is set via -ldflags "-X main.version=...".
var version = "0.0.0-dev"

func main() {
	log.SetFlags(0)
	log.SetPrefix("ptb-helper: ")
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "--version":
		fmt.Println(version)
	case "serve":
		os.Exit(serve())
	case "snapshot":
		if len(os.Args) != 3 || os.Args[2] != "--json" {
			usage()
		}
		os.Exit(snapshot())
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ptb-helper --version | serve | snapshot --json")
	os.Exit(daemon.ExitUsage)
}

// wiring builds the production configuration. hc, when non-nil, is the transport for every HTTP
// client (snapshot counts requests with it).
func wiring(hc *http.Client) (daemon.Config, *pokeapi.Client, error) {
	stateDir, cacheDir, err := daemon.Dirs(os.Getenv)
	if err != nil {
		return daemon.Config{}, nil, err
	}
	poke := pokeapi.New(pokeapi.Options{CacheDir: filepath.Join(cacheDir, "pokeapi"), Version: version, HTTPClient: hc})
	lim := &limits.Client{CredentialsPath: limits.DefaultCredentialsPath(), Version: version}
	if hc != nil {
		lim.HTTPClient = &http.Client{Timeout: 15 * time.Second, Transport: hc.Transport}
	}
	claudeProvider := claude.New(claude.Options{CacheDir: cacheDir})
	cfg := daemon.Config{
		Version:   version,
		StateDir:  stateDir,
		CacheDir:  cacheDir,
		Providers: []daemon.UsageSource{daemon.ClaudeSource{P: claudeProvider}},
		Limits:    []daemon.LimitsSpec{{ProviderID: claudeProvider.ID(), Fetcher: lim}},
		Data:      daemon.PokeSource{C: poke},
		Sprites:   sprites.New(sprites.Options{CacheDir: cacheDir, UserAgent: "poketokenbar-gnome/" + version, HTTPClient: hc}),
	}
	return cfg, poke, nil
}

func serve() int {
	if err := daemon.LowerPriority(); err != nil {
		log.Printf("cannot lower priority: %v", err)
	}
	cfg, poke, err := wiring(nil)
	if err != nil {
		b, _ := json.Marshal(map[string]string{"type": "fatal", "code": "stateDirUnavailable", "message": err.Error()})
		fmt.Println(string(b))
		return daemon.ExitStateDir
	}
	defer poke.Close()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	// A closed stdout must fail writes with EPIPE instead of killing the process before it flushes.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	return daemon.Serve(cfg, os.Stdin, os.Stdout, sig)
}

func snapshot() int {
	counter := &countingTransport{base: http.DefaultTransport, counts: map[string]int{}}
	if v := os.Getenv("PTB_SNAPSHOT_MAX_REQUESTS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			log.Printf("PTB_SNAPSHOT_MAX_REQUESTS must be a non-negative integer")
			return daemon.ExitUsage
		}
		counter.pokeCap = n
	}
	cfg, poke, err := wiring(&http.Client{Transport: counter})
	if err != nil {
		log.Printf("%v", err)
		return daemon.ExitStateDir
	}
	code := daemon.Snapshot(cfg, os.Stdout, 45*time.Second)
	poke.Close()
	log.Printf("requests: %s", counter.summary())
	return code
}

// countingTransport counts requests per host and can cap the PokéAPI ones.
type countingTransport struct {
	base    http.RoundTripper
	pokeCap int

	mu      sync.Mutex
	counts  map[string]int
	refused int
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := r.Context().Err(); err != nil {
		// The disk-only sprite check uses a cancelled context; nothing goes on the wire.
		return nil, err
	}
	host := r.URL.Hostname()
	c.mu.Lock()
	if c.pokeCap > 0 && strings.HasSuffix(host, "pokeapi.co") && c.pokeTotal() >= c.pokeCap {
		c.refused++
		c.mu.Unlock()
		return nil, fmt.Errorf("PokéAPI request cap (%d) reached", c.pokeCap)
	}
	c.counts[host]++
	c.mu.Unlock()
	return c.base.RoundTrip(r)
}

func (c *countingTransport) pokeTotal() int {
	n := 0
	for h, v := range c.counts {
		if strings.HasSuffix(h, "pokeapi.co") {
			n += v
		}
	}
	return n
}

func (c *countingTransport) summary() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var parts []string
	for h, v := range c.counts {
		parts = append(parts, fmt.Sprintf("%s=%d", h, v))
	}
	sort.Strings(parts)
	if c.refused > 0 {
		parts = append(parts, fmt.Sprintf("refusedByCap=%d", c.refused))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}
