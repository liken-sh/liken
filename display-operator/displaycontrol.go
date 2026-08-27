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
	"os"
	"reflect"
	"slices"
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
}

func newDisplayControl(client *Client, node string, controls *panelControls, outputs func() []Output) *displayControl {
	return &displayControl{
		client:     client,
		node:       node,
		controls:   controls,
		outputs:    outputs,
		now:        time.Now,
		wait:       waitFor,
		wakes:      make(chan struct{}, 1),
		restoring:  map[string]bool{},
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

// The loop. The watch wakes it on a spec that changed, the
// slice publisher wakes it on hardware that moved, and the tick is the
// backstop that covers a dropped watch.
func (d *displayControl) run(ctx context.Context) {
	tick := time.NewTicker(backstopInterval)
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
	present := map[string]Output{}
	for _, output := range d.outputs() {
		if !output.Connected {
			continue
		}
		if name := monitorID(output.Monitor); name != "" {
			present[name] = output
		}
	}
	published, err := listDisplays(d.client)
	if err != nil {
		return err
	}
	var failures []error
	for name, output := range present {
		if err := d.reconcile(ctx, name, output); err != nil {
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
	return errors.Join(failures...)
}

// One panel. The resource is created empty when it is absent,
// the panel is actuated, and the status is written last, so it reports
// what the actuation left behind.
func (d *displayControl) reconcile(ctx context.Context, name string, output Output) error {
	display, err := getDisplay(d.client, name)
	if errors.Is(err, ErrNotFound) {
		display, err = createDisplay(d.client, name)
	}
	if err != nil {
		return err
	}
	facts := d.controls.factsFor(output)
	actuated := d.actuate(ctx, display, output, facts)
	published := d.publish(display, d.statusOf(display, output, d.controls.factsFor(output)))
	return errors.Join(actuated, published)
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
