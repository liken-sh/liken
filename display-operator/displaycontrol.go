package main

// The controller behind the Display resource. It creates one
// resource per probed monitor, writes the whole of status,
// reconciles the resting spec on divergence, and obeys an override
// only after the capture is durable in status. A pass writes no byte
// on the wire to a panel that already holds its declaration.
//
// A DDC read wakes some panels, so every read has a cause. The poll is
// the one read with no cause of its own. It reads a responsive panel
// once per poll window, and never a panel whose last power value is
// anything but on: standby, suspend, off, or hard off. A panel with no
// power value, which is every panel that carries no power control,
// counts as lit, so the poll reads it every window even while it is
// dark. Every other read follows a cause:
//
//   - the probe of a monitor the operator has not seen on its
//     connector, in any power state (panels.go);
//   - the probe again, once per backstop window, of a panel that
//     refused the last probe, because a person can turn DDC/CI on at
//     the panel's menu with no event (the open problem "A refused
//     probe reads a dark panel every minute");
//   - the capture, which reads the value an override replaces;
//   - the readback after each write, and the read of the range and the
//     held value before a claim's brightness or power write
//     (controls.go).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

// The restore's backoff. A panel that is waking answers slowly,
// so the first retry is soon and the interval doubles to this ceiling.
// The attempts are capped, because a person sees each write on the
// panel. Eight writes with this backoff span about 90 seconds, and the
// operator treats a panel that has not taken the value by then as a
// panel that does not take it.
// The restore that gives up is recorded in status.unconfirmed, and an
// edit to spec starts a new restore.
const (
	restoreFirstDelay = 1 * time.Second
	restoreMaxDelay   = 30 * time.Second
	restoreAttempts   = 8
)

// The controller's own state. The outputs seam is the same
// sysfs walk the slice publisher makes, and the clock and the wait are
// fields for the reason the DDC client's sleep is one.
type displayControl struct {
	client *Client
	// displays reads and writes the Displays, from the watch's store
	// where it can (objectcache.go).
	displays *displayStore
	node     string
	controls *panelControls
	outputs  func() []Output
	now      func() time.Time
	wait     func(ctx context.Context, delay time.Duration) error
	wakes    chan struct{}
	// The connectors whose restore is running now, one restore
	// at a time each. A restore waits on a panel that may take
	// minutes to answer, so it runs apart from the pass, and this is
	// what keeps a second pass from starting a second one.
	mu        sync.Mutex
	restoring map[string]bool
	// The restores that ran out of attempts, by connector, until the
	// pass records them in status. The restore writes no status of
	// its own, so this is how its result reaches the pass.
	abandoned map[string][]abandonedRestore
	// The last poll failure reported for each connector, so a
	// panel that stays quiet prints one line and not one a minute.
	pollFaults map[string]string
	// How often the loop looks, and it is the poll's window: a
	// window that comes due needs a pass to act on it. A field for the
	// reason the clock is one.
	tick time.Duration
	// The panels of the last sweep and when it ran, which is
	// what holds the listing to the slower cadence.
	swept   []string
	sweptAt time.Time
	// The output devices a prepared claim holds. A claim's own
	// mode wins for its lifetime, and a compositor restart would end
	// the workload drawing on the screen, so both wait on this.
	prepared func() (map[string]bool, error)
	// The mode machinery of the prepare path, reused whole:
	// setMode writes the record, rewrites the config, restarts the
	// compositor, and reads the mode back; restart is the same restart
	// with no config change. Both are nil until the operator wires
	// them, and a nil seam does nothing.
	setMode func(ctx context.Context, output Output, mode string) error
	restart func() error
	// What the compositor reports it serves on each connector,
	// which is the mode status reports beside the kernel's. It is nil
	// until the operator wires the standing Wayland connection, and a
	// nil seam reports no mode at all.
	served func() servedOutputs
	// What the compositor's own output events left behind: when
	// the last one arrived, and whether an output was re-created. The
	// debt stands until the restart that pays it. The Wayland watch
	// reports from its own goroutine, so both fields take the lock
	// above.
	settled time.Time
	owed    bool
	// Whether the standing debt has printed its line already.
	// The pass is the only reader and writer, so it takes no lock.
	deferred bool
	// Metrics is the registry this pass reports the panel's own
	// readings on. It is nil in every test that drives a pass with no
	// listener behind it, and a nil metrics records nothing.
	metrics *metrics
}

