package main

// volume.go keeps the state of each volume on this node and builds the
// health message that the gauges and the log report.

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// An inline volume belongs to one pod and gets no stage call. A
// read-only claim is staged once on the node, and every pod on the node
// that uses it publishes the same tree. A writeable volume is staged
// once, and one pod writes to it.
type volumeKind int

const (
	inlineVolume volumeKind = iota
	readOnlyClaim
	writeableVolume
)

// volume is the driver's state for one volume on this node. mu guards
// the fields after it.
type volume struct {
	id         string
	attributes *attributes
	// credentials is read through credential(), because a republish
	// replaces it while the fetch and push loops run.
	credentials *credentials
	directory   string
	tree        string
	target      string
	kind        volumeKind
	// work is set only on a writeable volume. staging is set on a
	// writeable volume and on a read-only claim. An inline volume has
	// neither.
	work    *workTree
	staging string
	// context is the volume context that the volume's record stores. It
	// is kept here because an unpublish rewrites the record and the
	// unpublish call has no volume context.
	context map[string]string

	mu     sync.Mutex
	commit string
	// faults.go records these faults until a success clears them.
	fetchFault    string
	pushFault     string
	upstreamMoved string
	// pod is the pod of the last publish. A stage call names no pod.
	pod podReference
	// targets maps each target that a read-only claim is bound at on
	// this node to the pod that requested it. An Event about the tree
	// goes to every one of those pods.
	targets map[string]podReference
	// pending is the paths that the pod changed and the driver has not
	// committed. The driver commits them only while armed, which is
	// true when the claim's class resolved to rules.
	pending []change
	claim   claimReference
	class   string
	armed   bool
	rules   *policy
	// invalid is the reason that the driver could not read the class.
	invalid string
	// lastDemand is when a demand last named this volume, and lastPull
	// is when the fetch loop last ran for it.
	lastDemand time.Time
	lastPull   time.Time
	// satisfiedUntil is the latest demand time that a fetch of this
	// volume on this node has satisfied. actedDemand is the time of the
	// last demand the node acted on. acceptDemand describes how they
	// change.
	satisfiedUntil time.Time
	actedDemand    time.Time
	// skipped is the paths that commits leave out because they are
	// larger than commit.maxFileSize. unpushed is the number of commits
	// the remote does not have, and oldest is when the oldest of them
	// was made.
	skipped  []change
	unpushed int
	oldest   time.Time
	lastPush time.Time
	// diverged is the side branch that pushes go to while both the tree
	// and upstream have new commits. refDeleted is true when the last
	// fetch found no ref on the remote.
	diverged   string
	refDeleted bool
	// abnormal is the value that healthChange last returned.
	abnormal bool
	// credentialLost marks a resumed volume that had a credential and
	// waits for a republish to supply it again. noPublishSecret records
	// that the volume's PersistentVolume names no publish Secret.
	// republish.go describes how both change.
	credentialLost  bool
	noPublishSecret bool
}

func (v *volume) reportDiverged(branch string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.diverged = branch
}

func (v *volume) reportReturnedToRef() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.diverged = ""
}

// sideBranch returns an empty string when the volume pushes to its ref.
func (v *volume) sideBranch() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.diverged
}

func (v *volume) reportRefDeleted(commit string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.commit = commit
	v.refDeleted = true
}

// refIsDeleted reports whether the last fetch found no ref on the
// remote. While it is true, the driver pushes nothing, because a push
// would create the ref again.
func (v *volume) refIsDeleted() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.refDeleted
}

func (v *volume) authorEnv() []string {
	if rules := v.policyNow(); rules != nil {
		return rules.author()
	}
	return defaultPolicy().author()
}

// setPod records the pod named in a publish. The loops start at stage,
// before a publish names a pod, and their Events go to this pod.
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

// writeable reports whether the driver commits and pushes the volume.
func (v *volume) writeable() bool {
	return v.kind == writeableVolume
}

// bind records a target of a read-only claim and the pod that requested
// it, and makes that pod the volume's pod.
func (v *volume) bind(target string, pod podReference) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.targets[target] = pod
	v.pod = pod
}

