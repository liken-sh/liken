package main

// faults.go records the faults that a volume reports until a success
// clears them, and posts the Events for the faults that a stage finds.
//
// A fault posts its Event only on the first failure after a success, so
// a remote that is down for an hour posts one Event, not one for each
// retry. Each kind of fault has its own field, so a recorded fault of
// one kind does not hide the first failure of another kind.

import (
	"fmt"

	kevents "github.com/liken-sh/liken/kubernetes/events"
)

// reportFetchFailed records a failed fetch and returns true when it is
// the first since the last successful fetch. The caller posts an Event
// only then.
func (v *volume) reportFetchFailed(message string) bool {
	return v.setFault(&v.fetchFault, message)
}

// reportPushFailed records a failed push and returns true when it is
// the first since the last successful push.
func (v *volume) reportPushFailed(message string) bool {
	return v.setFault(&v.pushFault, message)
}

// reportUpstreamMoved records that upstream moved while the tree had
// uncommitted writes, and returns true when no such fault was recorded
// before.
func (v *volume) reportUpstreamMoved(message string) bool {
	return v.setFault(&v.upstreamMoved, message)
}

func (v *volume) setFault(field *string, message string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	first := *field == ""
	*field = message
	return first
}

// fault returns the message of the fault that the volume reports. The
// caller holds the lock.
func (v *volume) fault() string {
	switch {
	case v.fetchFault != "":
		return v.fetchFault
	case v.pushFault != "":
		return v.pushFault
	}
	return v.upstreamMoved
}

// stageFaults returns the faults that the stage found: upstream moved,
// and a ref that the remote deleted. The stage runs before the driver
// has found the pod or the claim, so it has no object to post an Event
// on. The arming posts these Events when it finds the claim.
func (v *volume) stageFaults() []stageFault {
	v.mu.Lock()
	defer v.mu.Unlock()
	found := []stageFault{}
	if v.upstreamMoved != "" {
		found = append(found, stageFault{reasonUpstreamMoved, v.upstreamMoved})
	}
	if v.refDeleted {
		found = append(found, stageFault{reasonRefDeleted,
			fmt.Sprintf("The remote has no ref %s, so the driver pushes nothing until the ref exists again",
				v.attributes.ref)})
	}
	return found
}

// stageFault is the reason and message of the Event for one fault
// that the stage found.
type stageFault struct {
	reason, message string
}

// postStageFaults posts an Event for each fault that the stage found,
// on the claim, and on the pod when one is published.
func (n *node) postStageFaults(staged *volume, claim claimReference) {
	for _, found := range staged.stageFaults() {
		n.report(staged, claim, kevents.TypeWarning, found.reason, found.message)
	}
}
