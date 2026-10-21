package main

// The reboot a person asks for, with nothing to apply.
//
// Every other reboot in liken is the tail of a staged document: the
// operator finds drift, stages the new bytes, and the boot actuates
// them (converge.go). That leaves no way to reboot a machine that
// has no drift at all, and a machine with no shell and no SSH server
// has no other way to get one. Two cases need one anyway. A kernel
// driver that bound the wrong device releases it only at boot, and a
// machine used as a testbed needs a clean boot between experiments.
// The only other way to start that boot is the power button, which
// takes no turn from the cluster, cordons nothing, and drains
// nothing.
//
// The request is a person's Disruption for the machine (requests.go),
// whose spec.hash names the identity of the boot that is running now.
// That value is what makes the request one-shot: the boot that comes
// back has a different identity, so the request no longer names the
// running boot, and machine-operator marks it Complete. A later
// request is a new Disruption, named for the new boot.
//
// From the gate onward this is an ordinary disruption. The decision
// below builds the same convergence value the documents build and
// hands it to gateDisruption, so rebootPolicy, the rollout
// conductor's turn, and the drain all apply unchanged, and
// status.pending carries the request where liken approve-reboot can
// find it.
//
// What the request never does is stage anything. A boot's slot
// bookkeeping reads the staged system release on machineState, not
// the reboot intent (armProvingBoot in init/proving.go), so a boot
// that no download preceded arms no trial and promotes no slot. The
// machine comes back on the documents it already ran.

import (
	"fmt"
	"slices"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/machine"
)

// rebootRequestCondition reports whether a requested reboot is still
// outstanding. True covers both settled states: no request at all,
// and a request whose reboot has happened.
const rebootRequestCondition = "RebootRequestHonored"

// decideRebootRequest is the whole decision, as a pure function over
// the machine's requests and the boot record. A request that names
// this boot gates the reboot on the policy and the turn, and its
// approvedHash approves it on a Manual machine. With no such request,
// nothing was asked for. The message then names this boot's identity,
// because that is the value a request created with kubectl can carry,
// and kubectl describe machine is where a person goes to find it. A
// machine with no boot record publishes no identity for a request to
// name, so while a person's request waits, the verdict is Unknown
// rather than a guess.
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
		message := "no reboot has been requested"
		if bootID != "" {
			message = fmt.Sprintf("no reboot has been requested; to ask for one, run liken request-reboot, or create a Disruption for this machine with spec.hash %.12s, this boot's identity",
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
		fmt.Sprintf("a reboot is requested for this boot (%.12s); rebootPolicy is Manual, so approve the reboot (or set rebootPolicy: Auto) to let it run", bootID),
		fmt.Sprintf("a reboot is requested for this boot (%.12s); waiting for the cluster to grant a reboot turn", bootID),
		fmt.Sprintf("reboot requested for this boot (%.12s); nothing is staged, so the machine comes back on the documents it runs now", bootID))
	return c
}

// carryOutRebootRequest performs the decision's one side effect. The
// documents go through carryOutConvergence, which writes a store and
// then the intent. This request has no store, and giving it one would
// open a path to the write a plain reboot must never make. dir is the
// operator's intent channel to init, named rather than assumed so a
// test can read what lands in it.
//
// The intent deliberately carries no manifest hash. That field names
// the staged document a reboot applies, and this reboot applies
// none.
func carryOutRebootRequest(dir string, conv convergence, now time.Time, out *passOutcome) api.Condition {
	if !conv.requestReboot {
		return conv.condition
	}
	intent := &machine.RebootIntent{
		Reason:      fmt.Sprintf("a reboot was requested for boot %.12s", conv.hash),
		RequestedAt: now,
	}
	if err := machine.WriteRebootIntent(dir, intent); err != nil {
		out.fail("writing the requested reboot's intent", err)
		return api.Condition{Type: rebootRequestCondition, Status: api.ConditionFalse,
			Reason: "RequestFailed", Message: err.Error()}
	}
	out.wrote("writing the requested reboot's intent")
	fmt.Printf("requested a reboot that a person asked for; nothing is staged to apply (boot %.12s)\n", conv.hash)
	return conv.condition
}
