package main

// The controller behind the Display resource. It creates one
// resource per probed monitor, writes the whole of status,
// reconciles the resting spec on divergence, and obeys an override
// only after the capture is durable in status. A pass that finds
// nothing diverged reads no panel and writes no byte on the wire,
// because a DDC read wakes some panels.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

// The restore's backoff. A panel that is waking answers slowly,
// so the first retry is soon and the interval doubles to this ceiling.
// Nothing caps the attempts: the operator's own context is the bound,
// because a panel that is dark and a person who is waiting for it are
// both still there after ten attempts.
const (
	restoreFirstDelay = 1 * time.Second
	restoreMaxDelay   = 30 * time.Second
)

// The controller's own state. The outputs seam is the same
// sysfs walk the slice publisher makes, and the clock and the wait are
// fields for the reason the DDC client's sleep is one.
type displayControl struct {
	client   *Client
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
	// The last poll failure reported for each connector, so a
	// panel that stays quiet prints one line and not one a minute.
	pollFaults map[string]string
	// The deferral standing for each connector, in the same
	// shape and for the same reason as the poll's faults above.
	darkFaults map[string]string
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
	// What each connector carried on the pass before, which is
	// how an output that was re-created is told from one that never
	// moved, and when the set last changed. The debt an output that
	// came back raises stands until the restart that pays it.
	seen     map[string]string
	settled  time.Time
	owed     bool
	deferred bool
}

