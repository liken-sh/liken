package main

// While a reservation is Ready, its runner keeps the guider as
// StartGuider left it: it creates the guider's pod again when the pod is
// gone, replaces it when PHD2's profile changes, and connects the camera
// and the mount of each new PHD2. A PHD2 that restarts, in a new pod or
// in the same one, starts with nothing connected. The runner connects
// the equipment once for each pod, and only while PHD2 is Stopped, so a
// holder that disconnects PHD2's equipment on purpose keeps it
// disconnected until the pod changes.

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
	err = r.o.startGuiderPod(ctx, nil, ref, guider, gear)
	if err == nil {
		err = r.connectNewGuider(ctx, guider, gear)
	}
	r.o.guiderFault(guider.Metadata.Name, err)
	if err != nil {
		r.o.logf("keeping Guider %s: %v", guider.Metadata.Name, err)
	}
}

// connectNewGuider connects the equipment of a PHD2 in a pod that the
// runner has not connected yet.
func (r *runner) connectNewGuider(ctx context.Context, guider *observatory.Guider, gear guiderEquipment) error {
	name := guiderName(guider)
	p, ok := r.o.snapshot().pods[name]
	if !ok || !p.ready() {
		return nil
	}
	conn, ok := r.o.guiderConns.get(name)
	if !ok {
		return nil
	}
	s := conn.client.State()
	switch {
	case !s.Open || s.Equipment == nil || p.Metadata.UID == r.guiderPod:
		return nil
	case equipment(s):
		r.guiderPod = p.Metadata.UID
		return nil
	case capturing(s):
		return nil
	}
	if err := r.o.connectGuider(ctx, func(string) {}, conn, gear); err != nil {
		return err
	}
	r.guiderPod = p.Metadata.UID
	return nil
}
