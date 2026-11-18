package main

// A session's record is a copy of the specs that a reservation's
// session started with, in the reservation's status.session
// (observatory.Session). An edit to a spec during the session takes
// effect when the session ends, and the record is in the status so it
// survives the restart of every pod of the server.
//
// For each device the record has its UID, parents, driver, served
// owner, power, claim, and whether a Guider guides with it. For the
// telescope it has the observatory, the OpticalTrains and their
// OpticalTubes, the Guiders, the reservation's outbox (outbox.go), and
// the merged FITS keywords of each imaging camera (fitskeywords.go).
// The StartSite step records the observatory and the devices of its
// server, and the PowerOn step records the rest, each before it creates
// a pod. A person retries a failed activation step after fixing a spec,
// so recordAgain records both parts again from the current specs before
// the step runs. Nothing else changes the record.
//
// Reservations of several telescopes in one observatory share the
// observatory's server, and each reservation writes only its own
// status. So each reservation records the observatory's devices, and
// the server runs the union of the records of every reservation that
// holds it. A reservation that starts while another holds the server
// records the devices that the other recorded, so the server keeps its
// devices until the last of those reservations ends.
//
// While a session holds a server, applyRecords and tubeSpec replace the
// spec of each recorded object in the tree with its recorded spec, and
// the server runs only the devices that the record has (tree.server).
// So the steps, the runner of a Ready reservation, the locks, the
// guider, the power, the release, and the status all act on the
// recorded specs. The tree reads a spec as a person wrote it only to
// start a session, for a device that no session holds, for the status
// that reports where a device belongs (tree.declared), and for a tube's
// status.
//
// The record identifies each object by its UID. An object that a person
// deletes and creates again with the same name has a new UID, so it
// waits for the next session, as a Guider that the record does not have
// does (guidersOf). A deleted object leaves the session at once,
// because applyRecords applies a record only to an object that exists.
//
// A device's settings, the Observatory's location, and the procedures
// of every kind are not recorded, because a person changes them during
// a session on purpose, such as a camera's gain or a trigger's actions.
// The runner writes the settings from the spec again when a driver
// restarts.

