package main

// A session freezes what it started. When a reservation's steps start
// a server, or a device's pod, the operator records in the API the
// facts that the session acts on: for each device, its parents, its
// driver, its served owner, its power, its claim, and whether a
// Guider guides with it, and for a telescope, its observatory, its
// OpticalTrains, the OpticalTubes of those trains, and its Guiders.
// The record is an annotation on the server's pod and on the pod of
// each device that the server runs, and each of these pods carries the
// whole record. So the record survives an eviction while one pod of
// the server is left, and the runner creates the other pods again from
// it.
//
// While a session holds a server, the tree reads each device that the
// record names with the recorded facts in its spec, each held
// Telescope with the recorded observatory, each recorded train and
// Guider with its recorded spec, and each recorded tube's optics from
// the record (applyRecords, tubeSpec). A Guider that the record does
// not name is not the telescope's Guider during the session
// (guidersOf). So every step, the runner of a Ready reservation, the
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
	"encoding/json"
	"maps"
	"slices"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// annotationSession holds a sessionRecord as JSON on a server's pod and
// on each device's pod.
const annotationSession = Group + "/session"

// sessionRecord is what a session recorded about one server.
type sessionRecord struct {
	// Observatory is the observatory of the telescope whose server the
	// pod belongs to, or "" on the pods of an observatory's server.
	Observatory string `json:"observatory,omitempty"`
	// Devices holds each device's record by the device's key, such as
	// Camera/east-main.
	Devices map[string]deviceRecord `json:"devices"`
	// Trains, Tubes, and Guiders hold the spec of each OpticalTrain and
	// Guider of the telescope, and of each OpticalTube of those trains,
	// by name.
	Trains  map[string]observatory.OpticalTrainSpec `json:"trains,omitempty"`
	Tubes   map[string]observatory.OpticalTubeSpec  `json:"tubes,omitempty"`
	Guiders map[string]observatory.GuiderSpec       `json:"guiders,omitempty"`
}

// deviceRecord is the spec of one device as its session started it.
// UID tells the device from one that a person created again with the
// same name during the session, which the session did not start.
type deviceRecord struct {
	UID          string `json:"uid,omitempty"`
	Telescope    string `json:"telescope,omitempty"`
	OpticalTrain string `json:"opticalTrain,omitempty"`
	Observatory  string `json:"observatory,omitempty"`
	observatory.DeviceSpec
	Guides bool `json:"guides,omitempty"`
}

// recordOf answers the record that each pod of a server carries: the
// facts that the tree reads now, which for a server that a session
// holds are the recorded ones. devices is the server's set of devices.
func (t *tree) recordOf(ref serverRef, devices []*device) string {
	out := sessionRecord{Devices: map[string]deviceRecord{}}
	for _, d := range devices {
		s := d.object.Spec
		out.Devices[d.key()] = deviceRecord{UID: d.object.Metadata.UID, Telescope: s.Telescope, OpticalTrain: s.OpticalTrain,
			Observatory: s.Observatory, DeviceSpec: s.DeviceSpec, Guides: t.guides(ref, d)}
	}
	if telescope, ok := t.telescopes[ref.name]; ok && ref.kind == observatory.TelescopeKind {
		out.Observatory = telescope.Spec.Observatory
		out.Trains, out.Tubes, out.Guiders = t.opticsOf(ref.name)
	}
	body, _ := json.Marshal(out)
	return string(body)
}

// opticsOf answers the specs of a telescope's trains, of their tubes,
// and of its Guiders: the recorded ones while a session holds the
// telescope, and the ones whose spec names the telescope otherwise.
func (t *tree) opticsOf(telescope string) (map[string]observatory.OpticalTrainSpec, map[string]observatory.OpticalTubeSpec, map[string]observatory.GuiderSpec) {
	if r, ok := t.records[serverRef{observatory.TelescopeKind, telescope}]; ok {
		return r.Trains, r.Tubes, r.Guiders
	}
	trains, tubes, guiders := map[string]observatory.OpticalTrainSpec{}, map[string]observatory.OpticalTubeSpec{}, map[string]observatory.GuiderSpec{}
	for name, train := range t.trains {
		if train.Spec.Telescope == telescope {
			trains[name] = train.Spec
			if tube, ok := t.tubes[train.Spec.OpticalTube]; ok {
				tubes[tube.Metadata.Name] = tube.Spec
			}
		}
	}
	for _, guider := range t.guidersOf(telescope) {
		guiders[guider.Metadata.Name] = guider.Spec
	}
	return trains, tubes, guiders
}

