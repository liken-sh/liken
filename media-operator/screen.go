package main

// This file connects media-operator to the Display resources that
// display-operator owns. media-operator never talks to a monitor. It
// finds the Display for the screen a Player's idle pod draws on, then
// applies or removes spec.override on it. The Display types are
// written by hand here because this module does not import
// display-operator's module.

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
)

const displayAPIVersion = "display.liken.sh/v1alpha1"

// monitorIDAttribute is the device attribute that holds the monitor
// id. display-operator names each Display by its monitor id, so the
// id on the allocated draw device is the name of the Display.
const monitorIDAttribute = "monitor.liken.sh/id"

// displayPowerOff is the only value media-operator writes into an
// override, for the power or for the backlight. displayPowerOn is the
// observed power value of a lit panel.
// To turn a panel back on, media-operator removes the override, and
// display-operator then restores the settings it saved before the
// override.
const (
	displayPowerOff = "Off"
	displayPowerOn  = "On"
)

// samePower compares two power values without regard to case, because
// display-operator can report "on" or "On", and an override can hold
// "off" or "Off". A case-sensitive compare would read a lit panel as
// off, and would apply an override that is already in place again.
func samePower(a, b string) bool {
	return strings.EqualFold(a, b)
}

// applyFieldManager is the server-side apply field manager for the
// override. The apply sets only spec.override, so it does not conflict
// with the fields of the Display that other managers set.
const applyFieldManager = "media-operator"

// Display has only the fields this operator uses: the override it
// applies, and the observed values it copies into the Player's status.
type Display struct {
	APIVersion string        `json:"apiVersion,omitempty"`
	Kind       string        `json:"kind,omitempty"`
	Metadata   ObjectMeta    `json:"metadata"`
	Spec       DisplaySpec   `json:"spec"`
	Status     DisplayStatus `json:"status"`
}

// DisplaySpec is the part of a Display's spec that this operator
// writes. An apply with a nil override removes the override.
type DisplaySpec struct {
	Override *DisplayOverride `json:"override,omitempty"`
}

// DisplayOverride replaces the panel's settings until media-operator
// removes it.
type DisplayOverride struct {
	Backlight string `json:"backlight,omitempty"`
	Power     string `json:"power,omitempty"`
}

// displayApply is the body of an apply. It has no status, because
// display-operator writes the status.
type displayApply struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Metadata   ObjectMeta  `json:"metadata"`
	Spec       DisplaySpec `json:"spec"`
}

type DisplayStatus struct {
	Mode       DisplayMode        `json:"mode,omitempty"`
	Observed   DisplayObserved    `json:"observed,omitempty"`
	Conditions []DisplayCondition `json:"conditions,omitempty"`
}

// DisplayMode is the mode display-operator set, for the kernel and for
// the compositor's canvas, such as 1920x1080@60. A pattern:// URI with
// no frame reads the canvas mode, because mpv draws on the canvas.
type DisplayMode struct {
	Kernel string `json:"kernel,omitempty"`
	Weston string `json:"weston,omitempty"`
}

// displayConnectedCondition is the only Display condition this
// operator reads. display-operator sets it to False with the reason
// NoPanel when the connector has no panel, for example when the monitor
// shows another input.
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

// The Screen condition of a Player and its reasons. Present and
// PanelAway replace the Display's True status and NoPanel reason, and
// the Player keeps every other Display reason unchanged. NoDisplay
// means the Display does not exist. NotReported means the Display has
// no Connected condition.
const (
	screenConditionType     = "Screen"
	screenReasonPresent     = "Present"
	screenReasonPanelAway   = "PanelAway"
	screenReasonNoDisplay   = "NoDisplay"
	screenReasonNotReported = "NotReported"

	conditionFalse   = "False"
	conditionUnknown = "Unknown"
)

// DisplayObserved is what display-operator last read from the panel.
// It is not read live, because on some panels a DDC read wakes the
// panel.
type DisplayObserved struct {
	Brightness *int   `json:"brightness,omitempty"`
	Power      string `json:"power,omitempty"`
}

// ResourceClaimStatus is the status the scheduler writes. The operator
// reads which device the draw request got and which pods use the claim.
type ResourceClaimStatus struct {
	Allocation *DeviceAllocationResult `json:"allocation,omitempty"`

	ReservedFor []ClaimConsumer `json:"reservedFor,omitempty"`
}

