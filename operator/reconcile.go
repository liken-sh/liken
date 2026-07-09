package main

// This file is the working half of the reconcile loop: each pass
// observes the machine, acts on the spec, and reports status.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/chrisguidry/liken/machine"
)

func getMachine(c *apiClient, name string) (*machine.Machine, error) {
	m := &machine.Machine{}
	if err := c.requestJSON(http.MethodGet, machinesPath+"/"+name, nil, m); err != nil {
		return nil, err
	}
	return m, nil
}

// ensureMachine makes the manifest's Machine real in the cluster. The
// retry-forever loop covers the operator's first minutes: k3s applies
// the Machine CRD from its manifests directory around the same time
// it starts this pod, and until the API server is serving that CRD,
// our URLs 404. The loop waits instead of crashing because that 404
// is expected during startup, not a sign of anything wrong.
func ensureMachine(c *apiClient, seed *machine.Machine) (*machine.Machine, error) {
	for {
		current, err := getMachine(c, seed.Metadata.Name)
		if err == nil {
			return current, nil
		}
		if !errors.Is(err, errNotFound) {
			return nil, err
		}

		body, err := json.Marshal(&machine.Machine{
			APIVersion: machine.APIVersion,
			Kind:       "Machine",
			Metadata:   machine.ObjectMeta{Name: seed.Metadata.Name},
			Spec:       seed.Spec,
		})
		if err != nil {
			return nil, err
		}
		err = c.requestJSON(http.MethodPost, machinesPath, body, nil)
		if err == nil {
			fmt.Printf("created machine %s from %s\n", seed.Metadata.Name, machine.BootManifestPath)
			continue // re-GET so we return the server's copy, resourceVersion and all
		}
		if errors.Is(err, errNotFound) {
			fmt.Println("machine API not served yet; waiting")
			retryPause()
			continue
		}
		return nil, err
	}
}

// ensureCluster makes the manifest's Cluster real in the cluster. It
// waits out an unserved CRD the same way ensureMachine does, and it
// tolerates one extra answer: 409 Conflict. Every machine's operator
// races to create the same object at boot, so all but one of those
// POSTs will conflict. That conflict is harmless: the loop's next GET
// confirms the object exists, which is the only outcome that matters.
func ensureCluster(c *apiClient, seed *machine.Cluster) error {
	for {
		if _, err := getCluster(c, seed.Metadata.Name); err == nil {
			return nil
		} else if !errors.Is(err, errNotFound) {
			return err
		}

		body, err := json.Marshal(&machine.Cluster{
			APIVersion: machine.APIVersion,
			Kind:       "Cluster",
			Metadata:   machine.ObjectMeta{Name: seed.Metadata.Name},
			Spec:       seed.Spec,
		})
		if err != nil {
			return err
		}
		switch err := c.requestJSON(http.MethodPost, clustersPath, body, nil); {
		case err == nil:
			fmt.Printf("created cluster %s from %s\n", seed.Metadata.Name, machine.ClusterManifestPath)
		case errors.Is(err, errNotFound):
			fmt.Println("cluster API not served yet; waiting")
			retryPause()
		case errors.Is(err, errConflict):
			// Another machine's operator got there first.
		default:
			return err
		}
	}
}

