package main

// faults.go keeps the three faults a volume reports until a success
// ends them, and posts the Events of the faults a stage finds.
//
// A fault posts its Event once, at the first failure after a success,
// so a remote that is down for an hour posts one Event and not one
// for each attempt. Each kind of fault has a field of its own: a
// volume whose stage found that upstream moved still posts the first
// push that fails after it.

import (
	"fmt"

	kevents "github.com/liken-sh/liken/kubernetes/events"
)

// reportFetchFailed records a failed fetch and reports whether it is
// the first since the last fetch that worked, which is when an Event is
// worth posting.
func (v *volume) reportFetchFailed(message string) bool {
	return v.setFault(&v.fetchFault, message)
}

// reportPushFailed records a failed push and reports whether it is the
// first since the last push that worked.
func (v *volume) reportPushFailed(message string) bool {
	return v.setFault(&v.pushFault, message)
}

// reportUpstreamMoved records that upstream moved past a tree the
// driver could not rewrite, and reports whether it is new.
func (v *volume) reportUpstreamMoved(message string) bool {
	return v.setFault(&v.upstreamMoved, message)
}

// setFault records the message in one fault's field and reports
// whether that field was empty.
func (v *volume) setFault(field *string, message string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	first := *field == ""
	*field = message
	return first
}

// fault is the fault the volume reports first: a failed fetch, then a
// failed push, then an upstream that moved. The caller holds the lock.
func (v *volume) fault() string {
	switch {
	case v.fetchFault != "":
		return v.fetchFault
	case v.pushFault != "":
		return v.pushFault
	}
	return v.upstreamMoved
}

// stageFaults answers the Events that the faults a stage found are
// worth: an upstream that moved, a ref the remote deleted, and the work
// tree of another volume that holds unpushed commits. A stage finds
// them before the driver has found the pod or the claim, so the arming
// posts them once it finds the claim.
func (v *volume) stageFaults() []stageFault {
	v.mu.Lock()
	defer v.mu.Unlock()
	found := []stageFault{}
	if v.upstreamMoved != "" {
		found = append(found, stageFault{reasonUpstreamMoved, v.upstreamMoved})
	}
	if v.refDeleted {
		found = append(found, stageFault{reasonRefDeleted,
			fmt.Sprintf("the remote holds no %s, so the driver pushes nothing until the ref exists again",
				v.attributes.ref)})
	}
	return found
}

// stageFault is the reason and the message of one Event a stage
// found.
type stageFault struct {
	reason, message string
}

// tellStageFaults posts the Events of the faults the stage found, on
// the claim the arming found and on the pod when one is published.
func (n *node) tellStageFaults(staged *volume, claim claimReference) {
	for _, found := range staged.stageFaults() {
		n.report(staged, claim, kevents.TypeWarning, found.reason, found.message)
	}
}
