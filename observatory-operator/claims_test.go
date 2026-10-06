package main

// The order in which waiting reservations take a free telescope. The
// runners of two waiting reservations call take in whatever order the
// scheduler runs them, so these cases call it directly.

import (
	"testing"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

var takeNow = time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)

// waiter builds a reservation of the east telescope that starts some
// minutes before takeNow, and ends some minutes after it when ends is
// not zero.
func waiter(name string, started, ends int) *observatory.Reservation {
	start := takeNow.Add(-time.Duration(started) * time.Minute)
	r := &observatory.Reservation{}
	r.Metadata.Name, r.Metadata.UID = name, "uid-"+name
	r.Spec.Telescope, r.Spec.Start = "east", &start
	if ends != 0 {
		end := takeNow.Add(time.Duration(ends) * time.Minute)
		r.Spec.End = &end
	}
	return r
}

func TestAFreeTelescopeGoesToTheEarliestWaiter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		first, taker *observatory.Reservation
		took         bool
		waitsFor     string
	}{
		{"the earliest takes it", waiter("first", 2, 0), waiter("first", 2, 0), true, ""},
		{"a later one waits for the earliest", waiter("first", 2, 0), waiter("second", 1, 0), false, "first"},
		{"a later one waits for an earlier one that ends later", waiter("first", 2, 30), waiter("second", 1, 0), false, "first"},
		{"a later one takes it from an earlier one that ended", waiter("first", 2, -1), waiter("second", 1, 0), true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tree := &tree{reservations: map[string]*observatory.Reservation{c.first.Metadata.Name: c.first, c.taker.Metadata.Name: c.taker}}
			took, other := newClaims().take(tree, c.taker, takeNow)
			if took != c.took || other != c.waitsFor {
				t.Errorf("take = %v, %q, want %v, %q", took, other, c.took, c.waitsFor)
			}
		})
	}
}
