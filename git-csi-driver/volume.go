package main

// volume.go holds one volume this node has and everything the driver
// reports about it.

import (
	"fmt"
	"sync"
)

// volume is one volume this node holds: the commit its tree stands on,
// the trouble since the last good fetch, what the pod wrote and the
// driver has not committed, and the claim and class that say whether it
// may commit.
type volume struct {
	id          string
	attributes  *attributes
	credentials *credentials
	directory   string
	tree        string
	target      string
	// A writeable volume has a git directory and a staging path. A read-
	// only volume has neither.
	work      *workTree
	staging   string
	writeable bool

	mu      sync.Mutex
	commit  string
	trouble string
	// The pod the kubelet named at publish. A stage call names no pod.
	pod podReference
	// What the pod wrote and the driver has not committed, and the claim
	// and class that say whether it may.
	pending []change
	claim   claimReference
	class   string
	armed   bool
}

// setPod records the pod a publish named, so an Event from a loop that
// started at stage reaches it.
func (v *volume) setPod(pod podReference) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.pod = pod
}

func (v *volume) podRef() podReference {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.pod
}

// reportPending records what the last scan found and reports whether
// the set went from empty to not empty, which is the one moment an
// Event is worth posting.
func (v *volume) reportPending(found []change) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	first := len(v.pending) == 0 && len(found) > 0
	v.pending = found
	return first
}

// reportArmed records the claim and the class and reports whether the
// volume moved between armed and unarmed.
func (v *volume) reportArmed(claim claimReference, class string, armed bool) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	changed := armed != v.armed
	v.claim = claim
	v.class = class
	v.armed = armed
	return changed
}

// reading is what the gauges carry: the claim that labels them, whether
// the volume is armed, and how many paths are pending.
func (v *volume) reading() (claimReference, bool, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.claim, v.armed, len(v.pending)
}

// report is the condition every NodeGetVolumeStats answer carries. A
// failure comes first, then an unarmed volume with work the driver may
// not commit, then the commit the tree stands on.
func (v *volume) report() (bool, string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	switch {
	case v.trouble != "":
		return true, v.trouble
	case v.writeable && !v.armed && len(v.pending) > 0:
		return true, fmt.Sprintf("unarmed: %d paths pending, no class on claim %s/%s",
			len(v.pending), v.claim.namespace, v.claim.name)
	case len(v.pending) > 0:
		return false, fmt.Sprintf("%s at %s, %d paths pending",
			v.attributes.ref, short(v.commit), len(v.pending))
	}
	return false, fmt.Sprintf("%s at %s", v.attributes.ref, short(v.commit))
}

// reportCommit records that the tree holds commit and nothing is wrong.
func (v *volume) reportCommit(commit string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.commit = commit
	v.trouble = ""
}

// reportTrouble records a failure and reports whether it is the first
// since the last success, which is when an Event is worth posting.
func (v *volume) reportTrouble(message string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	first := v.trouble == ""
	v.trouble = message
	return first
}

func (v *volume) condition() (string, string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.commit, v.trouble
}