import (
	"cmp"
	"context"
	"maps"
	"slices"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// recordOf returns the facts of each of a server's devices that a
// session records, keyed by device key. While a session holds the
// server, these are the recorded facts.
func (t *tree) recordOf(ref serverRef, devices []*device) map[string]observatory.SessionDevice {
	out := map[string]observatory.SessionDevice{}
	for _, d := range devices {
		s := d.object.Spec
		out[d.key()] = observatory.SessionDevice{UID: d.object.Metadata.UID, Telescope: s.Telescope, OpticalTrain: s.OpticalTrain,
			Observatory: s.Observatory, DeviceSpec: s.DeviceSpec, Guides: t.guides(ref, d)}
	}
	return out
}

// opticsOf returns the specs of a telescope's trains, their tubes, and
// its Guiders. While a session holds the telescope these are the
// recorded specs.
func (t *tree) opticsOf(telescope string) (map[string]observatory.SessionTrain, map[string]observatory.SessionTube, map[string]observatory.SessionGuider) {
	if r, ok := t.records[serverRef{observatory.TelescopeKind, telescope}]; ok {
		return r.Trains, r.Tubes, r.Guiders
	}
	trains, tubes, guiders := map[string]observatory.SessionTrain{}, map[string]observatory.SessionTube{}, map[string]observatory.SessionGuider{}
	for name, train := range t.trains {
		if train.Spec.Telescope == telescope {
			trains[name] = observatory.SessionTrain{UID: train.Metadata.UID, OpticalTrainSpec: train.Spec}
			if tube, ok := t.tubes[train.Spec.OpticalTube]; ok {
				tubes[tube.Metadata.Name] = observatory.SessionTube{UID: tube.Metadata.UID, OpticalTubeSpec: tube.Spec}
			}
		}
	}
	for _, guider := range t.guidersOf(telescope) {
		guiders[guider.Metadata.Name] = observatory.SessionGuider{UID: guider.Metadata.UID, GuiderSpec: guider.Spec}
	}
	return trains, tubes, guiders
}

// guidersOf returns the Guiders of a telescope, sorted by name. While a
// session holds the telescope these are only the Guiders that it
// recorded, with the recorded UID.
func (t *tree) guidersOf(telescope string) []*observatory.Guider {
	recorded, frozen := t.records[serverRef{observatory.TelescopeKind, telescope}]
	var out []*observatory.Guider
	for _, name := range slices.Sorted(maps.Keys(t.guiders)) {
		guider := t.guiders[name]
		r, ok := recorded.Guiders[name]
		if (frozen && ok && sameUID(r.UID, guider.Metadata)) || (!frozen && guider.Spec.Telescope == telescope) {
			out = append(out, guider)
		}
	}
	return out
}

// sameUID reports whether a record applies to an object. A record with
// no UID applies to any object of its name.
func sameUID(recorded string, m observatory.ObjectMeta) bool {
	return recorded == "" || recorded == m.UID
}

// applyRecords replaces the tree's objects with their recorded specs,
// for each reservation that holds its telescope.
//
// A record with an observatory also records that observatory's server,
// with no devices when the observatory had none. The server then counts
// as started, and a device created there waits for the next session.
// The server's record is the union of its holders' records. When two
// holders recorded the same device, the first record wins, and
// recordOrder puts a reservation whose StartSite step failed last,
// because that step never started its devices and a retry records them
// again. No device is in the records of two servers, because a step
// records only the devices that the tree places on its server, and the
// tree places a recorded device only on the server of its record
// (tree.server, forgotten).
func (t *tree) applyRecords() {
	t.records, t.recordedTubes, t.writtenTrains = map[serverRef]observatory.Session{}, map[string]observatory.SessionTube{}, map[string]*observatory.OpticalTrain{}
	for _, res := range recordOrder(t.reservations) {
		s := res.Status.Session
		if !holds(res) || s == nil {
			continue
		}
		scope := serverRef{observatory.TelescopeKind, res.Spec.Telescope}
		if len(s.Devices) > 0 {
			t.records[scope] = observatory.Session{Devices: s.Devices, Trains: s.Trains, Tubes: s.Tubes, Guiders: s.Guiders, Outbox: s.Outbox, FITSKeywords: s.FITSKeywords}
			t.freezeOptics(*s)
		}
		if telescope, ok := t.telescopes[scope.name]; ok && s.Observatory != "" && s.Observatory != telescope.Spec.Observatory {
			frozen := *telescope
			frozen.Spec.Observatory = s.Observatory
			t.telescopes[scope.name] = &frozen
		}
		if s.Observatory != "" {
			site := serverRef{observatory.ObservatoryKind, s.Observatory}
			union := t.records[site]
			if union.Devices == nil {
				union.Devices = map[string]observatory.SessionDevice{}
			}
			for key, d := range s.Site {
				if _, ok := union.Devices[key]; !ok {
					union.Devices[key] = d
				}
			}
			t.records[site] = union
		}
	}
	records := map[string]observatory.SessionDevice{}
	on := map[string]serverRef{}
	for _, ref := range slices.SortedFunc(maps.Keys(t.records), func(a, b serverRef) int { return cmp.Compare(a.String(), b.String()) }) {
		for key, d := range t.records[ref].Devices {
			records[key], on[key] = d, ref
		}
	}
	for i, d := range t.devices {
		r, ok := records[d.key()]
		if !ok || !sameUID(r.UID, d.object.Metadata) {
			continue
		}
		frozen := *d
		frozen.declared = &d.object.Spec
		frozen.record, frozen.recordedOn = &r, on[d.key()]
		frozen.object.Spec.Telescope, frozen.object.Spec.OpticalTrain, frozen.object.Spec.Observatory = r.Telescope, r.OpticalTrain, r.Observatory
		frozen.object.Spec.DeviceSpec = r.DeviceSpec
		t.devices[i] = &frozen
	}
}

// recordOrder returns the reservations sorted by name, with the ones
// whose StartSite step failed after the others.
func recordOrder(reservations map[string]*observatory.Reservation) []*observatory.Reservation {
	var first, last []*observatory.Reservation
	for _, name := range sortedNames(reservations) {
		if res := reservations[name]; failedSite(res) {
			last = append(last, res)
		} else {
			first = append(first, res)
		}
	}
	return append(first, last...)
}

// failedSite reports whether a reservation's StartSite step failed.
func failedSite(r *observatory.Reservation) bool {
	for _, s := range r.Status.Steps {
		if s.Name == observatory.StepStartSite {
			return s.State == observatory.StepFailed
		}
	}
	return false
}

// freezeOptics replaces each recorded train and Guider with a copy
// that has the recorded spec. A tube's object keeps its own spec, so
// its status shows the lengths that a person wrote, and tubeSpec reads
// the recorded spec from recordedTubes. writtenTrains keeps each train's
// own object for trainAsWritten.
func (t *tree) freezeOptics(r observatory.Session) {
	for name, spec := range r.Trains {
		if train, ok := t.trains[name]; ok && sameUID(spec.UID, train.Metadata) {
			frozen := *train
			frozen.Spec = spec.OpticalTrainSpec
			t.trains[name], t.writtenTrains[name] = &frozen, train
		}
	}
	for name, spec := range r.Tubes {
		t.recordedTubes[name] = spec
	}
	for name, spec := range r.Guiders {
		if guider, ok := t.guiders[name]; ok && sameUID(spec.UID, guider.Metadata) {
			frozen := *guider
			frozen.Spec = spec.GuiderSpec
			t.guiders[name] = &frozen
		}
	}
}

// tubeSpec returns the spec of an OpticalTube that the operator acts
// on, which is the recorded spec while a session holds the tube's
// telescope.
func (t *tree) tubeSpec(name string) (observatory.OpticalTubeSpec, bool) {
	tube, ok := t.tubes[name]
	if !ok {
		return observatory.OpticalTubeSpec{}, false
	}
	if spec, ok := t.recordedTubes[name]; ok && sameUID(spec.UID, tube.Metadata) {
		return spec.OpticalTubeSpec, true
	}
	return tube.Spec, true
}

// trainAsWritten returns an OpticalTrain with the spec that a person
// wrote, even when a session froze another spec for it.
func (t *tree) trainAsWritten(name string) (*observatory.OpticalTrain, bool) {
	if train, ok := t.writtenTrains[name]; ok {
		return train, true
	}
	train, ok := t.trains[name]
	return train, ok
}

// recordSession writes one part of the session's record into the
// reservation's status, and waits until the watch delivers it, so every
// tree that the step reads afterward has the record. fill sets the
// part, and recorded reports whether it is set already, so a step that
// runs again after an operator restart writes nothing.
func (r *runner) recordSession(ctx context.Context, w *stepWork, recorded func(*observatory.Session) bool, fill func(*observatory.Session)) error {
	if r.status.Session == nil {
		r.status.Session = &observatory.Session{}
	}
	if recorded(r.status.Session) {
		return nil
	}
	fill(r.status.Session)
	r.updateEditsPending(r.o.snapshot())
	// The step message changes in the same write, so the write is
	// visible in `kubectl get`.
	w.report("recording the devices that the session starts in status.session")
	if err := r.o.send(ctx, w.report, "recording the session in the status of the Reservation "+r.name, func() error { return r.save(ctx) }); err != nil {
		return err
	}
	return r.o.waitFor(ctx, nil, func(t *tree) (bool, string, error) {
		res, ok := t.reservations[r.name]
		return !ok || (res.Status.Session != nil && recorded(res.Status.Session)), "", nil
	})
}
