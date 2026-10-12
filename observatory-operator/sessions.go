package main

// Which server runs a device while sessions hold the servers. A
// reservation records the devices of its session in its status before
// it creates any pod (records.go), so from then on the record alone
// gives each server's set. A pod, a server's drivers annotation, or a
// device that its parents place there adds nothing to the set, and
// only a delete takes a device out of it.

import "github.com/liken-sh/liken/observatory-operator/observatory"

// server answers the INDI server that runs a device. While a session
// holds a server and has recorded it, the server runs a fixed set of
// devices: the devices that the record in the status of the
// reservations that hold the server names (heldOn). An eviction can
// delete the server's pod, or every pod of the server, and the runner
// creates the pods again from the record. A server whose holders
// recorded nothing yet takes the devices that the specs place there,
// as a new session does. During the session, the record places a
// device, and its spec does not, so a spec edit that would move it to
// another telescope, train, or the shelf, or a train deleted under it,
// takes effect when the session ends, and the release stops the device
// as its own. A device that its parents place on a recorded server and
// that the record does not name waits for the next session: one
// created during the session, one that a person created again with
// the same name, and a served device whose owner the session started.
// So a device runs on one server at a time: a move between two running
// servers would need one server to release the device while the other
// starts it, and the steps of both sessions to agree on where it is. A
// deleted device leaves its server at once, because its finalizer
// holds it only until its deactivation ran.
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

// sessions is what the reservations hold in one tree. The status
// writer places every device on each pass, so a tree computes it once.
type sessions struct {
	// holders names, for each held server, the reservations that hold
	// it, sorted: one for a telescope's server, and one for each held
	// telescope of an observatory for the observatory's server.
	holders map[serverRef][]string
	// started holds each held server whose set of devices is fixed,
	// because a holder's status records it.
	started map[serverRef]bool
}

// sessions answers what the reservations hold, from their steps
// (holds) and their records.
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

// started reports whether a session holds a server and the server's
// set of devices is fixed (heldOn).
func (t *tree) started(ref serverRef) bool {
	return t.sessions().started[ref]
}

// heldOn answers the held server whose record names a device. The
// record of a device that a person created again with the same name
// names another UID, so it does not apply (applyRecords).
func (t *tree) heldOn(d *device) (serverRef, bool) {
	if d.record == nil {
		return serverRef{}, false
	}
	return d.recordedOn, true
}
