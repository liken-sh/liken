package main

// The controller behind the Display resource. For each monitor the
// operator probes, it creates a Display, writes its status, and writes
// the declared values to the panel where the panel differs from them.
// Before it applies an override, it saves the panel's current value in
// status.captured, so the value survives an operator restart. In this
// file, "capture" means that saved panel value. It is not a screenshot,
// which is what "capture" means in the capture API.
//
// A DDC read can wake a panel from standby, so the operator reads a
// panel only when it has a reason to:
//
//   - the poll, once per poll window, finds a value that a person
//     changed at the panel's buttons, because DDC/CI sends no event.
//     It skips a panel whose last power reading is anything but on. A
//     panel with no power control counts as on, so the poll reads it
//     even when it is dark.
//   - the probe reads a monitor that is new on its connector, in any
//     power state (panels.go).
//   - the probe runs again once per backstop interval for a panel that
//     refused the last probe, because a person can turn DDC/CI on in
//     the panel's menu and that raises no event.
//   - the capture reads the value that an override replaces.
//   - each write reads back its value, and a claim's brightness or
//     power write reads the range and the current value first
//     (controls.go).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// A restore writes a value and reads it back, and retries with a delay
// that doubles from restoreFirstDelay up to restoreMaxDelay, because a
// panel that is waking responds late. Each write can show on the
// screen, so the attempts are capped: the waits between them are 1, 2,
// 4, 8, 16, 30, and 30 seconds. A restore that gives up is recorded in
// status.unconfirmed, and the next edit to spec starts a new restore.
const (
	restoreFirstDelay = 1 * time.Second
	restoreMaxDelay   = 30 * time.Second
	restoreAttempts   = 8
)

// displayControl reconciles the Displays of this node. Tests replace
// the function fields.
type displayControl struct {
	client *apiclient.Client
	// displays reads Displays from the watch's store where it can
	// (objectcache.go).
	displays *displayStore
	node     string
	controls *panelControls
	// outputs is the same sysfs walk that the slice publisher makes.
	outputs func() []Output
	now     func() time.Time
	wait    func(ctx context.Context, delay time.Duration) error
	wakes   chan struct{}
	// restoring is the set of connectors that have a restore running.
	// A restore can wait minutes for a panel to respond, so it runs in
	// its own goroutine, and this set keeps a later pass from starting
	// a second restore on the same connector.
	mu        sync.Mutex
	restoring map[string]bool
	// abandoned lists, by connector, the restores that ran out of
	// attempts. Only the pass writes status, so a restore leaves its
	// result here for the next pass to record.
	abandoned map[string][]abandonedRestore
	// pollFaults is the fault that each connector's last poll logged,
	// kept until a poll reads that connector cleanly, so a panel that
	// stays quiet logs one line and not one per window.
	pollFaults map[string]string
	faults     io.Writer
	// The loop ticks at the poll interval, because a poll runs only
	// inside a pass.
	tick time.Duration
	// swept and sweptAt are the panels and the time of the last listing
	// of every Display (sweepDue).
	swept   []string
	sweptAt time.Time
	// prepared and allocated return the output devices that claims
	// hold. prepared reads the claim specs on disk. allocated asks the
	// API server, which also reports a claim whose prepare has not
	// finished (screenholds.go).
	prepared  func() (map[string]bool, error)
	allocated func() (map[string]bool, error)
	// setMode is the prepare path's mode switch: it writes the mode
	// record, rewrites the compositor's config, restarts the
	// compositor, and reads the mode back. restart restarts the
	// compositor with no config change. Each does nothing while nil.
	setMode func(ctx context.Context, output Output, mode string) error
	restart func() error
	// served returns the mode the compositor reports on each connector.
	// It is nil until the operator opens its Wayland connection.
	served func() servedOutputs
	// lastOutputChange is when the compositor last reported an output
	// arriving or leaving. restartNeeded stays true from a report of a
	// re-created output until the compositor restarts. The Wayland
	// watch writes both from its own goroutine, so both are guarded by
	// mu.
	lastOutputChange time.Time
	restartNeeded    bool
	// waitLogged is true once the log has said why a restart waits.
	// Only the pass uses it, so it needs no lock.
	waitLogged bool
	// metrics is nil in tests with no listener, and a nil metrics
	// records nothing.
	metrics *metrics
}

