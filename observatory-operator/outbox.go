package main

// The outbox is a PersistentVolumeClaim that a reservation names in
// spec.outbox. The telescope's imaging cameras mount it at /outbox, and
// Configure points each camera driver at it, so each frame is written
// once, into the outbox. astrophotography-operator moves the frames out
// of the outbox afterward.
//
// The PowerOn step copies spec.outbox into status.session, and the pods
// are built from that copy. A pod's volumes cannot change, and
// replacing a camera's pod during an exposure loses the exposure, so an
// edit of spec.outbox takes effect at the next session.

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// driverGroup is the group ID that every driver pod runs as.
const driverGroup = 1000

// outbox returns the name of the claim that a device's pod mounts at
// /outbox, or "" for none. Only a Camera mounts it, and only when the
// session recorded an outbox. A Guider's camera does not, because it
// sends its frames to PHD2 and never writes a file.
func (t *tree) outbox(server serverRef, d *device) string {
	r, ok := t.records[server]
	if !ok || r.Outbox == nil || d.kind != observatory.CameraKind || t.guides(server, d) {
		return ""
	}
	return r.Outbox.ClaimName
}

// mountOutbox mounts a claim in a device's pod at /outbox. A new volume
// is often owned by root, and the driver runs as user and group 1000,
// so the pod sets fsGroup to driverGroup. With OnRootMismatch, the
// kubelet changes the ownership of every file only when the volume's
// root directory does not already match, which saves a walk of a large
// volume at each pod start.
func mountOutbox(p *pod, claim string) {
	p.Spec.Volumes = append(p.Spec.Volumes, volume{Name: outboxVolume, PersistentVolumeClaim: &claimVolume{ClaimName: claim}})
	p.Spec.Containers[0].VolumeMounts = append(p.Spec.Containers[0].VolumeMounts, volumeMount{Name: outboxVolume, MountPath: outboxDir})
	p.Spec.SecurityContext = &podSecurityContext{FSGroup: int64Pointer(driverGroup), FSGroupChangePolicy: "OnRootMismatch"}
}

func volumeClaimPath(namespace, name string) string {
	return "/api/v1/namespaces/" + namespace + "/persistentvolumeclaims/" + name
}

// checkOutbox fails PowerOn when the session's outbox names a claim
// that does not exist, before any pod is created. Otherwise the
// camera's pod would stay Pending, and nothing in its status would
// point the person at the reservation. A claim that exists but cannot
// be mounted on the camera's node also leaves the pod Pending, and
// waitReady reports the scheduler's message for that case.
//
// It reads the claim once with a GET. A watch of every claim in the
// cluster would cache each one and wake the operator for volumes it
// never mounts.
func (r *runner) checkOutbox(ctx context.Context, w *stepWork, outbox *observatory.Outbox) error {
	if outbox == nil {
		return nil
	}
	path := volumeClaimPath(r.o.namespace, outbox.ClaimName)
	missing := false
	err := r.o.send(ctx, w.report, "reading the PersistentVolumeClaim "+outbox.ClaimName, func() error {
		err := r.o.client.RequestJSON(http.MethodGet, path, nil, nil)
		missing = errors.Is(err, apiclient.ErrNotFound)
		if missing {
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	if missing {
		return fmt.Errorf("the PersistentVolumeClaim %s that spec.outbox.claimName names is not in the namespace %s", outbox.ClaimName, r.o.namespace)
	}
	return nil
}

// uploadToOutbox sets the camera driver's UPLOAD_DIR to
// /outbox/<Camera name> and UPLOAD_MODE to UPLOAD_BOTH, so the driver
// writes each frame to the outbox and also sends it to the INDI client.
//
// A driver picks the next file name from the files already in its
// directory, so two drivers that share a directory can pick the same
// name. That would happen when two cameras of one telescope image, or
// when two reservations name one claim. Camera names are unique in a
// namespace and a claim belongs to one namespace, so each driver gets
// its own directory. The driver creates the directory when it saves
// its first frame.
//
// UPLOAD_PREFIX keeps the driver's value. The holder of the telescope
// can change the mode and the directory later: astrophotography-operator
// sets UPLOAD_LOCAL and a directory for each target, and Ekos writes
// its own upload settings when it captures. A driver defines these
// properties only after it connects, so this runs in Configure, after
// Connect.
func uploadToOutbox(ctx context.Context, t *tree, h handle, notes *[]string, note func(bool, error) error) error {
	if t.outbox(h.d.recordedOn, h.d) == "" {
		return nil
	}
	if _, ok := h.property(ctx, "UPLOAD_SETTINGS"); !ok {
		*notes = append(*notes, fmt.Sprintf("no UPLOAD_SETTINGS on %s to write frames to the outbox", h))
		return nil
	}
	if err := note(h.setTexts(ctx, "UPLOAD_SETTINGS", map[string]string{"UPLOAD_DIR": outboxDir + "/" + h.d.name()})); err != nil {
		return err
	}
	return note(h.setSwitches(ctx, "UPLOAD_MODE", map[string]bool{"UPLOAD_BOTH": true}))
}
