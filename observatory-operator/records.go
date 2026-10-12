package main

// A session freezes what it started. Before a reservation's steps start
// a server, the operator records in the API the facts that the session
// acts on: for each device, its parents, its driver, its served owner,
// its power, its claim, and whether a Guider guides with it, and for a
// telescope, its observatory, its OpticalTrains, the OpticalTubes of
// those trains, its Guiders, the reservation's outbox (outbox.go), and
// the merged FITS keywords of each imaging camera (fitskeywords.go).
// The record is in the status of the reservation whose session it is
// (observatory.Session), so it outlives every pod of the server. The
// StartSite step records the observatory and the devices of its server,
// and the PowerOn step records the rest, each once and before it
// creates a pod. A retry of a failed activation step is a person saying
// that they fixed a spec, so it records both parts again from the specs
// as they are then, before the step runs again (recordAgain). Nothing
// else changes the record. Reservations of several
// telescopes in one observatory share the observatory's server, and
// each reservation is the only writer of its own status, so each one
// records the observatory's devices as its StartSite step found them.
// A reservation that starts while another holds the server records the
// set that the other one recorded, and the tree reads the union of the
// records of every reservation that holds the server. So the record
// stays while any of them holds it, in whatever order they end.
//
// While a session holds a server, the record alone decides which
// devices the server runs (tree.server), and the tree reads each device
// that the record names with the recorded facts in its spec, each held
// Telescope with the recorded observatory, each recorded train and
// Guider with its recorded spec, and each recorded tube's optics from
// the record (applyRecords, tubeSpec). The record names each object by
// its UID too, so an object that a person deletes and creates again
// with the same name is not the one that the session started. Such an
// object, and a Guider that the record does not name, waits for the
// next session (guidersOf). So every step, the runner of a Ready reservation, the
// locks, the guider, the power, the release, and the status lists act
// on what the session started, and a spec edit during the session takes
// effect when the session ends, as a move does (tree.server). The spec
// is read as a person wrote it only to start a session, for a device
// that no session holds, for the status that says where a device goes
// (tree.declared), and for a tube's status. A delete takes effect at
// once: the record freezes only the objects that still exist.
//
// The settings of a device, the Observatory's location, and the
// procedures of every kind are not frozen. A person changes them during
// a session on purpose, such as a camera's gain or a trigger's
// actions, and the runner writes the settings again from the spec when
// a driver comes back.

import (
	"cmp"
	"context"
	"maps"
	"slices"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// recordOf answers the record of a server's devices: the facts that
// the tree reads now, which for a server that a session holds are the
// recorded ones.
func (t *tree) recordOf(ref serverRef, devices []*device) map[string]observatory.SessionDevice {
	out := map[string]observatory.SessionDevice{}
	for _, d := range devices {
		s := d.object.Spec
		out[d.key()] = observatory.SessionDevice{UID: d.object.Metadata.UID, Telescope: s.Telescope, OpticalTrain: s.OpticalTrain,
			Observatory: s.Observatory, DeviceSpec: s.DeviceSpec, Guides: t.guides(ref, d)}
	}
	return out
}

// opticsOf answers the specs of a telescope's trains, of their tubes,
// and of its Guiders: the recorded ones while a session holds the
// telescope, and the ones whose spec names the telescope otherwise.
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

// guidersOf answers the Guiders of a telescope, sorted by name: the
// Guiders that a session recorded while one holds the telescope, and
// every Guider whose spec names it otherwise. A Guider that a person
// created again during the session is not the recorded one.
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

// sameUID reports whether a record applies to an object: the record
// names the object's UID, or no UID.
func sameUID(recorded string, m observatory.ObjectMeta) bool {
	return recorded == "" || recorded == m.UID
}

// applyRecords reads the record of each reservation that holds its
// telescope, and gives each held Telescope its recorded observatory,
// each recorded train and Guider its recorded spec, and each device
// that a record names its recorded spec. The record of a reservation
// that no longer holds its telescope freezes nothing.
//
// A record whose observatory is set records the observatory's server,
// with no devices when its observatory had none, so the server is
// started and a device created there waits for the next session. The
// server's record is the union of its holders' records, and where two
// of them record one device, the record of a reservation whose
// StartSite step failed gives way: that step never started its set,
// and a retry records it again. No two servers' records name one
// device: a step records only the devices that the tree places on its
// server, and the tree places a recorded device only where its record
// names it (tree.server, forgotten).
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

// recordOrder answers the reservations sorted by name, with each one
// whose StartSite step failed after the others (applyRecords).
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

// freezeOptics gives each train and Guider that a record names its
// recorded spec, and keeps the recorded spec of each tube for tubeSpec.
// A train, a tube, or a Guider that a person created again during the
// session keeps its own spec. The tube's object keeps its spec, so its
// status shows the lengths that a person wrote, and writtenTrains
// keeps each train's object as a person wrote it (trainAsWritten).
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

// tubeSpec answers the spec of an OpticalTube that the operator acts
// on: the recorded one while a session holds the tube's telescope.
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

// trainAsWritten answers an OpticalTrain as a person wrote it, whether
// or not a session froze its spec.
func (t *tree) trainAsWritten(name string) (*observatory.OpticalTrain, bool) {
	if train, ok := t.writtenTrains[name]; ok {
		return train, true
	}
	train, ok := t.trains[name]
	return train, ok
}

// recordSession writes a part of the session's record into the
// reservation's status, and waits until the store holds it, so each
// tree that the step reads after it holds the record. fill sets the
// part on the record. A step that runs again after a restart finds its
// part recorded and writes nothing, because the record holds what the
// session started with. A retry records the part again before the step
// runs again (recordAgain), so that step finds it recorded too. The
// write that the report sends
// carries the record, and send writes it again when that write failed.
func (r *runner) recordSession(ctx context.Context, w *stepWork, recorded func(*observatory.Session) bool, fill func(*observatory.Session)) error {
	if r.status.Session == nil {
		r.status.Session = &observatory.Session{}
	}
	if recorded(r.status.Session) {
		return nil
	}
	fill(r.status.Session)
	r.judgeSession(r.o.snapshot())
	// The step's summary changes in the same write, so the write shows
	// in `kubectl get` as each status write does.
	w.report("recording the devices that the session starts in status.session")
	if err := r.o.send(ctx, w.report, "recording the session in the status of the Reservation "+r.name, func() error { return r.save(ctx) }); err != nil {
		return err
	}
	return r.o.waitFor(ctx, nil, func(t *tree) (bool, string, error) {
		res, ok := t.reservations[r.name]
		return !ok || (res.Status.Session != nil && recorded(res.Status.Session)), "", nil
	})
}
