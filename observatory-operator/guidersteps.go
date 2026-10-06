package main

// The guider's steps. StartGuider runs last in activation: it starts
// PHD2, connects it to the guide camera and the mount, and leaves it
// idle. The operator never loops, calibrates, or guides, because
// tracking stays off after Prepare until the holder aligns the mount,
// and the holder drives PHD2 from then on. StopGuider runs before
// Disconnect and deletes the guider's pod, so PHD2 never sees its
// devices disconnect and never enters its reconnect path, which can
// open a modal progress dialog, and a modal dialog ends PHD2 on a
// compositor with no seat (plan 09). Abort stops PHD2's exposures and
// guiding first (deactivation.go).

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
	"github.com/liken-sh/liken/observatory-operator/phd2"
)

// guiderOf answers the Guider of a telescope, the first by name when a
// person declared two, or nil.
func (t *tree) guiderOf(telescope string) *observatory.Guider {
	for _, name := range slices.Sorted(maps.Keys(t.guiders)) {
		if t.guiders[name].Spec.Telescope == telescope {
			return t.guiders[name]
		}
	}
	return nil
}

// guiderDevices answers the guide camera and the mount that a Guider
// uses, and the guide tube's focal length. mount is nil for a telescope
// with no Mount whose guider pulses through the camera.
func (t *tree) guiderDevices(ref serverRef, guider *observatory.Guider) (camera, mount *device, focalLength float64, err error) {
	train, ok := t.trains[guider.Spec.OpticalTrain]
	if !ok || train.Spec.Telescope != ref.name {
		return nil, nil, 0, fmt.Errorf("missing OpticalTrain %s on Telescope %s", guider.Spec.OpticalTrain, ref.name)
	}
	for _, d := range t.devicesOf(ref, observatory.CameraKind) {
		if d.object.Spec.OpticalTrain == train.Metadata.Name {
			camera = d
			break
		}
	}
	if camera == nil {
		return nil, nil, 0, fmt.Errorf("no Camera on OpticalTrain %s", train.Metadata.Name)
	}
	if mounts := t.devicesOf(ref, observatory.MountKind); len(mounts) > 0 {
		mount = mounts[0]
	} else if guider.Spec.Pulses == observatory.PulsesMount {
		return nil, nil, 0, fmt.Errorf("no Mount on Telescope %s to send the pulses to", ref.name)
	}
	if tube, ok := t.tubes[train.Spec.OpticalTube]; ok {
		focalLength = tube.Spec.FocalLength
	}
	return camera, mount, focalLength, nil
}

// guiderName answers the name of a guider's pod, Service, and
// ConfigMap.
func guiderName(guider *observatory.Guider) string {
	return generatedName(observatory.GuiderKind, guider.Metadata.Name)
}

// guiderGear answers what PHD2's profile names, from the devices that
// the telescope's server defines. handles answers the handle of each
// device, by its key.
func guiderGear(camera, mount *device, focalLength float64, handles map[string]handle) (guiderEquipment, bool) {
	gear := guiderEquipment{focalLength: focalLength}
	h, ok := handles[camera.key()]
	if !ok {
		return gear, false
	}
	gear.camera = h.name
	if mount != nil {
		h, ok := handles[mount.key()]
		if !ok {
			return gear, false
		}
		gear.mount = h.name
	}
	return gear, true
}

// ensureFiles creates a ConfigMap, or replaces its data when it
// differs from the data built now.
func (o *operator) ensureFiles(ctx context.Context, report func(string), t *tree, built *configMap) error {
	name := built.Metadata.Name
	held, ok := t.configMaps[name]
	switch {
	case !ok:
		return o.send(ctx, report, "creating ConfigMap "+name, func() error {
			return o.create("/api/v1/namespaces/"+o.namespace+"/configmaps", built)
		})
	case maps.Equal(held.Data, built.Data):
		return nil
	}
	next := *built
	next.Metadata.ResourceVersion = held.Metadata.ResourceVersion
	return o.send(ctx, report, "updating ConfigMap "+name, func() error {
		return o.writeJSON(http.MethodPut, configMapPath(o.namespace, name), next)
	})
}

// startGuider creates the guider's ConfigMap, pod, and Service, waits
// until PHD2's event server answers, and connects PHD2 to its camera
// and mount.
func (r *runner) startGuider(ctx context.Context, w *stepWork) (outcome, error) {
	t := r.o.snapshot()
	ref, telescope, _, err := r.telescope(t)
	if err != nil {
		return outcome{}, err
	}
	guider := t.guiderOf(telescope.Metadata.Name)
	if guider == nil {
		return skipped("no Guider for Telescope %s", telescope.Metadata.Name)
	}
	camera, mount, focalLength, err := t.guiderDevices(ref, guider)
	if err != nil {
		return outcome{}, err
	}
	wanted := []*device{camera}
	if mount != nil {
		wanted = append(wanted, mount)
	}
	handles, err := r.o.waitDefined(ctx, w.report, ref, wanted)
	if err != nil {
		return outcome{}, err
	}
	gear, _ := guiderGear(camera, mount, focalLength, handles)
	if _, err := r.o.startGuiderPod(ctx, w.report, ref, guider, gear); err != nil {
		return outcome{}, err
	}
	conn, err := r.o.waitGuider(ctx, w.report, guider)
	if err != nil {
		return outcome{}, err
	}
	if err := r.o.connectGuider(ctx, w.report, conn, gear); err != nil {
		return outcome{}, err
	}
	return done("connected PHD2 to %s", gearText(gear))
}

