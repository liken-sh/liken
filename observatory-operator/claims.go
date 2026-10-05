package main

// One telescope serves one reservation at a time. A second reservation
// of a telescope waits in its Wait step until the first one is
// Released, and then the waiting reservations take the telescope in a
// fixed order: the earliest spec.start first, a reservation with no
// start at its creation time, and then by name. So the same
// reservations always take the telescope in the same order.
//
// The operator runs as one copy (deploy/operator.yaml), so the holder
// of each telescope is kept in memory. The status is the record: a
// reservation whose Wait step is Done holds its telescope until it is
// Released, and a new copy of the operator reads the holders from the
// status of each reservation before any runner starts.

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type claims struct {
	mu sync.Mutex
	// holders maps each telescope to the UID and name of the
	// reservation that holds it.
	holders map[string]holder
}

type holder struct {
	uid, name string
}

func newClaims() *claims { return &claims{holders: map[string]holder{}} }

// holds reports whether a reservation's steps show that it took its
// telescope and has not given it back.
func holds(r *observatory.Reservation) bool {
	if r.Status.Phase == observatory.ReservationReleased {
		return false
	}
	for _, s := range r.Status.Steps {
		if s.Name == observatory.StepWait {
			return s.State == observatory.StepDone
		}
	}
	return false
}

// seed reads the holders from the status of each reservation.
func (c *claims) seed(t *tree) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range sortedReservations(t) {
		if _, taken := c.holders[r.Spec.Telescope]; holds(r) && !taken {
			c.holders[r.Spec.Telescope] = holder{r.Metadata.UID, r.Metadata.Name}
		}
	}
}

// holderOf answers the name of the reservation that holds a telescope.
func (c *claims) holderOf(telescope string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.holders[telescope]
	return h.name, ok
}

// held answers the set of telescopes that a reservation holds.
func (c *claims) held() map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]bool{}
	for telescope := range c.holders {
		out[telescope] = true
	}
	return out
}

// take gives a reservation its telescope when no other reservation
// holds it and no waiting reservation comes before it. It answers the
// name of the reservation it waits for when it cannot take it.
func (c *claims) take(t *tree, r *observatory.Reservation, now time.Time) (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	telescope := r.Spec.Telescope
	if h, ok := c.holders[telescope]; ok {
		return h.uid == r.Metadata.UID, h.name
	}
	for _, other := range sortedReservations(t) {
		if other.Spec.Telescope != telescope {
			continue
		}
		if other.Metadata.UID == r.Metadata.UID {
			break
		}
		if waiting(other, now) {
			return false, other.Metadata.Name
		}
	}
	c.holders[telescope] = holder{r.Metadata.UID, r.Metadata.Name}
	return true, ""
}

// waiting reports whether a reservation waits for its telescope now:
// its start has come, it has not ended, and it holds nothing yet.
func waiting(r *observatory.Reservation, now time.Time) bool {
	if r.Metadata.DeletionTimestamp != nil || holds(r) || r.Status.Phase == observatory.ReservationReleased {
		return false
	}
	if r.Spec.End != nil && !now.Before(*r.Spec.End) {
		return false
	}
	return r.Spec.Start == nil || !now.Before(*r.Spec.Start)
}

// release gives a telescope back.
func (c *claims) release(r *observatory.Reservation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h, ok := c.holders[r.Spec.Telescope]; ok && h.uid == r.Metadata.UID {
		delete(c.holders, r.Spec.Telescope)
	}
}

// forgetGone gives back each telescope whose holder no longer exists,
// such as a reservation whose finalizer a person removed.
func (c *claims) forgetGone(t *tree) {
	c.mu.Lock()
	defer c.mu.Unlock()
	present := map[string]bool{}
	for _, r := range t.reservations {
		present[r.Metadata.UID] = true
	}
	for telescope, h := range c.holders {
		if !present[h.uid] {
			delete(c.holders, telescope)
		}
	}
}

// sortedReservations answers the reservations in the order in which
// they take a telescope.
func sortedReservations(t *tree) []*observatory.Reservation {
	var out []*observatory.Reservation
	for _, r := range t.reservations {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b *observatory.Reservation) int {
		if c := startOf(a).Compare(startOf(b)); c != 0 {
			return c
		}
		if c := created(a).Compare(created(b)); c != 0 {
			return c
		}
		return strings.Compare(a.Metadata.Name, b.Metadata.Name)
	})
	return out
}

func startOf(r *observatory.Reservation) time.Time {
	if r.Spec.Start != nil {
		return *r.Spec.Start
	}
	return created(r)
}

func created(r *observatory.Reservation) time.Time {
	if r.Metadata.CreationTimestamp != nil {
		return *r.Metadata.CreationTimestamp
	}
	return time.Time{}
}
