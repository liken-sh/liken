package main

// The Configure and Prepare steps: the settings that the resources
// state, and the moves that make a telescope ready to image.

import (
	"context"
	"fmt"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// configure writes each setting that the resources state: the
// observatory's location to the mount and the GPS, the mount's dome
// policy, each camera's ACTIVE_DEVICES from its train, the camera's
// gain, offset, and the tube's aperture and focal length, and the
// filter wheel's names. It then relays the domes' park state to the
// mount (locks.go), so the mount holds it before Prepare unparks it.
func (r *runner) configure(ctx context.Context, w *stepWork) (outcome, error) {
	t, handles, err := r.telescopeHandles(ctx, w)
	if err != nil {
		return outcome{}, err
	}
	_, site, err := r.siteOf(t)
	if err != nil {
		return outcome{}, err
	}
	var notes, wrote []string
	for _, h := range handles {
		w.report("configuring " + h.String())
		changed, err := configureDevice(ctx, t, site, h, handles, &notes)
		r.o.fault(h.d, err)
		if err != nil {
			return outcome{}, err
		}
		if changed {
			wrote = append(wrote, h.String())
		}
	}
	r.o.relayLocks(r.o.snapshot())
	if len(wrote) == 0 && len(notes) == 0 {
		return skipped("found every setting in place")
	}
	message := firstNonEmpty(strings.Join(wrote, ", "), "no device")
	message = "wrote the settings of " + message
	return done("%s", strings.Join(append([]string{message}, notes...), "; "))
}

// configureDevice writes one device's settings, and answers whether it
// sent any.
func configureDevice(ctx context.Context, t *tree, site *observatory.Observatory, h handle, all []handle, notes *[]string) (bool, error) {
	var changes []bool
	note := func(changed bool, err error) error {
		changes = append(changes, changed)
		return err
	}
	var err error
	switch h.d.kind {
	case observatory.MountKind:
		err = note(location(ctx, h, site.Spec.Location))
		if err == nil {
			err = note(activeDevices(ctx, h, map[string]string{"ACTIVE_GPS": nameOf(all, observatory.GPSKind, "")}))
		}
		if err == nil && site.Spec.Policies != nil {
			err = note(lockPolicy(ctx, h, "DOME_POLICY", "DOME_LOCKS", "DOME_IGNORED", site.Spec.Policies.DomeLocksMount, notes))
		}
	case observatory.GPSKind:
		err = note(location(ctx, h, site.Spec.Location))
	case observatory.FilterWheelKind:
		if len(h.d.object.Spec.Filters) > 0 {
			err = note(filterNames(ctx, h, h.d.object.Spec.Filters, notes))
		}
	case observatory.CameraKind:
		err = configureCamera(ctx, t, h, all, notes, note)
	}
	for _, c := range changes {
		if c {
			return true, err
		}
	}
	return false, err
}

// configureCamera writes ACTIVE_DEVICES from the camera's train, the
// gain and the offset, and SCOPE_INFO from the train's tube.
func configureCamera(ctx context.Context, t *tree, h handle, all []handle, notes *[]string, note func(bool, error) error) error {
	train := h.d.object.Spec.OpticalTrain
	snooped := map[string]string{
		"ACTIVE_TELESCOPE":  nameOf(all, observatory.MountKind, ""),
		"ACTIVE_FOCUSER":    nameOf(all, observatory.FocuserKind, train),
		"ACTIVE_FILTER":     nameOf(all, observatory.FilterWheelKind, train),
		"ACTIVE_ROTATOR":    nameOf(all, observatory.RotatorKind, train),
		"ACTIVE_SKYQUALITY": nameOf(all, observatory.SkyQualityMeterKind, ""),
	}
	if err := note(activeDevices(ctx, h, snooped)); err != nil {
		return err
	}
	spec := h.d.object.Spec
	if spec.Gain != nil {
		if err := note(optional(ctx, h, "CCD_GAIN", map[string]float64{"GAIN": *spec.Gain}, notes)); err != nil {
			return err
		}
	}
	if spec.Offset != nil {
		if err := note(optional(ctx, h, "CCD_OFFSET", map[string]float64{"OFFSET": *spec.Offset}, notes)); err != nil {
			return err
		}
	}
	if trainObject, ok := t.trains[train]; ok {
		if tube, ok := t.tubes[trainObject.Spec.OpticalTube]; ok {
			info := map[string]float64{"FOCAL_LENGTH": tube.Spec.FocalLength, "APERTURE": tube.Spec.Aperture}
			if err := note(optional(ctx, h, "SCOPE_INFO", info, notes)); err != nil {
				return err
			}
		}
	}
	return nil
}

// nameOf answers the INDI name of the first device of a kind among the
// handles, on one train when train is not empty, or "" when there is
// none.
func nameOf(handles []handle, kind observatory.Kind, train string) string {
	for _, h := range handles {
		if h.d.kind == kind && (train == "" || h.d.object.Spec.OpticalTrain == train) {
			return h.name
		}
	}
	return ""
}

// domePolicies writes the policies that the observatory states to each
// dome: the shutter policies, which the dome enforces alone, and the
// mount policy, which needs the mounts' park state that the operator
// relays (locks.go).
func domePolicies(ctx context.Context, t *tree, site *observatory.Observatory, handles map[string]handle) ([]string, error) {
	policies := site.Spec.Policies
	if policies == nil {
		return nil, nil
	}
	var notes []string
	for _, d := range t.devicesOf(serverRef{observatory.ObservatoryKind, site.Metadata.Name}, observatory.DomeKind) {
		h, ok := handles[d.key()]
		if !ok {
			continue
		}
		changed, err := h.setSwitches(ctx, "DOME_SHUTTER_PARK_POLICY", map[string]bool{
			"SHUTTER_CLOSE_ON_PARK":  policies.CloseShutterOnPark,
			"SHUTTER_OPEN_ON_UNPARK": policies.OpenShutterOnUnpark,
		})
		if err != nil {
			return nil, err
		}
		if changed {
			notes = append(notes, "wrote the shutter policy of "+d.kind.Name+" "+d.name())
		}
		changed, err = lockPolicy(ctx, h, "MOUNT_POLICY", "MOUNT_LOCKS", "MOUNT_IGNORED", policies.MountLocksDome, &notes)
		if err != nil {
			return nil, err
		}
		if changed {
			notes = append(notes, "wrote the mount policy of "+d.kind.Name+" "+d.name())
		}
	}
	return notes, nil
}

// lockPolicy writes one lock policy, DOME_POLICY or MOUNT_POLICY, which
// every INDI mount and dome defines before it connects. A driver that
// lacks it gets a note.
func lockPolicy(ctx context.Context, h handle, property, locks, ignored string, on bool, notes *[]string) (bool, error) {
	if _, ok := h.property(ctx, property); !ok {
		*notes = append(*notes, fmt.Sprintf("no %s on %s", property, h))
		return false, nil
	}
	return h.setSwitches(ctx, property, map[string]bool{locks: on, ignored: !on})
}

// prepare opens the dust caps, cools each camera that has a setpoint,
// and unparks the mount.
func (r *runner) prepare(ctx context.Context, w *stepWork) (outcome, error) {
	_, handles, err := r.telescopeHandles(ctx, w)
	if err != nil {
		return outcome{}, err
	}
	// already names each device that needed no change, so a Skipped
	// Prepare tells a telescope with nothing to do from one whose
	// devices were ready.
	var did, already, notes []string
	for _, h := range ofKind(handles, observatory.DustCapKind) {
		w.report("opening " + h.String())
		changed, err := h.switchOn(ctx, "CAP_PARK", "UNPARK")
		if err != nil {
			return outcome{}, err
		}
		if changed {
			did = append(did, "opened "+h.String())
		} else {
			already = append(already, "found "+h.String()+" open")
		}
	}
	var cameras []handle
	for _, h := range ofKind(handles, observatory.CameraKind) {
		if h.d.object.Spec.Temperature != nil {
			cameras = append(cameras, h)
		}
	}
	for _, h := range coolable(ctx, cameras, &notes) {
		target := *h.d.object.Spec.Temperature
		w.report(fmt.Sprintf("cooling %s to %s", h, quantity(target, 1, "°C")))
		if err := setTemperature(ctx, h, target, w.report); err != nil {
			return outcome{}, err
		}
		did = append(did, fmt.Sprintf("cooled %s to %s", h.String(), quantity(target, 1, "°C")))
	}
	for _, h := range ofKind(handles, observatory.MountKind) {
		w.report("unparking " + h.String())
		changed, err := h.switchOn(ctx, "TELESCOPE_PARK", "UNPARK")
		if err != nil {
			return outcome{}, err
		}
		if changed {
			did = append(did, "unparked "+h.String())
		} else {
			already = append(already, "found "+h.String()+" unparked")
		}
	}
	notes = append(already, notes...)
	if len(did) == 0 {
		return skipped("%s", firstNonEmpty(strings.Join(notes, "; "), "found nothing to open, cool, or unpark"))
	}
	return done("%s", strings.Join(append(did, notes...), "; "))
}

func ofKind(handles []handle, kind observatory.Kind) []handle {
	var out []handle
	for _, h := range handles {
		if h.d.kind == kind {
			out = append(out, h)
		}
	}
	return out
}
