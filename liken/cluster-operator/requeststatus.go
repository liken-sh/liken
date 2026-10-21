package main

// The conductor's reports on the requests and the budgets: the
// Approved and Granted conditions of each Disruption, the counts of
// each MachineDisruptionBudget, the default budget it keeps, and the
// complete requests it deletes.
//
// These are reports for a person. No program acts on a Disruption's
// Granted condition: the grant is the ledger entry, and a condition on
// another object could land after the turn it reports ended. So each
// report reads the ledger that the API server stored on this sweep,
// and a request reads Granted only once its turn is recorded.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// completeRequestTTL is how long a complete request stays before the
// conductor deletes it. A day leaves a person time to read what
// rebooted the machine overnight. The Events of its transitions carry
// the history for the hour the API server keeps them.
const completeRequestTTL = 24 * time.Hour

// The reasons of the Events that are not condition transitions.
const (
	reasonRequestDeleted = "DisruptionDeleted"
	reasonBudgetCreated  = "MachineDisruptionBudgetCreated"
)

// requestConditions composes a request's Approved and Granted
// conditions from its machine and the stored ledger.
func requestConditions(d *disruption.Disruption, m *machine.Machine, r rollout, stored []cluster.RebootTurn) (approved, granted api.Condition) {
	a := approve(d, m)
	approved = api.Condition{Type: disruption.ApprovedCondition, Status: api.ConditionTrue, Reason: a.reason, Message: a.message}
	if !a.approved {
		approved.Status = api.ConditionFalse
	}
	entry := slices.IndexFunc(stored, func(t cluster.RebootTurn) bool { return t.Machine == d.Spec.Machine })
	switch {
	case entry >= 0 && slices.Contains(stored[entry].Disruptions, d.Metadata.Name):
		granted = api.Condition{Type: disruption.GrantedCondition, Status: api.ConditionTrue, Reason: "TurnGranted",
			Message: "the machine's turn in the Cluster's status.rebootTurns grants this request"}
	case !a.approved:
		granted = api.Condition{Type: disruption.GrantedCondition, Status: api.ConditionFalse, Reason: "AwaitingApproval",
			Message: "the request waits for its approval"}
	case r.waits[d.Spec.Machine] != "":
		granted = api.Condition{Type: disruption.GrantedCondition, Status: api.ConditionFalse, Reason: "AwaitingTurn",
			Message: r.waits[d.Spec.Machine]}
	case !available(m.Status.Phase):
		granted = api.Condition{Type: disruption.GrantedCondition, Status: api.ConditionFalse, Reason: "AwaitingTurn",
			Message: fmt.Sprintf("%s is %s, out of service with no turn, and takes its turn when it is back", d.Spec.Machine, m.Status.Phase)}
	default:
		granted = api.Condition{Type: disruption.GrantedCondition, Status: api.ConditionFalse, Reason: "AwaitingTurn",
			Message: "waiting for the cluster's next sweep to grant the turn"}
	}
	approved.ObservedGeneration = d.Metadata.Generation
	granted.ObservedGeneration = d.Metadata.Generation
	return approved, granted
}

// reportRequests writes each open request's conditions, and deletes
// each request that completed more than a day ago. A request whose
// Machine is gone is left to the garbage collector, which deletes it
// with its owner.
func reportRequests(reads *fleetReader, machines []machine.Machine, q fleetRequests, r rollout, stored []cluster.RebootTurn, now time.Time) {
	for i := range q.disruptions {
		d := &q.disruptions[i]
		if c := api.FindCondition(d.Status.Conditions, disruption.CompleteCondition); c != nil && c.Status == api.ConditionTrue {
			if now.Sub(c.LastTransitionTime) > completeRequestTTL {
				deleteRequest(reads, d)
			}
			continue
		}
		at := slices.IndexFunc(machines, func(m machine.Machine) bool { return m.Metadata.Name == d.Spec.Machine })
		if at < 0 || d.Done(machine.BootID(machines[at].Status.Boot)) {
			continue
		}
		approved, granted := requestConditions(d, &machines[at], r, stored)
		written := api.SetCondition(slices.Clone(d.Status.Conditions), approved, now)
		written = api.SetCondition(written, granted, now)
		if slices.Equal(written, d.Status.Conditions) {
			continue
		}
		updated := *d
		updated.Status.Conditions = written
		if err := reads.publishRequestStatus(&updated); err != nil {
			if !apiclient.Stale(err) {
				fmt.Printf("reporting on the Disruption %s: %v\n", d.Metadata.Name, err)
			}
			continue
		}
		for _, t := range api.Transitions(d.Status.Conditions, written) {
			reads.recorder.Transition(requestReference(d), t, requestBadStatus(t))
		}
	}
}

