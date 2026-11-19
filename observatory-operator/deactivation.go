package main

// The deactivation steps of a Reservation, in the order of
// observatory.DeactivationSteps. They run when the Reservation is
// deleted, when spec.end passes, and when its Telescope or Observatory
// is deleted. The Reservation's finalizer keeps the object until they
// finish. The steps find the telescope's devices by the server that
// their pods run on, not by their parents, so they also stop a device
// whose object or parent a person deleted during the session.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// deactivate appends the deactivation steps on its first call, and
// skips any activation step that has not finished. Then it runs each
// deactivation step that has not finished. A Reservation that never
// took its Telescope is released without running any step.
func (r *runner) deactivate(ctx context.Context) {
	if !r.tookTelescope() {
		r.endUnstarted(ctx)
		return
	}
	if findStep(r.status.Steps, observatory.StepStopWork) == nil {
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
		} else if why := dawnSummary(r.status.Steps); why != "" {
			message += ": " + lowerFirst(why)
		}
		r.o.record(r.res, string(observatory.ReservationDeactivating), message)
	}
	steps := map[observatory.StepName]stepFunc{
		observatory.StepStopWork:     r.stopWork,
		observatory.StepDeactivation: r.deactivation,
		observatory.StepStopGuider:   r.stopGuider,
		observatory.StepDisconnect:   r.disconnect,
		observatory.StepStopDevices:  r.stopTelescopeDevices,
		observatory.StepPowerOff:     r.powerOff,
	}
	for _, name := range observatory.DeactivationSteps {
		switch r.step(name).State {
		case observatory.StepDone, observatory.StepSkipped:
			continue
		case observatory.StepFailed:
			return
		}
		if name == observatory.StepStopSite {
			// sitestop.go: the wait for the other reservations of the
			// observatory comes before the step starts.
			if !r.runStopSite(ctx) {
				return
			}
			continue
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

// tookTelescope reports whether the Reservation took its Telescope,
// which happens when its Wait step finishes.
func (r *runner) tookTelescope() bool {
	s := r.step(observatory.StepWait)
	return s != nil && s.State == observatory.StepDone
}

// endUnstarted releases a Reservation that never took its Telescope.
// Such a Reservation started no device, so nothing needs deactivating.
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
	message := "Released before it took Telescope " + r.res.Spec.Telescope
	if why := dawnSummary(r.status.Steps); why != "" {
		message += ": " + lowerFirst(why)
	}
	r.o.record(r.res, string(observatory.ReservationReleased), message)
}

// settleWait limits how long a deactivation step waits for the INDI
// connection to the server and for each running driver to define its
// device. The wait matters after an operator restart, when the server
// sends every definition again on the new connection. A device whose
// driver does not define it in time gets a note in the step's summary,
// and the step acts on the other devices.
const settleWait = 30 * time.Second

// liveHandles returns handles, in connect order, for the connected
// devices other than Switches whose driver pod runs on the telescope's
// server, and a note for each driver that did not appear. Because it
// selects devices by their pod, a camera whose train a person deleted
// still has its exposure aborted and is still disconnected in order.
func (r *runner) liveHandles(ctx context.Context, w *stepWork) ([]handle, []string) {
	t := r.o.snapshot()
	ref, _, _, err := r.telescope(t)
	if err != nil {
		return nil, nil
	}
	_, others := t.partition(t.devicesRunningOn(ref))
	return r.o.connectedHandles(ctx, w.report, t, ref, inOrder(others, connectOrder))
}

// connectedHandles waits up to settleWait for the server's connection
// and for the driver of each device whose pod is ready. It returns
// handles for the devices whose CONNECTION property is CONNECT, and a
// note for each driver that did not appear. It returns nothing when the
// server's pod is not ready.
func (o *operator) connectedHandles(ctx context.Context, report func(string), t *tree, ref serverRef, devices []*device) ([]handle, []string) {
	p, ok := t.pods[ref.String()]
	if !ok || !p.ready() {
		return nil, nil
	}
	var running []*device
	for _, d := range devices {
		if _, p, ok := t.podOf(d); ok && p.ready() {
			running = append(running, d)
		}
	}
	wait, cancel := context.WithTimeout(ctx, settleWait)
	defer cancel()
	byKey, _ := o.waitDefined(wait, report, nil, ref, running)
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

// stopWork stops PHD2, then aborts each exposure or slew in progress.
// It sends ABORT only to a device whose property reports Busy, because
// the simulators send no update when they abort an idle device. When a
// person deleted the Telescope or its Observatory, the summary starts
// with that, because nothing in the Reservation shows why it ended.
func (r *runner) stopWork(ctx context.Context, w *stepWork) (outcome, error) {
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
			return fmt.Errorf("aborting %s: %w", h, err)
		}
		if err := r.o.waitFor(ctx, nil, func(*tree) (bool, string, error) { return !busy(h, watched), "", nil }); err != nil {
			return fmt.Errorf("waiting for %s to stop reporting %s Busy: %w", h, watched, err)
		}
		did = append(did, "aborted "+h.String())
		return nil
	}
	for _, h := range handles {
		of := interfaces[h.d.kind]
		if of.abort == "" || !h.serving() {
			continue
		}
		if err := stop(h, of.watched, of.abort); err != nil {
			return outcome{}, err
		}
	}
	if len(did) == 0 {
		return skipped("%s", strings.Join(slices.Concat(why, []string{"found no exposure or slew to abort"}, notes), "; "))
	}
	return done("%s", strings.Join(slices.Concat(why, did, notes), "; "))
}

