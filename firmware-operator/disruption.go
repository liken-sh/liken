package main

// A database update takes effect at the machine's next boot, and the
// write must happen while no workload runs there. liken grants both
// through a Disruption (liken.sh/v1alpha1), its request for one
// machine's reboot turn. The operator files one with spec.hold, so the
// turn stops after the drain: machine-operator then sets Held to True,
// the apply pod writes on the drained machine, and the operator
// removes spec.hold, so the turn goes on to the reboot. A refused or
// failed write deletes the request instead, and the machine leaves its
// turn without a reboot it does not need.
//
// The operator declares its own struct for the fields it reads and
// writes, the way it does for a Pod, and links nothing from liken.
// Each request carries the label of its Firmware, which selects the
// requests the operator watches, and names the Firmware as its owner,
// so a deleted Firmware deletes its requests.

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

// The requester the operator names in each request, and the label that
// names the request's Firmware.
const (
	requester       = "firmware.liken.sh/firmware-operator"
	disruptionLabel = "firmware.liken.sh/firmware"
)

// The conditions that machine-operator writes on a request.
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

// disruptionSpec leaves out hash and approvedHash. machine-operator
// fills hash with the running boot, and only a person may set
// approvedHash, which an admission policy enforces.
type disruptionSpec struct {
	Machine   string `json:"machine"`
	Requester string `json:"requester"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason,omitempty"`
	Hold      bool   `json:"hold,omitempty"`
	// KeepBootNext asks liken's init to leave BootNext as fwupd set it
	// at the turn's reboot (liken's milestone 33).
	KeepBootNext bool `json:"keepBootNext,omitempty"`
}

type disruptionStatus struct {
	Conditions []condition `json:"conditions,omitempty"`
}

// held reports whether machine-operator drained the machine and waits
// for the operator to remove spec.hold.
func (d *disruption) held() bool { return d.conditionTrue(heldCondition) }

// complete reports whether the machine runs a boot after the one the
// request named, so the turn is over.
func (d *disruption) complete() bool { return d.conditionTrue(completeCondition) }

func (d *disruption) conditionTrue(kind string) bool {
	c, found := conditions.Find(d.Status.Conditions, kind)
	return found && c.Status == conditions.True
}

// disruptionName answers the one request of a Firmware for a machine,
// so a second create of it answers 409. The name is a DNS subdomain of
// at most 253 characters.
func disruptionName(firmware, machine string) string {
	name := machine + ".firmware-" + firmware
	if len(name) > 253 {
		name = name[:253]
	}
	return strings.TrimRight(name, "-.")
}

// buildDisruption answers the request for one machine's turn. It names
// no hash, so machine-operator names the running boot, and the request
// is spent by the reboot that ends the turn.
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

// fileDisruption creates a request. A request that exists under the
// name answers 409, which is the same result.
func (o *operator) fileDisruption(d disruption) error {
	err := o.client.RequestJSON(http.MethodPost, disruptionsPath, mustJSON(d), nil)
	if errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	return err
}

// readDisruption answers a request by name, or nil when it does not
// exist. It reads the watch's store, and the API server for a request
// the store does not hold, because the store can lag a request that the
// operator created a moment ago, and a request reported gone in error
// would fail an apply that waits for its turn.
func (o *operator) readDisruption(name string) (*disruption, error) {
	if held, found := informer.Cached[disruption](o.disruptions, name); found {
		return held, nil
	}
	held, err := apiclient.Get[disruption](o.client, disruptionPath(name))
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil, nil
	}
	return held, err
}

// releaseHold removes spec.hold, so the turn goes on to the reboot. A
// merge patch with a null removes the field, and leaves the rest of
// the spec, which machine-operator also writes, as it is. A capsule's
// release sets spec.keepBootNext in the same patch, so no reboot can
// run between the two.
func (o *operator) releaseHold(name string, keepBootNext bool) error {
	patch := `{"spec":{"hold":null}}`
	if keepBootNext {
		patch = `{"spec":{"hold":null,"keepBootNext":true}}`
	}
	return o.client.Request(http.MethodPatch, disruptionPath(name), "application/merge-patch+json", []byte(patch), nil)
}

// withdraw deletes a request. A 404 means it is already gone.
func (o *operator) withdraw(name string) error {
	err := o.client.RequestJSON(http.MethodDelete, disruptionPath(name), nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// disruptionMark is the part of a request whose change needs a pass:
// its spec, through the generation, the conditions that machine-operator
// sets, and the start of its deletion.
func disruptionMark(item disruption) string {
	return fmt.Sprint(item.Metadata.UID, item.Metadata.Generation, item.Metadata.DeletionTimestamp, item.held(), item.complete())
}
