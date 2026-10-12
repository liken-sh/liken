package main

// A server pod that is gone while a reservation is Ready is created
// again with every device that its session started.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// A server pod that is gone while Ready, such as one a node's eviction
// deleted, is created again with every device. The new pod lists the
// devices itself.
func TestAServerCreatedAgainStartsWithEveryDevice(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		before, _ := w.api.object(podsCollection, "east-telescope")
		w.api.deleteNamed(podsCollection, "east-telescope")
		w.until(time.Minute, "the server is not back", func() bool {
			p, ok := w.api.object(podsCollection, "east-telescope")
			return ok && podUID(p) != podUID(before) && len(w.indi.connected("east-telescope")) == 11
		})
		after, _ := w.api.object(podsCollection, "east-telescope")
		if !slices.Equal(links(after), links(before)) {
			t.Errorf("drivers = %v, want %v", links(after), links(before))
		}
	})
}