func newDisplayControl(client *apiclient.Client, node string, controls *panelControls, outputs func() []Output) *displayControl {
	return &displayControl{
		client:   client,
		displays: newDisplayStore(client, informer.View{}),
		node:     node,
		controls: controls,
		outputs:  outputs,
		now:      time.Now,
		wait:     waitFor,
		tick:     pollInterval,
		prepared: preparedOutputs,
		allocated: func() (map[string]bool, error) {
			return allocatedOutputs(client, node, clusterStores{})
		},
		wakes:      make(chan struct{}, 1),
		restoring:  map[string]bool{},
		abandoned:  map[string][]abandonedRestore{},
		pollFaults: map[string]string{},
		faults:     os.Stderr,
	}
}

func waitFor(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// wake asks the loop to run another pass. It drops the request when one
// is already pending, because the next pass handles every reason.
func (d *displayControl) wake() {
	select {
	case d.wakes <- struct{}{}:
	default:
	}
}

// run runs a pass at start, on each wake, and on each tick. The Display
// watch wakes it when a Display is created or deleted or its spec
// changes, but not on a status write that leaves status.node alone
// (displays.go). The slice publisher wakes it when the hardware
// changes, a restore wakes it when it ends, and a status write that
// fails with a conflict wakes it once.
//
// The tick starts the passes that run the poll. It is also the only
// retry of a pass that failed, for example on a failed status write.
func (d *displayControl) run(ctx context.Context) {
	tick := time.NewTicker(d.tick)
	defer tick.Stop()
	for {
		err := d.metrics.reconciled(kindDisplay, func() error { return d.pass(ctx) })
		if err != nil {
			fmt.Fprintf(os.Stderr, "reconciling the displays: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-d.wakes:
		case <-tick.C:
		}
	}
}

// pass reconciles every panel connected to this node, and then every
// Display of this node whose panel is gone. It never deletes a Display,
// because the Display keeps the captured values that restore the panel
// when it returns. The Connected condition reports that it is gone.
func (d *displayControl) pass(ctx context.Context) error {
	outputs := d.outputs()
	present := map[string]Output{}
	for _, output := range outputs {
		if !output.Connected {
			continue
		}
		if name := monitorID(output.Monitor); name != "" {
			present[name] = output
		}
	}
	ambiguous := ambiguousAddresses(outputs)
	// A panel that leaves raises a uevent that wakes this loop, so the
	// list of every Display, which finds the Displays of panels that
	// left, runs when the set of panels changes and otherwise only once
	// per backstop interval.
	sweep := d.sweepDue(present)
	var published []Display
	if sweep {
		listed, err := d.displays.list()
		if err != nil {
			return err
		}
		published = listed
	}
	var failures []error
	held, err := d.claimed()
	if err != nil {
		failures = append(failures, err)
	}
	for name, output := range present {
		// A connector in a short hotplug drop counts as present, so its
		// Display is not marked absent. The panel cannot respond during
		// the drop, so the pass skips it (hotplughold.go).
		if output.Held {
			continue
		}
		if err := d.reconcile(ctx, name, output, held, ambiguous[name]); err != nil {
			failures = append(failures, fmt.Errorf("reconciling the Display of %s: %w", name, err))
		}
	}
	for _, display := range published {
		if display.Status.Node != d.node {
			continue
		}
		if _, still := present[display.Metadata.Name]; still {
			continue
		}
		if err := d.absent(&display); err != nil {
			failures = append(failures, fmt.Errorf("marking the Display %s absent: %w", display.Metadata.Name, err))
		}
	}
	// The restart runs after every panel's status write. It ends the
	// Wayland clients but sends nothing over DDC, so it does not
	// interfere with the panel writes above.
	d.restartCompositorIfStale(held)
	return errors.Join(failures...)
}

// outputSettleWindow is how long the outputs must stay unchanged before
// the compositor restarts. Weston defers an output's destruction while
// a flip is pending, and a restart inside that window can hit a crash
// in Weston, so the operator waits for the output changes to stop.
const outputSettleWindow = 5 * time.Second

// restartCompositorIfStale restarts the compositor after it reported an
// output destroyed and re-created with another monitor or another mode.
// Weston does not send the clients on the other screens a corrected
// size, and a new compositor sizes every surface to its own output.
//
// The restart waits until the outputs have stopped changing for
// outputSettleWindow, until no panel restore is running, and until no
// claim holds a screen on this card, because a restart ends the
// workload that draws on a claimed screen. Waiting for a restore costs
// little, because the panel is still waking from an override.
func (d *displayControl) restartCompositorIfStale(held *screenHolds) {
	restartNeeded, lastChange := d.restartState()
	if !restartNeeded || d.restart == nil {
		return
	}
	if d.now().Before(lastChange.Add(outputSettleWindow)) {
		d.logRestartWait("the outputs are still settling")
		return
	}
	if d.restoresRunning() {
		d.logRestartWait("a panel is still being restored")
		return
	}
	// The claims are read last, because the read can list every claim
	// in the cluster and only a restart needs it.
	claimed, err := held.any()
	if err != nil {
		fmt.Fprintf(os.Stderr, "not restarting the compositor yet: reading the claims that hold a screen: %v\n", err)
		return
	}
	if claimed {
		d.logRestartWait("a claim holds a screen on this card")
		return
	}
	if err := d.restart(); err != nil {
		fmt.Fprintf(os.Stderr, "restarting the compositor to lay out the canvas again: %v\n", err)
		return
	}
	d.restartDone()
	d.waitLogged = false
	fmt.Printf("restarted the compositor after an output was created again, so every canvas is laid out again\n")
}

// outputsChanged receives the compositor's report that an output global
// arrived or left, and starts outputSettleWindow again. recreated is
// true when the output came back with another monitor or another mode.
// A restart by the operator ends the Wayland connection and the watch
// starts from a new baseline, so that restart does not report here.
func (d *displayControl) outputsChanged(recreated bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastOutputChange = d.now()
	d.restartNeeded = d.restartNeeded || recreated
}

func (d *displayControl) restartState() (bool, time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.restartNeeded, d.lastOutputChange
}

func (d *displayControl) restartDone() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.restartNeeded = false
}

// logRestartWait logs why the restart waits, once until the restart
// happens, so a long claim does not log a line on every tick.
func (d *displayControl) logRestartWait(reason string) {
	if d.waitLogged {
		return
	}
	d.waitLogged = true
	fmt.Printf("not restarting the compositor after an output was created again, because %s\n", reason)
}

func (d *displayControl) restoresRunning() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.restoring) > 0
}

