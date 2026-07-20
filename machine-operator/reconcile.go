package main

// This file is the working half of the reconcile loop. Each pass
// observes the machine, acts on the spec, and reports status.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// carryOutConvergence performs one convergence decision's side
// effects against one document's store, and returns the condition
// to publish. An I/O failure downgrades the condition to
// StagingFailed on the same condition type, so the report stays
// attached to the right document.
func carryOutConvergence(conv convergence, store machine.ManifestStore, what string, now time.Time) api.Condition {
	failed := func(err error) api.Condition {
		return api.Condition{Type: conv.condition.Type, Status: api.ConditionFalse, Reason: "StagingFailed", Message: err.Error()}
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
	if conv.requestRestart {
		intent := &machine.RestartIntent{
			Reason:      "applying the staged " + what,
			RequestedAt: now,
		}
		if err := machine.WriteRestartIntent(machine.OperatorRunDir, intent); err != nil {
			return failed(err)
		}
		fmt.Printf("requested a k3s restart to apply %s %.12s\n", what, conv.hash)
	}
	if conv.requestLoad {
		intent := &machine.ModulesIntent{
			Reason:       "loading the staged " + what + "'s added modules",
			ManifestHash: conv.hash,
			RequestedAt:  now,
		}
		if err := machine.WriteModulesIntent(machine.OperatorRunDir, intent); err != nil {
			return failed(err)
		}
		fmt.Printf("requested a live module load to apply %s %.12s\n", what, conv.hash)
	}
	return conv.condition
}

// disruptions is one pass's running record of what has already
// started: whether some document requested the reboot, and whether
// a drain is holding one back. The documents pass through the gate
// in a fixed order: the Machine's spec, the cluster document, the
// system release, the registry credentials, and finally the
// demotion. The restart suppression in gate depends on this order.
// A reboot requested by an earlier document silences a later
// document's restart, never the reverse.
type disruptions struct {
	draining  bool
	rebooting bool
}

// gate intercepts one document's convergence decision on its way to
// its side effects. A reboot already requested this pass covers any
// restart: the boot path re-renders everything a restart would have
// applied, so a second intent would only add noise. (Init also
// prefers the reboot file when both exist, so this guard is not
// strictly needed, but it does no harm.) A granted reboot goes
// through the drain first (drain.go): the node is cordoned and
// emptied before the intent is written, so workloads move to other
// nodes instead of being killed by the reboot. A pass whose Node
// read failed skips the drain, because during a demotion there is
// no Node to cordon, and the reboot must still happen.
func (d *disruptions) gate(c *kubernetes.Client, node *nodeObject, nodeErr error, t turn, now time.Time, conv convergence) convergence {
	conv.requestRestart = conv.requestRestart && !d.rebooting
	if conv.requestReboot && t == turnGranted && nodeErr == nil {
		conv = gateThroughDrain(c, node, conv, now)
		d.draining = d.draining || !conv.requestReboot
	}
	d.rebooting = d.rebooting || conv.requestReboot
	return conv
}

// reconcile is one full pass of the operator's job, always
// starting from the current state: read the facts init left, apply
// the spec's sysctls, read back what actually holds, and publish
// all of it as status. It deliberately keeps no memory between
// passes. Every value in the status it writes was observed moments
// ago, which is what the Kubernetes convention means by status
// being reconstructible.
func reconcile(c *kubernetes.Client, m *machine.Machine, clusterName string, f *fetcher) {
	now := time.Now()

	// This records what the object held before this pass touched
	// anything. It must be captured now, because it cannot be
	// captured later: SetCondition edits the slice it is given, so
	// the conditions this pass builds share their backing array with
	// m.Status, and by publish time the two are the same list. This
	// snapshot is what lets the publish step below skip a write that
	// would change nothing.
	before, _ := json.Marshal(&m.Status)

	status := &machine.MachineStatus{}

	facts, err := machine.ReadFacts(machine.FactsPath)
	if err == nil {
		*status = *facts
	}
	status.Conditions = api.SetCondition(m.Status.Conditions, factsCondition(err), now)

	// The operator's own existence is the evidence that promotes a
	// staged cluster document. If this line runs, the machine joined
	// its cluster under whatever document this boot ran (cluster.go).
	// The same evidence, together with the version this boot
	// reported in the facts, promotes a system release's proving
	// boot (release.go).
	settleClusterLifecycle(machine.MachineStateDir, cluster.ClusterManifestPath, facts)
	settleSystemReleaseLifecycle(machine.MachineStateDir, facts)

	// The imports lifecycle settles on its own evidence. This is not
	// this operator's existence, but the Ready condition of every OS
	// container on this node, because the trial covers every
	// tarball the boot imported, not only the one this pod runs from
	// (imports.go).
	status.Conditions = api.SetCondition(status.Conditions,
		settleImportsLifecycle(c, machine.MachineStateDir, m.Metadata.Name, facts), now)

	status.Sysctls, err = applySysctls(machine.SysctlDir, m.Spec.Sysctls)
	status.Conditions = api.SetCondition(status.Conditions, sysctlsCondition(err), now)

	// Modules judge what the boot reported, not what the spec asks
	// for now. A freshly declared module has no outcome yet; it
	// stays SpecConverged's concern until a reboot loads it. This
	// condition is the other half of that split: SpecConverged can
	// be True, meaning the boot ran the manifest, while this
	// condition is False, because a spec the boot honored can still
	// name modules the booted image never carried.
	status.Conditions = api.SetCondition(status.Conditions,
		modulesCondition(status.Modules), now)

	// The unclaimed-hardware report deliberately has no condition,
	// even though it looks like modules and features at first
	// glance. The difference is that those judge requests: a
	// declared module that did not load is a broken promise.
	// Unclaimed devices are hardware that nobody has asked anything
	// about, and staying undriven is a normal, permanent state.
	// Every QEMU guest carries a VGA adapter that no server image
	// drives, and a headless machine with a GPU leaves it undriven
	// by design. A condition would read every one of those machines
	// as Degraded forever. So the report follows the same pattern as
	// the undeclared-disk report instead: inventory in the status
	// (hardware.unclaimed arrives live from the facts, because
	// init's uevent watcher republishes on every hot-plug), loud on
	// the console, and judged by nobody until a person declares the
	// driver. At that point, status.modules judges the request.

	// Features judge what the boot reported, on the same terms as
	// modules. The split from ClusterConverged is the point of this
	// design: the cluster document's hash proves this boot ran the
	// document that enables a feature, and this condition proves the
	// booted image could carry it out. In the middle of a rollout,
	// the fleet runs mixed releases, so the answers legitimately
	// differ from machine to machine.
	status.Conditions = api.SetCondition(status.Conditions,
		featuresCondition(status.Features), now)

	// Storage compares the spec's declared roles against the facts'
	// report of where each is actually backed. The operator cannot
	// observe the disks directly, because claiming happened before
	// this cluster existed, so init's facts are the only source, and
	// this condition checks them against the spec.
	status.Conditions = api.SetCondition(status.Conditions,
		storageCondition(m.Spec.Storage, status.Storage), now)

	// t is this machine's standing with the rollout conductor. A
	// standalone machine reboots whenever it needs to. A cluster
	// member reboots only on a granted turn. The grant is a
	// condition the conductor wrote onto this Machine (rollout.go).
	// This operator reads it, carries it along in its own status
	// writes, and never sets or clears it.
	t := turnStandalone
	if clusterName != "" {
		t = turnAwaiting
		if g := api.FindCondition(m.Status.Conditions, machine.RebootApprovedCondition); g != nil && g.Status == api.ConditionTrue {
			t = turnGranted
		}
	}

	// This reads the machine's own Node once. The read serves three
	// purposes: the NodeHealthy condition, demotion cleanup, and the
	// cordon state the drain works through. The read can fail
	// without being a problem, because during a demotion the Node is
	// deleted and not yet re-registered. A pass where the read fails
	// simply skips all three, and the next pass settles them.
	node, nodeErr := getNode(c, m.Metadata.Name)

	// The device inventory converges on the same cadence as
	// everything else: one sysfs walk per pass, published as this
	// node's ResourceSlice (dra.go). It waits on the Node read,
	// because the slice is owned by the Node's UID. A pass without a
	// Node, during a demotion, has no owner to attach inventory to,
	// and skipping is correct: the old slice is being
	// garbage-collected along with the old Node. The facts supply
	// the storage roles, which is what keeps the machine's own disks
	// out of the offer.
	if nodeErr == nil {
		publishDeviceInventory(c, node, facts)
	}

	// Convergence checks whether the cluster's copy of each document
	// matches what this boot actuated. If not, it stages the
	// difference for the next boot (converge.go for the Machine,
	// cluster.go for the Cluster, release.go for the version
	// target, registries.go for the credentials). The decisions are
	// pure functions. carryOutConvergence performs their side
	// effects against each document's own store. The rejection
	// records come from the durable store, not from facts. Facts are
	// a snapshot taken at boot, and they do not change while the
	// machine runs. But a rejection cleared in the middle of a boot,
	// by an edit that reverted, must unblock a retry without waiting
	// for a reboot to refresh the facts. Every decision passes
	// through the disruption gate on its way to its side effects,
	// and the gate depends on the order in which the documents
	// converge (see disruptions).
	disr := &disruptions{}
	machineStore := machine.MachineManifests(machine.MachineStateDir)
	machineRejection, _ := machineStore.LoadRejection()
	conv := disr.gate(c, node, nodeErr, t, now,
		decideConvergence(m, facts, machineRejection, readStagedHash(machineStore), t))
	status.Conditions = api.SetCondition(status.Conditions,
		carryOutConvergence(conv, machineStore, "spec", now), now)

	// The cluster document converges through the same machinery, per
	// machine. This machine stages its own copy and reboots on its
	// own policy, and this condition is where the fleet's temporary
	// disagreement about the Cluster becomes visible. A machine with
	// no cluster document carries no operator-authored documents at
	// all, so the version target and the registry credentials also
	// converge only on a cluster member.
	var liveCluster *cluster.Cluster
	if clusterName != "" {
		clusterStore := machine.ClusterManifests(machine.MachineStateDir)
		var cconv convergence
		cconv, liveCluster = convergeClusterDocument(c, clusterStore, clusterName, m, facts, t)
		cconv = disr.gate(c, node, nodeErr, t, now, cconv)
		status.Conditions = api.SetCondition(status.Conditions,
			carryOutConvergence(cconv, clusterStore, "cluster document", now), now)

		// The version target reads the live Cluster's release feed,
		// so it can converge only on a pass that read the Cluster.
		if liveCluster != nil {
			systemStore := machine.SystemReleases(machine.MachineStateDir)
			vconv := disr.gate(c, node, nodeErr, t, now,
				convergeSystemRelease(systemStore, liveCluster, m, facts, f, t))
			status.Conditions = api.SetCondition(status.Conditions,
				carryOutConvergence(vconv, systemStore, "system release", now), now)
		}

		credentialsStore := machine.RegistryCredentialsStore(machine.MachineStateDir)
		rconv := disr.gate(c, node, nodeErr, t, now,
			convergeRegistryCredentials(c, credentialsStore, m, facts, t))
		status.Conditions = api.SetCondition(status.Conditions,
			carryOutConvergence(rconv, credentialsStore, "registry credentials", now), now)
	}

	if nodeErr == nil {
		// NodeHealthy mirrors the Node's Ready condition onto the
		// Machine. This catches the one failure the heartbeat
		// cannot: this operator runs on the host's network and talks
		// to the API directly, so it can keep reporting a
		// healthy-looking machine while the kubelet under it is
		// dead. The kubelet's own heartbeat, its node lease, which
		// the node controller turns into the Node's Ready condition,
		// is the evidence that the machine is actually serving the
		// cluster, not merely reachable.
		status.Conditions = api.SetCondition(status.Conditions, nodeHealthyCondition(node), now)

		// Node labels reconcile live, like sysctls, but against the
		// Node object instead of the kernel (labels.go). This
		// reapplies what the spec declares, and removes what it took
		// out, which the kubelet never does on its own.
		status.Conditions = api.SetCondition(status.Conditions,
			carryOutNodeLabels(c, m.Metadata.Name, decideNodeLabels(m.Spec.NodeLabels, node)), now)

		// Demotion cleanup (demotion.go). A follower whose Node
		// object still claims control-plane was just demoted. That
		// stale Node carries a registered etcd membership, so the
		// operator must delete it.
		d := decideDemotion(status.Role, node.Metadata.Labels, m.Spec.RebootPolicyOrDefault(), t)
		condition := carryOutDemotion(c, m.Metadata.Name, d)
		status.Conditions = api.SetCondition(status.Conditions, condition, now)
		disr.rebooting = disr.rebooting || d.cleanup

		// When this operator set a cordon and no longer needs it,
		// because the reboot happened and the machine converged, the
		// node goes back to the scheduler. This applies only to
		// cordons the operator set itself: decideUncordon leaves a
		// person's cordon in place.
		if !disr.rebooting && !disr.draining && decideUncordon(node) {
			if err := c.PatchJSON(nodesPath+"/"+node.Metadata.Name, uncordonPatch()); err != nil {
				fmt.Printf("uncordoning %s: %v\n", node.Metadata.Name, err)
			} else {
				fmt.Printf("uncordoned %s; its reboot is complete\n", node.Metadata.Name)
			}
		}
	}

	// Ready is the roll-up: True exactly when every other condition
	// is True. The scan skips any prior Ready, so the previous
	// pass's value cannot affect this one. It also skips the
	// conductor's grant, because the grant is a permission, not an
	// observation about this machine's health.
	ready := api.Condition{Type: "Ready", Status: api.ConditionTrue, Reason: "Reconciled"}
	for _, condition := range status.Conditions {
		if condition.Type == "Ready" || condition.Type == machine.RebootApprovedCondition {
			continue
		}
		if condition.Status != api.ConditionTrue {
			ready = api.Condition{
				Type: "Ready", Status: api.ConditionFalse,
				Reason: "Degraded", Message: condition.Type + " is " + string(condition.Status),
			}
		}
	}
	status.Conditions = api.SetCondition(status.Conditions, ready, now)

	// Every condition this pass publishes judged the spec at this
	// generation. The API server increases metadata.generation only
	// on spec writes, so recording it here lets a consumer tell a
	// verdict on the current spec apart from a verdict on a spec
	// that has since been edited. The conductor's grant keeps its
	// own generation stamp, because it is the conductor's verdict,
	// and this writer must not overwrite it.
	for i := range status.Conditions {
		if status.Conditions[i].Type == machine.RebootApprovedCondition {
			continue
		}
		status.Conditions[i].ObservedGeneration = m.Metadata.Generation
	}

	// The phase compresses the conditions into the one word a fleet
	// listing shows (phase.go).
	status.Phase = decidePhase(status.Conditions)

	// The heartbeat renews this machine's lease, so the fleet can
	// tell that this status is current, not the final report of a
	// machine that has since died (the kubernetes package explains
	// why this is a lease and not a status field). The heartbeat is
	// deliberately separate from the status write below. Status is
	// written when the machine's state changes. The heartbeat proves
	// the reporter is alive. Combining them would make every
	// heartbeat rewrite the whole object. Either write can fail
	// while the other lands, and that is the correct outcome: the
	// machine is alive and will retry on its next pass.
	//
	// The heartbeat goes first because of what each write means to
	// the cluster operator. A machine booting into a fleet that has
	// already declared it Lost announces its liveness here, so the
	// sweeper stops writing Lost verdicts onto the very object the
	// status write below is about to update. Writing status first
	// would invite that collision on every boot.
	kubernetes.RenewHeartbeat(c, m.Metadata.Name, now)

	if err := publishOwnStatus(c, m, status, before); err != nil {
		fmt.Printf("publishing status: %v\n", err)
	}
}

// publishOwnStatus is kubernetes.PublishStatus for the machine
// writing about itself, which is the one writer entitled to resolve
// a conflict, rather than give up on the write. A Machine's status
// has exactly two other writers: the rollout conductor, granting and
// reclaiming reboot turns, and the fleet sweep, marking silent
// machines Lost. If one of them wrote between this pass's read and
// its write, this machine's observations are still the freshest
// thing anyone has, because it observes the hardware directly. So
// the answer is to retry against a fresh read, rather than discard
// the pass. The merge honors each condition's owner. The
// conductor's grant carries over from the fresh copy exactly as
// written, present or absent, with its transition time untouched
// (the rollout's stall clock measures from that time), and every
// other field is this pass's own observation. A Lost verdict needs
// no special handling: overwriting it is exactly how a machine
// announces that it is back.
//
// before is the status the object carried when the pass began,
// rendered as the JSON a write would send. When this pass observed
// exactly that, the function writes nothing at all. A settled
// machine's report is the same every ten seconds, and sending it
// anyway would make the API server, and every etcd leader behind
// it, process a write that changes nothing. The kubelet applies the
// same restraint to Node status, and the machine's liveness does
// not depend on this write anyway, because that is the heartbeat
// lease's job. Skipping against a stale working copy is safe for
// the same reason every skipped event is safe: whatever made the
// server's copy differ arrives on the watch, and the pass it
// triggers sees the difference and writes.
//
// One retry is enough. A second conflict means the object is
// changing faster than this pass can read it, and the write that
// won the race is already queued on the watch, so the pass it
// triggers will publish moments from now.
func publishOwnStatus(c *kubernetes.Client, m *machine.Machine, status *machine.MachineStatus, before []byte) error {
	after, err := json.Marshal(status)
	if err == nil && bytes.Equal(before, after) {
		return nil
	}

	err = kubernetes.PublishStatus(c, m, status)
	if !errors.Is(err, kubernetes.ErrConflict) {
		return err
	}
	fresh, gerr := kubernetes.GetMachine(c, m.Metadata.Name)
	if gerr != nil {
		return err
	}
	status.Conditions = api.RemoveCondition(slices.Clone(status.Conditions), machine.RebootApprovedCondition)
	if grant := api.FindCondition(fresh.Status.Conditions, machine.RebootApprovedCondition); grant != nil {
		status.Conditions = append(status.Conditions, *grant)
	}
	return kubernetes.PublishStatus(c, fresh, status)
}
