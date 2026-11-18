package main

// The activation steps, in the order of observatory.ActivationSteps.
// The README describes what each step does.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// stepTimeouts sets each step's time limit. runStep measures it from
// the step's StartTime in the status, so a step that an operator
// restart interrupted keeps its original deadline. A step that passes
// its deadline fails the reservation. Wait and Requires have no limit,
// because they wait for spec.start and for requirements that a person
// or the weather meets. A reservation still waiting in either step at
// dawn ends (requires.go).
var stepTimeouts = map[observatory.StepName]time.Duration{
	// The first pod of an image on a node pulls the image, and the
	// simulators' image is large.
	observatory.StepStartSite:    10 * time.Minute,
	observatory.StepPowerOn:      10 * time.Minute,
	observatory.StepStartDevices: 10 * time.Minute,
	// A simulator connects in milliseconds and real hardware in seconds.
	observatory.StepConnect:   2 * time.Minute,
	observatory.StepConfigure: 2 * time.Minute,
	// Activation and Deactivation have no step deadline. The timeout of
	// each action limits them.
	// StartGuider pulls the PHD2 image and the weston image the first
	// time it runs on a node. PHD2 then connects real hardware in
	// seconds.
	observatory.StepStartGuider: 10 * time.Minute,
	observatory.StepStopWork:    2 * time.Minute,
	observatory.StepStopGuider:  2 * time.Minute,
	observatory.StepDisconnect:  2 * time.Minute,
	observatory.StepStopDevices: 2 * time.Minute,
	observatory.StepPowerOff:    5 * time.Minute,
	observatory.StepStopSite:    5 * time.Minute,
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
		observatory.StepRequires:     r.requires,
		observatory.StepPowerOn:      r.powerOn,
		observatory.StepStartDevices: r.startTelescopeDevices,
		observatory.StepConnect:      r.connect,
		observatory.StepConfigure:    r.configure,
		observatory.StepActivation:   r.activation,
		observatory.StepStartGuider:  r.startGuider,
	}
	for _, name := range observatory.ActivationSteps {
		// A status that an older operator wrote can lack a step that
		// this version added, such as Requires. Skip that step.
		if r.step(name) == nil {
			continue
		}
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
	// Time spent in the Requires step waiting for a person or the
	// weather is not part of how long the equipment took to start.
	took := time.Since(r.began(observatory.ActivationSteps[1:])) - r.took(observatory.StepRequires)
	r.o.series.activated(r.res.Spec.Telescope, took)
	r.o.record(r.res, string(observatory.ReservationReady),
		fmt.Sprintf("Ready in %s at %s:%d", duration(took), endpoint.Host, endpoint.Port))
}

