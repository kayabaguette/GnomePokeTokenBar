package companion

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
)

// Rng is the game's random source. Every roll is Next() % n, modulo bias included, as upstream.
type Rng interface{ Next() uint64 }

// SplitMix64 is the deterministic generator used by tests (upstream SeededRNG) and, with a zero
// seed replaced by the golden gamma, by profile generation (upstream ProfileRNG).
type SplitMix64 struct{ State uint64 }

// Next advances the generator and returns the next value.
func (r *SplitMix64) Next() uint64 {
	r.State += 0x9E3779B97F4A7C15
	z := r.State
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// profileRNG mirrors upstream ProfileRNG: SplitMix64 where seed 0 becomes 0x9E3779B97F4A7C15.
func profileRNG(seed uint64) *SplitMix64 {
	if seed == 0 {
		seed = 0x9E3779B97F4A7C15
	}
	return &SplitMix64{State: seed}
}

// cryptoRng is the production source, seeded per draw from crypto/rand.
type cryptoRng struct{}

func (cryptoRng) Next() uint64 {
	var b [8]byte
	_, _ = rand.Read(b[:]) // never fails since Go 1.24; it panics instead
	return binary.LittleEndian.Uint64(b[:])
}

// newUUID returns a random version-4 UUID. It never draws from the game Rng, like upstream UUID().
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// fnv1a64 is upstream PokemonProfileMigration.seed: a process-stable FNV-1a hash.
func fnv1a64(text string) uint64 {
	h := uint64(0xcbf29ce484222325)
	for i := 0; i < len(text); i++ {
		h = (h ^ uint64(text[i])) * 0x100000001B3
	}
	return h
}