func newDisplayControl(client *Client, node string, controls *panelControls, outputs func() []Output) *displayControl {
	return &displayControl{
		client:     client,
		displays:   newDisplayStore(client, storeView{}),
		node:       node,
		controls:   controls,
		outputs:    outputs,
		now:        time.Now,
		wait:       waitFor,
		tick:       pollInterval,
		prepared:   preparedOutputs,
		wakes:      make(chan struct{}, 1),
		restoring:  map[string]bool{},
		abandoned:  map[string][]abandonedRestore{},
		pollFaults: map[string]string{},
	}
}

// A wait that ends when the operator stops.
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

// One wake, dropped when a wake is already waiting. Each wake
// means look again, and one pass covers every reason it was woken.
func (d *displayControl) wake() {
	select {
	case d.wakes <- struct{}{}:
	default:
	}
}

// The loop. The Display watch wakes it on an edit to a Display, and
// not on a status write (displays.go). The slice publisher wakes it on
// hardware that moved, a restore wakes it when it ends, and a status
// write that another writer raced wakes it once.
//
// The tick has two jobs. It is the clock of the poll window, which is
// how a value a person changed at a lit panel's own buttons is found,
// because DDC/CI sends the host no event. It is also the only retry of
// a pass that failed, for example on a failed read of a Display or a
// failed status write. A tick reads each present Display from the
// watch's store (objectcache.go), and it reads a panel only through
// the poll's guards.
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

// One pass over every panel on this node, and then over every
// resource this node owns whose panel is gone. A resource is never
// deleted: it holds the captured state, and Connected is what reports
// the absence.
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
	// A monitor two connectors both serve, with different physical
	// addresses, so the Display for it publishes no current address.
	ambiguous := ambiguousAddresses(outputs)
	// The sweep for panels that left is the only work in a pass
	// that reads every resource, and nothing about it follows the
	// poll's cadence: a panel that leaves raises a uevent, and the
	// uevent wakes this loop. So the listing keeps the slower cadence
	// and runs at once when the panels on this node change.
	sweep := d.sweepDue(present)
	var published []Display
	if sweep {
		listed, err := d.displays.list()
		if err != nil {
			return err
		}
		published = listed
	}
	// One read of what the claims hold answers every panel of
	// the pass, and the compositor heal below reads the same answer.
	var failures []error
	held, err := d.claimed()
	if err != nil {
		failures = append(failures, err)
	}
	for name, output := range present {
		if err := d.reconcile(ctx, name, output, held, ambiguous[name]); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", name, err))
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
			failures = append(failures, fmt.Errorf("%s: %w", display.Metadata.Name, err))
		}
	}
	// The canvas heal runs last, after every panel is reconciled
	// and its status written. A compositor restart ends the Wayland
	// clients and touches no DDC wire, so it can follow the panels'
	// own work without disturbing it.
	d.healCanvas(held)
	return errors.Join(failures...)
}

// How long the outputs must hold still before the compositor
// restarts. Weston defers an output's destruction across a pending
// flip, and a restart inside that window can hit the crash the open
// problem records, so the heal waits for the flap to end.
const canvasSettleWindow = 5 * time.Second

