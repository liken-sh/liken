package main

// volume.go holds one volume this node has and everything the driver
// reports about it.

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// volumeKind is what the kubelet asked this node for.
//
// There are three kinds. An inline volume belongs to one pod and gets no
// stage call. A read-only claim is staged once on the node, and many
// pods publish from that one tree. A writeable volume is staged once
// and one pod writes it.
type volumeKind int

const (
	inlineVolume volumeKind = iota
	readOnlyClaim
	writeableVolume
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
	// A writeable volume has a git directory. Both staged kinds have a
	// staging path. An inline volume has neither.
	kind    volumeKind
	work    *workTree
	staging string
	// The volume context the record carries. It stays on the volume so
	// an unpublish can write the record again, because the unpublish
	// call carries no attributes.
	context map[string]string

	mu      sync.Mutex
	commit  string
	trouble string
	// The pod the kubelet named at publish. A stage call names no pod.
	pod podReference
	// Every target a read-only claim is bound at on this node, and the
	// pod that asked for each one. Many pods publish one staged tree,
	// and an Event about the tree goes to all of them.
	targets map[string]podReference
	// What the pod wrote and the driver has not committed, and the claim
	// and class that say whether it may.
	pending []change
	claim   claimReference
	class   string
	armed   bool
	// The rules the class resolved to, and the reason a class this
	// driver cannot read arms nothing.
	rules   *policy
	invalid string
	// What the size guard left out, what the remote does not hold
	// yet, and when a push last worked.
	skipped  []change
	unpushed int
	oldest   time.Time
	lastPush time.Time
	// The side branch every push goes to while the tree and
	// upstream have both moved, whether the remote holds the ref at all,
	// and the work tree the sweep left on this node with commits nothing
	// pushed.
	diverged   string
	refDeleted bool
	abandoned  string
	// What report() last said, so the gauge and the log carry a
	// volume's health at the moment it turns and never on every poll.
	abnormal bool
}

// reportDiverged records the side branch the volume pushes to.
func (v *volume) reportDiverged(branch string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.diverged = branch
}

// reportHealed records that the ref holds the work again.
func (v *volume) reportHealed() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.diverged = ""
}

// divergedFrom is the side branch, and the empty string on a
// volume that pushes to its ref.
func (v *volume) divergedFrom() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.diverged
}

// reportRefDeleted records a fetch that found the remote holds
// the ref no longer, and the commit the tree keeps.
func (v *volume) reportRefDeleted(commit string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.commit = commit
	v.refDeleted = true
}

// refIsDeleted reports the ref the remote no longer holds, which
// is what stops every push.
func (v *volume) refIsDeleted() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.refDeleted
}

// reportAbandoned records the work tree of this repository the
// sweep found with commits nothing pushed.
func (v *volume) reportAbandoned(message string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.abandoned = message
}

// authorEnv is the class's author where a class arms the volume,
// and the driver's own where none does.
func (v *volume) authorEnv() []string {
	if rules := v.policyNow(); rules != nil {
		return rules.author()
	}
	return defaultPolicy().author()
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

// writeable reports the one kind the driver commits and pushes for.
func (v *volume) writeable() bool {
	return v.kind == writeableVolume
}

// bind records one more target and the pod that asked for it.
//
// The pod is kept per target, because every pod that publishes the
// volume takes its Events.
func (v *volume) bind(target string, pod podReference) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.targets[target] = pod
	v.pod = pod
}

// boundAt reports whether the volume is already bound at the target.
func (v *volume) boundAt(target string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, standing := v.targets[target]
	return standing
}

// unbind takes one target away and answers how many are left.
func (v *volume) unbind(target string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.targets, target)
	return len(v.targets)
}

// boundTargets is every target the volume is bound at, in order, which
// is what the record carries.
func (v *volume) boundTargets() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	bound := make([]string, 0, len(v.targets))
	for target := range v.targets {
		bound = append(bound, target)
	}
	sort.Strings(bound)
	return bound
}

// boundPods is every pod the volume is published to on this node.
//
// An Event about a read-only claim goes to all of them, because each one
// reads the tree the Event is about.
func (v *volume) boundPods() []podReference {
	v.mu.Lock()
	defer v.mu.Unlock()
	pods := make([]podReference, 0, len(v.targets))
	for _, pod := range v.targets {
		pods = append(pods, pod)
	}
	return pods
}

// setClaim records the claim the volume handle is bound to, which
// labels the gauge and takes the volume's Events.
func (v *volume) setClaim(claim claimReference) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.claim = claim
}

// claimNow is that claim, and the empty one where the driver has not
// found it.
func (v *volume) claimNow() claimReference {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.claim
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

// reportArmed records the claim, the class, and the rules it resolved
// to, and reports whether the volume moved between armed and unarmed. A
// class the driver cannot read arms nothing and leaves its reason in
// invalid.
func (v *volume) reportArmed(
	claim claimReference, class string, rules *policy, invalid string,
) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	armed := rules != nil
	changed := armed != v.armed
	v.claim = claim
	v.class = class
	v.armed = armed
	v.rules = rules
	v.invalid = invalid
	return changed
}

