package main

// The deactivation steps, in the order of
// observatory.DeactivationSteps. They run after a delete, which the
// finalizer holds until they are done, or at spec.end. Each step acts
// on the devices that are there: a reservation whose activation failed
// at StartDevices has no device connected, and Secure then has nothing
// to park.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// warmLimit bounds the warm-up of one camera in Secure. A cooler cannot
// warm a sensor above the air around it, so on a cold night a sensor
// may never reach warmTarget. After this wait Secure switches the
// cooler off where the sensor is, and notes the temperature.
const warmLimit = 10 * time.Minute

// deactivate begins deactivation, if it has not begun, and runs the
// deactivation steps that are not finished.
func (r *runner) deactivate(ctx context.Context) {
	if !r.tookTelescope() {
		r.endUnstarted(ctx)
		return
	}
	if findStep(r.status.Steps, observatory.StepAbort) == nil {
		for i := range r.status.Steps {
			s := &r.status.Steps[i]
			if s.State == observatory.StepPending || s.State == observatory.StepRunning {
				s.State, s.Message = observatory.StepSkipped, errEnding.Error()
			}
		}
		r.status.Steps = append(r.status.Steps, pendingSteps(observatory.DeactivationSteps)...)
		r.status.Phase = observatory.ReservationDeactivating
		r.save(ctx)
		r.o.record(r.res, eventNormal, string(observatory.ReservationDeactivating), "deactivating the Telescope "+r.res.Spec.Telescope)
	}
	steps := map[observatory.StepName]stepFunc{
		observatory.StepAbort:       r.abort,
		observatory.StepSecure:      r.secure,
		observatory.StepDisconnect:  r.disconnect,
		observatory.StepStopDevices: r.stopTelescopeDevices,
		observatory.StepPowerOff:    r.powerOff,
		observatory.StepStopSite:    r.stopSite,
	}
	for _, name := range observatory.DeactivationSteps {
		switch r.step(name).State {
		case observatory.StepDone, observatory.StepSkipped:
			continue
		case observatory.StepFailed:
			return
		}
		if err := r.runStep(ctx, name, steps[name]); err != nil {
			return
		}
	}
	r.status.Phase = observatory.ReservationReleased
	r.save(ctx)
	r.o.record(r.res, eventNormal, string(observatory.ReservationReleased), "the devices of the Telescope "+r.res.Spec.Telescope+" are safe to power off")
}

// tookTelescope reports whether the reservation ever held its
// telescope.
func (r *runner) tookTelescope() bool {
	s := r.step(observatory.StepWait)
	return s != nil && s.State == observatory.StepDone
}

// endUnstarted ends a reservation that never took its telescope. It
// started nothing, so it has nothing to deactivate.
func (r *runner) endUnstarted(ctx context.Context) {
	for i := range r.status.Steps {
		s := &r.status.Steps[i]
		if s.State != observatory.StepDone && s.State != observatory.StepSkipped {
			now := stamp()
			s.State, s.Message, s.FinishTime = observatory.StepSkipped, "the reservation ended before it took the telescope", &now
		}
	}
	r.status.Phase = observatory.ReservationReleased
	r.save(ctx)
	r.o.record(r.res, eventNormal, string(observatory.ReservationReleased), "the reservation ended before it took the Telescope "+r.res.Spec.Telescope)
}

// settleWait bounds how long a deactivation step waits for the
// operator's INDI connection to a server whose pod runs, and for each
// running device's driver to define its device. After an operator
// restart, the connection opens and the server sends every definition
// again within a second. A device whose driver does not appear in this
// time is noted, and the step acts on the others.
const settleWait = 30 * time.Second

// liveHandles answers the connected devices among the telescope's
// devices that are not Switch devices, in connect order. A device whose
// pod does not run is left out.
func (r *runner) liveHandles(ctx context.Context, w *stepWork) ([]handle, []string) {
	t := r.o.snapshot()
	ref, _, devices, err := r.telescope(t)
	if err != nil {
		return nil, nil
	}
	_, others := partition(devices)
	return r.o.connectedHandles(ctx, w.report, t, ref, inOrder(others, connectOrder))
}

