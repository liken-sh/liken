package main

// The horizon filter of plan 04. A target is not eligible while it
// stands below the horizon of the reservation's telescope: the
// Horizon that the Telescope's spec.horizon names, or 0° when it names
// none. The runner checks before it claims a target and before each
// frame, so a target that sets during its sequences is released before
// the next frame, the way plan 04 plans one exposure at a time.
//
// The rise of a target is a clock time, so when no target is eligible,
// the runner waits until the first rise of a target below the horizon,
// or until a change wakes it, and checks again. It computes no
// altitude at any other time.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// skyLimit is the part of the sky a reservation's telescope can image.
type skyLimit struct {
	site sky.Site
	// known is false when the stores hold no Observatory of the
	// session, so no altitude can be computed, and the filter passes
	// every target.
	known bool
	// profile is the Horizon's points, and nil for a flat horizon at 0°.
	profile observatory.Profile
	// about names the horizon in a message, and note says why it is
	// flat, when it is.
	about, note string
}

// skyLimit reads the site from the session's Observatory, and the
// horizon from the Horizon that the telescope's spec.horizon names
// now. The horizon is read from the Telescope and not from the
// session, because a horizon blocks no device, and an edit of it
// applies to the next check.
func (r *runner) skyLimit() skyLimit {
	stores := r.o.stores.collections
	site, known := informer.Cached[observatory.Observatory](stores[observatory.ObservatoryKind.Name].View(),
		r.namespace+"/"+r.res.Status.Session.Observatory)
	if !known {
		return skyLimit{}
	}
	telescopeName := r.res.Spec.Telescope
	const flat = "so the operator counts its horizon as 0° at every azimuth."
	limit := skyLimit{
		site:  sky.Site{Latitude: site.Spec.Location.Latitude, Longitude: site.Spec.Location.Longitude},
		known: true,
		about: "the horizon of telescope " + telescopeName,
		note:  fmt.Sprintf("Telescope %s names no Horizon, %s", telescopeName, flat),
	}
	telescope, found := informer.Cached[observatory.Telescope](stores[observatory.TelescopeKind.Name].View(), r.namespace+"/"+telescopeName)
	if !found || telescope.Spec.Horizon == "" {
		return limit
	}
	name := telescope.Spec.Horizon
	horizon, found := informer.Cached[observatory.Horizon](stores[observatory.HorizonKind.Name].View(), r.namespace+"/"+name)
	if !found {
		limit.note = fmt.Sprintf("The Horizon %s that telescope %s names does not exist, %s", name, telescopeName, flat)
		return limit
	}
	// The CRD's rules refuse a profile that does not parse, so an error
	// here is a profile written before the rules, and it counts as flat.
	profile, err := observatory.ParseProfile(horizon.Spec.Profile)
	if err != nil {
		limit.note = fmt.Sprintf("The Horizon %s that telescope %s names does not read (%v), %s", name, telescopeName, err, flat)
		return limit
	}
	limit.profile = profile
	limit.about, limit.note = fmt.Sprintf("the Horizon %s of telescope %s", name, telescopeName), ""
	return limit
}

// below says that a target is below the horizon: why, for the Imaging
// condition's message, and when it rises, zero when not within a day.
type below struct {
	message string
	rises   time.Time
}

// check answers whether a target is below the horizon at now, and nil
// when it can be imaged. A target whose coordinates do not read passes,
// so that its slew fails and says why.
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
	b := &below{message: fmt.Sprintf("%s is at %.1f° altitude and %.1f° azimuth, below %s, which is at %.1f° there.",
		t.Spec.Object, at.Altitude, at.Azimuth, l.about, limit)}
	if l.note != "" {
		b.message += " " + l.note
	}
	rises, ok := sky.NextAbove(position, l.site, now, l.profile.Altitude)
	if !ok {
		b.message += " It does not rise above it within a day."
		return b
	}
	b.rises = rises
	b.message += " It rises above it at " + rises.UTC().Format(time.RFC3339) + "."
	return b
}

// markBelow sets Imaging False with BelowHorizon on a target that no
// other reservation images, and releases it when this reservation held
// it. A target that already says BelowHorizon is left alone, so the
// runner writes once for each time a target goes below the horizon,
// and the message keeps the altitude of that moment.
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

// riseWait answers how long an idle runner waits with no change: until
// the first rise of a target below the horizon, or forever.
func (r *runner) riseWait() time.Duration {
	if r.nextRise.IsZero() {
		return forever
	}
	return max(time.Until(r.nextRise), 0)
}

// noteRise keeps the first rise of the targets below the horizon.
func (r *runner) noteRise(b *below) {
	if !b.rises.IsZero() && (r.nextRise.IsZero() || b.rises.Before(r.nextRise)) {
		r.nextRise = b.rises
	}
}