// The canvas heal. The compositor's own registry reports when an output
// was destroyed and re-created, and weston never gives the clients on
// the surviving screens a corrected size. A fresh compositor places
// every surface at its own output's size, so the restart is the repair.
// The watch decides before it reports: an output that comes back on its
// connector carrying the same monitor at the same mode owes nothing,
// because every canvas is already the size it should be.
//
// It waits on three things: a claim that holds any screen on this card,
// an output set that is still moving, and a panel whose restore is
// still writing. The first is the workload's screen, the second is the
// hazard window upstream documents, and the third is a panel on its way
// back that has enough to do.
func (d *displayControl) healCanvas(held map[string]bool) {
	owed, settled := d.canvasDebt()
	if !owed || d.restart == nil {
		return
	}
	switch {
	case len(held) > 0:
		d.deferHeal("a prepared claim holds a screen on this card")
	case d.now().Before(settled.Add(canvasSettleWindow)):
		d.deferHeal("the outputs are still settling")
	case d.restoresRunning():
		d.deferHeal("a panel is still being restored")
	default:
		if err := d.restart(); err != nil {
			fmt.Fprintf(os.Stderr, "restarting the compositor to heal the canvas: %v\n", err)
			return
		}
		d.canvasHealed()
		d.deferred = false
		// The line a person reads in the operator's log. It stands for a re-
		// creation that changed the screen, so it names the change.
		fmt.Printf("an output was re-created: the compositor restarts and every canvas is laid out again\n")
	}
}

// What the compositor reported. Every output global that arrives or
// leaves starts the settling window again, and a re-creation that
// changed the screen owes the restart. Recreated means an output that
// came back carrying another monitor or another mode. The operator's
// own restarts end the watch's connection and start a new baseline, so
// they report nothing here.
func (d *displayControl) outputsMoved(recreated bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.settled = d.now()
	d.owed = d.owed || recreated
}

// The debt and the settling window, read together under one
// lock so the heal decides on one consistent pair.
func (d *displayControl) canvasDebt() (bool, time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.owed, d.settled
}

func (d *displayControl) canvasHealed() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.owed = false
}

// One line per debt, whatever holds it up. A line on every pass
// would print six times a minute for as long as a film runs.
func (d *displayControl) deferHeal(reason string) {
	if d.deferred {
		return
	}
	d.deferred = true
	fmt.Printf("an output was re-created: the compositor restart waits because %s\n", reason)
}

func (d *displayControl) restoresRunning() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.restoring) > 0
}

// What the prepared claims hold, and nothing when this operator
// has no way to read them. A failure here is reported and treated as
// no claims, because the seam reads the specs this driver wrote.
func (d *displayControl) claimed() (map[string]bool, error) {
	if d.prepared == nil {
		return nil, nil
	}
	held, err := d.prepared()
	if err != nil {
		return nil, fmt.Errorf("reading the claims the kubelet prepared: %w", err)
	}
	return held, nil
}

// One panel. A status write that another writer raced, such as the
// placement pass writing the same Display, is refused with a conflict,
// and the pass wakes the next pass, which reads the card and the
// Display again. A status write wakes no pass, so without the wake the
// next pass would wait for the poll's tick. The next pass reads a
// status that lacks the records this pass failed to write, such as a
// mode the compositor declined, so it can actuate again what this pass
// did. The prepare path's own budget of compositor restarts bounds a
// mode switch that repeats.
func (d *displayControl) reconcile(ctx context.Context, name string, output Output, held map[string]bool, ambiguous string) error {
	err := d.reconcileOnce(ctx, name, output, held, ambiguous)
	if errors.Is(err, ErrConflict) {
		d.wake()
	}
	return err
}

// One run over one panel. The resource is created empty when it is
// absent, the panel is actuated, and the status is written last, so it
// reports what the actuation left behind.
func (d *displayControl) reconcileOnce(ctx context.Context, name string, output Output, held map[string]bool, ambiguous string) error {
	display, err := d.displays.get(name)
	if errors.Is(err, ErrNotFound) {
		display, err = d.displays.create(name)
	}
	if err != nil {
		return err
	}
	facts := d.controls.factsFor(output)
	ledger := ledgerOf(display)
	actuated := d.actuate(ctx, display, output, facts, ledger)
	// The mode is the screen's, not the panel's: it lands
	// through the compositor and not on the DDC wire, so it runs
	// beside the controls rather than among them.
	rested := d.restMode(ctx, display, output, held, ledger)
	status := d.statusOf(display, output, d.controls.factsFor(output), ambiguous)
	status.Unconfirmed = ledger.published()
	status.Written = ledger.writtenPublished()
	published := d.publish(display, status)
	return errors.Join(actuated, rested, published)
}

