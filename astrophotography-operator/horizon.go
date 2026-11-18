package main

// A runner does not image a target whose altitude is at or below the
// horizon of the reservation's telescope. That horizon is the Horizon
// named by the Telescope's spec.horizon, or 0° at every azimuth when
// spec.horizon is empty. The runner checks before it claims a target and
// before each frame, so a target that sets is released before its next
// frame.
//
// When no target can be imaged, the runner waits until the earliest
// rise among the targets below the horizon, or until a watched object
// changes, and then checks again.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// skyLimit is the site and the horizon profile of a reservation's
// telescope.
type skyLimit struct {
	site sky.Site
	// known is false when the cache has no Observatory for the session.
	// Without a site the altitude cannot be computed, so check passes
	// every target.
	known bool
	// profile is nil for a flat horizon at 0°.
	profile observatory.Profile
	// about names the horizon in the Imaging message. note explains why
	// the horizon is flat, and is empty when it is not.
	about, note string
}

// skyLimit reads the site from the session's Observatory, and the
// horizon from the Telescope's current spec.horizon. The reservation's
// session records only what the running devices depend on. The horizon
// changes no device, so an edit to it applies at the next check.
func (r *runner) skyLimit() skyLimit {
	stores := r.o.stores.collections
	site, known := informer.Cached[observatory.Observatory](stores[observatory.ObservatoryKind.Name].View(),
		r.namespace+"/"+r.res.Status.Session.Observatory)
	if !known {
		return skyLimit{}
	}
	telescopeName := r.res.Spec.Telescope
	const flat = "so the operator uses a horizon of 0° at every azimuth"
	limit := skyLimit{
		site:  sky.Site{Latitude: site.Spec.Location.Latitude, Longitude: site.Spec.Location.Longitude},
		known: true,
		about: "the horizon of telescope " + telescopeName,
		note:  fmt.Sprintf("Telescope %s has no spec.horizon, %s", telescopeName, flat),
	}
	telescope, found := informer.Cached[observatory.Telescope](stores[observatory.TelescopeKind.Name].View(), r.namespace+"/"+telescopeName)
	if !found || telescope.Spec.Horizon == "" {
		return limit
	}
	name := telescope.Spec.Horizon
	horizon, found := informer.Cached[observatory.Horizon](stores[observatory.HorizonKind.Name].View(), r.namespace+"/"+name)
	if !found {
		limit.note = fmt.Sprintf("The Horizon %s that telescope %s refers to does not exist, %s", name, telescopeName, flat)
		return limit
	}
	// The CRD rejects a profile that does not parse, so this error means
	// the Horizon was created before the CRD had that rule.
	profile, err := observatory.ParseProfile(horizon.Spec.Profile)
	if err != nil {
		limit.note = fmt.Sprintf("The Horizon %s that telescope %s refers to does not parse, %s: %v", name, telescopeName, flat, err)
		return limit
	}
	limit.profile = profile
	limit.about, limit.note = fmt.Sprintf("the Horizon %s of telescope %s", name, telescopeName), ""
	return limit
}

// below is the result of a check that finds a target at or below the
// horizon. message is for the Imaging condition. rises is when the
// target next rises above the horizon, and is zero when it does not
// rise within a day.
type below struct {
	message string
	rises   time.Time
}

// check returns nil when the target is above the horizon at now. A
// target whose coordinates do not parse passes, so that its slew fails
// and reports the parse error.
func (l skyLimit) check(t *astrophotography.Target, now time.Time) *below {
	position, err := targetPosition(t)
	if !l.known || err != nil {
		return nil
	}
	at := sky.ToHorizontal(sky.ToDate(position, now), l.site, now)
	limit := l.profile.Altitude(at.Azimuth)
	if at.Altitude > limit {
		return nil
	}
	b := &below{message: fmt.Sprintf("%s is at %.1f° altitude and %.1f° azimuth, below %s, which is at %.1f° there",
		t.Spec.Object, at.Altitude, at.Azimuth, l.about, limit)}
	rises, ok := sky.NextAbove(position, l.site, now, l.profile.Altitude)
	if ok {
		b.rises = rises
		b.message += ", and rises above it at " + rises.UTC().Format(time.RFC3339)
	} else {
		b.message += ", and does not rise above it within a day"
	}
	if l.note != "" {
		b.message += ". " + l.note
	}
	return b
}

// markBelow sets Imaging to False with reason BelowHorizon on a target
// that no other reservation has claimed, and releases the target when
// this reservation claimed it. It skips a target whose reason is already
// BelowHorizon, so the runner writes once each time a target goes below
// the horizon, and the message keeps the altitude from that moment.
func (r *runner) markBelow(t *astrophotography.Target, b *below) error {
	_, err := r.o.writeTarget(t, func(fresh *astrophotography.Target, change *statusChange) bool {
		c, _ := conditions.Find(fresh.Status.Conditions, astrophotography.ConditionImaging)
		if c.Reason == astrophotography.ReasonBelowHorizon || (fresh.Status.Reservation != "" && fresh.Status.Reservation != r.name) {
			return false
		}
		fresh.Status.Reservation, fresh.Status.Exposure = "", nil
		change.setImaging(fresh, false, astrophotography.ReasonBelowHorizon, fmt.Sprintf("Reservation %s: %s", r.name, b.message))
		return true
	})
	return err
}

// riseWait returns how long an idle runner waits when nothing changes:
// until the earliest rise that noteRise recorded, or forever when there
// is none.
func (r *runner) riseWait() time.Duration {
	if r.nextRise.IsZero() {
		return forever
	}
	return max(time.Until(r.nextRise), 0)
}

// noteRise records b's rise time when it is the earliest so far.
func (r *runner) noteRise(b *below) {
	if !b.rises.IsZero() && (r.nextRise.IsZero() || b.rises.Before(r.nextRise)) {
		r.nextRise = b.rises
	}
}