// claimed returns which output devices the claims hold, for one pass.
// It reads the claim specs on disk now, and asks the API server only
// when a decision needs it. When the specs cannot be read, it returns
// the error and a result that asks only the API server.
func (d *displayControl) claimed() (*screenHolds, error) {
	held := &screenHolds{allocated: d.allocated}
	if d.prepared == nil {
		return held, nil
	}
	prepared, err := d.prepared()
	if err != nil {
		return held, fmt.Errorf("reading the claims the kubelet prepared: %w", err)
	}
	held.prepared = prepared
	return held, nil
}

// reconcile runs reconcileOnce for one panel, and wakes another pass
// when the status write fails with a conflict, for example because the
// placement pass wrote the same Display. A status write wakes no pass,
// so without the wake the retry would wait for the tick.
//
// The retry reads the status that the API server has, which lacks the
// records that the failed write held. When one of them recorded a mode
// that the compositor declined, the retry calls setMode for that mode
// again. applyMode then returns errModeDeclined with no restart,
// because it already restarted the compositor once for that mode.
func (d *displayControl) reconcile(ctx context.Context, name string, output Output, held *screenHolds, ambiguous string) error {
	err := d.reconcileOnce(ctx, name, output, held, ambiguous)
	if errors.Is(err, apiclient.ErrConflict) {
		d.wake()
	}
	return err
}

