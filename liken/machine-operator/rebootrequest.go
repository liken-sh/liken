package main

// A person can request a reboot of a machine that has nothing staged.
//
// Other reboots apply a staged document (converge.go), so a machine
// with no drift never reboots, and it has no shell or SSH server to
// reboot from. A person still needs a reboot when a kernel driver bound
// the wrong device, because the driver releases it only at boot, and a
// testbed needs a clean boot between experiments. The power button
// reboots too, but without a turn from the cluster, a cordon, or a
// drain.
//
// The request is a person's Disruption for the machine (requests.go)
// with spec.hash set to the running boot's ID. After the reboot the
// boot ID differs, so machine-operator marks the request Complete, and
// another reboot needs a new Disruption.
//
// decideRebootRequest passes the request through gateDisruption, as a
// staged document does, so rebootPolicy, the rollout turn, and the
// drain all apply, and the request is listed in status.pending, where
// liken approve-reboot finds it.
//
// The reboot stages nothing. init arms a proving boot only from a staged
// system release in machineState, never from the reboot intent
// (armProvingBoot in init/proving.go), so the machine comes back on the
// documents it already ran.

import (
	"fmt"
	"slices"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/machine"
)

// rebootRequestCondition is True when no requested reboot is waiting
// to run.
const rebootRequestCondition = "RebootRequestHonored"

// decideRebootRequest returns the convergence for a person's request to
// reboot the running boot. On a Manual machine, the request's
// approvedHash approves it.
//
// With no request, the condition message gives this boot's ID, because
// a person who creates the Disruption with kubectl must put it in
// spec.hash. With no boot record, an open request has no boot ID to
// match, so the condition is Unknown.
func decideRebootRequest(m *machine.Machine, facts *machine.MachineStatus, t turn, requests []disruption.Disruption) convergence {
	bootID := ""
	if facts != nil {
		bootID = machine.BootID(facts.Boot)
	}
	if bootID == "" && slices.ContainsFunc(peopleRequests(requests), func(d disruption.Disruption) bool { return !d.Complete() }) {
		return factsIncomplete(rebootRequestCondition)
	}
	d := requestForBoot(requests, bootID)
	if d == nil || bootID == "" {
		message := "No reboot is requested"
		if bootID != "" {
			message = fmt.Sprintf("No reboot is requested. To request one, run liken request-reboot, or create a Disruption for this machine with spec.hash %.12s, the identity of this boot",
				bootID)
		}
		return convergence{condition: converged(rebootRequestCondition, "NothingRequested", message)}
	}
	approval := ""
	if d.ApprovedByHash() {
		approval = bootID
	}

	c := convergence{hash: bootID}
	gateDisruption(&c, rebootRequestCondition, m.Spec.RebootPolicyOrDefault(), t, false,
		approval,
		"a reboot of this machine, with no change to apply",
		fmt.Sprintf("A reboot is requested for this boot (%.12s). rebootPolicy is Manual, so approve the reboot or set rebootPolicy to Auto to let it run", bootID),
		fmt.Sprintf("A reboot is requested for this boot (%.12s) and waits for a reboot turn from the cluster", bootID),
		fmt.Sprintf("A reboot is requested for this boot (%.12s). Nothing is staged, so the machine comes back with the documents that it runs now", bootID))
	return c
}

// carryOutRebootRequest writes the reboot intent into dir when conv
// requests a reboot, and returns the condition to publish. Unlike
// carryOutConvergence, it writes no store and leaves the intent's
// ManifestHash empty, because the reboot applies no staged document.
func carryOutRebootRequest(dir string, conv convergence, now time.Time, out *passOutcome) api.Condition {
	if !conv.requestReboot {
		return conv.condition
	}
	intent := &machine.RebootIntent{
		Reason:       fmt.Sprintf("a reboot was requested for boot %.12s", conv.hash),
		KeepBootNext: conv.keepBootNext,
		RequestedAt:  now,
	}
	if err := machine.WriteRebootIntent(dir, intent); err != nil {
		out.fail("writing the requested reboot's intent", err)
		return api.Condition{Type: rebootRequestCondition, Status: api.ConditionFalse,
			Reason: "RequestFailed", Message: "The reboot intent is not written: " + err.Error()}
	}
	out.wrote("writing the requested reboot's intent")
	fmt.Printf("requested the reboot that a Disruption asks for, with no staged change to apply (boot %.12s)\n", conv.hash)
	return conv.condition
}