func newDisplayControl(client *Client, node string, controls *panelControls, outputs func() []Output) *displayControl {
	return &displayControl{
		client:     client,
		node:       node,
		controls:   controls,
		outputs:    outputs,
		now:        time.Now,
		wait:       waitFor,
		tick:       pollInterval,
		prepared:   preparedOutputs,
		wakes:      make(chan struct{}, 1),
		restoring:  map[string]bool{},
		pollFaults: map[string]string{},
		darkFaults: map[string]string{},
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

// The loop. The watch wakes it on a spec that changed, the
// slice publisher wakes it on hardware that moved, and the tick is the
// backstop that covers a dropped watch.
func (d *displayControl) run(ctx context.Context) {
	tick := time.NewTicker(d.tick)
	defer tick.Stop()
	for {
		if err := d.pass(ctx); err != nil {
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
	// The sweep for panels that left is the only work in a pass
	// that reads every resource, and nothing about it follows the
	// poll's cadence: a panel that leaves raises a uevent, and the
	// uevent wakes this loop. So the listing keeps the slower cadence
	// and runs at once when the panels on this node change.
	sweep := d.sweepDue(present)
	var published []Display
	if sweep {
		listed, err := listDisplays(d.client)
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
		if err := d.reconcile(ctx, name, output, held); err != nil {
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
	d.healCanvas(outputs, held)
	return errors.Join(failures...)
}

// How long the outputs must hold still before the compositor
// restarts. Weston defers an output's destruction across a pending
// flip, and a restart inside that window can hit the crash the open
// problem records, so the heal waits for the flap to end.
const canvasSettleWindow = 5 * time.Second

// The canvas heal. An output that is present now and was absent
// or another monitor on the pass before was re-created, and Weston
// never gives the clients on the surviving screens a corrected size.
// A fresh compositor places every surface at its own output's size,
// so the restart is the repair.
//
// it waits on three things: a claim that holds any screen on
// this card, an output set that is still moving, and a panel whose
// restore is still writing. The first is the workload's screen, the
// second is the hazard window upstream documents, and the third is a
// panel on its way back that has enough to do.
func (d *displayControl) healCanvas(outputs []Output, held map[string]bool) {
	d.track(outputs)
	if !d.owed || d.restart == nil {
		return
	}
	switch {
	case len(held) > 0:
		d.deferHeal("a prepared claim holds a screen on this card")
	case d.now().Before(d.settled.Add(canvasSettleWindow)):
		d.deferHeal("the outputs are still settling")
	case d.restoresRunning():
		d.deferHeal("a panel is still being restored")
	default:
		if err := d.restart(); err != nil {
			fmt.Fprintf(os.Stderr, "restarting the compositor to heal the canvas: %v\n", err)
			return
		}
		d.owed, d.deferred = false, false
		fmt.Printf("an output was re-created: the compositor restarts and every canvas is laid out again\n")
	}
}

// What each connector carried, compared against the pass
// before. An output that is present now and was not the same then owes
// a restart, and the whole set holding still is what starts the
// settling window. The first pass of an operator learns the set and
// owes nothing: a compositor that just started has every canvas right.
func (d *displayControl) track(outputs []Output) {
	identity := map[string]string{}
	for _, output := range outputs {
		identity[output.Connector] = outputIdentity(output)
	}
	if d.seen == nil {
		d.seen, d.settled = identity, d.now()
		return
	}
	if maps.Equal(identity, d.seen) {
		return
	}
	for connector, monitor := range identity {
		if monitor != "" && monitor != d.seen[connector] {
			d.owed = true
		}
	}
	d.seen, d.settled = identity, d.now()
}

// What a connector carries, as one string: the monitor's own
// identity when it names one, the bare fact of a monitor when the EDID
// names nothing, and nothing at all for a dark connector.
func outputIdentity(output Output) string {
	if !output.Connected {
		return ""
	}
	if monitor := monitorID(output.Monitor); monitor != "" {
		return monitor
	}
	return output.Connector + " carries a monitor"
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

// One panel. The resource is created empty when it is absent,
// the panel is actuated, and the status is written last, so it reports
// what the actuation left behind.
func (d *displayControl) reconcile(ctx context.Context, name string, output Output, held map[string]bool) error {
	display, err := getDisplay(d.client, name)
	if errors.Is(err, ErrNotFound) {
		display, err = createDisplay(d.client, name)
	}
	if err != nil {
		return err
	}
	facts := d.controls.factsFor(output)
	actuated := d.actuate(ctx, display, output, facts)
	// The mode is the screen's, not the panel's: it lands
	// through the compositor and not on the DDC wire, so it runs
	// beside the controls rather than among them.
	rested := d.restMode(ctx, display, output, held)
	published := d.publish(display, d.statusOf(display, output, d.controls.factsFor(output)))
	return errors.Join(actuated, rested, published)
}

// The resting mode. A claim's own mode wins while the claim
// holds the screen, so a declaration edited during a claim waits for
// the claim to end, and the pass that finds the screen free applies
// it. The apply is the prepare path's own, so it restarts the
// compositor once and reads the mode back.
func (d *displayControl) restMode(ctx context.Context, display *Display, output Output, held map[string]bool) error {
	if display.Spec.Mode == nil {
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
		return nil
	}
	return d.setMode(ctx, output, want)
}

// What one pass writes to the panel. The override wins over the
// resting layer, a capture that stands with no override is restored,
// and an empty spec falls through all of it and writes nothing.
func (d *displayControl) actuate(ctx context.Context, display *Display, output Output, facts panelFacts) error {
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
		// brightness and power are panel-global, so darkening a
		// panel that shows another machine's input dims that machine's
		// picture. The declared attached input is the only fact that
		// says which input is ours, and the override waits until the
		// panel shows it.
		obeys, err := d.attached(display.Spec, output, facts)
		if err != nil {
			return err
		}
		if !obeys {
			return nil
		}
		d.reportDeferral(output.Connector, "")
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
		return d.restore(ctx, display, output)
	}
	if !facts.Responsive {
		return nil
	}
	// The read of what the panel holds now runs before the
	// resting layer, so one pass finds a value a person changed at the
	// panel's own buttons and writes the declaration back over it.
	d.poll(output, facts)
	return d.rest(display, output, d.controls.factsFor(output))
}

// Whether the panel shows this machine's own input, and so
// whether a darkening override may act. A panel with no declaration
// answers yes, which is every single-input panel and every panel
// nobody shares. The read that lifts a deferral is the poll: it keeps
// the shown input fresh, so the pass that finds the panel back on this
// machine's input obeys the override that was waiting.
func (d *displayControl) attached(spec DisplaySpec, output Output, facts panelFacts) (bool, error) {
	attached, declared, err := attachedInput(spec, output, facts)
	if err != nil {
		return false, err
	}
	if !declared {
		return true, nil
	}
	if shown, known := facts.Observed[vcpInput]; known && shown == attached {
		return true, nil
	}
	d.poll(output, facts)
	shown, known := d.controls.factsFor(output).Observed[vcpInput]
	if known && shown == attached {
		return true, nil
	}
	d.reportDeferral(output.Connector, fmt.Sprintf("%s shows %s and this machine is on %s",
		output.Connector, shownInput(shown, known), valueName(vcpInput, attached)))
	return false, nil
}

// What the panel is showing, in one word, for the line that
// says why the darkening waits.
func shownInput(shown uint16, known bool) string {
	if !known {
		return "an input this operator has not read"
	}
	return valueName(vcpInput, shown)
}

// One line per connector while a deferral stands, and the mark
// is cleared when the override finally acts, so the next deferral is
// reported again. A line on every pass would print six times a minute
// for a whole evening of somebody else's film.
func (d *displayControl) reportDeferral(connector, reason string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if reason == "" {
		delete(d.darkFaults, connector)
		return
	}
	if d.darkFaults[connector] == reason {
		return
	}
	d.darkFaults[connector] = reason
	fmt.Printf("%s: the darkening override waits\n", reason)
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
func (d *displayControl) restore(ctx context.Context, display *Display, output Output) error {
	targets := restoreTargets(display.Spec, *display.Status.Captured)
	facts := d.controls.factsFor(output)
	if !restored(targets, facts) {
		d.startRestore(ctx, output.Connector, targets)
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
func (d *displayControl) startRestore(ctx context.Context, connector string, targets []controlTarget) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.restoring[connector] {
		return
	}
	d.restoring[connector] = true
	go d.runRestore(ctx, connector, targets)
}

// The restore itself, on its own goroutine. It writes the wire
// and records what the panel took, and it writes no status: the wake
// it ends with brings the pass back, and the pass is the one writer of
// status.
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

func (d *displayControl) runRestore(ctx context.Context, connector string, targets []controlTarget) {
	defer func() {
		d.mu.Lock()
		delete(d.restoring, connector)
		d.mu.Unlock()
		d.wake()
	}()
	for _, target := range targets {
		if err := d.restoreOne(ctx, connector, target.Code, target.Want); err != nil {
			return
		}
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

// The write repeats until the readback matches. A panel that is
// waking answers late, refuses a write, or answers a value it has not
// applied yet, and the operator's context is the only bound.
func (d *displayControl) restoreOne(ctx context.Context, connector string, code byte, want uint16) error {
	delay := restoreFirstDelay
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			if err := d.wait(ctx, delay); err != nil {
				return err
			}
			delay = min(delay*2, restoreMaxDelay)
		}
		err := d.controls.writeControl(connector, code, want)
		if err == nil {
			return nil
		}
		fmt.Fprintf(os.Stderr, "restoring the %s of %s: %v\n", capabilityName(code), connector, err)
	}
}

// The resting layer, written only where the panel diverges from
// the declaration. A value the capability list refuses is reported and
// never written.
func (d *displayControl) rest(display *Display, output Output, facts panelFacts) error {
	var failures []error
	// The attached input is judged here with every other
	// declared value, and it is the one this operator never writes: it
	// states which input this machine's cable occupies, and the panel
	// is not asked to change anything by it.
	if _, _, err := attachedInput(display.Spec, output, facts); err != nil {
		failures = append(failures, err)
	}
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
			continue
		}
		if err := d.controls.writeControl(output.Connector, control.Code, want); err != nil {
			failures = append(failures, err)
		}
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

// The declared attached input as the number the panel reports
// for it, and whether one is declared at all. The value is judged
// against the panel's own input list, the way every declared value is,
// and it is never written: a fact about which cable is ours is not a
// request to switch the panel to it.
func attachedInput(spec DisplaySpec, output Output, facts panelFacts) (uint16, bool, error) {
	if spec.AttachedInput == nil {
		// The EDID is the second source and the owner's
		// declaration is the first, so this is read only when no
		// declaration stands.
		derived := derivedInput(output, facts)
		if derived == "" {
			return 0, false, nil
		}
		raw, named := valueRaw(vcpInput, derived)
		return raw, named, nil
	}
	name := *spec.AttachedInput
	capability, carried := facts.Capabilities[inputControl]
	if !carried {
		return 0, false, fmt.Errorf(
			"the spec states the attached input %s, and the panel carries no input control", name)
	}
	if !slices.Contains(capability.Values, name) {
		return 0, false, fmt.Errorf("the spec states the attached input %s, and the panel accepts %v",
			name, capability.Values)
	}
	raw, named := valueRaw(vcpInput, name)
	if !named {
		return 0, false, fmt.Errorf("the spec states the attached input %s, and no input carries that name", name)
	}
	return raw, true, nil
}

// The input this machine's cable occupies, as the panel's own
// EDID states it. The derivation holds only where the sink names the
// port: an HDMI connector, an address in the form one port has, and an
// input list that carries the name it maps to. A DisplayPort cable
// derives nothing, because an address in that EDID describes the
// sink's HDMI topology and not the port this cable is in.
func derivedInput(output Output, facts panelFacts) string {
	if output.Monitor.HDMIInput == 0 || !strings.HasPrefix(output.Connector, "HDMI") {
		return ""
	}
	name := fmt.Sprintf("HDMI-%d", output.Monitor.HDMIInput)
	capability, carried := facts.Capabilities[inputControl]
	if !carried || !slices.Contains(capability.Values, name) {
		return ""
	}
	return name
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
	for _, name := range []string{"off", "hardOff", "standby"} {
		if slices.Contains(capability.Values, name) {
			return valueRaw(vcpPowerMode, name)
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

// The status of a panel that is on its connector now.
func (d *displayControl) statusOf(display *Display, output Output, facts panelFacts) DisplayStatus {
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
	status.AttachedInput = derivedInput(output, facts)
	status.CurrentMode = output.CurrentMode
	status.Modes = output.OfferedModes
	status.Capabilities = facts.Capabilities
	if observed := observedValues(facts.Observed); observed != nil {
		status.Observed = observed
	}
	status.Conditions = setCondition(status.Conditions, d.condition(ConnectedCondition, true,
		"PanelAttached", output.Connector+" carries this panel"))
	status.Conditions = setCondition(status.Conditions, d.responsive(facts))
	return status
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
func (d *displayControl) absent(display *Display) error {
	status := display.Status
	status.Conditions = setCondition(status.Conditions, d.condition(ConnectedCondition, false,
		"NoPanel", "no panel on "+status.Connector))
	return d.publish(display, status)
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
	updated, err := writeDisplayStatus(d.client, display, status)
	if err != nil {
		return err
	}
	*display = *updated
	return nil
}
