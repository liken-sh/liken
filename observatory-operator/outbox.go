package main

// The outbox of plan 19: a PersistentVolumeClaim that a reservation
// names in spec.outbox, which the telescope's imaging cameras mount at
// /outbox. Configure points each of their drivers at it, so the driver
// writes every frame once, already in place, and
// astrophotography-operator moves the frames from there afterward.
//
// The PowerOn step records the outbox in status.session with the
// devices, and the pods read the record, never the spec. A claim
// cannot change under a running driver without a new pod, and a new
// pod in the middle of an exposure loses the exposure, so an edit
// takes effect at the next session (records.go).

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// driverGroup is the group that every driver runs as (restricted).
const driverGroup = 1000

// outbox answers the claim that a device's pod mounts at /outbox, or
// "" for none. Only a Camera of a telescope whose session recorded an
// outbox mounts it, and not a camera that a Guider guides with: that
// camera sends its frames to PHD2 and never writes a file.
func (t *tree) outbox(server serverRef, d *device) string {
	r, ok := t.records[server]
	if !ok || r.Outbox == nil || d.kind != observatory.CameraKind || t.guides(server, d) {
		return ""
	}
	return r.Outbox.ClaimName
}

// mountOutbox mounts a claim in a device's pod at /outbox. A new
// volume is often owned by root, and the driver runs as user and group
// 1000 on a read-only root, so the pod sets fsGroup to the driver's
// group. OnRootMismatch makes the kubelet walk a large volume only
// when the owner of its root does not match.
func mountOutbox(p *pod, claim string) {
	p.Spec.Volumes = append(p.Spec.Volumes, volume{Name: outboxVolume, PersistentVolumeClaim: &claimVolume{ClaimName: claim}})
	p.Spec.Containers[0].VolumeMounts = append(p.Spec.Containers[0].VolumeMounts, volumeMount{Name: outboxVolume, MountPath: outboxDir})
	p.Spec.SecurityContext = &podSecurityContext{FSGroup: int64Pointer(driverGroup), FSGroupChangePolicy: "OnRootMismatch"}
}

func volumeClaimPath(namespace, name string) string {
	return "/api/v1/namespaces/" + namespace + "/persistentvolumeclaims/" + name
}

// checkOutbox fails when the session's outbox names a claim that does
// not exist, before the PowerOn step creates a pod. A pod whose claim
// does not exist stays Pending with no message that names the
// reservation. A claim that exists and cannot be mounted on the
// camera's node also leaves the pod Pending, and StartDevices gives
// the scheduler's message (waitReady).
//
// It reads the claim once, with a GET, and does not watch the claims:
// a watch of every claim in the cluster would hold a copy of each one
// and wake the operator for volumes it never mounts.
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
		return fmt.Errorf("missing PersistentVolumeClaim %s in namespace %s, which spec.outbox.claimName names", outbox.ClaimName, r.o.namespace)
	}
	return nil
}

// uploadToOutbox points a camera's driver at the outbox: UPLOAD_DIR is
// /outbox, and UPLOAD_MODE is UPLOAD_BOTH, so every frame lands in the
// outbox and a desktop client still receives it. It leaves
// UPLOAD_PREFIX to the holder. The holder can change the mode and the
// directory afterward: astrophotography-operator chooses UPLOAD_LOCAL
// and a directory for each target, and Ekos writes its own upload
// settings when it captures. A driver defines both properties only
// after it connects, and Configure runs after Connect.
func uploadToOutbox(ctx context.Context, t *tree, h handle, notes *[]string, note func(bool, error) error) error {
	if t.outbox(h.d.recordedOn, h.d) == "" {
		return nil
	}
	if _, ok := h.property(ctx, "UPLOAD_SETTINGS"); !ok {
		*notes = append(*notes, fmt.Sprintf("no UPLOAD_SETTINGS on %s to write frames to the outbox", h))
		return nil
	}
	if err := note(h.setTexts(ctx, "UPLOAD_SETTINGS", map[string]string{"UPLOAD_DIR": outboxDir})); err != nil {
		return err
	}
	return note(h.setSwitches(ctx, "UPLOAD_MODE", map[string]bool{"UPLOAD_BOTH": true}))
}
