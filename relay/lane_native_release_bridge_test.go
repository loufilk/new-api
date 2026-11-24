package relay

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

func TestLaneNativeReleaseRouteConfigDeterministic(t *testing.T) {
	// Exercise nested maps above the codec's small-map cutoff. The pin must
	// not vary with Go's insertion/iteration order, including after upgrades
	// of the repository's JSON codec.
	makeParam := func(reverse bool) map[string]any {
		nested := make(map[string]any, 24)
		for i := range 24 {
			n := i
			if reverse {
				n = 23 - i
			}
			nested[fmt.Sprintf("field%d", n)] = n
		}
		return map[string]any{"max_completion_tokens": 6, "nested": nested}
	}
	key := [32]byte{6}
	s := dto.ChannelSettings{}
	o := dto.ChannelOtherSettings{}
	forward, reverse := makeParam(false), makeParam(true)
	want, ok := laneNativeReleaseRouteConfig(key, s, o, forward)
	if !ok || want == ([32]byte{}) {
		t.Fatal("cannot pin bounded route config")
	}
	for range 64 {
		got, valid := laneNativeReleaseRouteConfig(key, s, o, reverse)
		if !valid || got != want {
			t.Fatal("map order altered frozen route config")
		}
	}
	reverse["max_completion_tokens"] = 7
	changed, ok := laneNativeReleaseRouteConfig(key, s, o, reverse)
	if !ok || changed == want {
		t.Fatal("an actual override change must invalidate the pin")
	}
}
