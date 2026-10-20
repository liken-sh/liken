package main

// The status answers what a rule did on each port where it matched a
// device. One entry describes the latest appearance in its port, so a
// camera that is power-cycled every night keeps one entry, and the list
// grows only with the number of ports. The entry and the upload pod's
// annotations are the operator's whole record of an attempt.

import (
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// plan is what one pass decided for one rule, before its status write.
type plan struct {
	valid     condition
	results   []attempt
	conflicts []attempt
	starts    []attempt
}

// attempt is one entry the plan writes: an appearance, the rule's
// generation, and the result with its message.
type attempt struct {
	appearance
	device     string
	generation int64
	result     string
	message    string
}

// notice is one Event that a status write posts after it lands.
type notice struct {
	warning bool
	reason  string
	message string
}

// sameAttempt reports whether an entry records the attempt for an
// appearance at a generation of the rule.
func sameAttempt(e deviceEntry, a appearance, generation int64) bool {
	return e.DeviceNumber == a.deviceNumber && e.Generation == generation
}

// composeStatus answers the status that a plan gives a rule, the
// Events it posts, and whether Valid transitioned. The plan was made
// from a copy that can be older than the one held here, after a 409, so
// each change is checked against the held entry again.
func composeStatus(f *firmware, p plan, now time.Time) (firmwareStatus, []notice, bool) {
	stamp := now.UTC().Truncate(time.Second).Format(time.RFC3339)
	entries := map[port]deviceEntry{}
	for _, e := range f.Status.Devices {
		entries[port{machine: e.Machine, address: e.Address}] = e
	}
	var notices []notice
	write := func(a attempt) {
		entries[a.port()] = deviceEntry{
			Machine: a.machine, Device: a.device, Address: a.address, DeviceNumber: a.deviceNumber,
			Generation: a.generation, Result: a.result, Message: a.message, Time: stamp,
		}
	}

	for _, a := range p.results {
		// A result belongs to the entry of its own attempt while that
		// entry runs, or to a port with no entry, after a restart that
		// came between the pod's create and the entry's write.
		e, held := entries[a.port()]
		if held && !(sameAttempt(e, a.appearance, a.generation) && e.Result == resultRunning) {
			continue
		}
		write(a)
		notices = append(notices, resultNotice(a))
	}
	for _, a := range p.conflicts {
		e, held := entries[a.port()]
		if held && e.Result == resultConflict && sameAttempt(e, a.appearance, a.generation) && e.Message == a.message {
			continue
		}
		write(a)
		if !held || e.Result != resultConflict || e.DeviceNumber != a.deviceNumber {
			notices = append(notices, notice{warning: true, reason: reasonConflict, message: a.device + " on " + a.machine + ": " + a.message})
		}
	}
	for _, a := range p.starts {
		e, held := entries[a.port()]
		if held && sameAttempt(e, a.appearance, a.generation) && e.Result != resultConflict {
			continue
		}
		write(a)
		notices = append(notices, notice{reason: reasonUploadStarted, message: a.device + " on " + a.machine + ": " + a.message})
	}

	next := firmwareStatus{ObservedGeneration: f.Metadata.Generation}
	for _, e := range entries {
		next.Devices = append(next.Devices, e)
		next.Counts.add(e.Result)
	}
	slices.SortFunc(next.Devices, func(a, b deviceEntry) int {
		return strings.Compare(a.Machine+"\x00"+a.Address, b.Machine+"\x00"+b.Address)
	})
	// The list is cloned, because the held status is the API server's
	// copy until the write lands. Set moves lastTransitionTime only when
	// the status changes, so a write that changes nothing else changes
	// nothing.
	next.Conditions = slices.Clone(f.Status.Conditions)
	validity := p.valid
	validity.ObservedGeneration = f.Metadata.Generation
	validity.LastTransitionTime = now.UTC().Truncate(time.Second)
	transitioned := conditions.Set(&next.Conditions, validity)
	return next, notices, transitioned
}

func (c *counts) add(result string) {
	switch result {
	case resultRunning:
		c.Running++
	case resultLoaded:
		c.Loaded++
	case resultFailed:
		c.Failed++
	case resultConflict:
		c.Conflict++
	}
}

func resultNotice(a attempt) notice {
	about := a.device + " on " + a.machine + ": " + a.message
	if a.result == resultLoaded {
		return notice{reason: reasonLoaded, message: "loaded " + about}
	}
	return notice{warning: true, reason: reasonUploadFailed, message: "the upload failed on " + about}
}

// statusChanged reports whether a composed status differs from the one
// the API server holds, so the operator writes only a change.
func statusChanged(held, next firmwareStatus) bool {
	return !reflect.DeepEqual(held, next)
}