func (v *volume) boundAt(target string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, bound := v.targets[target]
	return bound
}

// unbind returns how many targets remain.
func (v *volume) unbind(target string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.targets, target)
	return len(v.targets)
}

// boundTargets returns the targets sorted, as the volume's record
// stores them.
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

func (v *volume) boundPods() []podReference {
	v.mu.Lock()
	defer v.mu.Unlock()
	pods := make([]podReference, 0, len(v.targets))
	for _, pod := range v.targets {
		pods = append(pods, pod)
	}
	return pods
}

func (v *volume) setClaim(claim claimReference) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.claim = claim
}

// claimNow returns an empty claimReference until the driver finds the
// claim.
func (v *volume) claimNow() claimReference {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.claim
}

// reportPending records the paths that the last scan found, and returns
// true when the set was empty before. The driver posts an Event only
// then.
func (v *volume) reportPending(found []change) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	first := len(v.pending) == 0 && len(found) > 0
	v.pending = found
	return first
}

// reportArmed records the claim, the class, and the rules that the
// class resolved to, and returns true when armed changed. A class that
// the driver cannot read gives nil rules and the reason in invalid.
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

// policyNow returns the current rules, or nil on an unarmed volume. The
// loops call it on every pass instead of keeping a copy, so a change to
// the class applies to a loop that is already running.
func (v *volume) policyNow() *policy {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.rules
}

// namespace returns the namespace that labels the volume's gauges: the
// claim's namespace, or the pod's for an inline volume, which has no
// claim. It is empty until the driver has found either.
func (v *volume) namespace() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.kind == inlineVolume {
		return v.pod.namespace
	}
	return v.claim.namespace
}

// gaugeValues returns what the gauges report: the claim that labels them,
// whether the volume is armed, and the number of pending paths.
func (v *volume) gaugeValues() (claimReference, bool, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.claim, v.armed, len(v.pending)
}

// reportSkipped records the paths that commits left out for their size,
// and returns the ones that the previous call did not have. The driver
// posts an Event only for those.
func (v *volume) reportSkipped(found []change) []change {
	v.mu.Lock()
	defer v.mu.Unlock()
	already := map[string]bool{}
	for _, one := range v.skipped {
		already[one.path] = true
	}
	fresh := []change{}
	for _, one := range found {
		if !already[one.path] {
			fresh = append(fresh, one)
		}
	}
	v.skipped = found
	return fresh
}

func (v *volume) reportUnpushed(count int, oldest time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.unpushed = count
	v.oldest = oldest
}

// reportPushed records a successful push and clears pushFault. It also
// clears upstreamMoved, because the remote now has the tree's commits on
// top of upstream.
func (v *volume) reportPushed(at time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.unpushed = 0
	v.oldest = time.Time{}
	v.lastPush = at
	v.pushFault = ""
	v.upstreamMoved = ""
}

// pushing returns the values of the push gauges: the number of
// unpushed commits, the time of the last successful push, and the
// number of skipped paths.
func (v *volume) pushing() (int, time.Time, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.unpushed, v.lastPush, len(v.skipped)
}

// overdue reports whether the oldest unpushed commit is older than
// push.maxLatency. The caller holds v.mu.
func (v *volume) overdue(now time.Time) bool {
	return v.unpushed > 0 && v.rules != nil && v.rules.maxLatency != 0 &&
		!v.oldest.IsZero() && now.Sub(v.oldest) > v.rules.maxLatency
}

// health returns whether the volume is abnormal, and the health
// message.
func (v *volume) health() (bool, string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.healthLocked()
}

// healthChange returns the same values as health, and also whether
// abnormal changed since the previous call. The log writes a line only
// when it changed.
func (v *volume) healthChange() (bool, string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	abnormal, message := v.healthLocked()
	changed := abnormal != v.abnormal
	v.abnormal = abnormal
	return abnormal, message, changed
}

// healthLocked builds the health message. The caller holds v.mu.
//
// Every message ends with the times of the last demand and the last
// pull, so a message about a failed pull also shows when the pull was
// requested.
func (v *volume) healthLocked() (bool, string) {
	abnormal, message := v.healthMessage()
	return abnormal, message + v.demandAndPullTimes()
}

