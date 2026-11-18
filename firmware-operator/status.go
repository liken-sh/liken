package main

// Firmware.status.devices has one entry for each USB port where the
// rule matched a device. Each new appearance of a device in a port
// replaces the port's entry, so a device that is power-cycled every
// night does not grow the list. The entry and the upload pod's
// annotations are the only record of an upload attempt.

import (
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// plan lists the status changes that one reconcile pass computes for
// one rule.
type plan struct {
	valid     condition
	results   []attempt
	conflicts []attempt
	starts    []attempt
}

// attempt is one device entry that a plan writes.
type attempt struct {
	appearance
	device     string
	generation int64
	result     string
	message    string
}

// notice is one Event that the operator posts after the status write
// succeeds.
type notice struct {
	warning bool
	reason  string
	message string
}

// sameAttempt reports whether an entry is for this appearance of the
// device and this generation of the rule.
func sameAttempt(e deviceEntry, a appearance, generation int64) bool {
	return e.DeviceNumber == a.deviceNumber && e.Generation == generation
}

// composeStatus applies a plan to a rule's current status f, and
// returns the new status, the Events to post, and whether the Valid
// condition transitioned. After a 409 conflict, the caller passes a
// newer f than the one the plan was made from, so each change is
// checked again against f's entry for the port.
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
		// A result replaces only its own attempt's entry, and only while
		// that entry is Running. It is also written to a port with no
		// entry, which happens when the operator restarted after it
		// created the pod and before it wrote the entry.
		e, found := entries[a.port()]
		if found && !(sameAttempt(e, a.appearance, a.generation) && e.Result == resultRunning) {
			continue
		}
		write(a)
		notices = append(notices, resultNotice(a))
	}
	for _, a := range p.conflicts {
		e, found := entries[a.port()]
		if found && e.Result == resultConflict && sameAttempt(e, a.appearance, a.generation) && e.Message == a.message {
			continue
		}
		write(a)
		if !found || e.Result != resultConflict || e.DeviceNumber != a.deviceNumber {
			notices = append(notices, notice{warning: true, reason: reasonConflict, message: a.device + " on " + a.machine + ": " + a.message})
		}
	}
	for _, a := range p.starts {
		e, found := entries[a.port()]
		if found && sameAttempt(e, a.appearance, a.generation) && e.Result != resultConflict {
			continue
		}
		write(a)
		notices = append(notices, notice{reason: reasonUploadStarted, message: a.device + " on " + a.machine + ": " + a.message})
	}

	next := firmwareStatus{ObservedGeneration: f.Metadata.Generation, Counts: counts{UploadCounts: &UploadCounts{}}}
	for _, e := range entries {
		next.Devices = append(next.Devices, e)
		next.Counts.add(e.Result)
	}
	slices.SortFunc(next.Devices, func(a, b deviceEntry) int {
		return strings.Compare(a.Machine+"\x00"+a.Address, b.Machine+"\x00"+b.Address)
	})
	// conditions.Set edits the slice in place, so it works on a clone
	// and f keeps the status read from the API server. Set moves
	// lastTransitionTime only when the status changes, so a pass that
	// changes nothing composes a status equal to f's.
	next.Conditions = slices.Clone(f.Status.Conditions)
	validity := p.valid
	validity.ObservedGeneration = f.Metadata.Generation
	validity.LastTransitionTime = now.UTC().Truncate(time.Second)
	transitioned := conditions.Set(&next.Conditions, validity)
	return next, notices, transitioned
}

func (c *UploadCounts) add(result string) {
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
		return notice{reason: reasonLoaded, message: "Loaded " + about}
	}
	return notice{warning: true, reason: reasonUploadFailed, message: "The upload failed on " + about}
}

// statusChanged reports whether the composed status differs from the
// status read from the API server. The operator writes only a status
// that differs.
func statusChanged(current, next firmwareStatus) bool {
	return !reflect.DeepEqual(current, next)
}