// ClaimConsumer is one object that uses an allocated claim. Its fields
// match DRA's ResourceClaimConsumerReference. Resource is the plural
// resource name, and this operator looks only at pods.
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

// DeviceRequestAllocationResult is one allocated device. The driver,
// the pool, and the device name together identify it in the driver's
// ResourceSlices.
type DeviceRequestAllocationResult struct {
	Request string `json:"request,omitempty"`
	Driver  string `json:"driver,omitempty"`
	Pool    string `json:"pool,omitempty"`
	Device  string `json:"device,omitempty"`
}

// ResourceSlice lists the devices a driver publishes. This operator
// reads it for the attributes of one allocated device.
type ResourceSlice struct {
	Metadata ObjectMeta        `json:"metadata"`
	Spec     ResourceSliceSpec `json:"spec"`
}

type ResourceSliceSpec struct {
	Driver string            `json:"driver,omitempty"`
	Pool   ResourceSlicePool `json:"pool"`
	// NodeName is the machine whose devices this slice lists. It is the
	// machine that a Receiver input refers to.
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

// DeviceAttribute has only the string value, because this operator
// reads no attribute of another type.
type DeviceAttribute struct {
	String *string `json:"string,omitempty"`
}

// screen is the machine a Player draws on and the monitor id, which
// is also the name of its Display.
type screen struct {
	node    string
	monitor string
}

// screens finds each Player's screen and Display, and caches the
// ResourceSlices, the screens, and the Displays it reads for one pass.
// The cluster view's watch caches can change during a pass, so without
// this cache the Screen condition, the panel, the pattern frame, and
// the Receiver match could each act on a different screen.
type screens struct {
	view     *clusterView
	slices   []ResourceSlice
	listed   bool
	resolved map[string]screen
	displays map[string]displayRead
}

// displayRead is a cached Display read with its error, so a failed
// read is also cached for the pass.
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

func (s *screens) displayFor(monitor string) (*Display, error) {
	if read, cached := s.displays[monitor]; cached {
		return read.display, read.err
	}
	display, err := s.view.Display(monitor)
	s.displays[monitor] = displayRead{display: display, err: err}
	return display, err
}

// screenLookup returns the screens cache for the current pass. pass
// clears it when it starts, and the first call in the pass creates it.
func (o *operator) screenLookup() *screens {
	if o.screenCache == nil {
		o.screenCache = newScreens(o.view)
	}
	return o.screenCache
}

// screenFor returns the screen of the device that the scheduler
// allocated to the draw request of a Player's idle claim. It returns
// false when the claim is not allocated or the device has no monitor id.
func (s *screens) screenFor(player *Player) (screen, bool) {
	key := playerKey(player.Metadata.Namespace, player.Metadata.Name)
	if cached, checked := s.resolved[key]; checked {
		return cached, cached.monitor != ""
	}
	found, resolved := s.screenOfClaim(player)
	s.resolved[key] = found
	return found, resolved
}

func (s *screens) screenOfClaim(player *Player) (screen, bool) {
	claim, err := s.view.ResourceClaim(player.Metadata.Namespace, idleClaimName(player.Metadata.Name))
	if err != nil || claim.Status == nil || claim.Status.Allocation == nil {
		return screen{}, false
	}
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Request != idleDrawRequest {
			continue
		}
		return s.screenOfDevice(result)
	}
	return screen{}, false
}

// screenOfDevice finds an allocated device in the ResourceSlices by
// driver, pool, and device name, and returns the slice's node and the
// device's monitor id. It lists the slices at most once per pass.
func (s *screens) screenOfDevice(result DeviceRequestAllocationResult) (screen, bool) {
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
			attribute, ok := device.Attributes[monitorIDAttribute]
			if !ok || attribute.String == nil {
				return screen{}, false
			}
			return screen{node: slice.Spec.NodeName, monitor: *attribute.String}, true
		}
	}
	return screen{}, false
}

// panelOverride records what this operator last applied for one Player.
// The override is stored beside the desire because the off desire
// becomes a backlight or a power override, depending on the off mode
// and spec.power. The monitor id is stored because deleting a Player
// deletes its idle claim, and removeGonePanelOverrides then has no other way to
// find the Display whose override it must remove.
type panelOverride struct {
	desire   string
	override *DisplayOverride
	monitor  string
}