// The resting mode. A claim's own mode wins while the claim
// holds the screen, so a declaration edited during a claim waits for
// the claim to end, and the pass that finds the screen free applies
// it. The apply is the prepare path's own, so it restarts the
// compositor once and reads the mode back. A mode the compositor
// declined is recorded in the ledger, and the operator does not
// restart the compositor for it again until spec changes.
func (d *displayControl) restMode(ctx context.Context, display *Display, output Output, held map[string]bool, ledger *unconfirmedLedger) error {
	if display.Spec.Mode == nil {
		return nil
	}
	// A pass that read no mode list from the card cannot judge the
	// mode, and the pass after the next read of the card applies it.
	if !output.ModesRead {
		return nil
	}
	want := *display.Spec.Mode
	if !slices.Contains(output.OfferedModes, want) {
		return fmt.Errorf("the spec states the mode %s, and %s offers %s",
			want, output.Connector, strings.Join(output.OfferedModes, " "))
	}
	if held[deviceName(output.Connector)] || d.setMode == nil {
		return nil
	}
	if modeMatches(want, output.CurrentMode) {
		ledger.clear(modeControl)
		return nil
	}
	if ledger.declined(modeControl, want) {
		return nil
	}
	err := d.setMode(ctx, output, want)
	if errors.Is(err, errModeDeclined) {
		ledger.record(modeControl, want, d.modeNow(output), err, d.now())
	}
	return err
}

// The mode the screen runs after a switch: what the compositor
// serves, or the card's own readback while no compositor answers.
func (d *displayControl) modeNow(output Output) string {
	if served := d.servedMode(output.Connector); served != "" {
		return served
	}
	return output.CurrentMode
}

// What one pass writes to the panel. The override wins over the
// resting layer, a capture that stands with no override is restored,
// and an empty spec falls through all of it and writes nothing.
func (d *displayControl) actuate(ctx context.Context, display *Display, output Output, facts panelFacts, ledger *unconfirmedLedger) error {
	// A connector whose restore is running is left to that
	// restore. One writer at a time reaches one panel's wire, and the
	// restore is the writer while it runs.
	if d.restoringNow(output.Connector) {
		return nil
	}
	if held, standing := display.Spec.override(); standing {
		if !facts.Responsive {
			return nil
		}
		if held == powerControl {
			return d.holdPower(display, output, facts)
		}
		if _, carried := facts.Capabilities[brightnessControl]; !carried {
			return fmt.Errorf("%s answers no brightness control, and the override states backlight off",
				output.Connector)
		}
		return d.hold(display, output, vcpBrightness, 0)
	}
	// A capture that stands is restored even against a panel
	// that answers nothing now, because a panel the operator powered
	// down answers nothing until the restore wakes it. This is the
	// state an operator that restarted while an override stood comes
	// back to.
	if !display.Status.Captured.empty() {
		return d.restore(ctx, display, output, ledger)
	}
	if !facts.Responsive {
		return nil
	}
	// The read of what the panel holds now runs before the
	// resting layer, so one pass finds a value a person changed at the
	// panel's own buttons and writes the declaration back over it.
	d.poll(output, facts)
	return d.rest(display, output, d.controls.factsFor(output), ledger)
}

// The guarded read. A DDC read is a wake stimulus on some
// panels, so it happens only against a panel that answers, that no
// override holds, that no restore is writing, and whose last power
// value reads on. A panel last seen in standby or off is never
// touched.
func (d *displayControl) poll(output Output, facts panelFacts) {
	if !lit(facts) || !d.controls.pollDue(output.Connector) {
		return
	}
	err := d.controls.pollControls(output.Connector)
	d.reportPoll(output.Connector, err)
}

// Whether the last power value the operator read says the
// panel is lit. A panel with no power value counts as lit, which is
// every panel that carries no power control.
func lit(facts panelFacts) bool {
	power, known := facts.Observed[vcpPowerMode]
	return !known || power == powerModeOn
}