// guidersOf answers the Guiders of a telescope, sorted by name: the
// Guiders that a session recorded while one holds the telescope, and
// every Guider whose spec names it otherwise.
func (t *tree) guidersOf(telescope string) []*observatory.Guider {
	recorded, frozen := t.records[serverRef{observatory.TelescopeKind, telescope}]
	var out []*observatory.Guider
	for _, name := range slices.Sorted(maps.Keys(t.guiders)) {
		guider := t.guiders[name]
		if _, ok := recorded.Guiders[name]; (frozen && ok) || (!frozen && guider.Spec.Telescope == telescope) {
			out = append(out, guider)
		}
	}
	return out
}

// applyRecords reads the record of each held server, and gives each
// held Telescope its recorded observatory, each recorded train and
// Guider its recorded spec, and each device that a record names its
// recorded spec. A record counts on the pods of a held server, so the
// pods of a server that no session holds any longer freeze nothing.
func (t *tree) applyRecords() {
	t.records, t.recordedTubes = map[serverRef]sessionRecord{}, map[string]observatory.OpticalTubeSpec{}
	for _, res := range t.reservations {
		telescope, ok := t.telescopes[res.Spec.Telescope]
		if !holds(res) || !ok {
			continue
		}
		scope := serverRef{observatory.TelescopeKind, telescope.Metadata.Name}
		site := telescope.Spec.Observatory
		if r, ok := t.recordOn(scope); ok {
			t.records[scope] = r
			t.freezeOptics(r)
			if r.Observatory != "" {
				site = r.Observatory
			}
		}
		if site != telescope.Spec.Observatory {
			frozen := *telescope
			frozen.Spec.Observatory = site
			t.telescopes[scope.name] = &frozen
		}
		ref := serverRef{observatory.ObservatoryKind, site}
		if r, ok := t.recordOn(ref); ok {
			t.records[ref] = r
		}
	}
	records := map[string]deviceRecord{}
	on := map[string]serverRef{}
	for _, ref := range slices.SortedFunc(maps.Keys(t.records), func(a, b serverRef) int { return cmp.Compare(a.String(), b.String()) }) {
		for key, d := range t.records[ref].Devices {
			records[key], on[key] = d, ref
		}
	}
	for i, d := range t.devices {
		r, ok := records[d.key()]
		if !ok || (r.UID != "" && r.UID != d.object.Metadata.UID) {
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

// freezeOptics gives each train and Guider that a record names its
// recorded spec, and keeps the recorded spec of each tube for tubeSpec.
// The tube's object keeps its spec, so its status shows the lengths
// that a person wrote.
func (t *tree) freezeOptics(r sessionRecord) {
	for name, spec := range r.Trains {
		if train, ok := t.trains[name]; ok {
			frozen := *train
			frozen.Spec = spec
			t.trains[name] = &frozen
		}
	}
	for name, spec := range r.Tubes {
		t.recordedTubes[name] = spec
	}
	for name, spec := range r.Guiders {
		if guider, ok := t.guiders[name]; ok {
			frozen := *guider
			frozen.Spec = spec
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
	if spec, ok := t.recordedTubes[name]; ok {
		return spec, true
	}
	return tube.Spec, true
}

// recordOn answers the record of a server: on its pod, or, once an
// eviction deleted that pod, on the pod of one of its devices, the
// first by name.
func (t *tree) recordOn(ref serverRef) (sessionRecord, bool) {
	read := func(p *pod) (sessionRecord, bool) {
		var r sessionRecord
		err := json.Unmarshal([]byte(p.Metadata.Annotations[annotationSession]), &r)
		return r, err == nil
	}
	if p, ok := t.pods[ref.String()]; ok {
		return read(p)
	}
	for _, name := range slices.Sorted(maps.Keys(t.pods)) {
		p := t.pods[name]
		if l := p.Metadata.Labels; l[labelServer] == ref.String() && l[labelRole] == roleDevice {
			if r, ok := read(p); ok {
				return r, true
			}
		}
	}
	return sessionRecord{}, false
}
