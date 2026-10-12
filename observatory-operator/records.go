package main

// A session freezes what it started. When a reservation's steps start
// a server, or a device's pod, the operator records in the API the
// facts that the session acts on: for each device, its parents, its
// driver, its served owner, its power, its claim, and whether a
// Guider guides with it, and for a telescope, its observatory. The
// record is an annotation on the server's pod, for every device the
// server runs, and on each device's pod, for that device and the
// devices it serves. So either record survives an eviction of the
// other pod, and the runner creates the pod again from it.
//
// While a session holds a server, the tree reads each device that the
// record names with the recorded facts in its spec, and each held
// Telescope with the recorded observatory (applyRecords). So every
// step, the runner of a Ready reservation, the locks, the guider, the
// power, the release, and the status lists act on what the session
// started, and a spec edit during the session takes effect when the
// session ends, as a move does (tree.server). The spec is read as a
// person wrote it only to start a session, for a device that no
// session holds, and for the status that says where a device goes
// (tree.declared). A device's settings are not frozen: the runner
// writes them again from the spec when a driver comes back.

import (
	"encoding/json"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// annotationSession holds a sessionRecord as JSON on a server's pod and
// on each device's pod.
const annotationSession = Group + "/session"

// sessionRecord is what a session recorded on one pod.
type sessionRecord struct {
	// Observatory is the observatory of the telescope whose server the
	// pod belongs to, or "" on the pods of an observatory's server.
	Observatory string `json:"observatory,omitempty"`
	// Devices holds each device's record by the device's key, such as
	// Camera/east-main.
	Devices map[string]deviceRecord `json:"devices"`
}

// deviceRecord is the spec of one device as its session started it.
type deviceRecord struct {
	Telescope    string `json:"telescope,omitempty"`
	OpticalTrain string `json:"opticalTrain,omitempty"`
	Observatory  string `json:"observatory,omitempty"`
	observatory.DeviceSpec
	Guides bool `json:"guides,omitempty"`
}

// recordOf answers the record that a server's pod, or the pod of the
// devices that run one driver, carries: the facts that the tree reads
// now, which for a device that a session holds are the recorded ones.
func (t *tree) recordOf(ref serverRef, devices []*device) string {
	out := sessionRecord{Devices: map[string]deviceRecord{}}
	if telescope, ok := t.telescopes[ref.name]; ok && ref.kind == observatory.TelescopeKind {
		out.Observatory = telescope.Spec.Observatory
	}
	for _, d := range devices {
		s := d.object.Spec
		out.Devices[d.key()] = deviceRecord{Telescope: s.Telescope, OpticalTrain: s.OpticalTrain, Observatory: s.Observatory,
			DeviceSpec: s.DeviceSpec, Guides: t.guides(ref, d)}
	}
	body, _ := json.Marshal(out)
	return string(body)
}

// applyRecords gives each held Telescope its recorded observatory and
// each device that a held server's record names its recorded spec. A
// record counts on the pod of a held server, and on a device pod that
// carries such a server's label, so the pods of a server that no
// session holds any longer freeze nothing. A device pod's record
// replaces its server's, because the runner writes it later.
func (t *tree) applyRecords() {
	read := func(p *pod) (sessionRecord, bool) {
		var r sessionRecord
		err := json.Unmarshal([]byte(p.Metadata.Annotations[annotationSession]), &r)
		return r, err == nil
	}
	held := map[string]serverRef{}
	for _, res := range t.reservations {
		telescope, ok := t.telescopes[res.Spec.Telescope]
		if !holds(res) || !ok {
			continue
		}
		scope := serverRef{observatory.TelescopeKind, telescope.Metadata.Name}
		site := telescope.Spec.Observatory
		if r, ok := t.recordOn(scope, read); ok && r.Observatory != "" {
			site = r.Observatory
		}
		if site != telescope.Spec.Observatory {
			frozen := *telescope
			frozen.Spec.Observatory = site
			t.telescopes[scope.name] = &frozen
		}
		held[scope.String()] = scope
		held[serverRef{observatory.ObservatoryKind, site}.String()] = serverRef{observatory.ObservatoryKind, site}
	}
	records := map[string]deviceRecord{}
	on := map[string]serverRef{}
	keep := func(ref serverRef, r sessionRecord) {
		for key, d := range r.Devices {
			records[key], on[key] = d, ref
		}
	}
	for name, ref := range held {
		if p, ok := t.pods[name]; ok {
			if r, ok := read(p); ok {
				keep(ref, r)
			}
		}
	}
	for _, p := range t.pods {
		ref, ok := held[p.Metadata.Labels[labelServer]]
		if r, recorded := read(p); ok && recorded && p.Metadata.Labels[labelRole] == roleDevice {
			keep(ref, r)
		}
	}
	for i, d := range t.devices {
		r, ok := records[d.key()]
		if !ok {
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

// recordOn answers the record of a telescope's server: on its pod, or
// on the pod of one of its devices once an eviction deleted the
// server's.
func (t *tree) recordOn(scope serverRef, read func(*pod) (sessionRecord, bool)) (sessionRecord, bool) {
	if p, ok := t.pods[scope.String()]; ok {
		return read(p)
	}
	for _, p := range t.pods {
		if l := p.Metadata.Labels; l[labelServer] == scope.String() && l[labelRole] == roleDevice {
			if r, ok := read(p); ok {
				return r, true
			}
		}
	}
	return sessionRecord{}, false
}