func gearText(gear guiderEquipment) string {
	text := "camera " + gear.camera
	if gear.mount != "" {
		text += " and mount " + gear.mount
	}
	return text
}

// startGuiderPod creates or replaces the guider's objects, and reports
// whether it created the pod.
func (o *operator) startGuiderPod(ctx context.Context, report func(string), ref serverRef, guider *observatory.Guider, gear guiderEquipment) (bool, error) {
	files, built, svc, err := guiderPod(o.namespace, ref, guider, gear)
	if err != nil {
		return false, err
	}
	if err := o.ensureFiles(ctx, report, o.snapshot(), files); err != nil {
		return false, err
	}
	return o.ensure(ctx, report, built, svc, nil)
}

// waitGuider waits until the guider's pod is Ready and the operator's
// connection to PHD2's event server is open.
func (o *operator) waitGuider(ctx context.Context, report func(string), guider *observatory.Guider) (*guiderConn, error) {
	name := guiderName(guider)
	var conn *guiderConn
	err := o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		p, ok := t.pods[name]
		if !ok || !p.ready() {
			phase := "not created"
			if ok {
				phase = firstNonEmpty(p.Status.Phase, "Pending")
			}
			return false, fmt.Sprintf("waiting for pod %s (%s)", name, phase), nil
		}
		c, ok := o.guiderConns.get(name)
		if !ok || !c.client.State().Open {
			return false, "waiting for PHD2's event server on " + name, nil
		}
		conn = c
		return true, "", nil
	})
	return conn, err
}

// equipment reports whether PHD2 reports its camera and mount
// connected.
func equipment(s phd2.State) bool { return s.Equipment != nil && *s.Equipment }

// connectGuider sends set_connected and waits until PHD2 reports its
// camera and mount connected. PHD2 refuses set_connected while it
// captures, so a PHD2 that reports its equipment connected already
// receives nothing.
func (o *operator) connectGuider(ctx context.Context, report func(string), conn *guiderConn, gear guiderEquipment) error {
	if equipment(conn.client.State()) {
		return nil
	}
	report("connecting PHD2 to " + gearText(gear))
	if err := conn.client.SetConnected(ctx, true); err != nil {
		return fmt.Errorf("PHD2 did not connect %s: %w", gearText(gear), err)
	}
	return conn.client.WaitFor(ctx, equipment)
}

// stopGuider deletes the guider's pod, Service, and ConfigMap.
func (r *runner) stopGuider(ctx context.Context, w *stepWork) (outcome, error) {
	ref := serverRef{observatory.TelescopeKind, r.res.Spec.Telescope}
	stopped, err := r.o.stopPods(ctx, w.report, func(l map[string]string) bool {
		return l[labelServer] == ref.String() && l[labelRole] == roleGuider
	})
	if err != nil {
		return outcome{}, err
	}
	if len(stopped) == 0 {
		return skipped("no guider pod running")
	}
	return done("stopped %s", strings.Join(stopped, ", "))
}

// stopCapture stops PHD2's exposures and guiding, for Abort. A PHD2
// whose connection is not open, or that takes no exposures, receives
// nothing.
func (r *runner) stopCapture(ctx context.Context, w *stepWork) (string, error) {
	t := r.o.snapshot()
	guider := t.guiderOf(r.res.Spec.Telescope)
	if guider == nil {
		return "", nil
	}
	conn, ok := r.o.guiderConns.get(guiderName(guider))
	if !ok {
		return "", nil
	}
	state := conn.client.State()
	if !state.Open || !capturing(state) {
		return "", nil
	}
	w.report("stopping PHD2, which is " + state.AppState)
	if err := conn.client.StopCapture(ctx); err != nil {
		if errors.Is(err, phd2.ErrNotConnected) || errors.Is(err, phd2.ErrDisconnected) {
			return "", nil
		}
		return "", fmt.Errorf("stopping PHD2: %w", err)
	}
	if err := conn.client.WaitFor(ctx, func(s phd2.State) bool { return !s.Open || !capturing(s) }); err != nil {
		return "", fmt.Errorf("PHD2 still reports %s: %w", conn.client.State().AppState, err)
	}
	return "stopped PHD2, which was " + state.AppState, nil
}

// capturing reports whether PHD2 takes exposures: it loops,
// calibrates, or guides.
func capturing(s phd2.State) bool {
	switch observatory.GuiderState(s.AppState) {
	case observatory.GuiderStopped, observatory.GuiderSelected, "":
		return false
	}
	return true
}

// guiderLimit bounds the steady runner's work on a guider that came
// back, as reapplyLimit bounds a device's.
const guiderLimit = 2 * time.Minute
