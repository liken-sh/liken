package main

// The reconcile loop's working half: observe, act, report.

import (
	"encoding/json"
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
		if err != errNotFound {
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
			fmt.Printf("created machine %s from %s\n", seed.Metadata.Name, machine.ManifestPath)
			continue // re-GET so we return the server's copy, resourceVersion and all
		}
		if err == errNotFound {
			fmt.Println("machine API not served yet; waiting")
			time.Sleep(5 * time.Second)
			continue
		}
		return nil, err
	}
}

// reconcile is one full pass of the operator's job, always from
// absolute state: read the facts init left, actuate the spec's sysctls,
// read back what actually holds, and publish all of it as status. It
// deliberately keeps no memory between passes: every value in the
// status it writes was observed moments ago, which is what the
// Kubernetes convention means by status being reconstructible.
func reconcile(c *apiClient, m *machine.Machine) {
	now := time.Now()
	status := &machine.MachineStatus{}

	facts, err := machine.ReadFacts(machine.FactsPath)
	if err == nil {
		*status = *facts
	}
	status.Conditions = machine.SetCondition(m.Status.Conditions, factsCondition(err), now)

	status.Sysctls, err = applySysctls(m.Spec.Sysctls)
	status.Conditions = machine.SetCondition(status.Conditions, sysctlsCondition(err), now)

	// Storage compares the spec's declared roles against the facts'
	// report of where each is actually backed. The operator can't
	// observe the disks directly (claiming happened before this
	// cluster existed), so init's facts are the only source, and this
	// condition checks them against the spec.
	status.Conditions = machine.SetCondition(status.Conditions,
		machine.StorageCondition(m.Spec.Storage, status.Storage), now)

	// Convergence: does the cluster's spec match what this boot
	// actuated, and if not, stage the difference for the next boot
	// (converge.go). The decision is pure; these lines are its hands.
	conv := decideConvergence(m, facts, readStagedHash())
	if conv.stage {
		if err := machine.WriteStaged(machine.MachineStateDir, conv.manifest); err != nil {
			conv = convergence{condition: notConverged("StagingFailed", err.Error())}
		} else {
			fmt.Printf("staged spec %.12s for the next boot\n", conv.hash)
		}
	}
	if conv.requestReboot {
		intent := &machine.RebootIntent{
			Reason:       "applying the staged spec",
			ManifestHash: conv.hash,
			RequestedAt:  now,
		}
		if err := machine.WriteRebootIntent(machine.OperatorRunDir, intent); err != nil {
			conv.condition = notConverged("StagingFailed", err.Error())
		} else {
			fmt.Printf("requested a reboot to apply spec %.12s\n", conv.hash)
		}
	}
	status.Conditions = machine.SetCondition(status.Conditions, conv.condition, now)

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
				Reason: "Degraded", Message: condition.Type + " is " + condition.Status,
			}
		}
	}
	status.Conditions = machine.SetCondition(status.Conditions, ready, now)

	if err := publishStatus(c, m, status); err != nil {
		fmt.Printf("publishing status: %v\n", err)
	}
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

		time.Sleep(5 * time.Second)
		if current, err := getMachine(c, name); err == nil {
			resourceVersion = current.Metadata.ResourceVersion
			events <- current
		}
	}
}