// connectedHandles waits up to settleWait for the server's connection
// and for the driver of each device whose pod runs, and answers the
// devices that report themselves connected, with a note for each
// driver that did not appear.
func (o *operator) connectedHandles(ctx context.Context, report func(string), t *tree, ref serverRef, devices []*device) ([]handle, []string) {
	p, ok := t.pods[ref.String()]
	if !ok || !p.ready() {
		return nil, nil
	}
	var running []*device
	for _, d := range devices {
		if name, err := objectName(d.kind, d.name()); err == nil && t.pods[name] != nil && t.pods[name].ready() {
			running = append(running, d)
		}
	}
	wait, cancel := context.WithTimeout(ctx, settleWait)
	defer cancel()
	byKey, _ := o.waitDefined(wait, report, ref, running)
	var out []handle
	var notes []string
	for _, d := range running {
		h, found := byKey[d.key()]
		if !found {
			notes = append(notes, fmt.Sprintf("the driver of the %s %s did not define its device on %s", d.kind.Name, d.name(), ref))
			continue
		}
		if connection, ok := h.property(ctx, "CONNECTION"); ok && slices.Equal(connection.On(), []string{"CONNECT"}) {
			out = append(out, h)
		}
	}
	return out, notes
}

// abort ends the exposures that run and stops the mount if it moves. A
// device that is idle receives nothing: the simulators answer an abort
// of nothing with no update at all.
func (r *runner) abort(ctx context.Context, w *stepWork) (outcome, error) {
	handles, notes := r.liveHandles(ctx, w)
	if len(handles) == 0 {
		return skipped("%s", strings.Join(append([]string{"no device is connected"}, notes...), "; "))
	}
	var did []string
	stop := func(h handle, watched, property string) error {
		if !busy(h, watched) {
			return nil
		}
		w.report("aborting " + h.String())
		if _, err := h.client().SetSwitches(h.name, property, map[string]bool{"ABORT": true}); err != nil {
			return fmt.Errorf("%s: %w", h, err)
		}
		if err := r.o.waitFor(ctx, nil, func(*tree) (bool, string, error) { return !busy(h, watched), "", nil }); err != nil {
			return fmt.Errorf("%s still reports %s Busy: %w", h, watched, err)
		}
		did = append(did, "aborted "+h.String())
		return nil
	}
	for _, h := range handles {
		var err error
		switch h.d.kind {
		case observatory.CameraKind:
			err = stop(h, "CCD_EXPOSURE", "CCD_ABORT_EXPOSURE")
		case observatory.MountKind:
			err = stop(h, "EQUATORIAL_EOD_COORD", "TELESCOPE_ABORT_MOTION")
		}
		if err != nil {
			return outcome{}, err
		}
	}
	if len(did) == 0 {
		return skipped("%s", strings.Join(append([]string{"no exposure ran and no mount moved"}, notes...), "; "))
	}
	return done("%s", strings.Join(append(did, notes...), "; "))
}

