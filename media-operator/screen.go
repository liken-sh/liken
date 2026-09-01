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
const (
	displayPowerOff = "off"
	displayPowerOn  = "on"
)

// The field manager this operator applies under. Server-side
// apply keeps it to spec.override, so the cluster owner's resting
// fields never conflict with it.
const displayFieldManager = "media-operator"

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
	Observed DisplayObserved `json:"observed,omitempty"`
}

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

type ResourceSliceList struct {
	Metadata ListMeta        `json:"metadata"`
	Items    []ResourceSlice `json:"items"`
}

type ResourceSliceSpec struct {
	Driver  string              `json:"driver,omitempty"`
	Pool    ResourceSlicePool   `json:"pool"`
	Devices []ResourceSliceItem `json:"devices,omitempty"`
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

// screens resolves each unit's screen to the monitor id that
// names its Display. It lists the driver's ResourceSlices at most once
// a pass, because one list answers every unit.
type screens struct {
	client *Client
	slices []ResourceSlice
	listed bool
}

func newScreens(client *Client) *screens {
	return &screens{client: client}
}

// monitorFor is the whole lookup: the unit's standing idle
// claim carries the allocation, the allocation names the draw device,
// and the device's attributes carry the monitor id. A claim the
// scheduler has not allocated yet names no screen, and the pass then
// writes no override.
func (s *screens) monitorFor(player *Player) (string, bool) {
	claim, err := GetResourceClaim(s.client,
		player.Metadata.Namespace, idleClaimName(player.Metadata.Name))
	if err != nil || claim.Status == nil || claim.Status.Allocation == nil {
		return "", false
	}
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Request != idleDrawRequest {
			continue
		}
		return s.monitorOf(result)
	}
	return "", false
}

// monitorOf reads the monitor id off the allocated device. The
// driver, the pool, and the device name are what a ResourceSlice entry
// is keyed by.
func (s *screens) monitorOf(result DeviceRequestAllocationResult) (string, bool) {
	if !s.listed {
		s.listed = true
		list, err := ListResourceSlices(s.client)
		if err != nil {
			fmt.Fprintf(os.Stderr, "listing resource slices: %v\n", err)
			return "", false
		}
		s.slices = list.Items
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
				return "", false
			}
			return *attribute.String, true
		}
	}
	return "", false
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
// state its status carries. The desire is the idle command pod's, the
// override is this operator's write, and the observed state is the
// display-operator's. A unit whose screen carries no Display keeps
// its panel lit: there is no second writer to fall back to.
func (o *operator) reconcilePanel(player *Player, key string, lookup *screens, mode string) string {
	desire := o.panels.stateFor(key)
	if desire == "" {
		return ""
	}
	monitor, found := lookup.monitorFor(player)
	if !found {
		// A screen the scheduler has not allocated is the one
		// silent fault, and only for the on desire: the panel is lit,
		// which is what the desire asks for.
		if desire == panelDesireOff {
			o.panelFault(key, fmt.Sprintf("player %s/%s: no allocated screen; the panel stays lit",
				player.Metadata.Namespace, player.Metadata.Name))
		}
		return ""
	}
	display, err := GetDisplay(o.client, monitor)
	if err != nil {
		o.panelFault(key, fmt.Sprintf("reading display %s: %v", monitor, err))
		return ""
	}
	if o.panelOverrides[key].desire != desire {
		if err := ApplyDisplayOverride(o.client, monitor, overrideFor(desire, mode)); err != nil {
			o.panelFault(key, fmt.Sprintf("overriding display %s: %v", monitor, err))
			return panelFromDisplay(display.Status.Observed)
		}
		o.panelOverrides[key] = panelOverride{desire: desire, monitor: monitor}
	}
	delete(o.panelFaults, key)
	return panelFromDisplay(display.Status.Observed)
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