// reconcileOnce writes status after the panel and mode writes, so
// status reports their result.
func (d *displayControl) reconcileOnce(ctx context.Context, name string, output Output, held *screenHolds, ambiguous string) error {
	display, err := d.displays.get(name)
	if errors.Is(err, apiclient.ErrNotFound) {
		display, err = d.displays.create(name)
	}
	if err != nil {
		return err
	}
	facts := d.controls.factsFor(output)
	records := writeRecordsOf(display)
	actuated := d.actuate(ctx, display, output, facts, records)
	modeResult := d.applyDeclaredMode(ctx, display, output, held, records)
	status := d.statusOf(display, output, d.controls.factsFor(output), ambiguous)
	status.Unconfirmed = records.publishedUnconfirmed()
	status.Written = records.publishedWrites()
	published := d.publish(display, status)
	return errors.Join(actuated, modeResult, published)
}

// applyDeclaredMode switches the screen to spec.mode through the
// prepare path's mode switch. A claim's mode wins while the claim
// holds the screen, so the switch waits for the first pass that finds
// the screen free. When the compositor declines the mode, the decline
// is recorded in status.unconfirmed, and the operator does not restart
// the compositor for that mode again until spec changes.
func (d *displayControl) applyDeclaredMode(ctx context.Context, display *Display, output Output, held *screenHolds, records *writeRecords) error {
	if display.Spec.Mode == nil {
		return nil
	}
	// Without a mode list from the card, the mode cannot be checked, so
	// a later pass applies it.
	if !output.ModesRead {
		return nil
	}
	want := *display.Spec.Mode
	if !slices.Contains(output.OfferedModes, want) {
		return fmt.Errorf("spec.mode is %s, and %s offers only %s",
			want, output.Connector, strings.Join(output.OfferedModes, " "))
	}
	if d.setMode == nil {
		return nil
	}
	if modeMatches(want, output.CurrentMode) {
		records.clear(modeControl)
		return nil
	}
	if records.isUnconfirmed(modeControl, want) {
		return nil
	}
	// The claims are read last, because the read can ask the API
	// server and only a mode switch needs it.
	claimed, err := held.holds(deviceName(output.Connector))
	if err != nil {
		return fmt.Errorf("reading which claims hold %s: %w", output.Connector, err)
	}
	if claimed {
		return nil
	}
	err = d.setMode(ctx, output, want)
	if errors.Is(err, errModeDeclined) {
		records.record(modeControl, want, d.modeNow(output), err, d.now())
	}
	return err
}

// modeNow returns the mode the compositor reports, or the card's mode
// while the operator has no compositor connection.
func (d *displayControl) modeNow(output Output) string {
	if served := d.servedMode(output.Connector); served != "" {
		return served
	}
	return output.CurrentMode
}

// actuate writes to the panel for one pass. An override wins over the
// declared values, and a saved capture with no override is restored
// before the declared values are written.
func (d *displayControl) actuate(ctx context.Context, display *Display, output Output, facts panelFacts, records *writeRecords) error {
	// Only one writer at a time uses a panel's DDC bus, so the pass
	// skips a panel while its restore runs.
	if d.restoringNow(output.Connector) {
		return nil
	}
	if control, active := display.Spec.override(); active {
		if !facts.Responsive {
			return nil
		}
		if control == powerControl {
			return d.applyPowerOverride(display, output, facts)
		}
		if _, supported := facts.Capabilities[brightnessControl]; !supported {
			return fmt.Errorf("spec.override turns the backlight off, and %s has no brightness control",
				output.Connector)
		}
		return d.applyOverride(display, output, vcpBrightness, 0)
	}
	// A saved capture is restored even when the panel does not respond,
	// because a panel that the override powered down does not respond
	// until the restore wakes it.
	if !display.Status.Captured.empty() {
		return d.restore(ctx, display, output, records)
	}
	if !facts.Responsive {
		return nil
	}
	// The poll runs first, so the pass that finds a value changed at
	// the panel's buttons also writes the declared value back.
	d.poll(output, facts)
	return d.writeDeclared(display, output, d.controls.factsFor(output), records)
}

// poll reads the panel's control values when its last power reading is
// on and its poll window has passed, because a DDC read wakes some
// panels. The caller has checked that the panel responds and that no
// override or restore is in progress.
func (d *displayControl) poll(output Output, facts panelFacts) {
	if !powerIsOn(facts) || !d.controls.pollDue(output.Connector) {
		return
	}
	quiet, err := d.controls.pollControls(output.Connector)
	d.reportPoll(output, quiet, err)
}

