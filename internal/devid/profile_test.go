package devid

import (
	"math/rand"
	"testing"
	"time"
)

func newTestRNG() *rand.Rand {
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}

func TestNewRandomProfileVariety(t *testing.T) {
	rng := newTestRNG()
	seen := map[uint32]bool{}
	for i := 0; i < 20; i++ {
		p := NewRandomProfile(rng)
		if p.ScreenW == 0 || p.ScreenH == 0 || p.Cores == 0 {
			t.Fatalf("invalid profile: %+v", p)
		}
		if p.EffType != "4g" {
			t.Fatalf("unexpected effType: %q", p.EffType)
		}
		seen[p.Seed] = true
	}
	if len(seen) < 10 {
		t.Fatalf("profile seeds not varied enough: %d unique in 20", len(seen))
	}
}