// reconcilePanel applies the override for the panel desire of one
// Player, and returns the panel value for the Player's status from the
// Display's observed state. When the screen has no Display, it writes
// nothing, so the panel stays lit.
//
// spec.power takes priority over the desire that the idle screen client
// sends. While a Player is off, its panel gets the power override, so a
// client that has stopped, or still sends the on desire, cannot leave
// the panel lit. In the pass that turns a Player on, the override is
// removed at once, without waiting for the client to send the on
// desire. outcome records what a change of spec.power did to the
// Display.
func (o *operator) reconcilePanel(player *Player, key string, lookup *screens, mode string, step powerStep, outcome *powerOutcome) string {
	desire := o.panels.desireFor(key, player.Status.PoweredOnAt)
	switch {
	case step.want == playerPowerOff:
		desire, mode = panelDesireOff, offModePower
	case step.turningOn():
		desire = panelDesireOn
	}
	if desire == "" {
		return ""
	}
	found, resolved := lookup.screenFor(player)
	monitor := found.monitor
	if !resolved {
		// With no allocated screen there is no Display to write. Only
		// the off desire reports a fault, because the on desire wants
		// the panel lit.
		if desire == panelDesireOff {
			o.panelFault(key, fmt.Sprintf("Player %s/%s has no allocated screen, so the panel stays lit",
				player.Metadata.Namespace, player.Metadata.Name))
		}
		return ""
	}
	display, err := lookup.displayFor(monitor)
	if err != nil {
		o.panelFault(key, fmt.Sprintf("reading Display %s: %v", monitor, err))
		if step.moving {
			outcome.fail(powerReasonDisplayWriteFailed, fmt.Sprintf("reading Display %s: %v", monitor, err))
		}
		return ""
	}
	want := overrideFor(desire, mode)
	if applied, tracked := o.panelOverrides[key]; !tracked || !sameOverride(applied.override, want) {
		cause := "the idle screen requested panel " + desire
		if step.want == playerPowerOff || step.turningOn() {
			cause = "spec.power is " + step.want
		}
		if err := o.writeOverride(key, cause, desire, monitor, want, display); err != nil {
			if step.moving {
				outcome.fail(powerReasonDisplayWriteFailed, fmt.Sprintf("applying %s to Display %s: %v",
					describeOverride(want), monitor, err))
			}
			return panelFromDisplay(display.Status.Observed)
		}
	}
	if step.moving {
		if want == nil {
			outcome.record("removed the override from Display %s", monitor)
		} else {
			outcome.record("applied %s to Display %s", describeOverride(want), monitor)
		}
	}
	delete(o.panelFaults, key)
	return panelFromDisplay(display.Status.Observed)
}

// writeOverride applies want to the Display and records it in
// panelOverrides. It applies only when the Display's override differs,
// so after a restart the operator records the override that the earlier
// run applied and makes no second write.
func (o *operator) writeOverride(key, cause, desire, monitor string, want *DisplayOverride, display *Display) error {
	if !sameOverride(display.Spec.Override, want) {
		if err := ApplyDisplayOverride(o.client, monitor, want); err != nil {
			o.panelFault(key, fmt.Sprintf("applying the override to Display %s: %v", monitor, err))
			return err
		}
		logLine(o.log, "player %s: %s, applied %s to Display %s",
			key, cause, describeOverride(want), monitor)
	}
	o.panelOverrides[key] = panelOverride{desire: desire, override: want, monitor: monitor}
	return nil
}

// sameOverride compares two overrides, ignoring case as samePower does.
// A nil override and an empty one are equal, because neither changes the
// panel's settings.
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

// reconcileScreen returns the screen and the Screen condition for the
// Player's status. When the idle claim is not allocated, it keeps the
// screen already in the status. That happens when the monitor shows
// another input, because display-operator then removes the draw
// device, and the status is the only record of which Display to read. The condition lets a person tell a Player that waits for its
// panel from a Player with a fault.
func (o *operator) reconcileScreen(player *Player, key string, lookup *screens) (*PlayerScreenStatus, []PlayerCondition) {
	current := player.Status.Screen
	if found, resolved := lookup.screenFor(player); resolved {
		current = &PlayerScreenStatus{Node: found.node, Monitor: found.monitor}
	}
	if current == nil {
		return nil, nil
	}
	condition, reported := o.screenCondition(player, key, lookup, current.Monitor)
	if !reported {
		return current, nil
	}
	return current, []PlayerCondition{condition}
}

