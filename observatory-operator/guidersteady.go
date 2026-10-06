package main

// While a reservation is Ready, its runner keeps the guider as
// StartGuider left it: it creates the guider's pod again when the pod is
// gone, replaces it when PHD2's profile changes, and connects the camera
// and the mount of each new PHD2. A PHD2 that restarts, in a new pod or
// in the same one, starts with nothing connected. A new INDI server
// ends PHD2's INDI connection, and PHD2 does not open it again. So the
// runner connects the equipment once for each pair of a guider's pod
// and a server's pod, after the server's camera and mount connect, and
// only while PHD2 is Stopped. A holder that disconnects PHD2's
// equipment on purpose keeps it disconnected until either pod changes.

import (
	"context"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// keepGuider keeps the telescope's guider, and records a failure in the
// Guider's status. A failure does not end the reservation: the holder
// may still image without guiding.
func (r *runner) keepGuider(ctx context.Context, t *tree, ref serverRef, telescope *observatory.Telescope) {
	guider := t.guiderOf(telescope.Metadata.Name)
	if guider == nil {
		return
	}
	camera, mount, focalLength, err := t.guiderDevices(ref, guider)
	if err != nil {
		r.o.guiderFault(guider.Metadata.Name, err)
		return
	}
	wanted := []*device{camera}
	if mount != nil {
		wanted = append(wanted, mount)
	}
	handles := map[string]handle{}
	for _, h := range r.o.handlesOf(t, ref, wanted) {
		handles[h.d.key()] = h
	}
	gear, defined := guiderGear(camera, mount, focalLength, handles)
	if !defined {
		// The drivers define their devices after a server restart, and
		// each definition rings structure, which runs this again.
		return
	}
	ctx, cancel := context.WithTimeout(ctx, guiderLimit)
	defer cancel()
	created, err := r.o.startGuiderPod(ctx, nil, ref, guider, gear)
	if created {
		r.recordPod(reference(observatory.GuiderKind, guider.Metadata), guiderName(guider), "The new PHD2 starts idle and not calibrated.")
	}
	if err == nil && gearConnected(handles) {
		err = r.connectNewGuider(ctx, ref, guider, gear)
	}
	r.o.guiderFault(guider.Metadata.Name, err)
	if err != nil {
		r.o.logf("keeping Guider %s: %v", guider.Metadata.Name, err)
	}
}

// gearConnected reports whether every device of the guider's equipment is
// connected on its server. PHD2's own INDI client would connect a
// device that is not, before the runner configures it again (steady.go).
func gearConnected(handles map[string]handle) bool {
	for _, h := range handles {
		if !h.connected() {
			return false
		}
	}
	return true
}

// connectNewGuider connects the equipment of a PHD2 that the runner has
// not connected to the server's pod that runs now.
func (r *runner) connectNewGuider(ctx context.Context, ref serverRef, guider *observatory.Guider, gear guiderEquipment) error {
	name := guiderName(guider)
	t := r.o.snapshot()
	p, ok := t.pods[name]
	server, running := t.pods[ref.String()]
	if !ok || !p.ready() || !running {
		return nil
	}
	link := p.Metadata.UID + "/" + server.Metadata.UID
	conn, ok := r.o.guiderConns.get(name)
	if !ok {
		return nil
	}
	s := conn.client.State()
	switch {
	case !s.Open || s.Equipment == nil || link == r.guiderLink:
		return nil
	case equipment(s):
		r.guiderLink = link
		return nil
	case capturing(s):
		return nil
	}
	if err := r.o.connectGuider(ctx, func(string) {}, conn, gear); err != nil {
		return err
	}
	r.guiderLink = link
	return nil
}