// powerIsOn counts a panel with no power reading as on, which includes
// every panel with no power control.
func powerIsOn(facts panelFacts) bool {
	power, known := facts.Observed[vcpPowerMode]
	return !known || power == powerModeOn
}

// reportPoll logs a failed poll once per fault, until a clean read
// clears it. A failed poll fails no pass and changes no condition,
// because Responsive reports what the probe found and the next window
// reads again. A fault is the monitor and the names of the controls
// that failed, or the error text when the bus did not open. The error
// text of a read is left out, because a sleeping panel can return
// different noise on each read, which would log a line every window.
func (d *displayControl) reportPoll(output Output, quiet []string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err == nil {
		delete(d.pollFaults, output.Connector)
		return
	}
	fault := monitorID(output.Monitor) + " " + strings.Join(quiet, ",")
	if len(quiet) == 0 {
		fault += err.Error()
	}
	if d.pollFaults[output.Connector] == fault {
		return
	}
	d.pollFaults[output.Connector] = fault
	fmt.Fprintf(d.faults, "polling the controls of %s: %v\n", output.Connector, err)
}

// applyOverride saves the panel's current value in status before it
// writes the override's value. When the save fails, it writes nothing,
// because only the saved value can bring the panel back.
func (d *displayControl) applyOverride(display *Display, output Output, code byte, value uint16) error {
	if _, captured := capturedRaw(display.Status.Captured, code); !captured {
		if err := d.capture(display, output, code); err != nil {
			return err
		}
	}
	if current, known := d.controls.factsFor(output).Observed[code]; known && current == value {
		return nil
	}
	return d.controls.writeControl(output.Connector, code, value)
}

// applyPowerOverride writes power off without a readback, because a
// panel that powers off stops responding. When the capture read fails
// and the last reading was already off, it returns no error.
func (d *displayControl) applyPowerOverride(display *Display, output Output, facts panelFacts) error {
	off, supported := powerOffValue(facts)
	if !supported {
		return fmt.Errorf("spec.override turns the power off, and %s has no power control", output.Connector)
	}
	if _, captured := capturedRaw(display.Status.Captured, vcpPowerMode); !captured {
		if err := d.capture(display, output, vcpPowerMode); err != nil {
			if current, known := facts.Observed[vcpPowerMode]; known && current == off {
				return nil
			}
			return err
		}
	}
	if current, known := d.controls.factsFor(output).Observed[vcpPowerMode]; known && current == off {
		return nil
	}
	return d.controls.writeControlBlind(output.Connector, vcpPowerMode, off)
}

// capture saves the panel's value in status. It reads the panel and not
// the last observed value, because a person may have changed the
// control in the panel's menu since the operator last read it.
func (d *displayControl) capture(display *Display, output Output, code byte) error {
	current, _, err := d.controls.readControl(output.Connector, code)
	if err != nil {
		return err
	}
	captured := DisplayValues{}
	if display.Status.Captured != nil {
		captured = *display.Status.Captured
	}
	captured.set(code, current)
	status := display.Status
	status.Captured = &captured
	status.Observed = observedValues(d.controls.factsFor(output).Observed)
	if err := d.publish(display, status); err != nil {
		return fmt.Errorf("saving the %s of %s before the override: %w",
			capabilityName(code), output.Connector, err)
	}
	return nil
}

// restore brings the panel back to its values after an override ends.
// It starts a restore goroutine and returns without waiting, because a
// panel that responds slowly or never would otherwise delay every other
// panel. A later pass clears the capture once the panel reads back
// every value.
//
// A control whose restore gave up in this spec generation is not
// written again. Its capture stays in status, and status.unconfirmed
// names the control.
func (d *displayControl) restore(ctx context.Context, display *Display, output Output, records *writeRecords) error {
	targets := restoreTargets(display.Spec, *display.Status.Captured)
	facts := d.controls.factsFor(output)
	for _, gaveUp := range d.takeAbandoned(output.Connector, records.generation) {
		name := capabilityName(gaveUp.target.Code)
		records.record(name, statusValue(gaveUp.target.Code, gaveUp.target.Want),
			readbackOf(facts, gaveUp.target.Code), gaveUp.failure, d.now())
	}
	var pending []controlTarget
	for _, target := range targets {
		name := capabilityName(target.Code)
		if current, known := facts.Observed[target.Code]; known && current == target.Want {
			records.clear(name)
			continue
		}
		if !records.isUnconfirmed(name, statusValue(target.Code, target.Want)) {
			pending = append(pending, target)
		}
	}
	if len(pending) > 0 {
		d.startRestore(ctx, output.Connector, records.generation, pending)
		return nil
	}
	if !restored(targets, facts) {
		return nil
	}
	status := display.Status
	status.Captured = nil
	status.Observed = observedValues(facts.Observed)
	return d.publish(display, status)
}

