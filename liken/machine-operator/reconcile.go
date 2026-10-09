package main

// This file is the working half of the reconcile loop. Each pass
// observes the machine, acts on the spec, and reports status.

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// factsTree is the facts init publishes, read through the operator's
// read-only /run/liken hostPath. It is a package variable so a test can
// point it at a tempdir instead of the machine's real /run.
var factsTree = machine.FactsTree{Dir: machine.FactsDir}

// sysctlRoot is the kernel's tuning interface the pass writes. It is a
// package variable for the same reason as factsTree: a test of a whole
// pass points it at a tempdir, so the test never writes the host's
// kernel parameters.
var sysctlRoot = machine.SysctlDir

// reconcile is one full pass of the operator's job, always
// starting from the current state: read the facts init left, apply
// the spec's sysctls, read back what actually holds, and publish
// all of it as status. It deliberately keeps no memory between
// passes. Every value in the status it writes was observed moments
// ago, which is what the Kubernetes convention means by status
// being reconstructible.
//
// The pass returns the status write's error. Everything above that
// write reports itself as a condition, which is a fact about the
// machine. A failed status write is different: it is a fault in the
// operator, and it means nobody outside this pod can see what the pass
// observed. That is what the layer 2 error counter counts
// (metrics.go).
//
// The pass also records into out each step it did not finish, each
// write it made, and the earliest time a step asks to run again
// (outcome.go). The pass's client reports every answer from the API
// server there, and each step on the machine reports its own failures,
// so the loop can retry the pass when something failed (retry.go).
func reconcile(r *reader, m *machine.Machine, clusterName string, f *fetcher, mm *machineMetrics, out *passOutcome) error {
	now := time.Now()
	r = r.observedBy(out)
	c := r.client

	// This records what the object held before this pass touched
	// anything. It must be captured now, because it cannot be
	// captured later: SetCondition edits the slice it is given, so
	// the conditions this pass builds share their backing array with
	// m.Status, and by publish time the two are the same list. This
	// snapshot is what lets the publish step below skip a write that
	// would change nothing.
	before, _ := json.Marshal(&m.Status)

	// The Events of this pass compare the status it writes with the
	// stored one (events.go), so the stored conditions need their own
	// copy for the same reason.
	stored := m.Status
	stored.Conditions = slices.Clone(m.Status.Conditions)
	notes := machineEvents{r.recorder, machineReference(m)}

	status := &machine.MachineStatus{}

	facts, err := factsTree.Read()
	if err == nil {
		*status = *facts
	}
	out.failSoon("reading the facts", err)
	// The pass starts from the conditions this release owns, so one
	// that a newer release wrote drops here (ownedconditions.go).
	status.Conditions = api.SetCondition(ownedConditions(m.Status.Conditions), factsCondition(err), now)

	// The operator's own existence is the evidence that promotes a
	// staged cluster document. If this line runs, the machine joined
	// its cluster under whatever document this boot ran (cluster.go).
	// The same evidence, together with the version this boot
	// reported in the facts, promotes a system release's proving
	// boot (release.go).
	settleClusterLifecycle(machine.MachineStateDir, cluster.ClusterManifestPath, facts, out)
	settleSystemReleaseLifecycle(machine.MachineStateDir, facts, out)

	// The imports lifecycle settles on its own evidence. This is not
	// this operator's existence, but the Ready condition of every OS
	// container on this node, because the trial covers every
	// tarball the boot imported, not only the one this pod runs from
	// (imports.go).
	status.Conditions = api.SetCondition(status.Conditions,
		settleImportsLifecycle(r, machine.MachineStateDir, m.Metadata.Name, facts, out), now)

	// Both sets of kernel parameters, on every pass. Applying the
	// settings every liken machine holds is what returns a parameter
	// that something else on the machine changed, within one pass and
	// without a reboot. status.sysctls reports the two together, so an
	// operator sees every parameter liken sets and its actual value in
	// one place.
	sysctls, missing, defaultsErr, specErr := applySysctls(sysctlRoot, machine.OSSysctls, m.Spec.Sysctls, out, r.sysctls)
	status.Sysctls = sysctls
	if out != nil {
		out.sysctls, out.sysctlsMissing = sysctls, missing
	}
	status.Conditions = api.SetCondition(status.Conditions, sysctlsCondition(defaultsErr, specErr), now)

	// podStale answers whether this pod's own template predates the
	// release it is running (staleness.go). A follower that reboots
	// first always runs its new binary inside the old pod spec for a
	// while, because the OS DaemonSets update on OnDelete and only a
	// leader's boot rewrites the AddOn manifests that produce a fresh
	// template. hostEntriesCondition reads this verdict below to judge
	// a missing mount as that ordinary lag instead of a fault.
	podStale := ownPodIsStale(r, m.Metadata.Name, status.Version.Liken)

	// Host entries reconcile live too, under the same write-on-
	// divergence rule (hosts.go). The hostname is the Machine's own
	// name rather than a read of the host's hostname, because init
	// derives the kernel's hostname from this same field
	// (unix.Sethostname(m.Metadata.Name) in init/main.go) and a
	// Kubernetes object's name never changes once it exists, so the
	// two can never disagree the way they could if this program
	// depended on the pod's network namespace carrying the host's UTS
	// namespace along with it.
	hostEntries, hostsErr := applyHostEntries(hostsPath, m.Metadata.Name, m.Spec.Network.HostEntries, out)
	status.HostEntries = hostEntries
	status.Conditions = api.SetCondition(status.Conditions,
		hostEntriesCondition(m.Spec.Network.HostEntries, hostsErr, podStale), now)

	// Modules judge what the boot reported, not what the spec asks
	// for now. A freshly declared module has no outcome yet; it
	// stays SpecConverged's concern until a reboot loads it. This
	// condition is the other half of that split: SpecConverged can
	// be True, meaning the boot ran the manifest, while this
	// condition is False, because a spec the boot honored can still
	// name modules the booted image never carried.
	status.Conditions = api.SetCondition(status.Conditions,
		modulesCondition(status.Modules), now)

	// ModulesLoaded keeps its meaning: a module that loaded is
	// loaded, whatever happened to its parameters. The parameters
	// report through their own condition, so each answers one
	// question and a person reads two plain answers instead of one
	// mixed one.
	status.Conditions = api.SetCondition(status.Conditions,
		moduleParametersCondition(m.Spec.ModuleParameters, status.Modules), now)

	// The serio attachments report what init holds now, not what the
	// boot did: init's serio watch rewrites status.serio when an
	// adapter is plugged in or unplugged. An entry declared since the
	// last pass has no report yet, and SpecConverged carries it until
	// the live load declares it to init.
	status.Conditions = api.SetCondition(status.Conditions,
		serioCondition(status.Serio), now)

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

	// The radios judge the boot's report, not the spec. A join
	// happens once, at boot, on the same terms as a module load, so
	// a freshly declared wireless entry is SpecConverged's concern
	// until a reboot joins it. A machine that joined nothing and
	// still reached this line reached it over some other interface,
	// which is the degraded case plans/completed/62-wifi.md describes.
	status.Conditions = api.SetCondition(status.Conditions,
		wirelessCondition(status.Network.Interfaces), now)

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
	// deleted and not yet re-registered, and while the API server is
	// down the Node's copy does not answer (watches.go). A pass where
	// the read fails simply skips all three, and the next pass
	// settles them.
	node, nodeErr := r.node(m.Metadata.Name)

	// The device inventory converges on the same cadence as
	// everything else: one sysfs walk per pass, published as this
	// node's ResourceSlice (dra.go). It waits on the Node read,
	// because the slice is owned by the Node's UID. A pass without a
	// Node, during a demotion, has no owner to attach inventory to,
	// and skipping is correct: the old slice is being
	// garbage-collected along with the old Node. The facts supply
	// the storage roles, which is what keeps the machine's own disks
	// out of the offer.
	//
	// The serio list is the spec's and the boot record's together
	// (dra.go), set here for the DRA plugin as well, so the inventory
	// and the claims it prepares withhold the same serial lines.
	serio := serioInEffect(m.Spec.Serio, facts)
	setDeclaredSerio(serio)
	if nodeErr == nil {
		_ = publishDeviceInventory(r, node, facts, serio, mm)
	}

	// The claims the kubelet already prepared get the same treatment,
	// because a device that enumerates again moves the nodes a claim
	// delivers (cdi.go). This runs without a Node, because a prepared
	// claim is a file on this machine, and containerd reads that file at
	// every container creation.
	refreshCDISpecs(draSysfsRoot, out)

	// Convergence checks whether the cluster's copy of each document
	// matches what this boot actuated. If not, it stages the
	// difference for the next boot (converge.go for the Machine,
	// cluster.go for the Cluster, release.go for the version
	// target, registries.go for the credentials). The decisions are
	// pure functions. carryOutConvergence performs their side
	// effects against each document's own store. The rejection
	// records come from the durable store, not from facts, because the
	// store is the rejections' authority. A rejection cleared in the
	// middle of a boot, by an edit that reverted, must unblock a retry
	// the moment it lands, and the store carries that change at once.
	// Every decision passes
	// through the disruption gate on its way to its side effects,
	// and the gate depends on the order in which the documents
	// converge (see disruptions).
	disr := &disruptions{events: notes, out: out}
	machineStore := machine.MachineManifests(machine.MachineStateDir)
	machineRejection, _ := machineStore.LoadRejection()
	conv := disr.gate(r, node, nodeErr, t, now,
		decideConvergence(m, facts, machineRejection, readStagedHash(machineStore), t))
	status.Conditions = api.SetCondition(status.Conditions,
		carryOutConvergence(conv, machineStore, machine.OperatorRunDir, "spec", now, out), now)
	// Each gated document also reports itself in status.pending, so
	// a client that needs the staged hash has a field instead of a
	// condition message to read. The list rebuilds on every pass,
	// like the rest of status.
	if conv.pending != nil {
		status.Pending = append(status.Pending, *conv.pending)
	}

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
		cconv, liveCluster = convergeClusterDocument(r, clusterStore, clusterName, m, facts, t)
		cconv = disr.gate(r, node, nodeErr, t, now, cconv)
		status.Conditions = api.SetCondition(status.Conditions,
			carryOutConvergence(cconv, clusterStore, machine.OperatorRunDir, "cluster document", now, out), now)
		if cconv.pending != nil {
			status.Pending = append(status.Pending, *cconv.pending)
		}

		// The version target reads the live Cluster's release feed,
		// so it can converge only on a pass that read the Cluster.
		if liveCluster != nil {
			systemStore := machine.SystemReleases(machine.MachineStateDir)
			vconv := disr.gate(r, node, nodeErr, t, now,
				convergeSystemRelease(systemStore, liveCluster, m, facts, f, t, out))
			status.Conditions = api.SetCondition(status.Conditions,
				carryOutConvergence(vconv, systemStore, machine.OperatorRunDir, "system release", now, out), now)
			if vconv.pending != nil {
				status.Pending = append(status.Pending, *vconv.pending)
			}
		}

		credentialsStore := machine.RegistryCredentialsStore(machine.MachineStateDir)
		rconv := disr.gate(r, node, nodeErr, t, now,
			convergeRegistryCredentials(r, credentialsStore, m, facts, t))
		status.Conditions = api.SetCondition(status.Conditions,
			carryOutConvergence(rconv, credentialsStore, machine.OperatorRunDir, "registry credentials", now, out), now)
		if rconv.pending != nil {
			status.Pending = append(status.Pending, *rconv.pending)
		}
	}

	// A reboot a person asked for, which no document requires
	// (rebootrequest.go). It goes through the same gate as every
	// staged document, so it takes its turn, cordons, and drains
	// like the rest. It goes last because the gate's order decides
	// which entry liken approve-reboot offers first, and a staged
	// document is the more useful answer: approving it reboots the
	// machine and satisfies the request along the way. This runs
	// outside the cluster block above, because a standalone machine
	// can be asked to reboot too.
	rreq := disr.gate(r, node, nodeErr, t, now, decideRebootRequest(m, facts, t))
	status.Conditions = api.SetCondition(status.Conditions,
		carryOutRebootRequest(machine.OperatorRunDir, rreq, now, out), now)
	if rreq.pending != nil {
		status.Pending = append(status.Pending, *rreq.pending)
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

		// Node taints reconcile live, and go before the labels
		// (taints.go). The taints patch names the resourceVersion this
		// pass read, and the labels patch names none. Sending the
		// taints patch first spends that precondition while the
		// version it names is still current. The labels patch cannot
		// conflict with the version bump the taints patch causes,
		// because it states no version to conflict with.
		status.Conditions = api.SetCondition(status.Conditions,
			carryOutNodeTaints(c, m.Metadata.Name, decideNodeTaints(m.Spec.NodeTaints, node)), now)

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
		condition := carryOutDemotion(c, machine.OperatorRunDir, node, d, out)
		status.Conditions = api.SetCondition(status.Conditions, condition, now)
		disr.rebooting = disr.rebooting || d.cleanup

		// When this operator set a cordon and no longer needs it,
		// because the reboot happened and the machine converged, the
		// node goes back to the scheduler. This applies only to
		// cordons the operator set itself: decideUncordon leaves a
		// person's cordon in place.
		if !disr.rebooting && !disr.draining && decideUncordon(node) {
			if err := kubernetes.PatchJSON(c, nodesPath+"/"+node.Metadata.Name, uncordonPatch()); err != nil {
				fmt.Printf("uncordoning %s: %v\n", node.Metadata.Name, err)
			} else {
				fmt.Printf("uncordoned %s; its reboot is complete\n", node.Metadata.Name)
				notes.normal(reasonUncordoned, "uncordoned the Node "+node.Metadata.Name+"; its reboot is complete")
			}
		}
	}

	// Ready is the roll-up: True exactly when every other condition
	// is True, with a reason that agrees with the phase (phase.go).
	status.Conditions = api.SetCondition(status.Conditions, readyCondition(status.Conditions), now)

	// Every condition this pass publishes judged the spec at this
	// generation. The API server increases metadata.generation only
	// on spec writes, so recording it here lets a consumer tell a
	// verdict on the current spec apart from a verdict on a spec
	// that has since been edited. The conductor's grant keeps its
	// own generation stamp, because it is the conductor's verdict,
	// and this writer must not overwrite it. The status carries the
	// same stamp at its top, where clients that only ask "has the
	// operator seen my edit yet" expect to find it.
	status.ObservedGeneration = m.Metadata.Generation
	for i := range status.Conditions {
		if status.Conditions[i].Type == machine.RebootApprovedCondition {
			continue
		}
		status.Conditions[i].ObservedGeneration = m.Metadata.Generation
	}

	// The phase compresses the conditions into the one word a fleet
	// listing shows (phase.go).
	status.Phase = decidePhase(status.Conditions)

	// The metrics read the very status this pass is about to
	// publish, so a graph and a `kubectl get machine -o yaml` always
	// answer from the same observation (metrics.go).
	mm.observeStatus(status)

	err = publishOwnStatus(r, m, status, before)
	if err != nil {
		fmt.Printf("publishing status: %v\n", err)
		return err
	}
	postStatusEvents(notes, &stored, status)
	return nil
}
