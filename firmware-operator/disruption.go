package main

// A Secure Boot database update must be written while no workload runs
// on the machine, and takes effect at the next boot. A Disruption
// (liken.sh/v1alpha1) asks machine-operator for a machine's turn to
// drain and reboot, so the apply uses one. The operator creates it with
// spec.hold set, so machine-operator drains the machine, sets the Held
// condition, and waits. The apply pod writes, and the operator removes
// spec.hold so the reboot follows. When the write is refused or fails,
// the operator deletes the Disruption instead, and the machine ends its
// turn without a reboot.
//
// The operator declares its own struct with only the fields it uses, as
// it does for a Pod, and does not import liken/disruption. The label
// disruptionLabel selects the Disruptions that the operator watches, and
// the owner reference to the Firmware makes the garbage collector delete
// them with it.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
)

const (
	disruptionAPIVersion = "liken.sh/v1alpha1"
	disruptionKind       = "Disruption"
	disruptionsPath      = "/apis/liken.sh/v1alpha1/disruptions"
)

func disruptionPath(name string) string { return disruptionsPath + "/" + name }

// requester is the value of spec.requester in each Disruption, and
// disruptionLabel holds the name of the Disruption's Firmware.
const (
	requester       = "firmware.liken.sh/firmware-operator"
	disruptionLabel = "firmware.liken.sh/firmware"
)

// The conditions that machine-operator sets on a Disruption.
const (
	heldCondition     = "Held"
	completeCondition = "Complete"
)

type disruption struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Metadata   disruptionMeta   `json:"metadata"`
	Spec       disruptionSpec   `json:"spec"`
	Status     disruptionStatus `json:"status,omitzero"`
}

type disruptionMeta struct {
	Name              string            `json:"name"`
	UID               string            `json:"uid,omitempty"`
	Generation        int64             `json:"generation,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	OwnerReferences   []ownerReference  `json:"ownerReferences,omitempty"`
	DeletionTimestamp string            `json:"deletionTimestamp,omitempty"`
}

// disruptionSpec omits hash and approvedHash. machine-operator sets
// hash to the current boot, and an admission policy lets only a person
// set approvedHash.
type disruptionSpec struct {
	Machine   string `json:"machine"`
	Requester string `json:"requester"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason,omitempty"`
	Hold      bool   `json:"hold,omitempty"`
	// KeepBootNext tells liken's init, at the turn's reboot, to leave
	// the BootNext variable as fwupd set it.
	KeepBootNext bool `json:"keepBootNext,omitempty"`
}

type disruptionStatus struct {
	Conditions []condition `json:"conditions,omitempty"`
}

// held reports whether machine-operator has drained the machine and is
// waiting for the operator to remove spec.hold.
func (d *disruption) held() bool { return d.conditionTrue(heldCondition) }

// complete reports whether machine-operator has seen the machine boot
// again after the Disruption was created, so the turn is over.
func (d *disruption) complete() bool { return d.conditionTrue(completeCondition) }

func (d *disruption) conditionTrue(kind string) bool {
	c, found := conditions.Find(d.Status.Conditions, kind)
	return found && c.Status == conditions.True
}

// disruptionName returns the name of a Firmware's Disruption for a
// machine. The name is deterministic, so creating the Disruption twice
// returns 409. It is cut to 253 characters, the limit for a DNS
// subdomain.
func disruptionName(firmware, machine string) string {
	name := machine + ".firmware-" + firmware
	if len(name) > 253 {
		name = name[:253]
	}
	return strings.TrimRight(name, "-.")
}

// buildDisruption returns the Disruption that requests a machine's turn
// for an apply of f.
func buildDisruption(f *firmware, machine, what string) disruption {
	return disruption{
		APIVersion: disruptionAPIVersion,
		Kind:       disruptionKind,
		Metadata: disruptionMeta{
			Name: disruptionName(f.Metadata.Name, machine),
			Labels: map[string]string{
				disruptionLabel:                f.Metadata.Name,
				"app.kubernetes.io/managed-by": component,
			},
			OwnerReferences: []ownerReference{{
				APIVersion: firmwareAPIVersion, Kind: firmwareKind, Name: f.Metadata.Name, UID: f.Metadata.UID,
			}},
		},
		Spec: disruptionSpec{
			Machine:   machine,
			Requester: requester,
			Kind:      "Reboot",
			Reason:    fmt.Sprintf("%s: install %s of %s", f.Metadata.Name, f.Spec.Update.Version, what),
			Hold:      true,
		},
	}
}

// createDisruption creates a Disruption. A 409 means it already exists,
// which is not an error.
func (o *operator) createDisruption(d disruption) error {
	err := o.client.RequestJSON(http.MethodPost, disruptionsPath, mustJSON(d), nil)
	if errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	return err
}

// readDisruption returns a Disruption by name, or nil when it does not
// exist. When the informer's store does not have it, readDisruption asks
// the API server, because the store can lag behind a Disruption that the
// operator just created, and a missing Disruption fails an apply that
// waits for its turn.
func (o *operator) readDisruption(name string) (*disruption, error) {
	if cached, found := informer.Cached[disruption](o.disruptions, name); found {
		return cached, nil
	}
	d, err := apiclient.Get[disruption](o.client, disruptionPath(name))
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil, nil
	}
	return d, err
}

// releaseHold removes spec.hold so the turn continues to the reboot. A
// merge patch with a null value removes only that field, and leaves
// the rest of the spec, which machine-operator also writes. For a
// capsule update, the same patch sets spec.keepBootNext, so the reboot
// cannot happen between the two changes.
func (o *operator) releaseHold(name string, keepBootNext bool) error {
	patch := `{"spec":{"hold":null}}`
	if keepBootNext {
		patch = `{"spec":{"hold":null,"keepBootNext":true}}`
	}
	return o.client.Request(http.MethodPatch, disruptionPath(name), "application/merge-patch+json", []byte(patch), nil)
}

// deleteDisruption deletes a Disruption. A 404 means it is already gone.
func (o *operator) deleteDisruption(name string) error {
	err := o.client.RequestJSON(http.MethodDelete, disruptionPath(name), nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// disruptionMark is a string that changes whenever a Disruption changes
// in a way that needs a new pass: its spec (through the generation),
// the Held and Complete conditions, and the start of its deletion.
func disruptionMark(item disruption) string {
	return fmt.Sprint(item.Metadata.UID, item.Metadata.Generation, item.Metadata.DeletionTimestamp, item.held(), item.complete())
}
