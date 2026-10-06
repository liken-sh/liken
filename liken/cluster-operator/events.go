package main

// The Events the cluster operator posts about the Machines and the
// Cluster. `kubectl describe` lists them under the status, so a person
// reads the history of a rollout, or of a machine that went silent,
// with no log to open. kubernetes/events writes them, and root plan 78
// gives the rule for a condition, an Event, and a log line.
//
// Both kinds are cluster-scoped, so their Events are in the namespace
// default. `kubectl describe` finds them there, and `kubectl events
// --for cluster/<name>` finds them only with -n default or -A.
//
//   - Each condition transition of the Cluster posts one Event, with
//     the condition's reason, after the status write lands: a fleet
//     that goes MachinesDegraded or AllMachinesReady, and a rollout
//     that goes RollingOut, RolloutStalled, or RolloutComplete.
//   - The verdicts this program writes onto a Machine post one Event
//     each on that Machine: MachineLost, RebootTurnGranted, and
//     RebootTurnReclaimed. Each one is an action this program takes on
//     an object that another program owns, so each Event names the
//     action and not the condition's reason.
//   - The flux feature's one-off actions post one Event each on the
//     Cluster: the mint of the deploy key and the planting of the
//     engine.
//
// Nothing that repeats on each sweep posts an Event: a poll of the
// release channel, an eviction the budget refuses, or the sweep
// itself.

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// The reasons of the Events that are not condition transitions. A
// transition's Event takes the condition's own reason.
const (
	reasonMachineLost         = "MachineLost"
	reasonRebootTurnGranted   = "RebootTurnGranted"
	reasonRebootTurnReclaimed = "RebootTurnReclaimed"
	reasonFluxDeployKeyMinted = "FluxDeployKeyMinted"
	reasonFluxEngineSeeded    = "FluxEngineSeeded"
)

// machineReference answers the object reference of a Machine, for an
// Event about it.
func machineReference(m *machine.Machine) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: api.APIVersion, Kind: machineKind,
		Name: m.Metadata.Name, UID: m.Metadata.UID,
	}
}

// clusterReference answers the object reference of the Cluster, for an
// Event about it.
func clusterReference(c *cluster.Cluster) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: api.APIVersion, Kind: clusterKind,
		Name: c.Metadata.Name, UID: c.Metadata.UID,
	}
}

// postClusterTransitions posts one Event for each Cluster condition
// that transitioned from stored to written, with the condition's reason
// and message.
func postClusterTransitions(recorder *events.Recorder, c *cluster.Cluster, stored, written []api.Condition) {
	for _, t := range api.Transitions(stored, written) {
		recorder.Transition(clusterReference(c), t, clusterBadStatus(t))
	}
}

// clusterBadStatus answers the status of a Cluster condition that
// needs a person, for events.Recorder.Transition. A degraded machine,
// a stalled rollout, and a declined flux teardown each need one. A
// fleet in the middle of a rollout is MachinesReady False too, and
// that needs no one.
func clusterBadStatus(c api.Condition) conditions.Status {
	switch c.Reason {
	case "MachinesDegraded", "RolloutStalled", "NotPlantedByLiken":
		return c.Status
	}
	return ""
}
