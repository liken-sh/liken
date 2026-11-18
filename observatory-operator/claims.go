package main

// A telescope serves one reservation at a time. A second reservation
// for the same telescope waits in its Wait step until the first is
// Released. The waiting reservations then take the telescope in order
// of spec.start, then creation time, then name. A reservation with no
// spec.start counts as starting when it was created.
//
// Only the copy of the operator that holds the Lease (leader.go) acts,
// so the holder of each telescope can be kept in memory. The status
// is the durable record: a reservation whose Wait step is Done holds
// its telescope until it is Released. A copy that takes the Lease
// calls load to rebuild the holders before any runner starts.

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// claims records which reservation holds each telescope.
type claims struct {
	mu sync.Mutex
	// holders maps a telescope's name to the UID and name of the
	// reservation that holds it.
	holders map[string]holder
}

type holder struct {
	uid, name string
}

func newClaims() *claims { return &claims{holders: map[string]holder{}} }

// holds reports whether a reservation's status shows that it took its
// telescope and has not released it.
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

// load loads the holders from the status of every reservation. If
// two reservations of one telescope both show a hold, the first in
// sorted order is the holder.
func (c *claims) load(t *tree) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range sortedReservations(t) {
		if _, taken := c.holders[r.Spec.Telescope]; holds(r) && !taken {
			c.holders[r.Spec.Telescope] = holder{r.Metadata.UID, r.Metadata.Name}
		}
	}
}

func (c *claims) holderOf(telescope string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.holders[telescope]
	return h.name, ok
}

func (c *claims) held() map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]bool{}
	for telescope := range c.holders {
		out[telescope] = true
	}
	return out
}

// take makes r the holder of its telescope when no other reservation
// holds it and no waiting reservation comes before r. It returns true
// when r holds the telescope. Otherwise it returns false and the name
// of the reservation that r waits for.
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

// waiting reports whether r is waiting for its telescope at now: it is
// not deleted or Released, it holds nothing yet, its start time has
// passed, and its end time has not.
func waiting(r *observatory.Reservation, now time.Time) bool {
	if r.Metadata.DeletionTimestamp != nil || holds(r) || r.Status.Phase == observatory.ReservationReleased {
		return false
	}
	if r.Spec.End != nil && !now.Before(*r.Spec.End) {
		return false
	}
	return r.Spec.Start == nil || !now.Before(*r.Spec.Start)
}

// release removes r as the holder of its telescope. It does nothing
// if another reservation is the holder.
func (c *claims) release(r *observatory.Reservation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h, ok := c.holders[r.Spec.Telescope]; ok && h.uid == r.Metadata.UID {
		delete(c.holders, r.Spec.Telescope)
	}
}

// forgetGone removes each holder whose reservation no longer exists,
// such as one that a person deleted by removing its finalizer.
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

// sortedReservations returns the reservations in the order in which
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