// carryOutConvergence performs one convergence decision's side
// effects against one document's store, and returns the condition to
// publish (an I/O failure downgrades it to StagingFailed on the same
// condition type, so the report stays attached to the right
// document).
func carryOutConvergence(conv convergence, store machine.ManifestStore, what string, now time.Time) machine.Condition {
	failed := func(err error) machine.Condition {
		return machine.Condition{Type: conv.condition.Type, Status: machine.ConditionFalse, Reason: "StagingFailed", Message: err.Error()}
	}
	if conv.withdraw {
		if err := store.WithdrawStaged(); err != nil {
			fmt.Printf("withdrawing the staged %s: %v\n", what, err)
		} else {
			fmt.Printf("withdrew the staged %s; the cluster's copy matches this boot again\n", what)
		}
	}
	if conv.clearRejection {
		if err := store.ClearRejection(); err != nil {
			fmt.Printf("clearing the %s rejection record: %v\n", what, err)
		}
	}
	if conv.stage {
		if err := store.WriteStaged(conv.manifest); err != nil {
			return failed(err)
		}
		fmt.Printf("staged %s %.12s for the next boot\n", what, conv.hash)
	}
	if conv.requestReboot {
		intent := &machine.RebootIntent{
			Reason:       "applying the staged " + what,
			ManifestHash: conv.hash,
			RequestedAt:  now,
		}
		if err := machine.WriteRebootIntent(machine.OperatorRunDir, intent); err != nil {
			return failed(err)
		}
		fmt.Printf("requested a reboot to apply %s %.12s\n", what, conv.hash)
	}
	return conv.condition
}

