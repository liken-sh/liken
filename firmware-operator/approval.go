package main

// A person approves one release of a catalog component by setting
// spec.update.version on its Firmware and pinning the release's
// cabinet by its SHA-256. The operator reports the approval in the
// Approved condition and in status.counts.pending. It reads only the
// spec, the entries that discovery writes, and the labels of each
// Machine, so an approval needs no scan and no download. The approval
// records which machines are waiting for the release, and
// reconcileUpdate starts their rehearsals and applies.
//
// The approval covers the pinned cabinet, not only the version string.
// When a catalog serves the version in a different cabinet, or
// withdraws it, Approved becomes False at the next scan, and the
// Warning `Event` of the transition tells the person.

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

// approvedCondition is the type of the condition that a Firmware has
// when spec.update names a version and pins a cabinet.
const approvedCondition = "Approved"

// The reasons of the Approved condition.
const (
	reasonOffered         = "Offered"
	reasonInstalled       = "Installed"
	reasonCabinetDiffers  = "CabinetDiffers"
	reasonNotOffered      = "NotOffered"
	reasonMachinesInvalid = "MachinesInvalid"
)

// approval returns a Firmware's Approved condition and the set of
// machines that are waiting for the approved release. The final bool is
// false when there is nothing to approve, because spec.update has no
// version or no pinned cabinet. The Firmware then has no Approved
// condition.
//
// A machine is pending when a catalog offers one of its devices the
// approved version in the pinned cabinet. A machine is installed when a
// device runs the approved version and no device of the machine is
// pending, so a machine with two devices of the component, one updated
// and one not, is pending.
func approval(u *update, selector *metav1.LabelSelector, entries []machineEntry, machineLabels map[string]map[string]string) (condition, map[string]bool, bool) {
	if u == nil || u.Version == "" || u.Cabinet == nil {
		return condition{}, nil, false
	}
	approved := func(status conditions.Status, reason, message string) condition {
		return condition{Type: approvedCondition, Status: status, Reason: reason, Message: message}
	}
	covers, err := filterMachines(selector, machineLabels)
	if err != nil {
		return approved(conditions.False, reasonMachinesInvalid, err.Error()), nil, true
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
		message := fmt.Sprintf("A catalog offers %s in the pinned cabinet to %s", u.Version, machineList(pending))
		if len(installed) > 0 {
			message += fmt.Sprintf(", and %s already runs it", machineList(installed))
		}
		return approved(conditions.True, reasonOffered, message), pending, true
	case len(installed) > 0:
		return approved(conditions.True, reasonInstalled, fmt.Sprintf(
			"%s runs %s, and no catalog offers it to another machine that the approval covers", machineList(installed), u.Version)), nil, true
	case len(differs) > 0:
		return approved(conditions.False, reasonCabinetDiffers, fmt.Sprintf(
			"A catalog offers %s to %s in a cabinet whose SHA-256 is not %s", u.Version, machineList(differs), u.Cabinet.SHA256)), nil, true
	}
	return approved(conditions.False, reasonNotOffered, fmt.Sprintf(
		"No catalog offers %s to a machine that the approval covers", u.Version)), nil, true
}

