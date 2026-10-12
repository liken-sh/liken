package main

// The Configure step: the settings that the resources state.

import (
	"context"
	"fmt"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// configure writes each setting that the resources state, once for the
// reservation: the settings in each device's spec (settings.go), and
// the settings that the tree decides, which no spec states. Those are
// the observatory's location to the mount and the GPS, the mount's
// ACTIVE_GPS and DOME_POLICY, and each camera's ACTIVE_DEVICES from its
// train, SCOPE_INFO from the train's tube, and its upload settings
// from the session's outbox (outbox.go). It then relays the domes'
// park state to the mount (locks.go), so the mount holds it before a
// procedure unparks it. The operator does not drive a setting back
// while the reservation runs, so a client such as a sequencer can
// change a camera's gain for one frame.
func (r *runner) configure(ctx context.Context, w *stepWork) (outcome, error) {
	t, handles, err := r.telescopeHandles(ctx, w)
	if err != nil {
		return outcome{}, err
	}
	_, site, err := r.siteOf(t)
	if err != nil {
		return outcome{}, err
	}
	ref, _, devices, err := r.telescope(t)
	if err != nil {
		return outcome{}, err
	}
	switches, _ := t.partition(devices)
	all := append(r.o.handlesOf(t, ref, switches), handles...)
	var notes, wrote []string
	for _, h := range all {
		w.report("configuring " + h.String())
		changed, err := r.o.configureDevice(ctx, t, site, h, handles, &notes)
		// A device that does not serve its interface keeps the fault
		// that detect recorded.
		if err != nil || h.serving() {
			r.o.fault(h.d, err)
		}
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

// configureDevice writes one resource's settings to the INDI device
// that serves it, and answers whether it sent any. A resource whose
// device does not serve its interface reports Error already (detect),
// and gets nothing until it heals (judgeInterfaces). The verdict
// records which of the two happened, so a heal writes the settings
// only when they did not go out. A write that fails counts as sent,
// because its fault is in the resource's status, and a heal that sent
// it again on each change would repeat the failure.
func (o *operator) configureDevice(ctx context.Context, t *tree, site *observatory.Observatory, h handle, all []handle, notes *[]string) (bool, error) {
	serving := h.serving()
	o.written(h.d, serving)
	if !serving {
		return false, nil
	}
	var changes []bool
	note := func(changed bool, err error) error {
		changes = append(changes, changed)
		return err
	}
	err := note(writeSettings(ctx, h, notes))
	if err == nil {
		err = configureTree(ctx, t, site, h, all, notes, note)
	}
	for _, c := range changes {
		if c {
			return true, err
		}
	}
	return false, err
}

// configureTree writes the settings that the tree decides for the
// interfaces that have them.
func configureTree(ctx context.Context, t *tree, site *observatory.Observatory, h handle, all []handle, notes *[]string, note func(bool, error) error) error {
	switch h.d.kind {
	case observatory.MountKind:
		err := note(location(ctx, h, site.Spec.Location))
		if err == nil {
			err = note(activeDevices(ctx, h, map[string]string{"ACTIVE_GPS": nameOf(all, observatory.GPSKind, "")}))
		}
		// INDI's mount starts locked, and only a dome's UNPARK report
		// unlocks it (Telescope::IsLocked in inditelescope.h, and
		// ISSnoopDevice in inditelescope.cpp). With no dome, no report
		// ever arrives, so a mount under DOME_LOCKS never unparks. So
		// a mount in an observatory with no dome ignores the dome.
		if err == nil {
			err = note(lockPolicy(ctx, h, "DOME_POLICY", "DOME_LOCKS", "DOME_IGNORED", len(domesOf(t, site)) > 0, notes))
		}
		return err
	case observatory.GPSKind:
		return note(location(ctx, h, site.Spec.Location))
	case observatory.CameraKind:
		return configureCamera(ctx, t, h, all, notes, note)
	}
	return nil
}

// configureCamera writes ACTIVE_DEVICES from the camera's train,
// SCOPE_INFO from the train's tube, and the upload settings of an
// imaging camera from the session's outbox. Each member of ACTIVE_DEVICES names
// the INDI device that serves the train's resource of that kind, so a
// camera that serves its train's FilterWheel names itself in
// ACTIVE_FILTER, which is how INDI's camera reads the filter of its own
// wheel.
func configureCamera(ctx context.Context, t *tree, h handle, all []handle, notes *[]string, note func(bool, error) error) error {
	train := h.d.object.Spec.OpticalTrain
	snooped := map[string]string{
		"ACTIVE_TELESCOPE": nameOf(all, observatory.MountKind, ""),
		"ACTIVE_FOCUSER":   nameOf(all, observatory.FocuserKind, train),
		"ACTIVE_FILTER":    nameOf(all, observatory.FilterWheelKind, train),
		"ACTIVE_ROTATOR":   nameOf(all, observatory.RotatorKind, train),
	}
	if err := note(activeDevices(ctx, h, snooped)); err != nil {
		return err
	}
	if trainObject, ok := t.trains[train]; ok {
		if tube, ok := t.tubeSpec(trainObject.Spec.OpticalTube); ok {
			info := map[string]float64{"FOCAL_LENGTH": tube.FocalLength, "APERTURE": tube.Aperture}
			if err := note(optional(ctx, h, "SCOPE_INFO", info, notes)); err != nil {
				return err
			}
		}
	}
	return uploadToOutbox(ctx, t, h, notes, note)
}

// nameOf answers the INDI name of the first device of a kind among the
// handles, on one train when train is not empty, or "" when there is
// none. A served resource's handle names its owner's device.
func nameOf(handles []handle, kind observatory.Kind, train string) string {
	for _, h := range handles {
		if h.d.kind == kind && (train == "" || h.d.object.Spec.OpticalTrain == train) {
			return h.name
		}
	}
	return ""
}

// domePolicies writes the rules that hold for every dome. The shutter
// follows the park state: it closes when the dome parks and opens when
// the dome unparks, and the dome enforces that alone. MOUNT_LOCKS keeps
// the dome from parking while a mount is unparked, and needs the
// mounts' park state that the operator relays (locks.go).
func domePolicies(ctx context.Context, t *tree, site *observatory.Observatory, handles map[string]handle) ([]string, error) {
	var notes []string
	for _, d := range t.devicesOf(serverRef{observatory.ObservatoryKind, site.Metadata.Name}, observatory.DomeKind) {
		h, ok := handles[d.key()]
		if !ok {
			continue
		}
		changed, err := h.setSwitches(ctx, "DOME_SHUTTER_PARK_POLICY", map[string]bool{
			"SHUTTER_CLOSE_ON_PARK":  true,
			"SHUTTER_OPEN_ON_UNPARK": true,
		})
		if err != nil {
			return nil, err
		}
		if changed {
			notes = append(notes, "wrote the shutter policy of "+d.kind.Name+" "+d.name())
		}
		changed, err = lockPolicy(ctx, h, "MOUNT_POLICY", "MOUNT_LOCKS", "MOUNT_IGNORED", true, &notes)
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
//
// After a change, the driver saves its configuration. INDI's mount
// reads DOME_POLICY from its configuration file again on each
// getProperties (Telescope::ISGetProperties). The operator's first
// write of the mount's location or ACTIVE_DEVICES saved the whole file,
// with the policy that the mount had then. Without the save, the next
// client that connects, such as PHD2 or KStars, turns the lock off. A
// saved policy also holds in a driver that restarts while the operator
// is down.
func lockPolicy(ctx context.Context, h handle, property, locks, ignored string, on bool, notes *[]string) (bool, error) {
	if _, ok := h.property(ctx, property); !ok {
		*notes = append(*notes, fmt.Sprintf("no %s on %s", property, h))
		return false, nil
	}
	changed, err := h.setSwitches(ctx, property, map[string]bool{locks: on, ignored: !on})
	if err != nil || !changed {
		return changed, err
	}
	if _, ok := h.property(ctx, "CONFIG_PROCESS"); !ok {
		*notes = append(*notes, fmt.Sprintf("no CONFIG_PROCESS on %s to save %s", h, property))
		return true, nil
	}
	sent, err := h.client().SetSwitches(h.name, "CONFIG_PROCESS", map[string]bool{"CONFIG_SAVE": true})
	if err == nil {
		_, err = h.client().Answered(ctx, sent)
	}
	if err != nil {
		return true, fmt.Errorf("%s: saving the configuration after %s: %w", h, property, err)
	}
	return true, nil
}