// A poll that failed says the panel went quiet between the
// probe and this read. It fails no pass and moves no condition:
// Responsive reports what the probe found, and the next window reads
// again. The message prints once, because a panel that stays quiet
// would otherwise print one line a minute for as long as it lasts.
func (d *displayControl) reportPoll(connector string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err == nil {
		delete(d.pollFaults, connector)
		return
	}
	if d.pollFaults[connector] == err.Error() {
		return
	}
	d.pollFaults[connector] = err.Error()
	fmt.Fprintf(os.Stderr, "reading what %s holds: %v\n", connector, err)
}

// The capture commits before the wire write. A status write that
// failed leaves the panel lit, because the value that brings it back is
// the whole reason this resource exists.
func (d *displayControl) hold(display *Display, output Output, code byte, held uint16) error {
	if _, captured := capturedRaw(display.Status.Captured, code); !captured {
		if err := d.capture(display, output, code); err != nil {
			return err
		}
	}
	if current, known := d.controls.factsFor(output).Observed[code]; known && current == held {
		return nil
	}
	return d.controls.writeControl(output.Connector, code, held)
}

// The power override. The write is blind, because a panel that
// obeyed stops answering, and a panel already down with nothing
// captured is left as it is rather than reported as a failure.
func (d *displayControl) holdPower(display *Display, output Output, facts panelFacts) error {
	off, carried := powerOffValue(facts)
	if !carried {
		return fmt.Errorf("%s answers no power control, and the override states power off", output.Connector)
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

// The read the capture makes is a read of the panel and not of
// the last observed value, because a person at the panel's own menu
// moved the control since the operator last looked.
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

// The restore, once the override is lifted. The pass never
// waits on it: a panel that answers slowly, or never, would otherwise
// hold up every other panel's reconcile. The pass starts the restore
// and moves on, and the restore's own wake brings the pass back to
// clear the capture once the panel holds the values again.
//
// A control whose restore ran out of attempts in this generation is
// not written again. The capture stands, so the value is not lost, and
// status.unconfirmed says which control did not come back.
func (d *displayControl) restore(ctx context.Context, display *Display, output Output, ledger *unconfirmedLedger) error {
	targets := restoreTargets(display.Spec, *display.Status.Captured)
	facts := d.controls.factsFor(output)
	for _, gaveUp := range d.takeAbandoned(output.Connector, ledger.generation) {
		name := capabilityName(gaveUp.target.Code)
		ledger.record(name, spokenValue(gaveUp.target.Code, gaveUp.target.Want),
			readbackOf(facts, gaveUp.target.Code), gaveUp.failure, d.now())
	}
	var pending []controlTarget
	for _, target := range targets {
		name := capabilityName(target.Code)
		if current, known := facts.Observed[target.Code]; known && current == target.Want {
			ledger.clear(name)
			continue
		}
		if !ledger.declined(name, spokenValue(target.Code, target.Want)) {
			pending = append(pending, target)
		}
	}
	if len(pending) > 0 {
		d.startRestore(ctx, output.Connector, ledger.generation, pending)
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

// One control and the value it goes back to.
type controlTarget struct {
	Code byte
	Want uint16
}

// Every control the capture holds, with the value each one goes
// back to.
func restoreTargets(spec DisplaySpec, captured DisplayValues) []controlTarget {
	var targets []controlTarget
	for _, control := range coreControls {
		if want, restores := restoreTarget(spec, captured, control.Code); restores {
			targets = append(targets, controlTarget{Code: control.Code, Want: want})
		}
	}
	return targets
}

// Whether the panel holds every restored value now. This is
// what the pass reads to know a restore landed, and it reads the
// values the restore recorded rather than the panel itself.
func restored(targets []controlTarget, facts panelFacts) bool {
	for _, target := range targets {
		if current, known := facts.Observed[target.Code]; !known || current != target.Want {
			return false
		}
	}
	return true
}

// One restore per connector at a time. A pass that finds one
// running leaves it alone, so two passes never write one panel twice.
func (d *displayControl) startRestore(ctx context.Context, connector string, generation int64, targets []controlTarget) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.restoring[connector] {
		return
	}
	d.restoring[connector] = true
	go d.runRestore(ctx, connector, generation, targets)
}

// One control whose restore ran out of attempts, the spec generation
// the restore ran for, and the last failure the panel answered.
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

// The restores of one connector that gave up, taken once. A restore
// that ran for an earlier generation ran against a spec that no longer
// stands, so it records nothing.
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

// What the panel held after the last write, as status spells it,
// and nothing when the operator never read it.
func readbackOf(facts panelFacts, code byte) string {
	current, known := facts.Observed[code]
	if !known {
		return ""
	}
	return spokenValue(code, current)
}

// Whether this pass lists the resources. It does when the
// panels on this node are not the panels of the last sweep, so a panel
// that arrives or leaves is answered on the pass that finds it, and
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

// The restore itself, on its own goroutine. It writes the wire
// and records what the panel took, and it writes no status: the wake
// it ends with brings the pass back, and the pass is the one writer of
// status. A control that ran out of attempts goes to the abandoned
// list, and the pass records it in status.unconfirmed.
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
		// A restore the operator's shutdown ended did not run out of
		// attempts, so it records nothing, and the next operator
		// starts it again.
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

// Which value one control goes back to.
func restoreTarget(spec DisplaySpec, captured DisplayValues, code byte) (uint16, bool) {
	if _, held := captured.raw(code); !held {
		return 0, false
	}
	if declared, stated := spec.raw(code); stated {
		return declared, true
	}
	return captured.raw(code)
}

// The write repeats until the readback matches, up to
// restoreAttempts writes. A panel that is waking answers late, refuses
// a write, or answers a value it has not applied yet. The last failure
// is the one the restore reports.
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

// The resting layer, written only where the panel diverges from
// the declaration. A value the capability list refuses is reported and
// never written. A write the panel did not confirm is recorded in the
// ledger and not repeated in this generation, because a panel that
// reads an input back under another code would otherwise take the
// write, and show its input banner, on every poll. A confirmed write
// is counted, and a value the panel moves away from again is written
// back at most writeBackLimit times in one generation. After that the
// value is recorded as unconfirmed, because a panel that changes it by
// itself would otherwise take a write every poll.
func (d *displayControl) rest(display *Display, output Output, facts panelFacts, ledger *unconfirmedLedger) error {
	var failures []error
	for _, control := range coreControls {
		want, stated := display.Spec.raw(control.Code)
		if !stated {
			continue
		}
		if err := declarable(control.Code, want, facts); err != nil {
			failures = append(failures, err)
			continue
		}
		if current, known := facts.Observed[control.Code]; known && current == want {
			ledger.clear(control.Name)
			continue
		}
		value := spokenValue(control.Code, want)
		if ledger.declined(control.Name, value) {
			continue
		}
		if ledger.writes(control.Name, value) > writeBackLimit {
			stopped := fmt.Errorf("the panel keeps changing the %s by itself: the operator wrote %s back %d times"+
				" in this generation, and writes it again after an edit to spec", control.Name, value, writeBackLimit)
			ledger.record(control.Name, value, readbackOf(facts, control.Code), stopped, d.now())
			fmt.Fprintf(os.Stderr, "%s: %v\n", output.Connector, stopped)
			continue
		}
		if err := d.controls.writeControl(output.Connector, control.Code, want); err != nil {
			after, _ := d.controls.cached(output.Connector)
			ledger.record(control.Name, value, readbackOf(after, control.Code), err, d.now())
			failures = append(failures, err)
			continue
		}
		ledger.wrote(control.Name, value)
	}
	return errors.Join(failures...)
}

// Whether the panel takes the declared value, judged against
// the capability list it published.
func declarable(code byte, want uint16, facts panelFacts) error {
	name := capabilityName(code)
	capability, carried := facts.Capabilities[name]
	if !carried {
		return fmt.Errorf("the spec states a %s, and the panel carries no %s control", name, name)
	}
	if len(capability.Values) == 0 {
		if int(want) > capability.Max {
			return fmt.Errorf("the spec states a %s of %d, and the panel accepts up to %d",
				name, want, capability.Max)
		}
		return nil
	}
	if !slices.Contains(capability.Values, valueName(code, want)) {
		return fmt.Errorf("the spec states a %s of %s, and the panel accepts %v",
			name, valueName(code, want), capability.Values)
	}
	return nil
}

// The value a power-down writes. A panel implements the subset
// of the power code that it chooses, so the write is the first of
// these the panel declared.
func powerOffValue(facts panelFacts) (uint16, bool) {
	capability, carried := facts.Capabilities[powerControl]
	if !carried {
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

// The status of a panel that is on its connector now. Ambiguous
// names another connector on this node serving the same monitor a
// different physical address, or is empty when this is the only one.
func (d *displayControl) statusOf(display *Display, output Output, facts panelFacts, ambiguous string) DisplayStatus {
	status := display.Status
	status.Node = d.node
	status.Connector = output.Connector
	// The identity, the size, and the modes come from the walk
	// this pass already made, so the resource and the slice report one
	// set of reads and cannot drift.
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
	// The same DDC/CI readings that just filled status.observed, on
	// the gauges the idle screen's drive shows up on.
	d.metrics.recordPanel(output.Connector, facts)
	status.Conditions = setCondition(status.Conditions, d.condition(ConnectedCondition, true,
		"PanelAttached", output.Connector+" carries this panel"))
	status.Conditions = setCondition(status.Conditions, d.responsive(facts))
	return d.withPhysicalAddress(status, output, ambiguous)
}

// The mode block of one output, and nothing at all when
// neither side names a mode: a connector that drives nothing, with
// no compositor serving it.
func displayMode(kernel, weston string) *DisplayMode {
	if kernel == "" && weston == "" {
		return nil
	}
	return &DisplayMode{Kernel: kernel, Weston: weston}
}

// What the compositor reports it serves on one connector, and
// nothing at all while this operator holds no connection to one. A
// restart clears every answer, so the field goes absent until the
// compositor that comes back states its outputs again. An absent
// value is honest where a carried-over one would be a guess.
func (d *displayControl) servedMode(connector string) string {
	if d.served == nil {
		return ""
	}
	return d.served().modes[connector]
}

// The panel's answer to the protocol itself. The message names
// the panel's own menu, because a panel that answers nothing is often
// a panel whose menu turns DDC/CI off.
func (d *displayControl) responsive(facts panelFacts) DisplayCondition {
	if facts.Responsive {
		return d.condition(ResponsiveCondition, true, "AnswersDDC", "the panel answers DDC/CI")
	}
	return d.condition(ResponsiveCondition, false, NoDDCReplyReason,
		"the panel answers no DDC/CI; some panels turn DDC/CI off in their own menu")
}

// The panel is gone from this node, and the resource stays with
// what it held.
//
// A retry after a conflict composes again from the fresh copy, and
// leaves a Display whose status now names another node, because its
// monitor moved there since the copy.
func (d *displayControl) absent(display *Display) error {
	absent := func(published DisplayStatus) (DisplayStatus, bool) {
		status := published
		status.Conditions = setCondition(status.Conditions, d.condition(ConnectedCondition, false,
			"NoPanel", "no panel on "+status.Connector))
		// The connector serves no EDID, or the EDID of another monitor,
		// such as the TV's EDID that a receiver in standby can pass
		// through. Either way the port this machine's cable is in has
		// not moved, so the address stays.
		status = d.retainAddress(status, status.Connector+" no longer serves this monitor's EDID")
		return status, published.Node == d.node
	}
	err := d.displays.settleStatus(display, absent)
	if errors.Is(err, ErrNotFound) {
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
		LastTransitionTime: d.now().UTC().Format(time.RFC3339),
	}
}

// The status write happens only where the published status and
// this pass's status differ, so a steady-state pass writes nothing.
func (d *displayControl) publish(display *Display, status DisplayStatus) error {
	if reflect.DeepEqual(display.Status, status) {
		return nil
	}
	updated, err := d.displays.writeStatus(display, status)
	if err != nil {
		return err
	}
	*display = *updated
	return nil
}