// reconcile is one full pass of the operator's job, always from
// absolute state: read the facts init left, actuate the spec's sysctls,
// read back what actually holds, and publish all of it as status. It
// deliberately keeps no memory between passes: every value in the
// status it writes was observed moments ago, which is what the
// Kubernetes convention means by status being reconstructible.
func reconcile(c *apiClient, m *machine.Machine, clusterName string, f *fetcher) {
	now := time.Now()

	// What the object records before this pass touches anything,
	// captured now because it can't be captured later: SetCondition
	// edits the slice it is given, so the conditions this pass builds
	// share their backing array with m.Status, and by publish time the
	// two are the same list. This snapshot is what lets the publish
	// below skip a write that would change nothing.
	before, _ := json.Marshal(&m.Status)

	status := &machine.MachineStatus{}

	facts, err := machine.ReadFacts(machine.FactsPath)
	if err == nil {
		*status = *facts
	}
	status.Conditions = machine.SetCondition(m.Status.Conditions, factsCondition(err), now)

	// The operator's own existence is the evidence that promotes a
	// staged cluster document: if this line runs, the machine joined
	// its cluster under whatever document this boot ran (cluster.go).
	// The same evidence, together with the version this boot reported
	// in the facts, is what promotes a system release's proving boot
	// (release.go).
	settleClusterLifecycle(machine.MachineStateDir, machine.ClusterManifestPath, facts)
	settleSystemReleaseLifecycle(machine.MachineStateDir, facts)

	status.Sysctls, err = applySysctls(m.Spec.Sysctls)
	status.Conditions = machine.SetCondition(status.Conditions, sysctlsCondition(err), now)

	// Modules judge what the boot reported, not what the spec asks
	// now: a freshly declared module has no outcome yet, and its story
	// is SpecConverged's until a reboot loads it. This condition is
	// the other half of the split: SpecConverged can be True (the boot
	// ran the manifest) while this is False, because a spec the boot
	// honored can still name modules the booted image never carried.
	status.Conditions = machine.SetCondition(status.Conditions,
		modulesCondition(status.Modules), now)

	// Storage compares the spec's declared roles against the facts'
	// report of where each is actually backed. The operator can't
	// observe the disks directly (claiming happened before this
	// cluster existed), so init's facts are the only source, and this
	// condition checks them against the spec.
	status.Conditions = machine.SetCondition(status.Conditions,
		storageCondition(m.Spec.Storage, status.Storage), now)

	// t is this machine's standing with the rollout conductor. A
	// standalone machine reboots at will; a cluster member reboots
	// only on a granted turn. The grant is a condition the conductor
	// wrote onto this Machine (rollout.go); this operator reads it,
	// carries it along in its own status writes, and never sets or
	// clears it.
	t := turnStandalone
	if clusterName != "" {
		t = turnAwaiting
		if g := machine.FindCondition(m.Status.Conditions, rebootApprovedCondition); g != nil && g.Status == machine.ConditionTrue {
			t = turnGranted
		}
	}

	// Read the machine's own Node once; it serves three purposes: the
	// NodeHealthy condition, demotion cleanup, and the cordon state
	// the drain works through. The read can fail benignly, because
	// mid-demotion the Node is deleted and not yet re-registered. A
	// pass where the read fails simply skips all three, and the next
	// pass settles them.
	node, nodeErr := getNode(c, m.Metadata.Name)

	// Convergence checks whether the cluster's copy of each document
	// matches what this boot actuated, and if not, stages the
	// difference for the next boot (converge.go for the Machine,
	// cluster.go for the Cluster). The decisions are pure functions;
	// carryOutConvergence performs their side effects against each
	// document's own store. The rejection records come from the
	// durable store, not from facts: facts are a snapshot taken at
	// boot and never change while the machine runs, but a rejection
	// cleared mid-boot (by an edit that reverted) must unblock a retry
	// without waiting for a reboot to refresh the facts. A granted
	// reboot goes through the drain first (drain.go): the node is
	// cordoned and emptied before the intent is written, so workloads
	// move to other nodes instead of being killed by the reboot.
	draining := false
	gate := func(conv convergence) convergence {
		if !conv.requestReboot || t != turnGranted || nodeErr != nil {
			return conv
		}
		gated := gateThroughDrain(c, node, conv, now)
		draining = draining || !gated.requestReboot
		return gated
	}
	machineStore := machine.MachineManifests(machine.MachineStateDir)
	machineRejection, _ := machineStore.LoadRejection()
	conv := gate(decideConvergence(m, facts, machineRejection, readStagedHash(machineStore), t))
	condition := carryOutConvergence(conv, machineStore, "spec", now)
	status.Conditions = machine.SetCondition(status.Conditions, condition, now)

	// The cluster document converges through the same machinery, per
	// machine: this machine stages its own copy and reboots on its own
	// policy, and this condition is where the fleet's transient
	// disagreement about the Cluster is visible.
	var liveCluster *machine.Cluster
	rebooting := conv.requestReboot
	if clusterName != "" {
		var cconv convergence
		clusterStore := machine.ClusterManifests(machine.MachineStateDir)
		if liveCluster, err = getCluster(c, clusterName); err != nil {
			cconv = convergence{condition: convergenceUnknown("ClusterConverged", "ClusterUnavailable",
				fmt.Sprintf("reading cluster %s: %v", clusterName, err))}
		} else {
			clusterRejection, _ := clusterStore.LoadRejection()
			cconv = gate(decideClusterConvergence(liveCluster, m, facts, clusterRejection,
				bootClusterHash(machine.BootClusterManifestPath), readStagedHash(clusterStore), t))
		}
		condition := carryOutConvergence(cconv, clusterStore, "cluster document", now)
		status.Conditions = machine.SetCondition(status.Conditions, condition, now)
		rebooting = rebooting || cconv.requestReboot

		// The version target converges through its own machinery: a
		// download aimed at the inactive slot. The download runs on
		// the fetcher's goroutine so that this pass, and the heartbeat
		// below, never wait on a socket (release.go decides, fetch.go
		// moves the bytes). Once the download verifies, the rest works
		// like the other two documents: a staged SystemRelease record,
		// the reboot chain, the drain gate, and the same
		// carryOutConvergence.
		if liveCluster != nil {
			systemStore := machine.SystemReleases(machine.MachineStateDir)
			systemRejection, _ := systemStore.LoadRejection()
			stagedSystemHash := readStagedHash(systemStore)
			ask, vcond, ok := versionAsk(liveCluster, facts)
			vconv := versionConvergence(vcond, stagedSystemHash, systemRejection)
			if ok {
				vconv = gate(decideSystemStaging(ask, f.Ensure(ask), m, systemRejection, stagedSystemHash, t))
			}
			condition := carryOutConvergence(vconv, systemStore, "system release", now)
			status.Conditions = machine.SetCondition(status.Conditions, condition, now)
			rebooting = rebooting || vconv.requestReboot
		}
	}

	if nodeErr == nil {
		// NodeHealthy mirrors the Node's Ready condition onto the
		// Machine. This catches the one failure the heartbeat can't:
		// this operator runs on the host's network and talks to the
		// API directly, so it can keep reporting a healthy-looking
		// machine while the kubelet beneath it is dead. The kubelet's
		// own heartbeat (its node lease, which the node controller
		// turns into the Node's Ready condition) is the evidence that
		// the machine is actually serving the cluster, not just
		// reachable.
		status.Conditions = machine.SetCondition(status.Conditions, nodeHealthyCondition(node), now)

		// Demotion cleanup (demotion.go): a follower whose Node object
		// still claims control-plane was just demoted. That stale Node
		// carries a registered etcd membership, so it has to be
		// deleted.
		d := decideDemotion(status.Role, node.Metadata.Labels, m.Spec.RebootPolicyOrDefault(), t)
		condition := carryOutDemotion(c, m.Metadata.Name, d)
		status.Conditions = machine.SetCondition(status.Conditions, condition, now)
		rebooting = rebooting || d.cleanup

		// When this operator set a cordon and no longer needs it,
		// because the reboot happened and the machine converged, the
		// node goes back to the scheduler. This only applies to
		// cordons the operator set itself: decideUncordon leaves a
		// human's cordon standing.
		if !rebooting && !draining && decideUncordon(node) {
			if err := c.patchJSON(nodesPath+"/"+node.Metadata.Name, uncordonPatch()); err != nil {
				fmt.Printf("uncordoning %s: %v\n", node.Metadata.Name, err)
			} else {
				fmt.Printf("uncordoned %s; its reboot is complete\n", node.Metadata.Name)
			}
		}
	}

	// Ready is the roll-up: True exactly when every other condition
	// is. The scan skips any prior Ready so the previous pass's value
	// can't affect this one. It also skips the conductor's grant,
	// because the grant is a permission token, not an observation
	// about this machine's health.
	ready := machine.Condition{Type: "Ready", Status: machine.ConditionTrue, Reason: "Reconciled"}
	for _, condition := range status.Conditions {
		if condition.Type == "Ready" || condition.Type == rebootApprovedCondition {
			continue
		}
		if condition.Status != machine.ConditionTrue {
			ready = machine.Condition{
				Type: "Ready", Status: machine.ConditionFalse,
				Reason: "Degraded", Message: condition.Type + " is " + string(condition.Status),
			}
		}
	}
	status.Conditions = machine.SetCondition(status.Conditions, ready, now)

	// Every condition this pass publishes judged the spec at this
	// generation. The API server bumps metadata.generation on spec
	// writes only, so stamping it here lets a consumer tell a verdict
	// on the current spec apart from a verdict on a spec that has
	// since been edited. The conductor's grant keeps its own stamp:
	// it is the conductor's verdict, and this writer must not restamp
	// it.
	for i := range status.Conditions {
		if status.Conditions[i].Type == rebootApprovedCondition {
			continue
		}
		status.Conditions[i].ObservedGeneration = m.Metadata.Generation
	}

	// The phase compresses the conditions into the one word a fleet
	// listing shows (phase.go).
	status.Phase = decidePhase(status.Conditions)

	// The heartbeat: renew this machine's lease so the fleet can tell
	// that this status is current, not the final report of a machine
	// that has since died (lease.go explains why this is a lease and
	// not a status field). The heartbeat is deliberately separate
	// from the status write below. Status is written when the
	// machine's state changes; the heartbeat proves the reporter is
	// alive; and combining them would make every heartbeat rewrite
	// the whole object. Either write can fail while the other lands,
	// and that is the correct outcome: the machine is alive and will
	// retry on its next pass.
	//
	// The heartbeat goes first because of what each write means to
	// the sweeping leader. A machine booting into a fleet that has
	// already declared it Lost announces its liveness here, so the
	// sweeper stops writing Lost verdicts onto the very object the
	// status write below is about to update. Publishing first would
	// invite that collision on every boot.
	renewMachineHeartbeat(c, m.Metadata.Name, now)

	if err := publishOwnStatus(c, m, status, before); err != nil {
		fmt.Printf("publishing status: %v\n", err)
	}

	// Fleet-level work belongs to the leaders: mark silent machines
	// Lost and keep the Cluster's headcount current. Every leader is
	// *able* to sweep, but only the one holding the lease does
	// (lease.go). That gives the fleet exactly one sweeper at a time,
	// with the other leaders ready to take over. This machine's own
	// heartbeat and status have already landed, so the sweep sees
	// itself fresh like everyone else.
	if liveCluster != nil && status.Role == machine.RoleLeader && holdFleetLease(c, m.Metadata.Name, now) {
		sweepFleet(c, m.Metadata.Name, liveCluster, now)
	}
}

