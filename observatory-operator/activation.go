package main

// The activation steps, in the order of observatory.ActivationSteps.
// Plan 07 and the README state what each one does.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// stepTimeouts bounds each step from its start time, which the status
// records, so a step that an operator restart interrupted keeps its
// deadline. A step that passes its deadline fails the reservation. Wait
// has no limit: it waits for spec.start and for the telescope.
var stepTimeouts = map[observatory.StepName]time.Duration{
	// The first start of a pod on a node pulls its image, and the
	// simulators' image is 380 MB.
	observatory.StepStartSite:    10 * time.Minute,
	observatory.StepPowerOn:      10 * time.Minute,
	observatory.StepStartDevices: 10 * time.Minute,
	// A simulator connects in a few milliseconds, and real hardware in
	// seconds.
	observatory.StepConnect:   2 * time.Minute,
	observatory.StepConfigure: 2 * time.Minute,
	// A cooled camera takes minutes to reach its setpoint: the CCD
	// simulator cools 0.5 °C a second, so 35 °C takes 70 seconds.
	observatory.StepPrepare: 20 * time.Minute,
	// StartGuider pulls the PHD2 image and the weston image on a node's
	// first start, and PHD2 then connects real hardware in seconds.
	observatory.StepStartGuider: 10 * time.Minute,
	observatory.StepAbort:       2 * time.Minute,
	// Secure warms each cooled camera for up to warmLimit, and parks
	// the mount.
	observatory.StepSecure:      20 * time.Minute,
	observatory.StepStopGuider:  2 * time.Minute,
	observatory.StepDisconnect:  2 * time.Minute,
	observatory.StepStopDevices: 2 * time.Minute,
	observatory.StepPowerOff:    5 * time.Minute,
	// StopSite parks the dome, which turns to its park position.
	observatory.StepStopSite: 10 * time.Minute,
}

// activate runs the activation steps that are not finished, in order,
// until the telescope is Ready, a step fails, or the reservation ends.
func (r *runner) activate(ctx context.Context) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	go r.cancelOnEnding(ctx, cancel)
	steps := map[observatory.StepName]stepFunc{
		observatory.StepWait:         r.wait,
		observatory.StepStartSite:    r.startSite,
		observatory.StepPowerOn:      r.powerOn,
		observatory.StepStartDevices: r.startTelescopeDevices,
		observatory.StepConnect:      r.connect,
		observatory.StepConfigure:    r.configure,
		observatory.StepPrepare:      r.prepare,
		observatory.StepStartGuider:  r.startGuider,
	}
	for _, name := range observatory.ActivationSteps {
		switch r.step(name).State {
		case observatory.StepDone, observatory.StepSkipped:
			continue
		}
		if err := r.runStep(ctx, name, steps[name]); err != nil {
			return
		}
	}
	r.status.Phase, r.status.Step = observatory.ReservationReady, ""
	r.save(ctx)
	endpoint := r.endpoint()
	r.o.record(r.res, eventNormal, string(observatory.ReservationReady),
		fmt.Sprintf("Ready in %s at %s:%d", duration(time.Since(r.began(observatory.ActivationSteps[1:]))), endpoint.Host, endpoint.Port))
}

