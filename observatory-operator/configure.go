package main

// The Configure step of an activation, which writes settings to the
// INDI devices of a reservation's telescope.

import (
	"context"
	"fmt"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// configure writes each device's settings once per session. Some come
// from the device's spec (settings.go). The operator derives the others
// from related resources: the observatory's location for the mount and
// the GPS, the mount's ACTIVE_GPS and DOME_POLICY, and the camera
// settings of configureCamera. Then it relays the domes' park state to
// the mount (locks.go), so the mount has that state before a procedure
// unparks it.
//
// The operator writes the settings again only after a device's driver
// restarts (steady.go). So a client such as a sequencer can change a
// camera's gain for one frame, and the operator does not undo it.
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
		// that detect recorded, so only clear or replace the fault for
		// a device that serves it.
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

// configureDevice writes one resource's settings to its INDI device, and
// returns whether any setting changed. When the device does not
// implement the resource's interface, it writes nothing, and
// checkInterfaces writes the settings when the interface appears. The
// settings are marked as written before the write, so a write that
// fails is not repeated on each change. Its error goes to the
// resource's status.
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

// configureTree writes the settings that the operator derives from
// related resources, for the kinds of device that have them.
func configureTree(ctx context.Context, t *tree, site *observatory.Observatory, h handle, all []handle, notes *[]string, note func(bool, error) error) error {
	switch h.d.kind {
	case observatory.MountKind:
		err := note(location(ctx, h, site.Spec.Location))
		if err == nil {
			err = note(activeDevices(ctx, h, map[string]string{"ACTIVE_GPS": nameOf(all, observatory.GPSKind, "")}))
		}
		// INDI's mount starts locked, and only a dome's UNPARK report
		// unlocks it (Telescope::IsLocked in inditelescope.h, and
		// ISSnoopDevice in inditelescope.cpp). In an observatory with
		// no dome, no report arrives and a mount under DOME_LOCKS could
		// never unpark, so it gets DOME_IGNORED.
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
// SCOPE_INFO from the train's tube, the upload settings for the
// session's outbox (outbox.go), and the FITS keywords of an imaging
// camera (fitskeywords.go). Each member of ACTIVE_DEVICES names the INDI
// device that serves the train's resource of that kind. So a camera
// that also serves its train's FilterWheel puts its own name in
// ACTIVE_FILTER, which is how an INDI camera reads the position of its
// built-in wheel.
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
	if err := uploadToOutbox(ctx, t, h, notes, note); err != nil {
		return err
	}
	return writeFITSKeywords(ctx, t, h, notes, note)
}

// nameOf returns the INDI name of the first device of a kind among the
// handles, on the given train when train is not empty, or "" when there
// is none.
func nameOf(handles []handle, kind observatory.Kind, train string) string {
	for _, h := range handles {
		if h.d.kind == kind && (train == "" || h.d.object.Spec.OpticalTrain == train) {
			return h.name
		}
	}
	return ""
}

// domePolicies writes the policies of each dome of the observatory. The
// shutter policy makes the dome driver close the shutter when the dome
// parks and open it when the dome unparks. MOUNT_LOCKS keeps the dome
// from parking while a mount is unparked, which works only with the
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

// lockPolicy sets a lock policy, DOME_POLICY on a mount or MOUNT_POLICY
// on a dome, to locks when on is true and to ignored otherwise. It adds
// a note when the driver does not define the property.
//
// After a change, it saves the driver's configuration. INDI's mount
// loads DOME_POLICY from its configuration file on each getProperties
// (Telescope::ISGetProperties), and the file already holds a policy,
// because the operator's write of the location or ACTIVE_DEVICES saved
// it. Without a save, the next client that connects, such as PHD2 or
// KStars, sets the policy back to the one in the file. The saved policy
// also survives a driver restart while the operator is down.
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
		return true, fmt.Errorf("saving the configuration of %s after writing %s: %w", h, property, err)
	}
	return true, nil
}