// healthMessage reports the first problem in this order: a missing
// credential, a recorded fault, a deleted ref, a side branch, a class
// the driver cannot read, skipped paths, an overdue push, or pending
// paths on an unarmed writeable volume. With no problem, it reports
// the ref and the commit that the tree is at.
func (v *volume) healthMessage() (bool, string) {
	if waiting, lost := v.credentialReport(); lost {
		return true, waiting
	}
	switch {
	case v.fault() != "":
		return true, v.fault()
	case v.refDeleted:
		return true, fmt.Sprintf("RefDeleted: the remote has no ref %s", v.attributes.ref)
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
	case v.kind == writeableVolume && !v.armed && len(v.pending) > 0:
		return true, fmt.Sprintf("Unarmed: %d paths pending, and the claim %s/%s sets no class",
			len(v.pending), v.claim.namespace, v.claim.name)
	case len(v.pending) > 0:
		return false, fmt.Sprintf("%s at %s, %d paths pending",
			v.attributes.ref, short(v.commit), len(v.pending))
	}
	return false, fmt.Sprintf("%s at %s", v.attributes.ref, short(v.commit))
}

func (v *volume) reportDemanded(at time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lastDemand = at
}

// reportPulled records when the fetch loop last ran for this volume,
// whether a timer or a demand started it.
func (v *volume) reportPulled(at time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lastPull = at
}

// satisfyDemandsBefore records that a fetch started at start. demandSkew
// describes which demands that fetch satisfies.
func (v *volume) satisfyDemandsBefore(start time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if satisfied := start.Add(-demandSkew); satisfied.After(v.satisfiedUntil) {
		v.satisfiedUntil = satisfied
	}
}

// acceptDemand returns true when no earlier fetch satisfied a demand
// stamped at the given time, and the caller then starts a fetch.
//
// The fetch starts after now, so acceptDemand moves satisfiedUntil to
// now minus demandSkew, the bound that satisfyDemandsBefore records for
// a fetch that starts now. The demand's own time is not a safe bound,
// because it comes from the writer's clock: a demand written later by a
// writer whose clock is further behind would have an earlier time and
// start no fetch. actedDemand records the demand's exact time, so the
// same demand read again from a list or a resumed watch starts no
// fetch.
func (v *volume) acceptDemand(at, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if at.Equal(v.actedDemand) || !at.After(v.satisfiedUntil) {
		return false
	}
	v.actedDemand = at
	if satisfied := now.Add(-demandSkew); satisfied.After(v.satisfiedUntil) {
		v.satisfiedUntil = satisfied
	}
	return true
}

// demandAndPullTimes returns the times that end every health message.
// A time that was never recorded is left out.
func (v *volume) demandAndPullTimes() string {
	times := ""
	if !v.lastDemand.IsZero() {
		times += ", demanded " + v.lastDemand.UTC().Format(time.RFC3339)
	}
	if !v.lastPull.IsZero() {
		times += ", pulled " + v.lastPull.UTC().Format(time.RFC3339)
	}
	return times
}

// reportCommit records that a successful fetch brought the tree to
// commit. That fetch found the ref and the tree matches upstream, so it
// clears fetchFault, upstreamMoved, and refDeleted. It returns true when
// a fetch fault was recorded, because the driver posts a recovery Event
// only then.
func (v *volume) reportCommit(commit string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	recovered := v.fetchFault != ""
	v.commit = commit
	v.fetchFault = ""
	v.upstreamMoved = ""
	v.refDeleted = false
	return recovered
}

// skippedMessage lists every skipped path, so a person can see which
// files the commits do not include.
func skippedMessage(skipped []change) string {
	paths := make([]string, 0, len(skipped))
	for _, one := range skipped {
		paths = append(paths, one.path)
	}
	return fmt.Sprintf("%d files over %s: %s",
		len(skipped), maxFileSizeParameter, strings.Join(paths, ", "))
}

// commitAndFault returns the commit the tree is at and the first recorded
// failure, which is empty when there is none.
func (v *volume) commitAndFault() (string, string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.commit, v.fault()
}