type controlTarget struct {
	Code byte
	Want uint16
}

func restoreTargets(spec DisplaySpec, captured DisplayValues) []controlTarget {
	var targets []controlTarget
	for _, control := range coreControls {
		if want, restores := restoreTarget(spec, captured, control.Code); restores {
			targets = append(targets, controlTarget{Code: control.Code, Want: want})
		}
	}
	return targets
}

// restored checks the cached readings, which the restore's readbacks
// updated, so the pass does not read the panel again.
func restored(targets []controlTarget, facts panelFacts) bool {
	for _, target := range targets {
		if current, known := facts.Observed[target.Code]; !known || current != target.Want {
			return false
		}
	}
	return true
}

func (d *displayControl) startRestore(ctx context.Context, connector string, generation int64, targets []controlTarget) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.restoring[connector] {
		return
	}
	d.restoring[connector] = true
	go d.runRestore(ctx, connector, generation, targets)
}

type abandonedRestore struct {
	target     controlTarget
	generation int64
	failure    error
}

func (d *displayControl) abandon(connector string, gaveUp abandonedRestore) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.abandoned[connector] = append(d.abandoned[connector], gaveUp)
}

// takeAbandoned returns and clears the abandoned restores of one
// connector. It drops a restore of an earlier generation, because that
// restore ran for a spec that has since changed.
func (d *displayControl) takeAbandoned(connector string, generation int64) []abandonedRestore {
	d.mu.Lock()
	defer d.mu.Unlock()
	var current []abandonedRestore
	for _, gaveUp := range d.abandoned[connector] {
		if gaveUp.generation == generation {
			current = append(current, gaveUp)
		}
	}
	delete(d.abandoned, connector)
	return current
}

// readbackOf returns the last reading of a control as status spells
// it, or an empty string when the control was never read.
func readbackOf(facts panelFacts, code byte) string {
	current, known := facts.Observed[code]
	if !known {
		return ""
	}
	return statusValue(code, current)
}

// sweepDue reports whether this pass lists every Display: when the
// panels on this node differ from those of the last listing, and
// otherwise once per backstop interval.
func (d *displayControl) sweepDue(present map[string]Output) bool {
	panels := make([]string, 0, len(present))
	for name := range present {
		panels = append(panels, name)
	}
	slices.Sort(panels)
	if !slices.Equal(panels, d.swept) {
		d.swept, d.sweptAt = panels, d.now()
		return true
	}
	if d.now().Before(d.sweptAt.Add(backstopInterval)) {
		return false
	}
	d.sweptAt = d.now()
	return true
}

// runRestore restores each target in turn and stops at the first that
// runs out of attempts. It writes no status, because only the pass
// writes status, so it records the failure in abandoned and wakes the
// pass when it ends.
func (d *displayControl) runRestore(ctx context.Context, connector string, generation int64, targets []controlTarget) {
	defer func() {
		d.mu.Lock()
		delete(d.restoring, connector)
		d.mu.Unlock()
		d.wake()
	}()
	for _, target := range targets {
		err := d.restoreOne(ctx, connector, target.Code, target.Want)
		if err == nil {
			continue
		}
		// A restore that the operator's shutdown ended records nothing,
		// so the next operator starts it again.
		if ctx.Err() == nil {
			d.abandon(connector, abandonedRestore{target: target, generation: generation, failure: err})
		}
		return
	}
}

func (d *displayControl) restoringNow(connector string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.restoring[connector]
}