func machineList(machines map[string]bool) string {
	names := make([]string, 0, len(machines))
	for name := range machines {
		names = append(names, name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// approveAll handles every Firmware that has spec.update: its approval,
// and the rehearsals and applies of each machine that the approval
// waits for (rehearsal.go and apply.go). It runs after the entries of
// the scans are written, so it reads the entries of this pass. It
// returns the earliest deadline of a rehearsal or an apply in progress.
func (o *operator) approveAll(rules []firmware, w observed, catalogs map[string]*catalog, now time.Time) (time.Time, error) {
	var next time.Time
	var problems []error
	for index := range rules {
		f := &rules[index]
		if f.Spec.Update == nil || f.Metadata.DeletionTimestamp != "" {
			continue
		}
		due, err := o.reconcileUpdate(f, w, catalogs, now)
		next = earliest(next, due)
		problems = append(problems, err)
	}
	return next, errors.Join(problems...)
}

// reconcileUpdate follows one Firmware's rehearsals and applies, starts
// the ones that its pending machines need, and writes the approval, the
// rehearsals, the applies, and their conditions in one status write.
// After the write it posts the `Event`s of the transitions, brings the
// Disruptions and the waiting apply pods in line with the records it
// wrote, and deletes each finished rehearsal and apply pod.
func (o *operator) reconcileUpdate(f *firmware, w observed, catalogs map[string]*catalog, now time.Time) (time.Time, error) {
	ends, busy, due, err := o.rehearsalEnds(f, w.rehearsals[f.Metadata.Name], now)
	problems := []error{err}
	steps, finished, applyDue, err := o.applyEnds(f, w.applies[f.Metadata.Name], now)
	due, problems = earliest(due, applyDue), append(problems, err)
	var starts []rehearsalAttempt
	if c, pending, present := approval(f.Spec.Update, f.Spec.Machines, f.Status.Machines, w.machines); present && c.Status == conditions.True {
		started, err := o.startRehearsals(f, pending, w, catalogs, busy)
		starts, problems = started, append(problems, err)
		advanced, err := o.advanceApplies(f, pending, w, catalogs, now)
		steps, problems = append(steps, advanced...), append(problems, err)
	}

	var approved, rehearsals, applies condition
	var approvedMoved, rehearsedMoved, appliedMoved bool
	var notices, applyNotices []notice
	var records []applyRecord
	written, err := informer.SettleStatus[firmware](o.client, o.firmwares.Versions, firmwarePath(f.Metadata.Name), f, func(current *firmware) bool {
		next := current.Status
		next.Conditions = slices.Clone(current.Status.Conditions)
		var pending map[string]bool
		var present bool
		approved, pending, present = approval(current.Spec.Update, current.Spec.Machines, current.Status.Machines, w.machines)
		next.Counts = counts{UpdateCounts: updateCounts(current.Status.Machines, len(pending))}
		next.Rehearsals, notices = composeRehearsals(current, pending, ends, starts, now)
		next.Applies, applyNotices = composeApplies(current, pending, steps, now)
		records = next.Applies
		approvedMoved = setOrClearCondition(&next.Conditions, approvedCondition, approved, present, current.Metadata.Generation, now)
		var rehearsing, applying bool
		rehearsals, rehearsing = rehearsed(next.Rehearsals, pending)
		rehearsedMoved = setOrClearCondition(&next.Conditions, rehearsedCondition, rehearsals, rehearsing, current.Metadata.Generation, now)
		applies, applying = applied(next.Applies, pending)
		appliedMoved = setOrClearCondition(&next.Conditions, appliedCondition, applies, applying, current.Metadata.Generation, now)
		if len(next.Conditions) == 0 {
			next.Conditions = nil
		}
		if !statusChanged(current.Status, next) {
			return false
		}
		current.APIVersion, current.Kind, current.Status = firmwareAPIVersion, firmwareKind, next
		return true
	})
	if errors.Is(err, apiclient.ErrNotFound) {
		return due, errors.Join(problems...)
	}
	if err != nil {
		return due, errors.Join(append(problems, err)...)
	}
	if written {
		o.post(f, approved, nil, approvedMoved)
		o.post(f, rehearsals, notices, rehearsedMoved)
		o.post(f, applies, applyNotices, appliedMoved)
	}
	// The requests and the pods change only after the records are
	// written. When the operator stops here, the next pass finds each
	// result in the status and finishes the rest.
	problems = append(problems, o.reconcileDisruptions(f, records, w.disruptions[f.Metadata.Name], w.fleet))
	problems = append(problems, o.retireApplyPods(records, w.applies[f.Metadata.Name]))
	due = earliest(due, writeDeadline(records, now))
	for _, end := range ends {
		if end.pod != nil {
			problems = append(problems, o.remove(*end.pod))
		}
	}
	for _, p := range finished {
		problems = append(problems, o.remove(p))
	}
	return due, errors.Join(problems...)
}

// setOrClearCondition sets c in a list of conditions when present is
// true, and removes the condition of type kind when it is false. It
// reports whether a condition transitioned.
func setOrClearCondition(list *[]condition, kind string, c condition, present bool, generation int64, now time.Time) bool {
	if !present {
		*list = slices.DeleteFunc(*list, func(existing condition) bool { return existing.Type == kind })
		return false
	}
	c.ObservedGeneration = generation
	c.LastTransitionTime = now.UTC().Truncate(time.Second)
	return conditions.Set(list, c)
}