// nodeHealthyCondition translates the Node's Ready condition into the
// Machine's vocabulary. A missing Ready condition on the Node reads
// as unhealthy: a kubelet that has never reported in cannot be
// assumed to be serving.
func nodeHealthyCondition(node *nodeObject) machine.Condition {
	for _, c := range node.Status.Conditions {
		if c.Type != "Ready" {
			continue
		}
		if c.Status == machine.ConditionTrue {
			return machine.Condition{Type: "NodeHealthy", Status: machine.ConditionTrue, Reason: "KubeletReady",
				Message: "the Node reports Ready; the kubelet is serving this machine to the cluster"}
		}
		return machine.Condition{Type: "NodeHealthy", Status: machine.ConditionFalse, Reason: "NodeNotReady",
			Message: fmt.Sprintf("the Node reports Ready=%s: %s", c.Status, c.Message)}
	}
	return machine.Condition{Type: "NodeHealthy", Status: machine.ConditionFalse, Reason: "NodeNotReady",
		Message: "the Node carries no Ready condition; the kubelet has never reported in"}
}

// applySysctls actuates spec.sysctls against the host's /proc/sys,
// reachable directly because this pod runs privileged in the host's
// namespaces, then reads every parameter back. The returned map is
// what the kernel now reports, not what we wrote: if some other agent
// resets a value, the next pass both re-asserts it and reports what
// was actually observed.
func applySysctls(desired map[string]string) (map[string]string, error) {
	var firstErr error
	observed := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(desired)) {
		if err := machine.ApplySysctl(machine.SysctlDir, name, desired[name]); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if value, err := machine.ReadSysctl(machine.SysctlDir, name); err == nil {
			observed[name] = value
		}
	}
	return observed, firstErr
}

