package main

// This file defines the Events that the cluster operator posts about
// Machines and the Cluster, so `kubectl describe` shows what happened
// to an object without a person reading the operator's log. The
// kubernetes/events package writes them.
//
// Machine and Cluster are cluster-scoped, so their Events are in the
// default namespace. `kubectl describe` finds them there, but
// `kubectl events --for cluster/<name>` finds them only with
// `-n default` or `-A`.
//
//   - Each condition transition of the Cluster posts one Event with
//     the condition's reason, after the status write succeeds.
//   - Each change the operator makes to a Machine posts one Event on
//     that Machine: MachineLost, RebootTurnGranted, or
//     RebootTurnReclaimed. These are actions on an object that
//     another program owns, so the reason names the action and not a
//     condition.
//   - The flux feature posts one Event on the Cluster when it creates
//     the deploy key and one when it creates the Flux controllers
//     again from the embedded Flux manifest.
//
// Work that repeats on every pass posts no Event: polling the release
// channel, an eviction that the disruption budget refuses, and the
// pass itself.

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// The reasons of Events that do not come from a condition transition.
// A transition's Event uses the condition's own reason.
const (
	reasonMachineLost         = "MachineLost"
	reasonRebootTurnGranted   = "RebootTurnGranted"
	reasonRebootTurnReclaimed = "RebootTurnReclaimed"
	reasonFluxDeployKeyMinted = "FluxDeployKeyMinted"
	reasonFluxEngineSeeded    = "FluxEngineSeeded"
)

// machineReference returns the object reference of a Machine, for an
// Event about it.
func machineReference(m *machine.Machine) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: api.APIVersion, Kind: machineKind,
		Name: m.Metadata.Name, UID: m.Metadata.UID,
	}
}

// clusterReference returns the object reference of the Cluster, for an
// Event about it.
func clusterReference(c *cluster.Cluster) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: api.APIVersion, Kind: clusterKind,
		Name: c.Metadata.Name, UID: c.Metadata.UID,
	}
}

// postClusterTransitions posts one Event for each Cluster condition
// that transitioned between the stored and the written conditions,
// with the condition's reason and message.
func postClusterTransitions(recorder *events.Recorder, c *cluster.Cluster, stored, written []api.Condition) {
	for _, t := range api.Transitions(stored, written) {
		recorder.Transition(clusterReference(c), t, clusterBadStatus(t))
	}
}

// clusterBadStatus returns the condition's status when its reason
// needs a person to act, and "" otherwise, for
// events.Recorder.Transition. NotPlantedByLiken is the reason when the
// operator refuses to tear down a Flux installation that liken did not
// create. MachinesReady is also False during a rollout, and that needs
// no one.
func clusterBadStatus(c api.Condition) conditions.Status {
	switch c.Reason {
	case "MachinesDegraded", "RolloutStalled", "NotPlantedByLiken":
		return c.Status
	}
	return ""
}
