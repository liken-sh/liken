package main

// The reconcile loop's working half: observe, act, report.

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
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
// our URLs 404. Waiting beats crashing here, because the 404 is
// expected, not exceptional.
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

// ensureCluster makes the manifest's Cluster real in the cluster,
// with the same wait-for-the-CRD patience as ensureMachine and one
// extra tolerated answer: 409 Conflict. Every machine's operator
// races to create the same object at boot; the losers' POSTs conflict
// with the winner's, and the loop's next GET confirms the object
// exists, which is all anyone wanted.
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

func getCluster(c *apiClient, name string) (*machine.Cluster, error) {
	cluster := &machine.Cluster{}
	if err := c.requestJSON(http.MethodGet, clustersPath+"/"+name, nil, cluster); err != nil {
		return nil, err
	}
	return cluster, nil
}

// carryOutConvergence performs one convergence decision's side
// effects against one document's store, and returns the condition to
// publish (an I/O failure downgrades it to StagingFailed on the same
// condition type, so the report stays attached to the right
// document).
func carryOutConvergence(conv convergence, store machine.ManifestStore, what string, now time.Time) machine.Condition {
	failed := func(err error) machine.Condition {
		return machine.Condition{Type: conv.condition.Type, Status: "False", Reason: "StagingFailed", Message: err.Error()}
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
func reconcile(c *apiClient, m *machine.Machine, clusterName string) {
	now := time.Now()
	status := &machine.MachineStatus{}

	facts, err := machine.ReadFacts(machine.FactsPath)
	if err == nil {
		*status = *facts
	}
	status.Conditions = machine.SetCondition(m.Status.Conditions, factsCondition(err), now)

	// The operator's existence is the proof a staged cluster document
	// was waiting for: if this line runs, the machine joined its
	// cluster under whatever document this boot ran (cluster.go).
	settleClusterLifecycle(machine.MachineStateDir, machine.ClusterManifestPath, facts)

	status.Sysctls, err = applySysctls(m.Spec.Sysctls)
	status.Conditions = machine.SetCondition(status.Conditions, sysctlsCondition(err), now)

	// Storage compares the spec's declared roles against the facts'
	// report of where each is actually backed. The operator can't
	// observe the disks directly (claiming happened before this
	// cluster existed), so init's facts are the only source, and this
	// condition checks them against the spec.
	status.Conditions = machine.SetCondition(status.Conditions,
		machine.StorageCondition(m.Spec.Storage, status.Storage), now)

	// Convergence: does the cluster's copy of each document match what
	// this boot actuated, and if not, stage the difference for the
	// next boot (converge.go for the Machine, cluster.go for the
	// Cluster). The decisions are pure; carryOutConvergence performs
	// their side effects against each document's own store.
	// The rejection records come from the durable store, not from
	// facts: facts are the boot's frozen memory, and a rejection
	// cleared mid-boot (by an edit that reverted) must unblock a
	// retry without waiting for a reboot to refresh the facts.
	machineRejection, _ := machine.MachineManifests(machine.MachineStateDir).LoadRejection()
	conv := decideConvergence(m, facts, machineRejection, readStagedHash())
	condition := carryOutConvergence(conv, machine.MachineManifests(machine.MachineStateDir), "spec", now)
	status.Conditions = machine.SetCondition(status.Conditions, condition, now)

	// The cluster document converges through the same machinery, per
	// machine: this machine stages its own copy and reboots on its own
	// policy, and this condition is where the fleet's transient
	// disagreement about the Cluster is visible.
	var liveCluster *machine.Cluster
	if clusterName != "" {
		var cconv convergence
		if liveCluster, err = getCluster(c, clusterName); err != nil {
			cconv = convergence{condition: machine.Condition{
				Type: "ClusterConverged", Status: "Unknown", Reason: "ClusterUnavailable",
				Message: fmt.Sprintf("reading cluster %s: %v", clusterName, err),
			}}
		} else {
			clusterRejection, _ := machine.ClusterManifests(machine.MachineStateDir).LoadRejection()
			cconv = decideClusterConvergence(liveCluster, m, facts, clusterRejection,
				bootClusterHash(machine.BootClusterManifestPath), readStagedClusterHash())
		}
		condition := carryOutConvergence(cconv, machine.ClusterManifests(machine.MachineStateDir), "cluster document", now)
		status.Conditions = machine.SetCondition(status.Conditions, condition, now)
	}

	// The machine's own Node, read once for two conditions. The read
	// can fail benignly (mid-demotion, the Node is deleted and not yet
	// re-registered); that pass just skips both conditions and the
	// next one settles them.
	if node, err := getNode(c, m.Metadata.Name); err == nil {
		// NodeHealthy mirrors the Node's Ready condition onto the
		// Machine. This catches the one failure the heartbeat can't:
		// this operator runs on the host's network and talks to the
		// API directly, so it can keep reporting a healthy-looking
		// machine while the kubelet beneath it is dead. The kubelet's
		// own heartbeat (its node lease, which the node controller
		// turns into the Node's Ready condition) is the witness that
		// the machine is actually serving the cluster, not just
		// reachable.
		status.Conditions = machine.SetCondition(status.Conditions, nodeHealthyCondition(node), now)

		// Demotion cleanup (demotion.go): a follower whose Node object
		// still claims control-plane was just demoted, and the stale
		// Node — with its registered etcd membership — must go.
		d := decideDemotion(status.Role, node.Metadata.Labels, m.Spec.RebootPolicyOrDefault())
		condition := carryOutDemotion(c, m.Metadata.Name, d)
		status.Conditions = machine.SetCondition(status.Conditions, condition, now)
	}

	// Ready is the roll-up: True exactly when every other condition
	// is. The scan skips any prior Ready so the previous pass's value
	// can't affect this one.
	ready := machine.Condition{Type: "Ready", Status: "True", Reason: "Reconciled"}
	for _, condition := range status.Conditions {
		if condition.Type == "Ready" {
			continue
		}
		if condition.Status != "True" {
			ready = machine.Condition{
				Type: "Ready", Status: "False",
				Reason: "Degraded", Message: condition.Type + " is " + string(condition.Status),
			}
		}
	}
	status.Conditions = machine.SetCondition(status.Conditions, ready, now)

	// Every condition this pass publishes judged the spec at this
	// generation: the API server bumps metadata.generation on spec
	// writes only, so stamping it here is what lets a consumer tell a
	// verdict on the current spec from a verdict on one already
	// edited past.
	for i := range status.Conditions {
		status.Conditions[i].ObservedGeneration = m.Metadata.Generation
	}

	// The phase compresses the conditions into the one word a fleet
	// listing shows (phase.go), and observedAt is the heartbeat that
	// proves this machine computed it recently — the leaders treat its
	// silence as the machine's death (fleet.go).
	//
	// The heartbeat renews on a cadence, not on every pass, and the
	// reason is a feedback loop worth knowing about: this operator
	// reconciles on every watch event, including the event caused by
	// its own status write. When nothing changed, the write is a no-op
	// the API server doesn't even version — no event, and the loop
	// settles until the ticker. A timestamp that moved on every pass
	// would make every write real, every write an event, and every
	// event another pass: the operator would spin flat-out against
	// the API server forever. Aging the heartbeat past the renewal
	// window before touching it keeps event-driven passes as no-ops,
	// and the 30-second ticker guarantees the renewals keep coming.
	status.Phase = decidePhase(status.Conditions)
	status.ObservedAt = m.Status.ObservedAt
	if status.ObservedAt == nil || now.Sub(*status.ObservedAt) >= heartbeatRenewAfter {
		observedAt := now
		status.ObservedAt = &observedAt
	}

	if err := publishStatus(c, m, status); err != nil {
		fmt.Printf("publishing status: %v\n", err)
	}

	// Fleet-level work is the leaders': mark silent machines Lost and
	// keep the Cluster's headcount current. Every leader is *able* to
	// sweep, but only the one holding the lease does (lease.go), so
	// the fleet has one watcher at a time and a warm line of
	// successors. This machine's own status write has already landed,
	// so the sweep sees its own heartbeat fresh like everyone else's.
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
		if c.Status == "True" {
			return machine.Condition{Type: "NodeHealthy", Status: "True", Reason: "KubeletReady",
				Message: "the Node reports Ready; the kubelet is serving this machine to the cluster"}
		}
		return machine.Condition{Type: "NodeHealthy", Status: "False", Reason: "NodeNotReady",
			Message: fmt.Sprintf("the Node reports Ready=%s: %s", c.Status, c.Message)}
	}
	return machine.Condition{Type: "NodeHealthy", Status: "False", Reason: "NodeNotReady",
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

func factsCondition(err error) machine.Condition {
	if err != nil {
		return machine.Condition{
			Type: "FactsPublished", Status: "False",
			Reason: "FactsUnreadable", Message: err.Error(),
		}
	}
	return machine.Condition{Type: "FactsPublished", Status: "True", Reason: "FactsRead"}
}

func sysctlsCondition(err error) machine.Condition {
	if err != nil {
		return machine.Condition{
			Type: "SysctlsApplied", Status: "False",
			Reason: "ApplyFailed", Message: err.Error(),
		}
	}
	return machine.Condition{Type: "SysctlsApplied", Status: "True", Reason: "Applied"}
}

// publishStatus writes through the status subresource: a separate
// endpoint (…/machines/<name>/status) that updates *only* the status
// half of the object, so a controller can never accidentally rewrite
// the spec it takes orders from, and RBAC can grant the two halves
// separately. The write is a PUT carrying the object's resourceVersion:
// if anything else changed the object in between, the server answers
// 409 Conflict instead of applying our stale copy. The caller's next
// pass re-reads and tries again: optimistic concurrency, the way every
// Kubernetes controller handles contention.
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

// watchMachine turns the API server's watch mechanism into a channel of
// fresh Machine objects. A watch is an ordinary GET with ?watch=true:
// the response never ends, and each line of it is a JSON event like
// {"type": "MODIFIED", "object": {…}}, pushed the moment the object
// changes. This is the mechanism informers, kubectl get -w, and every
// controller's responsiveness are built on.
//
// resourceVersion tells the server where to resume so no change is
// missed between reconnects; when history has been compacted away the
// server says 410 Gone, and the recovery is to re-GET the object and
// watch from its current version. Stream drops are routine (the server
// ends watches on its own schedule); the loop just reconnects.
func watchMachine(c *apiClient, name, resourceVersion string, events chan<- *machine.Machine) {
	for {
		path := machinesPath +
			"?watch=true&fieldSelector=metadata.name%3D" + name +
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
				events <- &event.Object
			}
		}
		if resp != nil {
			resp.Body.Close()
		}

		retryPause()
		if current, err := getMachine(c, name); err == nil {
			resourceVersion = current.Metadata.ResourceVersion
			events <- current
		}
	}
}