// restoreTarget returns the value that one captured control goes back
// to: the value in spec if spec has one, and otherwise the captured
// value. It returns false when nothing was captured for the control.
func restoreTarget(spec DisplaySpec, captured DisplayValues, code byte) (uint16, bool) {
	if _, saved := captured.raw(code); !saved {
		return 0, false
	}
	if declared, isSet := spec.raw(code); isSet {
		return declared, true
	}
	return captured.raw(code)
}

// restoreOne writes the value until the readback matches, up to
// restoreAttempts writes, because a panel that is waking can respond
// late, refuse a write, or read back a value it has not applied yet.
// It returns the last failure.
func (d *displayControl) restoreOne(ctx context.Context, connector string, code byte, want uint16) error {
	delay := restoreFirstDelay
	var err error
	for attempt := 0; attempt < restoreAttempts; attempt++ {
		if attempt > 0 {
			if waited := d.wait(ctx, delay); waited != nil {
				return waited
			}
			delay = min(delay*2, restoreMaxDelay)
		}
		err = d.controls.writeControl(connector, code, want)
		if err == nil {
			return nil
		}
		fmt.Fprintf(os.Stderr, "restoring the %s of %s: %v\n", capabilityName(code), connector, err)
	}
	return err
}

// writeDeclared writes each declared value that the panel differs
// from. A value that the panel's capability list does not allow is
// reported and not written.
//
// A write that the panel does not confirm is recorded in
// status.unconfirmed and not repeated in this spec generation. Without
// that, a panel that reads an input back under another code would
// take the write, and show its input banner, on every poll. A panel
// that keeps changing a value by itself is written back at most
// writeBackLimit times per generation and then recorded the same way.
func (d *displayControl) writeDeclared(display *Display, output Output, facts panelFacts, records *writeRecords) error {
	var failures []error
	for _, control := range coreControls {
		want, isSet := display.Spec.raw(control.Code)
		if !isSet {
			continue
		}
		if err := declarable(control.Code, want, facts); err != nil {
			failures = append(failures, err)
			continue
		}
		if current, known := facts.Observed[control.Code]; known && current == want {
			records.clear(control.Name)
			continue
		}
		value := statusValue(control.Code, want)
		if records.isUnconfirmed(control.Name, value) {
			continue
		}
		if records.confirmedWrites(control.Name, value) > writeBackLimit {
			stopped := fmt.Errorf("the panel changes the %s by itself: the operator wrote %s back %d times"+
				" in this spec generation, and writes it again after the spec changes", control.Name, value, writeBackLimit)
			records.record(control.Name, value, readbackOf(facts, control.Code), stopped, d.now())
			fmt.Fprintf(os.Stderr, "%s: %v\n", output.Connector, stopped)
			continue
		}
		if err := d.controls.writeControl(output.Connector, control.Code, want); err != nil {
			after, _ := d.controls.cached(output.Connector)
			records.record(control.Name, value, readbackOf(after, control.Code), err, d.now())
			failures = append(failures, err)
			continue
		}
		records.countWrite(control.Name, value)
	}
	return errors.Join(failures...)
}

func declarable(code byte, want uint16, facts panelFacts) error {
	name := capabilityName(code)
	capability, supported := facts.Capabilities[name]
	if !supported {
		return fmt.Errorf("the spec sets a %s, and the panel has no %s control", name, name)
	}
	if len(capability.Values) == 0 {
		if int(want) > capability.Max {
			return fmt.Errorf("the spec sets a %s of %d, and the panel accepts up to %d",
				name, want, capability.Max)
		}
		return nil
	}
	if !slices.Contains(capability.Values, valueName(code, want)) {
		return fmt.Errorf("the spec sets a %s of %s, and the panel accepts %v",
			name, valueName(code, want), capability.Values)
	}
	return nil
}

// powerOffValue returns the first of off, hard off, and standby that
// the panel's capability list allows, because a panel implements only
// some of the power values. A panel that lists no values gets off.
func powerOffValue(facts panelFacts) (uint16, bool) {
	capability, supported := facts.Capabilities[powerControl]
	if !supported {
		return 0, false
	}
	if len(capability.Values) == 0 {
		return powerModeOff, true
	}
	for _, raw := range []uint16{powerModeOff, powerModeHardOff, powerModeStandby} {
		if slices.Contains(capability.Values, valueName(vcpPowerMode, raw)) {
			return raw, true
		}
	}
	return 0, false
}