// disconnect disconnects the telescope's devices in the reverse of the
// Connect order. A served device is the same INDI device as its owner,
// so only the owner is disconnected.
func (r *runner) disconnect(ctx context.Context, w *stepWork) (outcome, error) {
	handles, notes := r.liveHandles(ctx, w)
	handles = owners(handles)
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

// stopTelescopeDevices stops every driver on the telescope's server
// except the drivers of the Switches, which PowerOff still uses, and
// then deletes the device pods of the stopped drivers. It selects the
// pods by label, so it also deletes the pod of a device whose object a
// person deleted. The server's session record still lists that device
// after its pod and object are gone, so PowerOff can switch its output
// off (poweredBy).
func (r *runner) stopTelescopeDevices(ctx context.Context, w *stepWork) (outcome, error) {
	ref := serverRef{observatory.TelescopeKind, r.res.Spec.Telescope}
	t := r.o.snapshot()
	switches, _ := t.partition(t.releasedOn(ref))
	if _, err := r.o.setDrivers(ctx, w.report, ref, switches); err != nil {
		return outcome{}, err
	}
	kept := map[string]bool{}
	for _, d := range driverDevices(switches) {
		kept[d.key()] = true
	}
	stopped, err := r.o.stopPods(ctx, w.report, func(l map[string]string) bool {
		return l[labelServer] == ref.String() && l[labelRole] == roleDevice && !kept[l[labelKind]+"/"+l[labelResource]]
	})
	if err != nil {
		return outcome{}, err
	}
	if len(stopped) == 0 {
		return skipped("no device pod running")
	}
	return done("stopped %s", strings.Join(stopped, ", "))
}

// powerOff switches off the outputs of the telescope's devices, except
// an output that a device on another server still uses, then
// disconnects the Switches and stops their pods and the telescope's
// server. The server stops last, because the operator reaches a Switch
// through it. releasedOn and poweredBy also find a device whose train
// or object a person deleted.
func (r *runner) powerOff(ctx context.Context, w *stepWork) (outcome, error) {
	t := r.o.snapshot()
	ref := serverRef{observatory.TelescopeKind, r.res.Spec.Telescope}
	devices := t.releasedOn(ref)
	notes, err := r.o.setOutputs(ctx, t, t.unpowered(t.poweredBy(ref, devices), ref), false)
	if err != nil {
		return outcome{}, err
	}
	switches, _ := t.partition(devices)
	live, missing := r.o.connectedHandles(ctx, w.report, t, ref, switches)
	notes = append(notes, missing...)
	for _, h := range owners(live) {
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

// stopSite disconnects the Observatory's devices, switches off the
// outputs that no device on another server uses, and stops the
// Observatory's server. runStopSite starts it only when siteStopFor
// (sitestop.go) chose this reservation to stop the Observatory, and
// stopSite asks siteStopFor again under the site's lock. So a telescope
// that became Ready, Activating, or in Error, or a reservation that
// began to deactivate since the wait, cannot make two reservations stop
// the Observatory, or none. It does not park the dome, because the
// Deactivation step already ran the dome's park procedure while the
// mounts were connected.
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
	t = r.o.snapshot()
	switch stop := r.o.siteStopFor(t, r.res); stop.action {
	case siteInUse, siteStoppedByOther:
		return skipped("%s", stop.skipMessage())
	case siteWait:
		return outcome{}, errSiteChanged
	}
	ref := serverRef{observatory.ObservatoryKind, site}
	if _, running := t.pods[ref.String()]; !running {
		return skipped("no server running for Observatory %s", site)
	}
	devices := t.releasedOn(ref)
	switches, others := t.partition(devices)
	live, did := r.o.connectedHandles(ctx, w.report, t, ref, inOrder(others, siteOrder))
	live = owners(live)
	slices.Reverse(live)
	for _, h := range live {
		if err := h.disconnect(ctx); err != nil {
			return outcome{}, err
		}
	}
	notes, err := r.o.setOutputs(ctx, t, t.unpowered(devices, ref), false)
	if err != nil {
		return outcome{}, err
	}
	did = append(did, notes...)
	liveSwitches, missing := r.o.connectedHandles(ctx, w.report, t, ref, switches)
	did = append(did, missing...)
	for _, h := range owners(liveSwitches) {
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
