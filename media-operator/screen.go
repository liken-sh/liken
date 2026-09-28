package main

// This file is the media layer's half of the display-operator's
// Display resource. The media layer never writes a panel. It finds
// the Display that names the screen a unit's idle pod draws on, and
// it applies or lifts spec.override there. The types are hand-written
// for the same reason the Kubernetes types in api.go are, and the
// display-operator's Go module is not a dependency.

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// The group the display-operator serves. A Display is
// cluster-scoped, because a panel is physical and belongs to no
// namespace.
const displayAPIVersion = "display.liken.sh/v1alpha1"

// The attribute both of a connector's devices carry. The
// display-operator names each Display by this value, so the allocated
// draw device names the Display through it.
const monitorIDAttribute = "monitor.liken.sh/id"

// The one override value the media layer writes, and the one
// observed power word that means a lit panel. The panel comes back
// when the block is deleted, so nothing here writes an on value: the
// display-operator answers the lift by restoring what it captured.
// Both are PascalCase, the form of every enum value in a Kubernetes
// resource.
const (
	displayPowerOff = "Off"
	displayPowerOn  = "On"
)

// samePower compares two Display power words without regard to case.
// The display-operator reports a word in lowercase or in PascalCase,
// "on" or "On", depending on its build, and a Display's override can
// hold "off" or "Off" in the same way. Both spellings name one state.
// An exact compare reads a lit panel as down, or a standing override as
// one to write again, when the spelling differs.
func samePower(a, b string) bool {
	return strings.EqualFold(a, b)
}

// The field manager this operator applies under. Server-side
// apply keeps it to the one block this operator states, so the cluster
// owner's resting fields never conflict with it.
const applyFieldManager = "media-operator"

// A Display carries only what this operator reads or writes:
// the override it applies, and the observed values it folds into the
// Player's status.
type Display struct {
	APIVersion string        `json:"apiVersion,omitempty"`
	Kind       string        `json:"kind,omitempty"`
	Metadata   ObjectMeta    `json:"metadata"`
	Spec       DisplaySpec   `json:"spec"`
	Status     DisplayStatus `json:"status"`
}

// The spec half this operator writes is the override alone. A
// nil override is the apply that lifts one.
type DisplaySpec struct {
	Override *DisplayOverride `json:"override,omitempty"`
}

// The temporary layer over the panel's resting settings. A backlight
// at zero still answers DDC. Power off stops some panels from
// answering DDC at all, so a Player states it only for a panel the
// drill proved wakes.
type DisplayOverride struct {
	Backlight string `json:"backlight,omitempty"`
	Power     string `json:"power,omitempty"`
}

// The body of an apply. It carries the spec alone, because the
// status is the display-operator's to write.
type displayApply struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Metadata   ObjectMeta  `json:"metadata"`
	Spec       DisplaySpec `json:"spec"`
}

type DisplayStatus struct {
	Observed   DisplayObserved    `json:"observed,omitempty"`
	Conditions []DisplayCondition `json:"conditions,omitempty"`
}

// The one Display condition this operator reads. The display-operator
// sets it False with the NoPanel reason when the connector carries no
// panel, which is a monitor that shows another input.
const (
	displayConnectedCondition = "Connected"
	displayReasonNoPanel      = "NoPanel"
)