// secure switches the flat panels off, closes the dust caps, parks the
// mount, and warms each cooled camera before it switches the cooler
// off.
func (r *runner) secure(ctx context.Context, w *stepWork) (outcome, error) {
	handles, notes := r.liveHandles(ctx, w)
	if len(handles) == 0 {
		return skipped("%s", strings.Join(append([]string{"no device is connected"}, notes...), "; "))
	}
	var did []string
	moves := []struct {
		kind             observatory.Kind
		property, member string
		// doing names the change while it runs, and verb once it is done.
		doing, verb string
	}{
		{observatory.FlatPanelKind, "FLAT_LIGHT_CONTROL", "FLAT_LIGHT_OFF", "switching off", "switched off"},
		{observatory.DustCapKind, "CAP_PARK", "PARK", "closing", "closed"},
		{observatory.MountKind, "TELESCOPE_PARK", "PARK", "parking", "parked"},
	}
	for _, move := range moves {
		for _, h := range ofKind(handles, move.kind) {
			w.report(fmt.Sprintf("%s %s", move.doing, h))
			changed, err := h.switchOn(ctx, move.property, move.member)
			if err != nil {
				return outcome{}, err
			}
			if changed {
				did = append(did, move.verb+" "+h.String())
			}
		}
	}
	var cooled []handle
	for _, h := range ofKind(handles, observatory.CameraKind) {
		if h.d.object.Spec.Temperature != nil || on(h, "CCD_COOLER", "COOLER_ON") {
			cooled = append(cooled, h)
		}
	}
	for _, h := range coolable(ctx, cooled, &notes) {
		note, err := warm(ctx, h, w.report)
		if err != nil {
			return outcome{}, err
		}
		did = append(did, note)
	}
	if len(did) == 0 {
		return skipped("%s", firstNonEmpty(strings.Join(notes, "; "), "every device was secure"))
	}
	return done("%s", strings.Join(append(did, notes...), "; "))
}

// warm raises a camera's setpoint to warmTarget, waits up to warmLimit,
// and switches the cooler off.
func warm(ctx context.Context, h handle, report func(string)) (string, error) {
	p, _ := h.client().Property(h.name, "CCD_TEMPERATURE")
	now, _ := number(p, "CCD_TEMPERATURE_VALUE")
	note := fmt.Sprintf("%s was at %.1f °C", h.String(), now)
	if now < warmTarget-coolTolerance {
		report(fmt.Sprintf("warming %s from %.1f °C to %.1f °C", h, now, warmTarget))
		wait, cancel := context.WithTimeout(ctx, warmLimit)
		err := setTemperature(wait, h, warmTarget, report)
		cancel()
		switch {
		case err == nil:
			note = fmt.Sprintf("warmed %s to %.1f °C", h.String(), warmTarget)
		case ctx.Err() != nil:
			return "", err
		default:
			note = fmt.Sprintf("warmed %s for %v: %v", h.String(), warmLimit, err)
		}
	}
	if _, ok := h.client().Property(h.name, "CCD_COOLER"); ok {
		changed, err := h.switchOn(ctx, "CCD_COOLER", "COOLER_OFF")
		if err != nil {
			return "", err
		}
		if changed {
			note += ", and switched its cooler off"
		} else {
			note += ", and its cooler was off"
		}
	}
	return note, nil
}

// disconnect disconnects the telescope's devices in the reverse order
// of Connect.
func (r *runner) disconnect(ctx context.Context, w *stepWork) (outcome, error) {
	handles, notes := r.liveHandles(ctx, w)
	if len(handles) == 0 {
		return skipped("%s", strings.Join(append([]string{"no device is connected"}, notes...), "; "))
	}
	slices.Reverse(handles)
	var disconnected []string
	for _, h := range handles {
		w.report("disconnecting " + h.String())
		if err := h.disconnect(ctx); err != nil {
			return outcome{}, err
		}
		disconnected = append(disconnected, h.String())
	}
	return done("%s", strings.Join(append([]string{"disconnected " + strings.Join(disconnected, ", ") + ", in that order"}, notes...), "; "))
}

// stopTelescopeDevices deletes the pods of the telescope's devices,
// apart from its Switch devices.
func (r *runner) stopTelescopeDevices(ctx context.Context, w *stepWork) (outcome, error) {
	ref := serverRef{observatory.TelescopeKind, r.res.Spec.Telescope}
	stopped, err := r.o.stopPods(ctx, w.report, func(l map[string]string) bool {
		return l[labelServer] == ref.String() && l[labelRole] == roleDevice && !isSwitch(l[labelKind])
	})
	if err != nil {
		return outcome{}, err
	}
	if len(stopped) == 0 {
		return skipped("no device pod ran")
	}
	return done("stopped %s", strings.Join(stopped, ", "))
}