// storageCondition summarizes storage as one standard Kubernetes
// condition, comparing what the spec declared against where each role
// is actually backed. True means every declared role sits on its
// partition. False should be unreachable on a running machine, since
// init powers off rather than boot with a declared role unsatisfied.
// But a condition has to be able to express every state it names, and
// a future, softer failure mode may need it.
func storageCondition(spec machine.StorageSpec, status machine.StorageStatus) machine.Condition {
	var placed, inMemory []string
	for _, role := range spec.Roles() {
		rs := status.Role(role.Name)
		if rs != nil && rs.Backing == machine.BackingPartition {
			placed = append(placed, fmt.Sprintf("%s on %s", role.Name, rs.Device))
		} else {
			inMemory = append(inMemory, string(role.Name))
		}
	}
	switch {
	case len(inMemory) > 0:
		return machine.Condition{
			Type: "StorageReady", Status: machine.ConditionFalse, Reason: "RolesInMemory",
			Message: fmt.Sprintf("declared roles backed by memory: %s", strings.Join(inMemory, ", ")),
		}
	case len(placed) > 0:
		return machine.Condition{
			Type: "StorageReady", Status: machine.ConditionTrue, Reason: "AllRolesPlaced",
			Message: strings.Join(placed, ", "),
		}
	default:
		return machine.Condition{
			Type: "StorageReady", Status: machine.ConditionTrue, Reason: "NothingDeclared",
			Message: "no storage declared; all roles backed by memory",
		}
	}
}

func factsCondition(err error) machine.Condition {
	if err != nil {
		return machine.Condition{
			Type: "FactsPublished", Status: machine.ConditionFalse,
			Reason: "FactsUnreadable", Message: err.Error(),
		}
	}
	return machine.Condition{Type: "FactsPublished", Status: machine.ConditionTrue, Reason: "FactsRead"}
}