type DisplayCondition struct {
	Type    string `json:"type,omitempty"`
	Status  string `json:"status,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// The Screen condition a Player carries, and its reasons. Present and
// PanelAway are this operator's words for the Display's True and
// NoPanel; every other Display reason passes through. NoDisplay and
// NotReported are the two ways the Display answers nothing.
const (
	screenConditionType     = "Screen"
	screenReasonPresent     = "Present"
	screenReasonPanelAway   = "PanelAway"
	screenReasonNoDisplay   = "NoDisplay"
	screenReasonNotReported = "NotReported"

	conditionFalse   = "False"
	conditionUnknown = "Unknown"
)

// What the display-operator last read from the panel. It is
// last-known and never live, because a DDC read is itself a wake
// stimulus on some panels.
type DisplayObserved struct {
	Brightness *int   `json:"brightness,omitempty"`
	Power      string `json:"power,omitempty"`
}

// The claim status the scheduler writes. The operator reads it
// for two questions: which device the draw request took, and which pods
// hold the claim now.
type ResourceClaimStatus struct {
	Allocation *DeviceAllocationResult `json:"allocation,omitempty"`

	// ReservedFor names every object that holds an allocated
	// claim. An allocated claim carries the delete-protection finalizer,
	// so a delete stays in Terminating until every holder is gone.
	ReservedFor []ClaimConsumer `json:"reservedFor,omitempty"`
}

// One holder of an allocated claim. The field names match DRA's
// ResourceClaimConsumerReference. Resource is the plural resource name,
// and the operator acts on pods alone.
type ClaimConsumer struct {
	APIGroup string `json:"apiGroup,omitempty"`
	Resource string `json:"resource,omitempty"`
	Name     string `json:"name,omitempty"`
	UID      string `json:"uid,omitempty"`
}

type DeviceAllocationResult struct {
	Devices DeviceAllocationDevices `json:"devices,omitempty"`
}

type DeviceAllocationDevices struct {
	Results []DeviceRequestAllocationResult `json:"results,omitempty"`
}

// One allocated device. The three coordinates name it in the
// driver's ResourceSlices: the driver, the pool, and the device name
// inside that pool.
type DeviceRequestAllocationResult struct {
	Request string `json:"request,omitempty"`
	Driver  string `json:"driver,omitempty"`
	Pool    string `json:"pool,omitempty"`
	Device  string `json:"device,omitempty"`
}

// A ResourceSlice is the driver's published inventory. This
// operator reads it for the attributes on one allocated device.
type ResourceSlice struct {
	Metadata ObjectMeta        `json:"metadata"`
	Spec     ResourceSliceSpec `json:"spec"`
}

type ResourceSliceSpec struct {
	Driver string            `json:"driver,omitempty"`
	Pool   ResourceSlicePool `json:"pool"`
	// The machine whose devices this slice publishes, which is the machine
	// a Receiver input names.
	NodeName string              `json:"nodeName,omitempty"`
	Devices  []ResourceSliceItem `json:"devices,omitempty"`
}

type ResourceSlicePool struct {
	Name string `json:"name,omitempty"`
}

type ResourceSliceItem struct {
	Name       string                     `json:"name,omitempty"`
	Attributes map[string]DeviceAttribute `json:"attributes,omitempty"`
}

// An attribute is one of four types, and one field of the four
// is set. This operator reads the string form alone.
type DeviceAttribute struct {
	String *string `json:"string,omitempty"`
}

// A resolved screen is the machine the unit draws on and the monitor id
// that names its Display.
type screen struct {
	node    string
	monitor string
}

// screens resolves each unit's screen to the monitor id that
// names its Display. It reads the driver's ResourceSlices from the view
// at most once a pass, because one read answers every unit.
//
// Each unit's answer is held for the pass, because the panel and the
// equipment both ask the same question of the same claim. Each
// Display read is held the same way, because the Screen condition
// and the panel both read the same one, and every caller in the pass
// then reads the same answer.
type screens struct {
	view     *clusterView
	slices   []ResourceSlice
	listed   bool
	resolved map[string]screen
	displays map[string]displayRead
}

// One Display read, kept with its error so every caller in the pass
// sees the same answer.
type displayRead struct {
	display *Display
	err     error
}

func newScreens(view *clusterView) *screens {
	return &screens{
		view:     view,
		resolved: map[string]screen{},
		displays: map[string]displayRead{},
	}
}

// displayFor reads one Display at most once a pass.
func (s *screens) displayFor(monitor string) (*Display, error) {
	if read, held := s.displays[monitor]; held {
		return read.display, read.err
	}
	display, err := s.view.Display(monitor)
	s.displays[monitor] = displayRead{display: display, err: err}
	return display, err
}

// screenLookup is the pass's one screens. It is built on first use and
// dropped when the pass ends.
func (o *operator) screenLookup() *screens {
	if o.screenCache == nil {
		o.screenCache = newScreens(o.view)
	}
	return o.screenCache
}

// screenFor is the whole lookup: the unit's standing idle
// claim carries the allocation, the allocation names the draw device,
// and the device's attributes carry the monitor id. A claim the
// scheduler has not allocated yet names no screen, and the pass then
// writes no override.
func (s *screens) screenFor(player *Player) (screen, bool) {
	key := playerKey(player.Metadata.Namespace, player.Metadata.Name)
	if held, checked := s.resolved[key]; checked {
		return held, held.monitor != ""
	}
	found, resolved := s.lookUp(player)
	s.resolved[key] = found
	return found, resolved
}

func (s *screens) lookUp(player *Player) (screen, bool) {
	claim, err := s.view.ResourceClaim(player.Metadata.Namespace, idleClaimName(player.Metadata.Name))
	if err != nil || claim.Status == nil || claim.Status.Allocation == nil {
		return screen{}, false
	}
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Request != idleDrawRequest {
			continue
		}
		return s.screenOf(result)
	}
	return screen{}, false
}

// screenOf reads the monitor id off the allocated device. The
// driver, the pool, and the device name are what a ResourceSlice entry
// is keyed by.
//
// The slice also names the machine that publishes the device, which is
// the machine a Receiver input names.
func (s *screens) screenOf(result DeviceRequestAllocationResult) (screen, bool) {
	if !s.listed {
		s.listed = true
		slices, err := s.view.ResourceSlices()
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading resource slices: %v\n", err)
			return screen{}, false
		}
		s.slices = slices
	}
	for _, slice := range s.slices {
		if slice.Spec.Driver != result.Driver || slice.Spec.Pool.Name != result.Pool {
			continue
		}
		for _, device := range slice.Spec.Devices {
			if device.Name != result.Device {
				continue
			}
			attribute, held := device.Attributes[monitorIDAttribute]
			if !held || attribute.String == nil {
				return screen{}, false
			}
			return screen{node: slice.Spec.NodeName, monitor: *attribute.String}, true
		}
	}
	return screen{}, false
}

// panelOverride is what this operator last wrote for one unit: the
// desire it applied, and the monitor it applied it to. The monitor is
// held because a deleted Player takes its idle claim with it, and the
// claim was the only path from the unit to its screen. The lift a
// dark panel is still owed goes to the remembered monitor.
type panelOverride struct {
	desire  string
	monitor string
}

// reconcilePanel settles one unit's panel and answers the
// state its status carries. The desire is the idle screen client's, the
// override is this operator's write, and the observed state is the
// display-operator's. A unit whose screen carries no Display keeps
// its panel lit: there is no second writer to fall back to.
func (o *operator) reconcilePanel(player *Player, key string, lookup *screens, mode string) string {
	desire := o.panels.stateFor(key)
	if desire == "" {
		return ""
	}
	found, resolved := lookup.screenFor(player)
	monitor := found.monitor
	if !resolved {
		// A screen the scheduler has not allocated is the one
		// silent fault, and only for the on desire: the panel is lit,
		// which is what the desire asks for.
		if desire == panelDesireOff {
			o.panelFault(key, fmt.Sprintf("player %s/%s: no allocated screen; the panel stays lit",
				player.Metadata.Namespace, player.Metadata.Name))
		}
		return ""
	}
	display, err := lookup.displayFor(monitor)
	if err != nil {
		o.panelFault(key, fmt.Sprintf("reading display %s: %v", monitor, err))
		return ""
	}
	if o.panelOverrides[key].desire != desire {
		if !o.writeOverride(key, desire, monitor, overrideFor(desire, mode), display) {
			return panelFromDisplay(display.Status.Observed)
		}
	}
	delete(o.panelFaults, key)
	return panelFromDisplay(display.Status.Observed)
}

// writeOverride brings the Display's override to the one a new desire
// asks for, and records the desire. It reads the override the Display
// carries first, and writes only when that override differs, so an
// operator that restarts adopts the override its earlier run wrote and
// the panel sees no second write. It answers whether the Display now
// carries the override.
func (o *operator) writeOverride(key, desire, monitor string, want *DisplayOverride, display *Display) bool {
	if !sameOverride(display.Spec.Override, want) {
		if err := ApplyDisplayOverride(o.client, monitor, want); err != nil {
			o.panelFault(key, fmt.Sprintf("overriding display %s: %v", monitor, err))
			return false
		}
		logLine(o.log, "player %s: the idle screen asked for panel %s, applied %s to display %s",
			key, desire, describeOverride(want), monitor)
	}
	o.panelOverrides[key] = panelOverride{desire: desire, monitor: monitor}
	return true
}

// sameOverride compares two override blocks, each value without regard
// to case, for the reason samePower gives. No block and an empty block
// are the same: each leaves the panel at its resting settings.
func sameOverride(a, b *DisplayOverride) bool {
	var left, right DisplayOverride
	if a != nil {
		left = *a
	}
	if b != nil {
		right = *b
	}
	return samePower(left.Backlight, right.Backlight) && samePower(left.Power, right.Power)
}

// reconcileScreen answers one unit's screen memory and its Screen
// condition. The memory is the screen the claim resolves to this
// pass, or the one the Player already remembers when the claim has
// deallocated: a monitor that shows another input drops the draw
// device, and the claim was the only path from the unit to its
// Display. The condition reads that Display, so a person can tell a
// unit that waits for its panel from a unit at fault.
func (o *operator) reconcileScreen(player *Player, key string, lookup *screens) (*PlayerScreenStatus, []PlayerCondition) {
	remembered := player.Status.Screen
	if found, resolved := lookup.screenFor(player); resolved {
		remembered = &PlayerScreenStatus{Node: found.node, Monitor: found.monitor}
	}
	if remembered == nil {
		return nil, nil
	}
	condition, reported := o.screenCondition(player, key, lookup, remembered.Monitor)
	if !reported {
		return remembered, nil
	}
	return remembered, []PlayerCondition{condition}
}

// screenCondition folds the Display's Connected condition into the
// unit's Screen condition. A Display that is gone or carries no
// report answers Unknown. A read that fails any other way is not a
// change on the panel, so the unit keeps the condition it holds and
// the fault reports once.
func (o *operator) screenCondition(player *Player, key string, lookup *screens, monitor string) (PlayerCondition, bool) {
	held, holds := heldCondition(player.Status.Conditions, screenConditionType)
	display, err := lookup.displayFor(monitor)
	var condition PlayerCondition
	switch {
	case errors.Is(err, ErrNotFound):
		condition = PlayerCondition{
			Status:  conditionUnknown,
			Reason:  screenReasonNoDisplay,
			Message: "no Display named " + monitor,
		}
	case err != nil:
		o.panelFault(key, fmt.Sprintf("reading display %s: %v", monitor, err))
		return held, holds
	default:
		condition = screenFromDisplay(display)
	}
	condition.Type = screenConditionType
	condition.LastTransitionTime = held.LastTransitionTime
	if !holds || held.Status != condition.Status {
		condition.LastTransitionTime = time.Now().UTC().Format(time.RFC3339)
	}
	return condition, true
}

// screenFromDisplay reads the Display's Connected condition. True is
// Present, and NoPanel is PanelAway; the Display's message comes
// along either way, because it names the connector.
func screenFromDisplay(display *Display) PlayerCondition {
	for _, connected := range display.Status.Conditions {
		if connected.Type != displayConnectedCondition {
			continue
		}
		if connected.Status == conditionTrue {
			return PlayerCondition{Status: conditionTrue, Reason: screenReasonPresent, Message: connected.Message}
		}
		reason := connected.Reason
		if reason == displayReasonNoPanel {
			reason = screenReasonPanelAway
		}
		return PlayerCondition{Status: conditionFalse, Reason: reason, Message: connected.Message}
	}
	return PlayerCondition{Status: conditionUnknown, Reason: screenReasonNotReported}
}

// heldCondition finds the condition of one type on a Player.
func heldCondition(conditions []PlayerCondition, kind string) (PlayerCondition, bool) {
	for _, condition := range conditions {
		if condition.Type == kind {
			return condition, true
		}
	}
	return PlayerCondition{}, false
}

// retainPanels shrinks the record to the units the cluster
// still holds, and lifts the override of every unit it drops. A
// Player deleted while its panel is dark has no idle claim any more,
// so nothing would ever write that Display again and the panel would
// stay dark. A lift that fails keeps the entry, so the next pass
// writes it again.
func (o *operator) retainPanels(live map[string]bool) {
	for key, override := range o.panelOverrides {
		if live[key] {
			continue
		}
		if override.desire == panelDesireOff {
			// A Display that is gone carries no override, so the
			// lift it refuses has already landed. Every other failure
			// keeps the entry, because the block still stands.
			err := ApplyDisplayOverride(o.client, override.monitor, nil)
			if err != nil && !errors.Is(err, ErrNotFound) {
				o.panelFault(key, fmt.Sprintf("lifting the override on display %s: %v",
					override.monitor, err))
				continue
			}
			logLine(o.log, "player %s: lifted the override on display %s, because the player is gone", key, override.monitor)
		}
		delete(o.panelOverrides, key)
		delete(o.panelFaults, key)
	}
	// A fault outlives its unit only while that unit still owes
	// a lift, because the retry reports the same message once and not
	// once a pass.
	for key := range o.panelFaults {
		if _, owed := o.panelOverrides[key]; !live[key] && !owed {
			delete(o.panelFaults, key)
		}
	}
}

// panelFault reports a panel this pass could not write. Each
// distinct message reports once per unit, so a cluster with no
// Display support does not log every pass.
func (o *operator) panelFault(key, message string) {
	if o.panelFaults[key] == message {
		return
	}
	o.panelFaults[key] = message
	fmt.Fprintln(os.Stderr, message)
}

// describeOverride names an override block the way a line reads it.
func describeOverride(override *DisplayOverride) string {
	switch {
	case override == nil:
		return "no override"
	case override.Power != "":
		return "power " + override.Power
	}
	return "backlight " + override.Backlight
}

// overrideFor turns one desire and the resolved off mode into
// the override block. The on desire carries no block, and that apply
// is what deletes the one standing.
func overrideFor(desire, mode string) *DisplayOverride {
	if desire != panelDesireOff {
		return nil
	}
	if mode == offModePower {
		return &DisplayOverride{Power: displayPowerOff}
	}
	return &DisplayOverride{Backlight: displayPowerOff}
}
