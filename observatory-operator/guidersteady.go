package main

// While a reservation is Ready, its runner keeps the guider as the
// StartGuider step set it up. It creates the guider's pod again when the
// pod is gone, updates the pod when PHD2's profile changes, and connects
// the camera and the mount in each new PHD2.
//
// A PHD2 that restarts starts with no equipment connected. When the
// INDI server restarts, PHD2 loses its INDI connection and does not
// reconnect. So the runner connects PHD2's equipment once for each pair
// of guider pod and server pod, after the camera and the mount are
// connected on the server, and only while PHD2 is not capturing. If the
// holder disconnects PHD2's equipment on purpose, it stays disconnected
// until the guider pod or the server pod changes.

import (
	"context"
	"errors"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// keepGuider runs one pass of the work above and records a failure in
// the Guider's status. A failure does not end the reservation, because
// the holder can still image without guiding.
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
		// After an INDI server restart, the drivers define their devices
		// again, and each definition runs keepGuider again through
		// o.structure.
		return
	}
	ctx, cancel := context.WithTimeout(ctx, guiderLimit)
	defer cancel()
	created, err := r.o.startGuiderPod(ctx, nil, r.kept, ref, guider, gear)
	if created {
		r.recordPod(reference(observatory.GuiderKind, guider.Metadata), guiderName(guider), "The new PHD2 starts idle and not calibrated")
	}
	if errors.Is(err, errReplaceLater) {
		// The guider's status reads the ended pod's phase until the
		// replacement, so the wait is not a fault.
		r.o.logf("keeping Guider %s: %v", guider.Metadata.Name, err)
		return
	}
	if err == nil && gearConnected(handles) {
		err = r.connectNewGuider(ctx, ref, guider, gear)
	}
	r.o.guiderFault(guider.Metadata.Name, err)
	if err != nil {
		r.o.logf("keeping Guider %s: %v", guider.Metadata.Name, err)
	}
}

// gearConnected reports whether every device that the guider uses is
// connected on the INDI server. The runner waits for this, or PHD2
// would connect a device before the runner configures it (steady.go).
func gearConnected(handles map[string]handle) bool {
	for _, h := range handles {
		if !h.connected() {
			return false
		}
	}
	return true
}

// connectNewGuider connects PHD2's equipment when the runner has not
// yet done so for this pair of guider pod and server pod. When PHD2
// already has its equipment connected, it only records the pair.
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
