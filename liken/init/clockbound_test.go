package main

import (
	"slices"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

func bound(text string) machine.OffsetBound {
	b, ok := machine.ParseOffsetBound(text)
	if !ok {
		panic("not a bound: " + text)
	}
	return b
}

// publishes feeds the hold one bound per poll, starting from the
// published bound, and answers the bound published after each poll.
func publishes(published string, polls ...string) []string {
	h := &boundHold{}
	var after []string
	for _, p := range polls {
		if next, ok := h.next(published, bound(p)); ok {
			published = next
		}
		after = append(after, published)
	}
	return after
}

func TestBoundHold(t *testing.T) {
	cases := map[string]struct {
		published string
		polls     []string
		want      []string
	}{
		"a worse bound publishes at once": {
			"±5ms", []string{"±128ms"}, []string{"±128ms"},
		},
		"a better bound publishes at the third poll": {
			"±128ms", []string{"±5ms", "±5ms", "±5ms"}, []string{"±128ms", "±128ms", "±5ms"},
		},
		"a poll at the published bound restarts the count": {
			"±128ms", []string{"±5ms", "±128ms", "±5ms", "±5ms"}, []string{"±128ms", "±128ms", "±128ms", "±128ms"},
		},
		"the streak publishes its worst bound": {
			"±1s", []string{"±1ms", "±25ms", "±5ms"}, []string{"±1s", "±1s", "±25ms"},
		},
		"the same bound publishes nothing": {
			"±5ms", []string{"±5ms", "±5ms", "±5ms", "±5ms"}, []string{"±5ms", "±5ms", "±5ms", "±5ms"},
		},
		"no published bound takes the first poll": {
			"", []string{"±1ms"}, []string{"±1ms"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := publishes(tc.published, tc.polls...); !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
