package main

// A person who fixes the spec that failed an activation step asks for
// a retry with the annotation observatory.liken.sh/retry. The record of
// the session holds the spec that failed (records.go), so a step that
// ran again with it would fail the same way. A retry is the person
// saying that the specs are fixed, so the runner records the session
// again from the specs as they are, before any step runs again. A pod
// that the record built with the old spec no longer matches the record,
// and the step that runs again replaces it (ensure).

import (
	"context"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// recordAgain records the parts of the session that the reservation
// recorded, from the specs as they are now, and waits until the store
// holds the new record. The part of the observatory's server holds the
// set that the other reservations that hold the server recorded, as
// the StartSite step would, so a session that runs there keeps its
// devices. A retry of a deactivation step records nothing: the release
// stops what the session started.
func (r *runner) recordAgain(ctx context.Context) error {
	old := r.status.Session
	if old == nil || isDeactivation(r.status.Step) {
		return nil
	}
	if old.Observatory != "" {
		lock := r.o.siteLock(old.Observatory)
		if err := lock.acquire(ctx); err != nil {
			return err
		}
		defer lock.release()
	}
	t := r.o.stores.read(r.o.namespace)
	t.forget(r.name)
	t.applyRecords()
	next := &observatory.Session{Observatory: old.Observatory}
	if old.Observatory != "" {
		site := serverRef{observatory.ObservatoryKind, old.Observatory}
		if devices := t.devicesOn(site); len(devices) > 0 {
			next.Site = t.recordOf(site, devices)
		}
	}
	if len(old.Devices) > 0 {
		ref := serverRef{observatory.TelescopeKind, r.res.Spec.Telescope}
		next.Devices = t.recordOf(ref, t.devicesOn(ref))
		next.Trains, next.Tubes, next.Guiders = t.opticsOf(ref.name)
	}
	r.status.Session = next
	if err := r.o.send(ctx, nil, "recording the session again in the status of the Reservation "+r.name, func() error { return r.save(ctx) }); err != nil {
		return err
	}
	return r.o.waitFor(ctx, nil, func(t *tree) (bool, string, error) {
		res, ok := t.reservations[r.name]
		return !ok || equalJSON(res.Status.Session, next), "", nil
	})
}

// forget makes a tree that applies no record yet read as if a
// reservation had recorded nothing. A reservation whose StartSite step
// failed is forgotten too: that step never started the set it
// recorded, and its spec may be the one that a person fixed. Such a
// reservation records nothing besides the site, because its PowerOn
// step never ran.
func (t *tree) forget(name string) {
	for other, res := range t.reservations {
		if res.Status.Session != nil && (other == name || failedSite(res)) {
			copied := *res
			copied.Status.Session = nil
			t.reservations[other] = &copied
		}
	}
}