func capturedRaw(captured *DisplayValues, code byte) (uint16, bool) {
	if captured == nil {
		return 0, false
	}
	return captured.raw(code)
}

// statusOf builds the status of a connected panel. ambiguous is another
// connector on this node that serves the same monitor at a different
// physical address, or empty when there is none.
func (d *displayControl) statusOf(display *Display, output Output, facts panelFacts, ambiguous string) DisplayStatus {
	status := display.Status
	status.Node = d.node
	status.Connector = output.Connector
	// The identity, size, and modes come from the same sysfs walk as
	// the slice's, so the Display and the slice agree.
	status.Manufacturer = output.Monitor.Manufacturer
	status.Model = output.Monitor.ModelName
	status.Serial = output.Monitor.Serial
	status.WidthMillimeters = output.Monitor.WidthMillimeters
	status.HeightMillimeters = output.Monitor.HeightMillimeters
	status.Mode = displayMode(output.CurrentMode, d.servedMode(output.Connector))
	status.Modes = output.OfferedModes
	status.Capabilities = facts.Capabilities
	if observed := observedValues(facts.Observed); observed != nil {
		status.Observed = observed
	}
	d.metrics.recordPanel(output.Connector, facts)
	status.Conditions = setCondition(status.Conditions, d.condition(ConnectedCondition, true,
		PanelAttachedReason, "The panel is connected to "+output.Connector))
	status.Conditions = setCondition(status.Conditions, d.responsive(facts))
	return d.withPhysicalAddress(status, output, ambiguous)
}

// displayMode returns nil when neither the kernel nor the compositor
// reports a mode, which is a connector that drives nothing.
func displayMode(kernel, weston string) *DisplayMode {
	if kernel == "" && weston == "" {
		return nil
	}
	return &DisplayMode{Kernel: kernel, Weston: weston}
}

// servedMode returns the mode that the compositor reports on one
// connector, or an empty string while the operator has no connection
// to the compositor. A compositor restart clears every report, so
// status shows no compositor mode until the new compositor reports,
// rather than a mode from before the restart that may be wrong.
func (d *displayControl) servedMode(connector string) string {
	if d.served == nil {
		return ""
	}
	return d.served().modes[connector]
}

// responsive returns the Responsive condition, which reports whether
// the panel responds to DDC/CI at all. When it is false, the message
// names the causes a person can check: standby, and a menu setting
// that turns DDC/CI off.
func (d *displayControl) responsive(facts panelFacts) DisplayCondition {
	if facts.Responsive {
		return d.condition(ResponsiveCondition, true, AnswersDDCReason, "The panel responds to DDC/CI")
	}
	return d.condition(ResponsiveCondition, false, NoDDCReplyReason,
		"The panel does not respond to DDC/CI. A panel in standby does not respond, and some panels turn DDC/CI off in their own menu")
}

// absent sets Connected false on the Display of a panel that left this
// node, and keeps its other status. When a retry after a conflict finds
// that status.node is another node, because the monitor moved there,
// it leaves the Display alone.
func (d *displayControl) absent(display *Display) error {
	absent := func(published DisplayStatus) (DisplayStatus, bool) {
		status := published
		status.Conditions = setCondition(status.Conditions, d.condition(ConnectedCondition, false,
			NoPanelReason, "No panel is connected to "+status.Connector))
		// The connector serves no EDID, or the EDID of another monitor,
		// such as a TV's EDID that a receiver in standby passes
		// through. The cable is still in the same port, so the
		// physical address stays.
		status = d.retainAddress(status, status.Connector+" no longer reports this monitor's EDID")
		return status, published.Node == d.node
	}
	err := d.displays.settleStatus(display, absent)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

func (d *displayControl) condition(kind string, met bool, reason, message string) DisplayCondition {
	status := conditionFalse
	if met {
		status = conditionTrue
	}
	return DisplayCondition{
		Type:               kind,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: d.now().UTC().Truncate(time.Second),
	}
}

func (d *displayControl) publish(display *Display, status DisplayStatus) error {
	if reflect.DeepEqual(display.Status, status) {
		return nil
	}
	return d.displays.writeStatus(display, status)
}