// requestBadStatus answers the status of a request's condition that
// needs a person: a Manual machine's request that waits for its
// approval.
func requestBadStatus(c api.Condition) conditions.Status {
	if c.Reason == "AwaitingApproval" {
		return c.Status
	}
	return ""
}

func requestReference(d *disruption.Disruption) events.ObjectReference {
	return events.ObjectReference{APIVersion: api.APIVersion, Kind: disruptionKind, Name: d.Metadata.Name, UID: d.Metadata.UID}
}

func deleteRequest(reads *fleetReader, d *disruption.Disruption) {
	err := reads.client.RequestJSON(http.MethodDelete, disruptionPath(d.Metadata.Name), nil, nil)
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		fmt.Printf("deleting the complete Disruption %s: %v\n", d.Metadata.Name, err)
		return
	}
	if err == nil {
		reads.recorder.Normal(requestReference(d), reasonRequestDeleted,
			fmt.Sprintf("the request completed more than %s ago", completeRequestTTL))
	}
}

// reportBudgets writes what each budget counted on this sweep, and
// creates the default budget when the cluster serves the kind and has
// none.
func reportBudgets(reads *fleetReader, clusterDoc *cluster.Cluster, q fleetRequests, r rollout, now time.Time) {
	if q.budgetsServed && !slices.ContainsFunc(q.budgets, func(b disruption.Budget) bool { return b.Metadata.Name == disruption.DefaultBudget }) {
		createDefaultBudget(reads, clusterDoc)
	}
	for i := range q.budgets {
		b := &q.budgets[i]
		counted, ok := r.budgets[b.Metadata.Name]
		if !ok {
			continue
		}
		written := slices.Clone(b.Status.Conditions)
		for _, c := range counted.Conditions {
			written = api.SetCondition(written, c, now)
		}
		counted.Conditions = written
		if counted.ObservedGeneration == b.Status.ObservedGeneration && counted.Selected == b.Status.Selected &&
			counted.Unavailable == b.Status.Unavailable && counted.Allowed == b.Status.Allowed &&
			slices.Equal(written, b.Status.Conditions) {
			continue
		}
		updated := *b
		updated.Status = counted
		if err := reads.publishBudgetStatus(&updated); err != nil {
			if !apiclient.Stale(err) {
				fmt.Printf("reporting on the MachineDisruptionBudget %s: %v\n", b.Metadata.Name, err)
			}
			continue
		}
		for _, t := range api.Transitions(b.Status.Conditions, written) {
			reads.recorder.Transition(budgetReference(b), t, "")
		}
	}
}

func budgetReference(b *disruption.Budget) events.ObjectReference {
	return events.ObjectReference{APIVersion: api.APIVersion, Kind: budgetKind, Name: b.Metadata.Name, UID: b.Metadata.UID}
}

func createDefaultBudget(reads *fleetReader, clusterDoc *cluster.Cluster) {
	body, err := json.Marshal(disruption.NewDefaultBudget(defaultBudgetLimit))
	if err != nil {
		return
	}
	err = reads.client.RequestJSON(http.MethodPost, kubernetes.BudgetsPath, body, nil)
	switch {
	case err == nil:
		fmt.Printf("created the default MachineDisruptionBudget with maxUnavailable %d\n", defaultBudgetLimit)
		reads.recorder.Normal(clusterReference(clusterDoc), reasonBudgetCreated,
			fmt.Sprintf("created the MachineDisruptionBudget default, which allows %d machine out of service at a time; edit the budget to change the limit", defaultBudgetLimit))
	case errors.Is(err, apiclient.ErrConflict), errors.Is(err, apiclient.ErrNotFound), errors.Is(err, apiclient.ErrForbidden):
	default:
		fmt.Printf("creating the default MachineDisruptionBudget: %v\n", err)
	}
}

// publishRequestStatus writes a Disruption's status and notes the
// version for the Disruptions' copy.
func (r *fleetReader) publishRequestStatus(d *disruption.Disruption) error {
	return r.disruptionVersions.Send(d.Metadata.Name, func() (string, error) {
		return replaceStatus(r.client, disruptionPath(d.Metadata.Name)+"/status", d)
	})
}

// publishBudgetStatus writes a budget's status and notes the version
// for the budgets' copy.
func (r *fleetReader) publishBudgetStatus(b *disruption.Budget) error {
	return r.budgetVersions.Send(b.Metadata.Name, func() (string, error) {
		return replaceStatus(r.client, budgetPath(b.Metadata.Name)+"/status", b)
	})
}

// replaceStatus writes a status subresource, conditional on the
// object's resourceVersion, and answers the version the API server
// stored.
func replaceStatus(c *apiclient.Client, path string, object any) (string, error) {
	body, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	var written struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
	}
	if err := c.RequestJSON(http.MethodPut, path, body, &written); err != nil {
		return "", err
	}
	return written.Metadata.ResourceVersion, nil
}