// modulesCondition summarizes the boot's declared-module outcomes as
// one condition. Loaded and Builtin are both healthy; anything else
// carries init's message, which names the fix (a rebuilt image for a
// Missing module, the hardware's error for a Failed one), because a
// status that says what would repair it beats one that only says
// what's wrong.
func modulesCondition(observed []machine.ModuleStatus) machine.Condition {
	var problems []string
	for _, s := range observed {
		if s.State == machine.ModuleLoaded || s.State == machine.ModuleBuiltin {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s: %s", s.Name, s.Message))
	}
	switch {
	case len(problems) > 0:
		return machine.Condition{
			Type: "ModulesLoaded", Status: machine.ConditionFalse, Reason: "ModulesNotLoaded",
			Message: strings.Join(problems, "; "),
		}
	case len(observed) > 0:
		return machine.Condition{
			Type: "ModulesLoaded", Status: machine.ConditionTrue, Reason: "AllLoaded",
			Message: fmt.Sprintf("all %d declared modules are in the kernel", len(observed)),
		}
	default:
		return machine.Condition{
			Type: "ModulesLoaded", Status: machine.ConditionTrue, Reason: "NothingDeclared",
			Message: "no extra modules declared",
		}
	}
}

func sysctlsCondition(err error) machine.Condition {
	if err != nil {
		return machine.Condition{
			Type: "SysctlsApplied", Status: machine.ConditionFalse,
			Reason: "ApplyFailed", Message: err.Error(),
		}
	}
	return machine.Condition{Type: "SysctlsApplied", Status: machine.ConditionTrue, Reason: "Applied"}
}

// publishStatus writes through the status subresource: a separate
// endpoint (…/machines/<name>/status) that updates *only* the status
// half of the object. That means a controller can never accidentally
// rewrite the spec it is acting on, and RBAC can grant the two halves
// separately. The write is a PUT carrying the object's resourceVersion:
// if anything else changed the object in between, the server answers
// 409 Conflict instead of applying our stale copy. The caller's next
// pass re-reads and tries again. This is optimistic concurrency, and
// it is how every Kubernetes controller handles contention.
func publishStatus(c *apiClient, m *machine.Machine, status *machine.MachineStatus) error {
	updated := *m
	updated.Status = *status
	body, err := json.Marshal(&updated)
	if err != nil {
		return err
	}
	path := machinesPath + "/" + m.Metadata.Name + "/status"
	return c.requestJSON(http.MethodPut, path, body, nil)
}

// publishOwnStatus is publishStatus for the machine writing about
// itself, which is the one writer entitled to resolve a conflict
// rather than concede it. A Machine's status has exactly two other
// writers: the rollout conductor, granting and reclaiming reboot
// turns, and the sweeping leader, marking silent machines Lost. If
// one of them wrote between this pass's read and its write, this
// machine's observations are still the freshest thing anyone has (it
// is standing on the hardware), so the answer is to retry against a
// fresh read rather than discard the pass. The merge honors each
// condition's owner: the conductor's grant rides in from the fresh
// copy exactly as written, present or absent, with its transition
// time untouched (the rollout's stall clock measures from it), and
// every other field is this pass's own observation. A Lost verdict
// needs no special handling: overwriting it is precisely how a
// machine announces it is back.
//
// before is the status the object carried when the pass began,
// rendered as the JSON a write would send. When this pass observed
// exactly that, nothing is written at all: a settled machine's
// report is the same every ten seconds, and sending it anyway would
// make the API server, and every etcd leader behind it, process a
// write that changes nothing. The kubelet applies the same restraint
// to Node status, and the machine's liveness doesn't ride on this
// write anyway; that is the heartbeat lease's job. Skipping against
// a stale working copy is safe for the same reason every skipped
// event is: whatever made the server's copy differ arrives on the
// watch, and the pass it triggers sees the difference and writes.
//
// One retry is enough. A second conflict means the object is
// changing faster than this pass can read it, and the write that
// beat us is already queued on the watch, so the pass it triggers
// will publish moments from now.
func publishOwnStatus(c *apiClient, m *machine.Machine, status *machine.MachineStatus, before []byte) error {
	after, err := json.Marshal(status)
	if err == nil && bytes.Equal(before, after) {
		return nil
	}

	err = publishStatus(c, m, status)
	if !errors.Is(err, errConflict) {
		return err
	}
	fresh, gerr := getMachine(c, m.Metadata.Name)
	if gerr != nil {
		return err
	}
	status.Conditions = machine.RemoveCondition(slices.Clone(status.Conditions), rebootApprovedCondition)
	if grant := machine.FindCondition(fresh.Status.Conditions, rebootApprovedCondition); grant != nil {
		status.Conditions = append(status.Conditions, *grant)
	}
	return publishStatus(c, fresh, status)
}

// watchMachines turns the API server's watch mechanism into a channel
// of fresh Machine objects. A watch is an ordinary GET with
// ?watch=true: the response never ends, and each line of it is a JSON
// event like {"type": "MODIFIED", "object": {…}}, pushed the moment
// the object changes. This is the mechanism informers, kubectl get -w,
// and every controller's responsiveness are built on.
//
// The watch spans the whole Machine collection, not just this
// machine's object. The sweeping leader derives the Cluster's status
// from every Machine (fleet.go), so another machine's transition is
// exactly as much a reason to look as this machine's own; main.go
// explains why the extra wakeups are safe and quiet.
//
// resourceVersion tells the server where to resume so no change is
// missed between reconnects; when history has been compacted away the
// server says 410 Gone. Stream drops are routine too (the server ends
// watches on its own schedule). Both recover the same way, the one
// informers use: list the collection and watch from the *list's*
// resourceVersion, which is the current revision of the world. A
// single object's version would not do here: it is the revision of
// that object's own last write, and on a quiet object that can be old
// enough to have been compacted away, which would earn another 410
// and strand the loop. This machine's own copy from the recovery list
// is delivered as an event, so the caller's working copy is refreshed
// along the way.
//
// allowWatchBookmarks asks the server to send an occasional BOOKMARK
// event: no object change, just "you are current through version X."
// A watch on a quiet fleet would otherwise sit on an ever-staler
// resourceVersion, and the next reconnect would be more likely to
// find that version compacted away (the 410 above). Bookmarks keep
// the resume point fresh for free; informers request them for
// exactly this reason.
func watchMachines(c *apiClient, name, resourceVersion string, events chan<- *machine.Machine) {
	for {
		path := machinesPath +
			"?watch=true&allowWatchBookmarks=true" +
			"&resourceVersion=" + resourceVersion

		resp, err := c.do(http.MethodGet, path, "", nil)
		if err == nil && resp.StatusCode == http.StatusOK {
			decoder := json.NewDecoder(resp.Body)
			for {
				var event struct {
					Type   string          `json:"type"`
					Object machine.Machine `json:"object"`
				}
				if err := decoder.Decode(&event); err != nil {
					break
				}
				if event.Type == "ERROR" {
					// Usually 410 Gone wrapped in an event; fall back to
					// a fresh GET below.
					break
				}
				resourceVersion = event.Object.Metadata.ResourceVersion
				if event.Type == "BOOKMARK" {
					// A bookmark only refreshes the resume point; there
					// is no change to reconcile.
					continue
				}
				events <- &event.Object
			}
		}
		if resp != nil {
			resp.Body.Close()
		}

		retryPause()
		var list struct {
			Metadata struct {
				ResourceVersion string `json:"resourceVersion"`
			} `json:"metadata"`
			Items []machine.Machine `json:"items"`
		}
		if err := c.requestJSON(http.MethodGet, machinesPath, nil, &list); err == nil {
			resourceVersion = list.Metadata.ResourceVersion
			for i := range list.Items {
				if list.Items[i].Metadata.Name == name {
					events <- &list.Items[i]
				}
			}
		}
	}
}