// screenCondition builds the Player's Screen condition from the
// Display's Connected condition. The status is Unknown when the Display
// does not exist or has no Connected condition. Any other read error
// says nothing about the panel, so it returns the Player's current
// Screen condition, if any, and reports the fault once. conditions.Set
// keeps the transition time when the status has not changed.
func (o *operator) screenCondition(player *Player, key string, lookup *screens, monitor string) (PlayerCondition, bool) {
	existing, exists := conditionOfType(player.Status.Conditions, screenConditionType)
	display, err := lookup.displayFor(monitor)
	var condition PlayerCondition
	switch {
	case errors.Is(err, apiclient.ErrNotFound):
		condition = PlayerCondition{
			Status:  conditionUnknown,
			Reason:  screenReasonNoDisplay,
			Message: "no Display named " + monitor,
		}
	case err != nil:
		o.panelFault(key, fmt.Sprintf("reading Display %s: %v", monitor, err))
		return existing, exists
	default:
		condition = screenFromDisplay(display)
	}
	condition.Type = screenConditionType
	list := append([]PlayerCondition(nil), player.Status.Conditions...)
	conditions.Set(&list, condition)
	condition, _ = conditions.Find(list, screenConditionType)
	return condition, true
}

// screenFromDisplay converts the Display's Connected condition into a
// Screen condition. True becomes Present and the reason NoPanel becomes
// PanelAway. The message is copied unchanged, because it names the
// connector.
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

func conditionOfType(conditions []PlayerCondition, kind string) (PlayerCondition, bool) {
	for _, condition := range conditions {
		if condition.Type == kind {
			return condition, true
		}
	}
	return PlayerCondition{}, false
}

// removeGonePanelOverrides removes the override of each Player that is not in live,
// and drops its entry. A deleted Player's idle claim is deleted with
// it, so no later pass would find the Display, and a panel left off
// would stay off. When the removal fails, the entry stays and the next
// pass retries it.
func (o *operator) removeGonePanelOverrides(live map[string]bool) {
	for key, override := range o.panelOverrides {
		if live[key] {
			continue
		}
		if override.override != nil {
			// A Display that no longer exists has no override, so a
			// NotFound error means the override is already gone. Any
			// other error keeps the entry, because the override may
			// still be applied.
			err := ApplyDisplayOverride(o.client, override.monitor, nil)
			if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
				o.panelFault(key, fmt.Sprintf("lifting the override on Display %s: %v",
					override.monitor, err))
				continue
			}
			logLine(o.log, "player %s: removed the override from Display %s, because the Player was deleted", key, override.monitor)
		}
		delete(o.panelOverrides, key)
		delete(o.panelFaults, key)
	}
	// Keep the fault of a deleted Player until its override is removed,
	// so the retry does not log the same message on every pass.
	for key := range o.panelFaults {
		if _, pending := o.panelOverrides[key]; !live[key] && !pending {
			delete(o.panelFaults, key)
		}
	}
}

// panelFault logs a failure to read or write a Display, once per Player
// and message, so a cluster without display-operator does not log the
// same failure on every pass.
func (o *operator) panelFault(key, message string) {
	if o.panelFaults[key] == message {
		return
	}
	o.panelFaults[key] = message
	fmt.Fprintln(os.Stderr, message)
}

// describeOverride returns a short description of an override for log
// lines and condition messages.
func describeOverride(override *DisplayOverride) string {
	switch {
	case override == nil:
		return "no override"
	case override.Power != "":
		return "power " + override.Power
	}
	return "backlight " + override.Backlight
}

// overrideFor returns the override for a desire and the off mode. The
// on desire returns nil, and applying nil removes the current override.
func overrideFor(desire, mode string) *DisplayOverride {
	if desire != panelDesireOff {
		return nil
	}
	if mode == offModePower {
		return &DisplayOverride{Power: displayPowerOff}
	}
	return &DisplayOverride{Backlight: displayPowerOff}
}
