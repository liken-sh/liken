package main

// The deactivation steps, in the order of
// observatory.DeactivationSteps. They run after a delete, which the
// finalizer holds until they are done, at spec.end, or after a delete
// of the reservation's Telescope or its Observatory. Each step acts
// on the devices that are there: a reservation whose activation failed
// at StartDevices has no device connected, and the Deactivation step
// then skips the actions of each device (procsteps.go).

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

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
				s.State, s.Summary = observatory.StepSkipped, sentence(errEnding.Error())
			}
		}
		r.status.Steps = append(r.status.Steps, pendingSteps(observatory.DeactivationSteps)...)
		r.status.Phase = observatory.ReservationDeactivating
		r.save(ctx)
		message := "Deactivating Telescope " + r.res.Spec.Telescope
		if parent := heldParentDeleted(r.o.snapshot(), r.res); parent != "" {
			message += ": " + parent + " was deleted"
		}
		r.o.record(r.res, string(observatory.ReservationDeactivating), message)
	}
	steps := map[observatory.StepName]stepFunc{
		observatory.StepAbort:        r.abort,
		observatory.StepDeactivation: r.deactivation,
		observatory.StepStopGuider:   r.stopGuider,
		observatory.StepDisconnect:   r.disconnect,
		observatory.StepStopDevices:  r.stopTelescopeDevices,
		observatory.StepPowerOff:     r.powerOff,
		observatory.StepStopSite:     r.stopSite,
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
	r.status.Phase, r.status.Step = observatory.ReservationReleased, ""
	r.save(ctx)
	r.o.record(r.res, string(observatory.ReservationReleased),
		fmt.Sprintf("Released in %s: Telescope %s is safe to power off", duration(time.Since(r.began(observatory.DeactivationSteps))), r.res.Spec.Telescope))
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
			s.State, s.Summary, s.StopTime = observatory.StepSkipped, "Reservation ended before it took the Telescope", &now
		}
	}
	r.status.Phase, r.status.Step = observatory.ReservationReleased, ""
	r.save(ctx)
	r.o.record(r.res, string(observatory.ReservationReleased), "Released before it took Telescope "+r.res.Spec.Telescope)
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
			notes = append(notes, fmt.Sprintf("missing the driver of %s %s on %s", d.kind.Name, d.name(), ref))
			continue
		}
		if connection, ok := h.property(ctx, "CONNECTION"); ok && slices.Equal(connection.On(), []string{"CONNECT"}) {
			out = append(out, h)
		}
	}
	return out, notes
}

// abort stops PHD2, ends the exposures that run, and stops the mount if
// it moves. A device that is idle receives nothing: the simulators answer an abort
// of nothing with no update at all. When a person deleted the
// telescope or its observatory, the step's summary begins with that,
// because the reservation's own spec and metadata do not show why it
// ended.
func (r *runner) abort(ctx context.Context, w *stepWork) (outcome, error) {
	var why []string
	if parent := heldParentDeleted(r.o.snapshot(), r.res); parent != "" {
		why = append(why, parent+" was deleted")
	}
	// PHD2 stops first, so no guide pulse follows the mount's stop.
	stopped, err := r.stopCapture(ctx, w)
	if err != nil {
		return outcome{}, err
	}
	var did []string
	if stopped != "" {
		did = append(did, stopped)
	}
	handles, notes := r.liveHandles(ctx, w)
	if len(handles) == 0 && len(did) == 0 {
		return skipped("%s", strings.Join(slices.Concat(why, []string{"no device connected"}, notes), "; "))
	}
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
		return skipped("%s", strings.Join(slices.Concat(why, []string{"found no exposure or slew to abort"}, notes), "; "))
	}
	return done("%s", strings.Join(slices.Concat(why, did, notes), "; "))
}

// disconnect disconnects the telescope's devices in the reverse order
// of Connect.
func (r *runner) disconnect(ctx context.Context, w *stepWork) (outcome, error) {
	handles, notes := r.liveHandles(ctx, w)
	if len(handles) == 0 {
		return skipped("%s", strings.Join(append([]string{"no device connected"}, notes...), "; "))
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
	return done("%s", strings.Join(append([]string{"disconnected in order: " + strings.Join(disconnected, ", ")}, notes...), "; "))
}

// stopTelescopeDevices stops the drivers of the telescope's devices,
// apart from its Switch devices, on the server that keeps running, and
// then deletes their pods (setDrivers).
func (r *runner) stopTelescopeDevices(ctx context.Context, w *stepWork) (outcome, error) {
	ref := serverRef{observatory.TelescopeKind, r.res.Spec.Telescope}
	switches, _ := partition(r.o.snapshot().devicesOn(ref))
	if _, _, err := r.o.setDrivers(ctx, w.report, ref, switches); err != nil {
		return outcome{}, err
	}
	stopped, err := r.o.stopPods(ctx, w.report, func(l map[string]string) bool {
		return l[labelServer] == ref.String() && l[labelRole] == roleDevice && !isSwitch(l[labelKind])
	})
	if err != nil {
		return outcome{}, err
	}
	if len(stopped) == 0 {
		return skipped("no device pod running")
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
	stopped, err := r.o.stopServer(ctx, w.report, ref)
	if err != nil {
		return outcome{}, err
	}
	if len(stopped) == 0 && len(notes) == 0 {
		return skipped("no server or Switch running")
	}
	if len(stopped) > 0 {
		notes = append(notes, "stopped "+strings.Join(stopped, ", "))
	}
	return done("%s", strings.Join(notes, "; "))
}

// stopSite disconnects the observatory's devices, switches their
// outputs off, and stops the observatory's server, unless a
// reservation of another telescope in the observatory holds its
// telescope. The dome's park is a procedure, which the Deactivation
// step ran while the mounts were still connected.
func (r *runner) stopSite(ctx context.Context, w *stepWork) (outcome, error) {
	t := r.o.snapshot()
	telescope, ok := t.telescopes[r.res.Spec.Telescope]
	if !ok {
		return skipped("missing Telescope %s: its Observatory is not known", r.res.Spec.Telescope)
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
			return skipped("in use by Reservation %s", holder)
		}
	}
	ref := serverRef{observatory.ObservatoryKind, site}
	if _, running := t.pods[ref.String()]; !running {
		return skipped("no server running for Observatory %s", site)
	}
	devices := t.devicesOn(ref)
	switches, others := partition(devices)
	live, did := r.o.connectedHandles(ctx, w.report, t, ref, inOrder(others, siteOrder))
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
	stopped, err := r.o.stopServer(ctx, w.report, ref)
	if err != nil {
		return outcome{}, err
	}
	did = append(did, "stopped "+strings.Join(stopped, ", "))
	return done("%s", strings.Join(did, "; "))
}