// cancelOnEnding ends ctx with errEnding when the reservation is
// deleted or reaches spec.end. The timer is a clock: spec.end.
func (r *runner) cancelOnEnding(ctx context.Context, cancel context.CancelCauseFunc) {
	for ctx.Err() == nil {
		wake := r.o.structure.wait()
		res, ok := r.o.snapshot().reservations[r.name]
		if !ok || ending(res, time.Now()) {
			cancel(errEnding)
			return
		}
		var end <-chan time.Time
		var timer *time.Timer
		if res.Spec.End != nil {
			timer = time.NewTimer(time.Until(*res.Spec.End))
			end = timer.C
		}
		select {
		case <-ctx.Done():
		case <-wake:
		case <-end:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// wait waits for spec.start, then for the telescope to exist and for
// no other reservation to hold it.
func (r *runner) wait(ctx context.Context, w *stepWork) (outcome, error) {
	for {
		if ctx.Err() != nil {
			return outcome{}, context.Cause(ctx)
		}
		r.refresh()
		res, now := r.res, time.Now()
		if res.Spec.Start != nil && now.Before(*res.Spec.Start) {
			w.report("waiting until " + res.Spec.Start.UTC().Format(time.RFC3339))
			r.o.sleepUntil(ctx, *res.Spec.Start)
			continue
		}
		wake := r.o.structure.wait()
		t := r.o.snapshot()
		if _, ok := t.telescopes[res.Spec.Telescope]; !ok {
			w.report("missing Telescope " + res.Spec.Telescope)
		} else if took, other := r.o.claims.take(t, res, now); took {
			return done("took Telescope %s", res.Spec.Telescope)
		} else {
			w.report("waiting for Reservation " + other)
		}
		select {
		case <-ctx.Done():
		case <-wake:
		}
	}
}

// siteOf answers the observatory of the reservation's telescope.
func (r *runner) siteOf(t *tree) (*observatory.Telescope, *observatory.Observatory, error) {
	telescope, ok := t.telescopes[r.res.Spec.Telescope]
	if !ok {
		return nil, nil, fmt.Errorf("missing Telescope %s", r.res.Spec.Telescope)
	}
	site, ok := t.observatories[telescope.Spec.Observatory]
	if !ok {
		return nil, nil, fmt.Errorf("missing Observatory %s of Telescope %s", telescope.Spec.Observatory, telescope.Metadata.Name)
	}
	return telescope, site, nil
}

// startSite starts the observatory's server and its devices, and
// connects and configures them. Another reservation of a telescope in
// the same observatory may have started them, and then the step finds
// each device connected and changes nothing.
func (r *runner) startSite(ctx context.Context, w *stepWork) (outcome, error) {
	t := r.o.snapshot()
	_, site, err := r.siteOf(t)
	if err != nil {
		return outcome{}, err
	}
	ref := serverRef{observatory.ObservatoryKind, site.Metadata.Name}
	devices := t.devicesOn(ref)
	if len(devices) == 0 {
		return skipped("no devices on Observatory %s", site.Metadata.Name)
	}
	lock := r.o.siteLock(site.Metadata.Name)
	if err := lock.acquire(ctx); err != nil {
		return outcome{}, err
	}
	defer lock.release()
	if err := r.o.startServer(ctx, w.report, ref, site.Metadata.UID, devices); err != nil {
		return outcome{}, err
	}
	switches, others := partition(devices)
	notes, err := r.powerSwitches(ctx, w, ref, switches, devices)
	if err != nil {
		return outcome{}, err
	}
	ordered := inOrder(others, siteOrder)
	handles, err := r.startAndConnect(ctx, w, ref, ordered)
	if err != nil {
		return outcome{}, err
	}
	configured, err := domePolicies(ctx, t, site, handles)
	if err != nil {
		return outcome{}, err
	}
	notes = append(notes, configured...)
	return done("%s", strings.Join(append([]string{fmt.Sprintf("connected %s on %s", names(devices), ref)}, notes...), "; "))
}

// powerSwitches starts and connects the Switch devices of a server, and
// switches on the outputs that the devices name.
func (r *runner) powerSwitches(ctx context.Context, w *stepWork, ref serverRef, switches, powered []*device) ([]string, error) {
	if _, err := r.startAndConnect(ctx, w, ref, switches); err != nil {
		return nil, err
	}
	return r.o.setOutputs(ctx, r.o.snapshot(), powered, true)
}

// startAndConnect starts the pods of the devices, waits until each
// driver defines its device on the server, and connects them in order.
func (r *runner) startAndConnect(ctx context.Context, w *stepWork, ref serverRef, devices []*device) (map[string]handle, error) {
	if err := r.o.startDevices(ctx, w.report, ref, devices); err != nil {
		return nil, err
	}
	if err := r.o.waitReady(ctx, w.report, devices); err != nil {
		return nil, err
	}
	handles, err := r.o.waitDefined(ctx, w.report, ref, devices)
	if err != nil {
		return nil, err
	}
	for _, d := range devices {
		h := handles[d.key()]
		w.report("connecting " + h.String())
		err := h.connect(ctx)
		r.o.fault(d, err)
		if err != nil {
			return nil, err
		}
	}
	return handles, nil
}

// telescope answers the reservation's telescope server and its devices.
func (r *runner) telescope(t *tree) (serverRef, *observatory.Telescope, []*device, error) {
	telescope, ok := t.telescopes[r.res.Spec.Telescope]
	if !ok {
		return serverRef{}, nil, nil, fmt.Errorf("missing Telescope %s", r.res.Spec.Telescope)
	}
	ref := serverRef{observatory.TelescopeKind, telescope.Metadata.Name}
	return ref, telescope, t.devicesOn(ref), nil
}

// powerOn starts the telescope's server and its Switch devices, and
// switches on the output of each device that names one.
func (r *runner) powerOn(ctx context.Context, w *stepWork) (outcome, error) {
	t := r.o.snapshot()
	ref, telescope, devices, err := r.telescope(t)
	if err != nil {
		return outcome{}, err
	}
	if len(devices) == 0 {
		return outcome{}, fmt.Errorf("no devices on Telescope %s", telescope.Metadata.Name)
	}
	if err := r.o.startServer(ctx, w.report, ref, telescope.Metadata.UID, devices); err != nil {
		return outcome{}, err
	}
	switches, _ := partition(devices)
	notes, err := r.powerSwitches(ctx, w, ref, switches, devices)
	if err != nil {
		return outcome{}, err
	}
	message := fmt.Sprintf("started %s for %d devices", ref, len(devices))
	if len(switches) > 0 {
		message += "; connected " + names(switches)
	}
	return done("%s", strings.Join(append([]string{message}, notes...), "; "))
}

// startTelescopeDevices creates the pod of each device that is not a
// Switch, and waits until each pod is Ready and its driver has defined
// its device on the server.
func (r *runner) startTelescopeDevices(ctx context.Context, w *stepWork) (outcome, error) {
	t := r.o.snapshot()
	ref, _, devices, err := r.telescope(t)
	if err != nil {
		return outcome{}, err
	}
	_, others := partition(devices)
	if len(others) == 0 {
		return skipped("no devices besides Switches")
	}
	if err := r.o.startDevices(ctx, w.report, ref, others); err != nil {
		return outcome{}, err
	}
	if err := r.o.waitReady(ctx, w.report, others); err != nil {
		return outcome{}, err
	}
	if _, err := r.o.waitDefined(ctx, w.report, ref, others); err != nil {
		return outcome{}, err
	}
	return done("started %d devices on %s", len(others), ref)
}

// telescopeHandles answers the handles of the telescope's devices that
// are not Switch devices, in connect order.
func (r *runner) telescopeHandles(ctx context.Context, w *stepWork) (*tree, []handle, error) {
	t := r.o.snapshot()
	ref, _, devices, err := r.telescope(t)
	if err != nil {
		return nil, nil, err
	}
	_, others := partition(devices)
	ordered := inOrder(others, connectOrder)
	byKey, err := r.o.waitDefined(ctx, w.report, ref, ordered)
	if err != nil {
		return nil, nil, err
	}
	var handles []handle
	for _, d := range ordered {
		handles = append(handles, byKey[d.key()])
	}
	return t, handles, nil
}

// connect connects the telescope's devices in connectOrder.
func (r *runner) connect(ctx context.Context, w *stepWork) (outcome, error) {
	_, handles, err := r.telescopeHandles(ctx, w)
	if err != nil {
		return outcome{}, err
	}
	if len(handles) == 0 {
		return skipped("no devices besides Switches")
	}
	var connected []string
	for _, h := range handles {
		w.report("connecting " + h.String())
		err := h.connect(ctx)
		r.o.fault(h.d, err)
		if err != nil {
			return outcome{}, err
		}
		connected = append(connected, h.String())
	}
	return done("connected in order: %s", strings.Join(connected, ", "))
}
