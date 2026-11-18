package main

import "github.com/liken-sh/liken/observatory-operator/observatory"

// server returns the INDI server that should run a device, and false
// when no server should.
//
// While a session holds a server and has recorded its devices, the
// server runs exactly the devices in the records of the reservations
// that hold it (records.go). A device that the specs place there but the record
// does not have, such as one created during the session, waits for the
// next session. A spec edit that would move a recorded device takes
// effect when the session ends. So a device never moves between two
// running servers, which would need one server to stop it while the
// other starts it. Before the holders have recorded anything, the
// server runs the devices that the specs place on it.
//
// A deleted device is on no server, so the server that runs it handles
// it as a device that left and runs its deactivation (finalizers.go).
func (t *tree) server(d *device) (serverRef, bool) {
	if deleting(d.object.Metadata) {
		return serverRef{}, false
	}
	if on, ok := t.heldOn(d); ok {
		return on, true
	}
	ref, ok := t.declared(d)
	if ok && t.started(ref) {
		return serverRef{}, false
	}
	return ref, ok
}

// sessions describes which servers the reservations hold in one tree.
// The tree computes it once, because the status writer calls server for
// every device on each pass.
type sessions struct {
	// holders lists the names of the reservations that hold each
	// server, sorted. An observatory's server is held by the
	// reservation of each of its held telescopes.
	holders map[serverRef][]string
	// started is true for each held server that has a record.
	started map[serverRef]bool
}

func (t *tree) sessions() sessions {
	t.heldOnce.Do(func() {
		t.held = sessions{holders: map[serverRef][]string{}, started: map[serverRef]bool{}}
		for _, name := range sortedNames(t.reservations) {
			res := t.reservations[name]
			if !holds(res) {
				continue
			}
			scope := serverRef{observatory.TelescopeKind, res.Spec.Telescope}
			t.held.holders[scope] = append(t.held.holders[scope], name)
			if telescope, ok := t.telescopes[scope.name]; ok {
				site := serverRef{observatory.ObservatoryKind, telescope.Spec.Observatory}
				t.held.holders[site] = append(t.held.holders[site], name)
			}
		}
		for ref := range t.held.holders {
			if _, recorded := t.records[ref]; recorded {
				t.held.started[ref] = true
			}
		}
	})
	return t.held
}

// started reports whether a session holds the server and has recorded
// its devices.
func (t *tree) started(ref serverRef) bool {
	return t.sessions().started[ref]
}

// heldOn returns the server whose record has the device. applyRecords
// matches records by UID, so a device that a person deleted and created
// again with the same name has none.
func (t *tree) heldOn(d *device) (serverRef, bool) {
	if d.record == nil {
		return serverRef{}, false
	}
	return d.recordedOn, true
}
