package main

// A person approves one release of a catalog component by setting
// spec.update.version on its Firmware and pinning the release's
// cabinet by its SHA-256. The operator answers the approval with the
// Approved condition and status.counts.pending. It reads only the spec,
// the entries that discovery writes, and the labels of each Machine, so
// an approval needs no scan and no download. Installing the release is
// not this file's work: the approval only records which machines wait
// for it.
//
// The pin is what a person approves, not the version string alone. A
// catalog that serves the version in another cabinet, or that withdraws
// it, moves Approved to False at the next scan, and the transition's
// Warning tells the person.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// approvedCondition is the condition of an update rule with a version.
const approvedCondition = "Approved"

// The reasons of Approved.
const (
	reasonOffered         = "Offered"
	reasonInstalled       = "Installed"
	reasonCabinetDiffers  = "CabinetDiffers"
	reasonNotOffered      = "NotOffered"
	reasonMachinesInvalid = "MachinesInvalid"
)

// approval answers an update rule's Approved condition and the number
// of machines the approval waits for. A rule with no version approves
// nothing, and answers false: it has no Approved condition.
//
// A machine is pending when a catalog offers one of its devices the
// version in the pinned cabinet, and installed when a device runs the
// version and none is pending. A machine with two devices of the
// component, one updated and one not, is pending.
func approval(u *update, selector *metav1.LabelSelector, entries []machineEntry, machineLabels map[string]map[string]string) (condition, int, bool) {
	if u == nil || u.Version == "" || u.Cabinet == nil {
		return condition{}, 0, false
	}
	approved := func(status conditions.Status, reason, message string) condition {
		return condition{Type: approvedCondition, Status: status, Reason: reason, Message: message}
	}
	covers, err := filterMachines(selector, machineLabels)
	if err != nil {
		return approved(conditions.False, reasonMachinesInvalid, err.Error()), 0, true
	}
	pending, installed, differs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range entries {
		if !covers(e.Machine) {
			continue
		}
		if e.Version == u.Version {
			installed[e.Machine] = true
		}
		for _, r := range e.Releases {
			switch {
			case r.Version != u.Version:
			case r.Cabinet != nil && r.Cabinet.SHA256 == u.Cabinet.SHA256:
				pending[e.Machine] = true
			default:
				differs[e.Machine] = true
			}
		}
	}
	for machine := range pending {
		delete(installed, machine)
	}
	switch {
	case len(pending) > 0:
		message := fmt.Sprintf("a catalog offers %s in the pinned cabinet to %s", u.Version, machineList(pending))
		if len(installed) > 0 {
			message += fmt.Sprintf(", and %s already runs it", machineList(installed))
		}
		return approved(conditions.True, reasonOffered, message), len(pending), true
	case len(installed) > 0:
		return approved(conditions.True, reasonInstalled, fmt.Sprintf(
			"%s runs %s, and no catalog offers it to another machine the approval covers", machineList(installed), u.Version)), 0, true
	case len(differs) > 0:
		return approved(conditions.False, reasonCabinetDiffers, fmt.Sprintf(
			"a catalog offers %s to %s in a cabinet whose SHA-256 is not %s", u.Version, machineList(differs), u.Cabinet.SHA256)), 0, true
	}
	return approved(conditions.False, reasonNotOffered, fmt.Sprintf(
		"no catalog offers %s to a machine the approval covers", u.Version)), 0, true
}

// machineList names a set of machines in a fixed order.
func machineList(machines map[string]bool) string {
	names := make([]string, 0, len(machines))
	for name := range machines {
		names = append(names, name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// approveAll writes the approval of every update rule. It runs after the
// scans' entries are written, so it reads the entries of this pass.
func (o *operator) approveAll(rules []firmware, machineLabels map[string]map[string]string, now time.Time) error {
	var problems []error
	for index := range rules {
		f := &rules[index]
		if f.Spec.Update == nil || f.Metadata.DeletionTimestamp != "" {
			continue
		}
		problems = append(problems, o.settleApproval(f, machineLabels, now))
	}
	return errors.Join(problems...)
}

// settleApproval writes one rule's Approved condition and pending
// count, and posts the condition's transition.
func (o *operator) settleApproval(f *firmware, machineLabels map[string]map[string]string, now time.Time) error {
	var c condition
	var transitioned bool
	written, err := informer.SettleStatus[firmware](o.client, o.firmwares.Versions, firmwarePath(f.Metadata.Name), f, func(held *firmware) bool {
		next := held.Status
		next.Conditions = slices.Clone(held.Status.Conditions)
		var pending int
		var holds bool
		c, pending, holds = approval(held.Spec.Update, held.Spec.Machines, held.Status.Machines, machineLabels)
		next.Counts = counts{UpdateCounts: updateCounts(held.Status.Machines, pending)}
		transitioned = false
		if holds {
			c.ObservedGeneration = held.Metadata.Generation
			c.LastTransitionTime = now.UTC().Truncate(time.Second)
			transitioned = conditions.Set(&next.Conditions, c)
		} else {
			next.Conditions = slices.DeleteFunc(next.Conditions, func(held condition) bool { return held.Type == approvedCondition })
		}
		if len(next.Conditions) == 0 {
			next.Conditions = nil
		}
		if !statusChanged(held.Status, next) {
			return false
		}
		held.APIVersion, held.Kind, held.Status = firmwareAPIVersion, firmwareKind, next
		return true
	})
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	if written {
		o.post(f, c, nil, transitioned)
	}
	return err
}
