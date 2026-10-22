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

// approval answers an update rule's Approved condition and the
// machines the approval waits for. A rule with no version approves
// nothing, and answers false: it has no Approved condition.
//
// A machine is pending when a catalog offers one of its devices the
// version in the pinned cabinet, and installed when a device runs the
// version and none is pending. A machine with two devices of the
// component, one updated and one not, is pending.
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
		message := fmt.Sprintf("a catalog offers %s in the pinned cabinet to %s", u.Version, machineList(pending))
		if len(installed) > 0 {
			message += fmt.Sprintf(", and %s already runs it", machineList(installed))
		}
		return approved(conditions.True, reasonOffered, message), pending, true
	case len(installed) > 0:
		return approved(conditions.True, reasonInstalled, fmt.Sprintf(
			"%s runs %s, and no catalog offers it to another machine the approval covers", machineList(installed), u.Version)), nil, true
	case len(differs) > 0:
		return approved(conditions.False, reasonCabinetDiffers, fmt.Sprintf(
			"a catalog offers %s to %s in a cabinet whose SHA-256 is not %s", u.Version, machineList(differs), u.Cabinet.SHA256)), nil, true
	}
	return approved(conditions.False, reasonNotOffered, fmt.Sprintf(
		"no catalog offers %s to a machine the approval covers", u.Version)), nil, true
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

// approveAll answers every update rule: its approval, and the
// rehearsals and the applies of each machine the approval waits for
// (rehearsal.go and apply.go). It runs after the scans' entries are
// written, so it reads the entries of this pass. It answers the
// earliest deadline of a rehearsal or an apply in progress.
func (o *operator) approveAll(rules []firmware, w observed, catalogs map[string]*catalog, now time.Time) (time.Time, error) {
	var next time.Time
	var problems []error
	for index := range rules {
		f := &rules[index]
		if f.Spec.Update == nil || f.Metadata.DeletionTimestamp != "" {
			continue
		}
		due, err := o.answerUpdate(f, w, catalogs, now)
		next = earliest(next, due)
		problems = append(problems, err)
	}
	return next, errors.Join(problems...)
}

// answerUpdate follows one rule's rehearsals and applies, starts the
// ones its pending machines need, and writes its approval, its
// rehearsals, its applies, and their conditions in one status write.
// Then it posts the transitions, brings the requests for a reboot turn
// in line with the records it wrote, and deletes each finished
// rehearsal and apply pod.
func (o *operator) answerUpdate(f *firmware, w observed, catalogs map[string]*catalog, now time.Time) (time.Time, error) {
	ends, busy, due, err := o.rehearsalEnds(f, w.rehearsals[f.Metadata.Name], now)
	problems := []error{err}
	steps, finished, applyDue, err := o.applyEnds(f, w.applies[f.Metadata.Name], now)
	due, problems = earliest(due, applyDue), append(problems, err)
	var starts []rehearsalAttempt
	if c, pending, holds := approval(f.Spec.Update, f.Spec.Machines, f.Status.Machines, w.machines); holds && c.Status == conditions.True {
		started, err := o.startRehearsals(f, pending, w, catalogs, busy)
		starts, problems = started, append(problems, err)
		advanced, err := o.advanceApplies(f, pending, w, catalogs)
		steps, problems = append(steps, advanced...), append(problems, err)
	}

	var approved, rehearsals, applies condition
	var approvedMoved, rehearsedMoved, appliedMoved bool
	var notices, applyNotices []notice
	var records []applyRecord
	written, err := informer.SettleStatus[firmware](o.client, o.firmwares.Versions, firmwarePath(f.Metadata.Name), f, func(held *firmware) bool {
		next := held.Status
		next.Conditions = slices.Clone(held.Status.Conditions)
		var pending map[string]bool
		var holds bool
		approved, pending, holds = approval(held.Spec.Update, held.Spec.Machines, held.Status.Machines, w.machines)
		next.Counts = counts{UpdateCounts: updateCounts(held.Status.Machines, len(pending))}
		next.Rehearsals, notices = composeRehearsals(held, pending, ends, starts, now)
		next.Applies, applyNotices = composeApplies(held, pending, steps, now)
		records = next.Applies
		approvedMoved = holdCondition(&next.Conditions, approvedCondition, approved, holds, held.Metadata.Generation, now)
		var rehearsing, applying bool
		rehearsals, rehearsing = rehearsed(next.Rehearsals, pending)
		rehearsedMoved = holdCondition(&next.Conditions, rehearsedCondition, rehearsals, rehearsing, held.Metadata.Generation, now)
		applies, applying = applied(next.Applies, pending)
		appliedMoved = holdCondition(&next.Conditions, appliedCondition, applies, applying, held.Metadata.Generation, now)
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
	// The requests and the pods follow the records only once the records
	// are written, so an operator that stops here finds each result in
	// the status and does the rest again.
	problems = append(problems, o.settleTurns(f, records, w.disruptions[f.Metadata.Name], w.fleet))
	for _, end := range ends {
		if end.rehearsal != nil {
			problems = append(problems, o.remove(*end.rehearsal))
		}
	}
	for _, p := range finished {
		problems = append(problems, o.remove(p))
	}
	return due, errors.Join(problems...)
}

// holdCondition sets c in a list of conditions when it holds, and
// removes the condition of its type when it does not. It reports a
// transition.
func holdCondition(list *[]condition, kind string, c condition, holds bool, generation int64, now time.Time) bool {
	if !holds {
		*list = slices.DeleteFunc(*list, func(held condition) bool { return held.Type == kind })
		return false
	}
	c.ObservedGeneration = generation
	c.LastTransitionTime = now.UTC().Truncate(time.Second)
	return conditions.Set(list, c)
}