// cancelOnEnding cancels ctx with errEnding when the reservation is
// gone or ending returns true. It checks at spec.end and on each change
// to the tree.
func (r *runner) cancelOnEnding(ctx context.Context, cancel context.CancelCauseFunc) {
	for ctx.Err() == nil {
		wake := r.o.structure.wait()
		t := r.o.snapshot()
		res, ok := t.reservations[r.name]
		if !ok || ending(t, res, time.Now()) {
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

// wait waits for spec.start, then until the telescope exists, neither
// it nor its Observatory is being deleted, the Observatory's
// requirements are met, and no other reservation holds it. When it
// takes the telescope, it adds the finalizer to the telescope and its
// Observatory (finalizers.go).
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
		telescope, ok := t.telescopes[res.Spec.Telescope]
		// Check the Observatory's requirements before taking the
		// telescope, so a reservation that waits for them holds nothing,
		// and its telescope and devices stay Idle.
		var g gate
		if site, found := t.observatories[observatoryOf(telescope)]; ok && found {
			var err error
			if g, err = gateOn(t, t.gateOf(ofObservatory(site), serverRef{observatory.ObservatoryKind, site.Metadata.Name}), telescope, r.waitBegan()); err != nil {
				return outcome{}, err
			}
		}
		switch {
		case !ok:
			w.report("missing Telescope " + res.Spec.Telescope)
		case deletedParent(t, telescope) != "":
			w.report(deletedParent(t, telescope) + " is being deleted")
		case g.waiting != "":
			w.report(g.waiting)
		default:
			took, other := r.o.claims.take(t, res, now)
			if !took {
				w.report("waiting for Reservation " + other)
				break
			}
			if err := r.o.addParentFinalizers(ctx, w.report, telescope); err != nil {
				return outcome{}, err
			}
			return done("took Telescope %s", res.Spec.Telescope)
		}
		awaitChangeOr(ctx, wake, g.dawn)
	}
}

// observatoryOf returns the name of a telescope's Observatory, or ""
// when the telescope is nil.
func observatoryOf(telescope *observatory.Telescope) string {
	if telescope == nil {
		return ""
	}
	return telescope.Spec.Observatory
}

// waitBegan returns the time from which the Wait step finds the next
// dawn: the later of the step's start time and spec.start. A
// reservation made days ahead then ends at the dawn after its own
// night, not at the first dawn after it was created.
func (r *runner) waitBegan() time.Time {
	began := *r.step(observatory.StepWait).StartTime
	if start := r.res.Spec.Start; start != nil && start.After(began) {
		return *start
	}
	return began
}

// siteOf returns the reservation's telescope and its Observatory.
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

// startSite starts the observatory's server and devices, connects
// them, and writes their settings. It runs for every reservation in
// the observatory, including when another reservation already started
// them.
func (r *runner) startSite(ctx context.Context, w *stepWork) (outcome, error) {
	_, site, err := r.siteOf(r.o.snapshot())
	if err != nil {
		return outcome{}, err
	}
	// Take the lock before the snapshot, so two reservations that start
	// the site at the same time record the same devices.
	lock := r.o.siteLock(site.Metadata.Name)
	if err := lock.acquire(ctx); err != nil {
		return outcome{}, err
	}
	defer lock.release()
	t := r.o.snapshot()
	ref := serverRef{observatory.ObservatoryKind, site.Metadata.Name}
	if err := t.driverConflicts(ref); err != nil {
		return outcome{}, err
	}
	devices := t.devicesOn(ref)
	if err := r.recordSession(ctx, w, func(s *observatory.Session) bool { return s.Observatory != "" }, func(s *observatory.Session) {
		s.Observatory = site.Metadata.Name
		if len(devices) > 0 {
			s.Site = t.recordOf(ref, devices)
		}
	}); err != nil {
		return outcome{}, err
	}
	if len(devices) == 0 {
		return skipped("no devices on Observatory %s", site.Metadata.Name)
	}
	if _, err := r.o.startServer(ctx, w.report, ref, site.Metadata.UID, devices); err != nil {
		return outcome{}, err
	}
	switches, others := t.partition(devices)
	notes, err := r.powerSwitches(ctx, w, ref, switches, devices)
	if err != nil {
		return outcome{}, err
	}
	ordered := inOrder(others, siteOrder)
	handles, lacking, err := r.startAndConnect(ctx, w, ref, ordered)
	if err != nil {
		return outcome{}, err
	}
	notes = append(notes, lacking...)
	// The observatory's devices have no Configure step of their own, so
	// this step writes their settings from their specs.
	for _, h := range r.o.handlesOf(r.o.snapshot(), ref, devices) {
		if !h.serving() {
			continue
		}
		if _, err := writeSettings(ctx, h, &notes); err != nil {
			r.o.fault(h.d, err)
			return outcome{}, err
		}
	}
	configured, err := domePolicies(ctx, t, site, handles)
	if err != nil {
		return outcome{}, err
	}
	notes = append(notes, configured...)
	return done("%s", strings.Join(append([]string{fmt.Sprintf("connected %s on %s", names(devices), ref)}, notes...), "; "))
}

// powerSwitches starts and connects the Switch devices of a server, and
// switches on the outputs that the powered devices name. It returns a
// note for each output it switched and for each resource whose device
// does not serve its interface.
func (r *runner) powerSwitches(ctx context.Context, w *stepWork, ref serverRef, switches, powered []*device) ([]string, error) {
	_, lacking, err := r.startAndConnect(ctx, w, ref, switches)
	if err != nil {
		return nil, err
	}
	notes, err := r.o.setOutputs(ctx, r.o.snapshot(), powered, true)
	return append(notes, lacking...), err
}

// startAndConnect starts the pods of the devices, waits until each
// driver defines its device on the server, and connects them in order
// with connectAll. It returns the handles by device key, and a note for
// each resource whose device does not serve its interface.
func (r *runner) startAndConnect(ctx context.Context, w *stepWork, ref serverRef, devices []*device) (map[string]handle, []string, error) {
	if _, err := r.o.startDevices(ctx, w.report, ref, devices); err != nil {
		return nil, nil, err
	}
	if err := r.o.waitReady(ctx, w.report, devices); err != nil {
		return nil, nil, err
	}
	handles, err := r.o.waitDefined(ctx, w.report, ref, devices)
	if err != nil {
		return nil, nil, err
	}
	var ordered []handle
	for _, d := range devices {
		ordered = append(ordered, handles[d.key()])
	}
	_, lacking, err := r.connectAll(ctx, w, ordered)
	return handles, lacking, err
}

// connectAll connects each INDI device once, in order, through the
// handle that runs its driver. Then detect checks each resource against
// its device. A resource whose device does not serve its interface
// reports Error and the others continue, so a camera with no filter
// wheel attached does not stop the telescope. connected has a line for
// each INDI device with the resources that share it, and lacking has a
// line for each resource that is not served.
func (r *runner) connectAll(ctx context.Context, w *stepWork, handles []handle) (connected, lacking []string, err error) {
	for _, h := range owners(handles) {
		w.report("connecting " + h.String())
		err := h.connect(ctx)
		r.o.fault(h.d, err)
		if err != nil {
			return nil, nil, err
		}
	}
	served := map[string][]string{}
	for _, h := range handles {
		switch {
		case !r.o.detect(ctx, h):
			lacking = append(lacking, h.String()+" is not served: "+lowerFirst(firstNonEmpty(r.o.lackingOf(h.d), missingInterface(h).Error())))
		case !h.d.runsDriver():
			served[h.name] = append(served[h.name], h.String())
		}
	}
	for _, h := range owners(handles) {
		text := h.String()
		if names := served[h.name]; len(names) > 0 {
			text += " with " + strings.Join(names, ", ")
		}
		connected = append(connected, text)
	}
	return connected, lacking, nil
}

// telescope returns the reservation's telescope server, the Telescope,
// and its devices.
func (r *runner) telescope(t *tree) (serverRef, *observatory.Telescope, []*device, error) {
	telescope, ok := t.telescopes[r.res.Spec.Telescope]
	if !ok {
		return serverRef{}, nil, nil, fmt.Errorf("missing Telescope %s", r.res.Spec.Telescope)
	}
	ref := serverRef{observatory.TelescopeKind, telescope.Metadata.Name}
	return ref, telescope, t.devicesOn(ref), nil
}

// powerOn records the session, checks the outbox, starts the
// telescope's server and its Switch devices, and switches on the output
// of each device that names one.
func (r *runner) powerOn(ctx context.Context, w *stepWork) (outcome, error) {
	t := r.o.snapshot()
	ref, telescope, devices, err := r.telescope(t)
	if err != nil {
		return outcome{}, err
	}
	if len(devices) == 0 {
		return outcome{}, fmt.Errorf("no devices on Telescope %s", telescope.Metadata.Name)
	}
	if err := t.driverConflicts(ref); err != nil {
		return outcome{}, err
	}
	if err := r.recordSession(ctx, w, func(s *observatory.Session) bool { return len(s.Devices) > 0 }, func(s *observatory.Session) {
		s.Observatory = firstNonEmpty(s.Observatory, telescope.Spec.Observatory)
		s.Devices = t.recordOf(ref, devices)
		s.Trains, s.Tubes, s.Guiders = t.opticsOf(ref.name)
		s.Outbox = r.res.Spec.Outbox
		s.FITSKeywords = t.fitsKeywordsOf(ref, s.Observatory, devices, r.res.Spec.FITSKeywords)
	}); err != nil {
		return outcome{}, err
	}
	if err := r.checkOutbox(ctx, w, r.status.Session.Outbox); err != nil {
		return outcome{}, err
	}
	if _, err := r.o.startServer(ctx, w.report, ref, telescope.Metadata.UID, devices); err != nil {
		return outcome{}, err
	}
	switches, _ := t.partition(devices)
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
	_, others := t.partition(devices)
	if len(others) == 0 {
		return skipped("no devices besides Switches")
	}
	if _, err := r.o.startDevices(ctx, w.report, ref, others); err != nil {
		return outcome{}, err
	}
	if err := r.o.waitReady(ctx, w.report, others); err != nil {
		return outcome{}, err
	}
	if _, err := r.o.waitDefined(ctx, w.report, ref, others); err != nil {
		return outcome{}, err
	}
	return done("started %d devices on %s", len(driverDevices(others)), ref)
}

// telescopeHandles returns the handles of the telescope's devices that
// are not Switches, in connect order.
func (r *runner) telescopeHandles(ctx context.Context, w *stepWork) (*tree, []handle, error) {
	t := r.o.snapshot()
	ref, _, devices, err := r.telescope(t)
	if err != nil {
		return nil, nil, err
	}
	_, others := t.partition(devices)
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

// connect connects the telescope's devices in connectOrder through
// connectAll.
func (r *runner) connect(ctx context.Context, w *stepWork) (outcome, error) {
	_, handles, err := r.telescopeHandles(ctx, w)
	if err != nil {
		return outcome{}, err
	}
	if len(handles) == 0 {
		return skipped("no devices besides Switches")
	}
	connected, lacking, err := r.connectAll(ctx, w, handles)
	if err != nil {
		return outcome{}, err
	}
	return done("%s", strings.Join(append([]string{"connected in order: " + strings.Join(connected, ", ")}, lacking...), "; "))
}