// powerOff switches off the outputs that the telescope's devices name,
// then stops the Switch pods and the telescope's server. The server
// stops last, because the operator reaches a Switch through it.
func (r *runner) powerOff(ctx context.Context, w *stepWork) (outcome, error) {
	t := r.o.snapshot()
	ref := serverRef{observatory.TelescopeKind, r.res.Spec.Telescope}
	devices := t.devicesOn(ref)
	notes, err := r.o.setOutputs(ctx, t, devices, false)
	if err != nil {
		return outcome{}, err
	}
	switches, _ := partition(devices)
	live, missing := r.o.connectedHandles(ctx, w.report, t, ref, switches)
	notes = append(notes, missing...)
	for _, h := range live {
		if err := h.disconnect(ctx); err != nil {
			return outcome{}, err
		}
	}
	stopped, err := r.o.stopPods(ctx, w.report, onServer(ref, func(string) bool { return true }))
	if err != nil {
		return outcome{}, err
	}
	if len(stopped) == 0 && len(notes) == 0 {
		return skipped("no server or Switch ran")
	}
	if len(stopped) > 0 {
		notes = append(notes, "stopped "+strings.Join(stopped, ", "))
	}
	return done("%s", strings.Join(notes, "; "))
}

// stopSite parks the domes, disconnects the observatory's devices,
// switches their outputs off, and stops the observatory's server,
// unless a reservation of another telescope in the observatory holds
// its telescope.
func (r *runner) stopSite(ctx context.Context, w *stepWork) (outcome, error) {
	t := r.o.snapshot()
	telescope, ok := t.telescopes[r.res.Spec.Telescope]
	if !ok {
		return skipped("the Telescope %s does not exist, so its observatory is not known", r.res.Spec.Telescope)
	}
	site := telescope.Spec.Observatory
	lock := r.o.siteLock(site)
	if err := lock.acquire(ctx); err != nil {
		return outcome{}, err
	}
	defer lock.release()
	for other := range r.o.claims.held() {
		if scope, ok := t.telescopes[other]; ok && other != telescope.Metadata.Name && scope.Spec.Observatory == site {
			holder, _ := r.o.claims.holderOf(other)
			return skipped("the server of the Observatory %s stays up for the Reservation %s", site, holder)
		}
	}
	ref := serverRef{observatory.ObservatoryKind, site}
	if _, running := t.pods[ref.String()]; !running {
		return skipped("the Observatory %s runs no server", site)
	}
	devices := t.devicesOn(ref)
	switches, others := partition(devices)
	live, did := r.o.connectedHandles(ctx, w.report, t, ref, inOrder(others, siteOrder))
	for _, h := range live {
		if h.d.kind == observatory.DomeKind {
			w.report("parking " + h.String())
			changed, err := h.switchOn(ctx, "DOME_PARK", "PARK")
			if err != nil {
				return outcome{}, err
			}
			if changed {
				did = append(did, "parked "+h.String())
			}
		}
	}
	slices.Reverse(live)
	for _, h := range live {
		if err := h.disconnect(ctx); err != nil {
			return outcome{}, err
		}
	}
	notes, err := r.o.setOutputs(ctx, t, devices, false)
	if err != nil {
		return outcome{}, err
	}
	did = append(did, notes...)
	liveSwitches, missing := r.o.connectedHandles(ctx, w.report, t, ref, switches)
	did = append(did, missing...)
	for _, h := range liveSwitches {
		if err := h.disconnect(ctx); err != nil {
			return outcome{}, err
		}
	}
	stopped, err := r.o.stopPods(ctx, w.report, onServer(ref, func(string) bool { return true }))
	if err != nil {
		return outcome{}, err
	}
	did = append(did, "stopped "+strings.Join(stopped, ", "))
	return done("%s", strings.Join(did, "; "))
}
