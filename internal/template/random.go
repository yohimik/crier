package template

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"sync"
)

// goldenGamma is the second PCG stream selector. It only has to be an odd
// constant; this is the golden-ratio one every implementation uses.
const goldenGamma = 0x9E3779B97F4A7C15

// Rand is the run's one source of randomness.
//
// A template that varies its accent colour or its gradient angle has to vary
// it the same way for every platform variant and every video frame, or a clip
// flickers and a post looks like a different post on each network. So there is
// one source per run, it is seeded explicitly, and the seed is logged: a run
// somebody liked can be reproduced with --render-seed.
type Rand struct {
	mu   sync.Mutex
	seed int64
	r    *rand.Rand
}

// NewRand builds a source from a seed. A zero seed draws one from the
// operating system, which is what makes each run different by default.
func NewRand(seed int64) *Rand {
	if seed == 0 {
		seed = randomSeed()
	}
	return &Rand{seed: seed, r: rand.New(rand.NewPCG(uint64(seed), goldenGamma))}
}

// randomSeed draws a seed that is worth logging: unpredictable, and small
// enough to type back in.
func randomSeed() int64 {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return 1
	}
	seed := int64(binary.LittleEndian.Uint64(b[:]) >> 1)
	if seed == 0 {
		seed = 1
	}
	return seed
}

// Seed is the value that reproduces this run.
func (s *Rand) Seed() int64 { return s.seed }

// IntN returns a number in [0,n).
func (s *Rand) IntN(n int) int {
	if n <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.r.IntN(n)
}

// Float64 returns a number in [0,1).
func (s *Rand) Float64() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.r.Float64()
}

// Choose picks one element, and reports whether there was one to pick.
func (s *Rand) Choose(n int) (int, bool) {
	if n <= 0 {
		return 0, false
	}
	return s.IntN(n), true
}

// randomFuncs are the template functions a source provides.
//
// They are in both the layout templates and the caption templates, because a
// post that varies its picture and not its words is only half varied.
func randomFuncs(s *Rand) map[string]any {
	return map[string]any{
		"randChoice": func(items ...any) (any, error) {
			flat := flatten(items)
			if len(flat) == 0 {
				return nil, fmt.Errorf("randChoice needs at least one item")
			}
			return flat[s.IntN(len(flat))], nil
		},
		"randInt": func(minimum, maximum int) (int, error) {
			if maximum < minimum {
				return 0, fmt.Errorf("randInt: %d is below the minimum %d", maximum, minimum)
			}
			if maximum == minimum {
				return minimum, nil
			}
			return minimum + s.IntN(maximum-minimum+1), nil
		},
		"randFloat": func(minimum, maximum float64) (float64, error) {
			if maximum < minimum {
				return 0, fmt.Errorf("randFloat: %v is below the minimum %v", maximum, minimum)
			}
			return minimum + s.Float64()*(maximum-minimum), nil
		},
		"randShuffle": func(items ...any) []any {
			flat := flatten(items)
			out := append([]any(nil), flat...)
			for i := len(out) - 1; i > 0; i-- {
				j := s.IntN(i + 1)
				out[i], out[j] = out[j], out[i]
			}
			return out
		},
		"randSeed": func() int64 { return s.Seed() },
	}
}

// flatten lets the random helpers be called either with a list — `randChoice
// .colours` — or with the items written out — `randChoice "#f00" "#0f0"`.
func flatten(items []any) []any {
	if len(items) != 1 {
		return items
	}
	switch t := items[0].(type) {
	case []any:
		return t
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out
	default:
		return items
	}
}