// policyNow is the rules in force, and nil on an unarmed volume,
// so a loop that started before the class reads what the class says now.
func (v *volume) policyNow() *policy {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.rules
}

// namespace is where a person looks the volume up.
//
// A staged volume of either kind is named by its claim. An inline volume
// is named by the pod that mounts it, which is the only namespace an
// inline volume has.
func (v *volume) namespace() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.kind == inlineVolume {
		return v.pod.namespace
	}
	return v.claim.namespace
}

// reading is what the gauges carry: the claim that labels them, whether
// the volume is armed, and how many paths are pending.
func (v *volume) reading() (claimReference, bool, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.claim, v.armed, len(v.pending)
}

// reportSkipped records what the size guard left out and answers
// the paths the last pass did not already hold, which is what an Event
// is worth posting for.
func (v *volume) reportSkipped(found []change) []change {
	v.mu.Lock()
	defer v.mu.Unlock()
	standing := map[string]bool{}
	for _, one := range v.skipped {
		standing[one.path] = true
	}
	fresh := []change{}
	for _, one := range found {
		if !standing[one.path] {
			fresh = append(fresh, one)
		}
	}
	v.skipped = found
	return fresh
}

// reportUnpushed records what the work tree holds that the remote
// does not, and when the oldest of those commits was made.
func (v *volume) reportUnpushed(count int, oldest time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.unpushed = count
	v.oldest = oldest
}

// reportPushed records a push that worked, which leaves nothing
// unpushed and ends the trouble the failures before it reported.
func (v *volume) reportPushed(at time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.unpushed = 0
	v.oldest = time.Time{}
	v.lastPush = at
	v.trouble = ""
}

// pushing is what the push gauges carry.
func (v *volume) pushing() (int, time.Time, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.unpushed, v.lastPush, len(v.skipped)
}

// overdue reports whether the oldest unpushed commit has outlived
// push.maxLatency. The caller holds the lock.
func (v *volume) overdue(now time.Time) bool {
	return v.unpushed > 0 && v.rules != nil && v.rules.maxLatency != 0 &&
		!v.oldest.IsZero() && now.Sub(v.oldest) > v.rules.maxLatency
}

// report is the one source of the volume's health, which the
// gauge and the log carry. A failure comes first, then a class the
// driver cannot read, then the work the driver may not commit or has
// not pushed, then the commit the tree stands on.
func (v *volume) report() (bool, string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reportNow()
}

// takeHealth is the report and whether the abnormal flag moved
// since the last call, which is the one moment the log says so.
func (v *volume) takeHealth() (bool, string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	abnormal, message := v.reportNow()
	moved := abnormal != v.abnormal
	v.abnormal = abnormal
	return abnormal, message, moved
}

// reportNow is the report itself, read under the lock the two
// callers above hold, so a health reading and the flag it moves come
// from one look at the volume.
func (v *volume) reportNow() (bool, string) {
	switch {
	case v.trouble != "":
		return true, v.trouble
	case v.refDeleted:
		return true, fmt.Sprintf("RefDeleted: the remote holds no %s", v.attributes.ref)
	case v.diverged != "":
		return true, fmt.Sprintf("Diverged: the tree pushes to %s, not %s",
			v.diverged, v.attributes.ref)
	case v.invalid != "":
		return true, v.invalid
	case len(v.skipped) > 0:
		return true, skippedMessage(v.skipped)
	case v.overdue(time.Now()):
		return true, fmt.Sprintf("%d unpushed commits, the oldest older than %s",
			v.unpushed, v.rules.maxLatency)
	case v.abandoned != "":
		return true, v.abandoned
	case v.kind == writeableVolume && !v.armed && len(v.pending) > 0:
		return true, fmt.Sprintf("unarmed: %d paths pending, no class on claim %s/%s",
			len(v.pending), v.claim.namespace, v.claim.name)
	case len(v.pending) > 0:
		return false, fmt.Sprintf("%s at %s, %d paths pending",
			v.attributes.ref, short(v.commit), len(v.pending))
	}
	return false, fmt.Sprintf("%s at %s", v.attributes.ref, short(v.commit))
}

// reportCommit records that the tree holds commit and nothing is wrong.
// A resolved commit means the fetch reached the ref, so a deleted ref is
// reported no longer.
func (v *volume) reportCommit(commit string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.commit = commit
	v.trouble = ""
	v.refDeleted = false
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

// skippedMessage names every path the size guard left out, so a
// person reads which files the commit does not carry.
func skippedMessage(skipped []change) string {
	paths := make([]string, 0, len(skipped))
	for _, one := range skipped {
		paths = append(paths, one.path)
	}
	return fmt.Sprintf("%d files over %s: %s",
		len(skipped), maxFileSizeParameter, strings.Join(paths, ", "))
}

func (v *volume) condition() (string, string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.commit, v.trouble
}
