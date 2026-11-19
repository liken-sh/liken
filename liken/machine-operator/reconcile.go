package main

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

// factsTree reads the facts that init publishes, through the
// operator's read-only /run/liken hostPath. A test points it at a
// temporary directory.
var factsTree = machine.FactsTree{Dir: machine.FactsDir}

// sysctlRoot is the directory the pass writes kernel parameters to.
// A test of a whole pass points it at a temporary directory, so the
// test does not change the host's kernel parameters.
var sysctlRoot = machine.SysctlDir

// reconcile runs one pass. It reads the facts that init published,
// applies the spec's sysctls and the other live settings, reads back
// the values, and publishes the result as the Machine's status. It
// keeps no state between passes, so every value in the status was read
// during this pass.
//
// A failure in a step before the status write becomes a condition,
// because it describes the machine. reconcile returns only the error
// of the status write, because after that failure nobody outside this
// pod can see what the pass read. metrics.go counts those errors.
//
// out records each step that did not finish, each write the pass made,
// and the earliest time a step must run again (outcome.go). The loop
// reads it to decide when to retry the pass (retry.go).
func reconcile(r *reader, m *machine.Machine, clusterName string, f *fetcher, mm *machineMetrics, out *passOutcome) error {
	now := time.Now()
	// quiet reads and writes Disruptions without reporting into out. A
	// cluster that runs an older release with no Disruption CRD or RBAC
	// refuses those requests on every pass, and that refusal is not a
	// failure of the pass (requests.go).
	quiet := r
	r = r.observedBy(out)
	c := r.client

	// The pass copies the status before it changes anything, because
	// SetCondition edits the slice it is given and the conditions built
	// below share a backing array with m.Status. publishOwnStatus
	// compares with this copy to skip a write that changes nothing.
	before, _ := json.Marshal(&m.Status)

	// postStatusEvents compares the new conditions with these
	// (events.go), so they need their own copy for the same reason.
	stored := m.Status
	stored.Conditions = slices.Clone(m.Status.Conditions)
	notes := machineEvents{r.recorder, machineReference(m)}

	status := &machine.MachineStatus{}

	facts, err := factsTree.Read()
	if err == nil {
		*status = *facts
	}
	out.failSoon("reading the facts", err)
	// The pass keeps only the conditions that this release owns, so a
	// condition that a newer release wrote is dropped (ownedconditions.go).
	status.Conditions = api.SetCondition(ownedConditions(m.Status.Conditions), factsCondition(err), now)

	// A running operator proves that the machine joined its cluster
	// with the cluster document this boot used, so the pass promotes
	// that document to proven (cluster.go). It also promotes the system
	// release that the facts say this boot ran (release.go).
	settleClusterLifecycle(machine.MachineStateDir, cluster.ClusterManifestPath, facts, out)
	settleSystemReleaseLifecycle(machine.MachineStateDir, facts, out)

	// The pass promotes the record of the image tarballs that this boot
	// imported only when every container on this node that runs a
	// liken.sh/ image is Ready (imports.go).
	status.Conditions = api.SetCondition(status.Conditions,
		settleImportsLifecycle(r, machine.MachineStateDir, m.Metadata.Name, facts, out), now)

	// Every pass applies both the default sysctls and the spec's, so a
	// parameter that something else changed returns to its value within
	// one pass, with no reboot. status.sysctls reports both sets.
	//
	// A parameter of machine.ModuleSysctls whose file does not exist
	// yet is not a failure. It goes in the missing list, and the sysctl
	// check (backstop.go) starts a pass when the module loads and the
	// file appears.
	defaults, absent := machine.DefaultSysctls(sysctlRoot, m.Spec.Sysctls)
	sysctls, missing, defaultsErr, specErr := applySysctls(sysctlRoot, defaults, m.Spec.Sysctls, out, r.sysctls)
	missing = append(missing, absent...)
	status.Sysctls = sysctls
	if out != nil {
		out.sysctls, out.sysctlsMissing = sysctls, missing
	}
	status.Conditions = api.SetCondition(status.Conditions, sysctlsCondition(defaultsErr, specErr), now)

	// podStale is true when this pod's template is older than the
	// release it runs. A follower that reboots into a new release
	// before a leader does runs the new binary in the old pod spec
	// until a leader's boot rewrites the manifests (staleness.go).
	// hostEntriesCondition then reports a missing mount as that delay
	// and not as a fault.
	podStale := ownPodIsStale(r, m.Metadata.Name, status.Version.Liken)

	// The hostname for the host entries is the Machine's name, not read
	// from the host. init sets the kernel's hostname from the same name
	// (unix.Sethostname in init/main.go), and an object's name cannot
	// change, so the two agree. Reading the host's hostname would work
	// only while the pod shares the host's UTS namespace.
	hostEntries, hostsErr := applyHostEntries(hostsPath, m.Metadata.Name, m.Spec.Network.HostEntries, out)
	status.HostEntries = hostEntries
	status.Conditions = api.SetCondition(status.Conditions,
		hostEntriesCondition(m.Spec.Network.HostEntries, hostsErr, podStale), now)

	// The modules condition reads what the boot reported, not the
	// current spec. A module declared since the boot has no result yet,
	// and SpecConverged reports it as pending until a reboot loads it.
	// So SpecConverged can be True while this condition is False: the
	// boot used the current manifest, and the booted image does not
	// contain a module that the manifest lists.
	status.Conditions = api.SetCondition(status.Conditions,
		modulesCondition(status.Modules), now)

	// Module parameters have their own condition, so a failed parameter
	// does not make ModulesLoaded False.
	status.Conditions = api.SetCondition(status.Conditions,
		moduleParametersCondition(m.Spec.ModuleParameters, status.Modules), now)

	// The serio condition reads the attachments that init has now, not
	// at boot, because init rewrites status.serio when an adapter is
	// plugged in or unplugged. An entry declared since the last pass
	// has no status yet, and SpecConverged reports it as pending until
	// init attaches it in a live module load (converge.go).
	status.Conditions = api.SetCondition(status.Conditions,
		serioCondition(status.Serio), now)

	// status.hardware.unclaimed has no condition, because a device with
	// no declared driver is often intended. Every QEMU guest has a VGA
	// adapter that no server image drives, and a headless machine
	// leaves its GPU without a driver, so a condition would mark those
	// machines Degraded permanently. When a person declares the driver,
	// the modules condition reports whether it loaded.

	// The features condition also reads what the boot reported.
	// ClusterConverged shows that this boot used the cluster document
	// that enables a feature, and this condition shows whether the
	// booted image can run it. During a rollout the machines run
	// different releases, so this condition can differ between them.
	status.Conditions = api.SetCondition(status.Conditions,
		featuresCondition(status.Features), now)

	// The machine joins wireless networks only at boot, so the wireless
	// condition also reads the boot's report, and SpecConverged reports
	// an entry declared since the boot as pending until a reboot. A
	// machine whose wireless join failed but that still runs this
	// operator reached the cluster over another interface.
	status.Conditions = api.SetCondition(status.Conditions,
		wirelessCondition(status.Network.Interfaces), now)

	status.Conditions = api.SetCondition(status.Conditions,
		clockCondition(status.Time), now)

	// The storage condition compares the spec's storage roles with the
	// disks that init assigned to them at boot, as the facts report
	// them. The operator does not inspect the disks itself.
	status.Conditions = api.SetCondition(status.Conditions,
		storageCondition(m.Spec.Storage, status.Storage), now)

	// t says whether this machine may reboot. A machine with no
	// cluster reboots whenever it needs to. A cluster member reboots
	// only when it has a reboot turn, which is its entry in the
	// Cluster's status.rebootTurns (turn.go). cluster-operator
	// also writes a RebootApproved condition for a
	// machine-operator of the previous release. This operator keeps
	// that condition in its status writes and never sets or clears it.
	t := turnStandalone
	if clusterName != "" {
		t = turnAwaiting
		if holdsTurn(r, clusterName, m.Metadata.Name) {
			t = turnGranted
		}
	}

	// The pass reads the machine's Node once, for the NodeHealthy
	// condition, the demotion cleanup, and the cordon that the drain
	// uses. The read can fail on a healthy machine. During a demotion
	// the Node is deleted and not yet registered again, and while the
	// API server is down the read fails (watches.go). A pass whose read
	// fails skips the steps that need the Node, and a later pass runs
	// them.
	node, nodeErr := r.node(m.Metadata.Name)

	// The pass publishes the devices it finds in sysfs as this node's
	// ResourceSlice (dra.go). The Node owns the slice, so the publish
	// needs the Node's UID and runs only when the Node read succeeds.
	// During a demotion there is no Node, and the old slice is
	// garbage-collected with the old Node.
	//
	// The DRA plugin reads the same serio list and disk protection, so
	// the slice and the claims that the plugin prepares leave out the
	// same serial lines and disks. The protected disks are those that
	// back a storage role, or every disk when the facts could not be
	// read (protection.go).
	serio := serioInEffect(m.Spec.Serio, facts)
	setDeclaredSerio(serio)
	setPlatformProtection(protectionOf(facts))
	// A device that enumerates again gets new device nodes, so the pass
	// also rewrites the CDI files of claims that the kubelet already
	// prepared (cdi.go). The files are on this machine, so this needs no
	// Node. It runs before the slice is published, because it marks the
	// claims whose device was unplugged, and the publish taints those
	// devices (unplugged.go).
	refreshCDISpecs(draSysfsRoot, out, notes)
	if nodeErr == nil {
		_ = publishDeviceInventory(r, node, facts, serio, mm)
	}

	// For each document, the pass compares the cluster's copy with the
	// copy this boot used, and stages the new one for the next boot
	// when they differ: converge.go for the Machine, cluster.go for the
	// Cluster, release.go for the system release, and registries.go for
	// the registry credentials. The decide functions have no side
	// effects. disr.gate decides whether the machine may reboot or
	// drain for the decision, and carryOutConvergence then writes the
	// files. The order of the documents below matters to the gate
	// (disruptions.go).
	//
	// The pass reads a rejection from the document's store and not from
	// the facts, because init writes the facts' copy only at boot. When
	// an edit that reverts a rejected spec clears the rejection during
	// a boot, the store shows the change at once, and the retry starts
	// at once.
	//
	// requests are the machine's Disruptions: requests from people and
	// this operator's own request (requests.go). When the read fails,
	// the pass acts on no request and leaves its own request unchanged.
	// The decisions read a copy of the Machine that includes the
	// approval of the operator's own request.
	requests, requestsErr := quiet.requests(m.Metadata.Name)
	if requestsErr != nil {
		fmt.Printf("reading the Disruptions of Machine %s: %v\n", m.Metadata.Name, requestsErr)
	}
	decided := withRequestApprovals(m, requests)

	bootID := ""
	if facts != nil {
		bootID = machine.BootID(facts.Boot)
	}
	disr := &disruptions{events: notes, out: out,
		holding:  holdingRequests(requests, m.Spec.RebootPolicyOrDefault(), bootID),
		handover: handoverRequest(requests, m.Spec.RebootPolicyOrDefault(), bootID)}
	machineStore := machine.MachineManifests(machine.MachineStateDir)
	machineRejection, _ := machineStore.LoadRejection()
	conv := disr.gate(r, node, nodeErr, t, now,
		decideConvergence(decided, facts, machineRejection, readStagedHash(machineStore), t))
	status.Conditions = api.SetCondition(status.Conditions,
		carryOutConvergence(conv, machineStore, machine.OperatorRunDir, "spec", now, out), now)
	// A document that waits for a reboot is also listed in
	// status.pending, so a client reads the staged hash from a field
	// and not from a condition message.
	if conv.pending != nil {
		status.Pending = append(status.Pending, *conv.pending)
	}

	// Each machine stages its own copy of the cluster document and
	// reboots under its own rebootPolicy, so during a change the
	// machines of a fleet can run different versions of the Cluster,
	// and ClusterConverged shows which. The system release and the
	// registry credentials come from the Cluster, so they converge only
	// on a cluster member.
	var liveCluster *cluster.Cluster
	if clusterName != "" {
		clusterStore := machine.ClusterManifests(machine.MachineStateDir)
		var cconv convergence
		cconv, liveCluster = convergeClusterDocument(r, clusterStore, clusterName, decided, facts, t)
		cconv = disr.gate(r, node, nodeErr, t, now, cconv)
		status.Conditions = api.SetCondition(status.Conditions,
			carryOutConvergence(cconv, clusterStore, machine.OperatorRunDir, "cluster document", now, out), now)
		if cconv.pending != nil {
			status.Pending = append(status.Pending, *cconv.pending)
		}

		// The target system release comes from the Cluster's release
		// feed, so it converges only in a pass that read the Cluster.
		if liveCluster != nil {
			systemStore := machine.SystemReleases(machine.MachineStateDir)
			vconv := disr.gate(r, node, nodeErr, t, now,
				convergeSystemRelease(systemStore, liveCluster, decided, facts, f, t, out))
			status.Conditions = api.SetCondition(status.Conditions,
				carryOutConvergence(vconv, systemStore, machine.OperatorRunDir, "system release", now, out), now)
			if vconv.pending != nil {
				status.Pending = append(status.Pending, *vconv.pending)
			}
		}

		credentialsStore := machine.RegistryCredentialsStore(machine.MachineStateDir)
		rconv := disr.gate(r, node, nodeErr, t, now,
			convergeRegistryCredentials(r, credentialsStore, decided, facts, t))
		status.Conditions = api.SetCondition(status.Conditions,
			carryOutConvergence(rconv, credentialsStore, machine.OperatorRunDir, "registry credentials", now, out), now)
		if rconv.pending != nil {
			status.Pending = append(status.Pending, *rconv.pending)
		}
	}

	// A person can request a reboot that no document needs
	// (rebootrequest.go). It goes through disr.gate like a staged
	// document, so it waits for its reboot turn, cordons, and drains.
	// It comes last because `liken approve-reboot` offers the entries in
	// this order, and approving a staged document first also satisfies
	// the request with the same reboot. A machine with no cluster can
	// take a reboot request too.
	rreq := disr.gate(r, node, nodeErr, t, now, decideRebootRequest(m, facts, t, requests))
	status.Conditions = api.SetCondition(status.Conditions,
		carryOutRebootRequest(machine.OperatorRunDir, rreq, now, out), now)
	if rreq.pending != nil {
		status.Pending = append(status.Pending, *rreq.pending)
	}

	if nodeErr == nil {
		// NodeHealthy copies the Node's Ready condition to the
		// Machine. This operator runs on the host network and calls
		// the API server directly, so it can keep running after the
		// kubelet has died. The kubelet reports Ready, and the node
		// controller sets it to Unknown when the kubelet stops
		// renewing its node lease, so Ready shows that the kubelet
		// still runs.
		status.Conditions = api.SetCondition(status.Conditions, nodeHealthyCondition(node), now)

		// The taints patch sends the resourceVersion that this pass
		// read, so it goes first, while that version is still current
		// (taints.go). The labels patch sends no resourceVersion, so
		// the version change that the taints patch causes cannot make
		// it conflict.
		status.Conditions = api.SetCondition(status.Conditions,
			carryOutNodeTaints(c, m.Metadata.Name, decideNodeTaints(m.Spec.NodeTaints, node)), now)

		// The pass sets the spec's labels on the Node again on every
		// pass and removes the labels that the spec dropped, which the
		// kubelet does not do (labels.go).
		status.Conditions = api.SetCondition(status.Conditions,
			carryOutNodeLabels(c, m.Metadata.Name, decideNodeLabels(m.Spec.NodeLabels, node)), now)

		// A follower whose Node still has the control-plane label was a
		// leader before this boot, and its etcd member is still
		// registered. The operator deletes the Node, and k3s then
		// removes the etcd member (demotion.go).
		d := decideDemotion(status.Role, node.Metadata.Labels, m.Spec.RebootPolicyOrDefault(), t)
		condition := carryOutDemotion(c, machine.OperatorRunDir, node, d, out)
		status.Conditions = api.SetCondition(status.Conditions, condition, now)
		disr.rebooting = disr.rebooting || d.cleanup

		// This operator's own Disruption lists what waits for a reboot
		// in this pass. A pass that did not read the Node cannot decide
		// the demotion, so it leaves the request unchanged. A machine
		// with no cluster has no cluster-operator to grant a reboot
		// turn, so it makes no request.
		if clusterName != "" && requestsErr == nil {
			syncOwnRequest(quiet, m, requests, ownRequest(m.Metadata.Name, status.Pending, d.condition))
		}

		// When no reboot or drain is in progress, the operator removes
		// a cordon that it set. decideUncordon leaves a cordon that a
		// person set.
		if !disr.rebooting && !disr.draining && decideUncordon(node) {
			if err := kubernetes.PatchJSON(c, nodesPath+"/"+node.Metadata.Name, uncordonPatch()); err != nil {
				fmt.Printf("uncordoning Node %s: %v\n", node.Metadata.Name, err)
			} else {
				var boot *time.Time
				if facts != nil {
					boot = facts.Boot.Time
				}
				message := uncordonMessage(node, boot)
				fmt.Println(message)
				notes.normal(reasonUncordoned, message)
			}
		}
	}

	if requestsErr == nil && facts != nil {
		settlePeopleRequests(quiet, m, requests, bootID, now)
		settleHolds(quiet, requests, disr.holding, disr.drained, now)
	}

	// Ready is True when every other condition is True, and False
	// otherwise. It skips RebootApproved and the conditions that
	// reportsOnly lists. Its reason matches the phase (phase.go).
	status.Conditions = api.SetCondition(status.Conditions, readyCondition(status.Conditions), now)

	// Each condition that this pass set records the generation of the
	// spec it evaluated, so a reader can tell a result for the current
	// spec from a result for an older one. A condition that this pass
	// kept from an earlier pass, and cluster-operator's RebootApproved,
	// keep their own (observedgeneration.go).
	status.ObservedGeneration = m.Metadata.Generation
	stampObservedGeneration(status.Conditions, m.Metadata.Generation, nodeErr == nil)

	status.Phase = decidePhase(status.Conditions)

	// The metrics read the status that this pass publishes, so they
	// match `kubectl get machine -o yaml` (metrics.go).
	mm.observeStatus(status)

	err = publishOwnStatus(r, m, status, before)
	if err != nil {
		fmt.Printf("publishing the status of Machine %s: %v\n", m.Metadata.Name, err)
		return err
	}
	postStatusEvents(notes, &stored, status)
	return nil
}
