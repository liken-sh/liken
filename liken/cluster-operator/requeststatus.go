package main

// A Disruption's Approved and Granted conditions are for people to read,
// and no program acts on them. A machine's reboot turn is its entry in
// the Cluster's status.rebootTurns. So Granted is True only when the
// status.rebootTurns that the API server stored in this pass has an
// entry that lists the Disruption.

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

// completeRequestTTL is how long a complete Disruption stays before
// this program deletes it. A day gives a person time to read what
// rebooted a machine overnight. The API server keeps the Events of its
// transitions for only an hour.
const completeRequestTTL = 24 * time.Hour

// Reasons of the Events that do not report a condition transition.
const (
	reasonRequestDeleted = "DisruptionDeleted"
	reasonBudgetCreated  = "MachineDisruptionBudgetCreated"
)

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
			Message: "The machine's entry in the Cluster's status.rebootTurns covers this request"}
	case !a.approved:
		granted = api.Condition{Type: disruption.GrantedCondition, Status: api.ConditionFalse, Reason: "AwaitingApproval",
			Message: "The request is not approved yet"}
	case r.waits[d.Spec.Machine] != "":
		granted = api.Condition{Type: disruption.GrantedCondition, Status: api.ConditionFalse, Reason: "AwaitingTurn",
			Message: r.waits[d.Spec.Machine]}
	case !available(m.Status.Phase):
		granted = api.Condition{Type: disruption.GrantedCondition, Status: api.ConditionFalse, Reason: "AwaitingTurn",
			Message: fmt.Sprintf("Machine %s is %s and out of service with no turn. It gets a turn when it is available again", d.Spec.Machine, m.Status.Phase)}
	default:
		granted = api.Condition{Type: disruption.GrantedCondition, Status: api.ConditionFalse, Reason: "AwaitingTurn",
			Message: "Waiting for the next cluster-operator pass to grant the turn"}
	}
	approved.ObservedGeneration = d.Metadata.Generation
	granted.ObservedGeneration = d.Metadata.Generation
	return approved, granted
}

// reportRequests writes the conditions of each open Disruption and
// deletes each one that completed more than completeRequestTTL ago. It
// skips a Disruption whose Machine is gone, because the garbage
// collector deletes a Disruption with the Machine that owns it.
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
				fmt.Printf("writing the status of the Disruption %s: %v\n", d.Metadata.Name, err)
			}
			continue
		}
		for _, t := range api.Transitions(d.Status.Conditions, written) {
			reads.recorder.Transition(requestReference(d), t, requestBadStatus(t))
		}
	}
}

// requestBadStatus makes a transition's Event a Warning when the
// condition needs a person to act: a Disruption of a Manual machine
// that waits for approval.
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
		fmt.Printf("deleting the completed Disruption %s: %v\n", d.Metadata.Name, err)
		return
	}
	if err == nil {
		reads.recorder.Normal(requestReference(d), reasonRequestDeleted,
			fmt.Sprintf("Deleted the request, which completed more than %s ago", completeRequestTTL))
	}
}

// reportBudgets writes the counts that decideRollout computed in this
// pass to each budget's status. It creates the default budget
// when the API server serves the kind and no budget has that name.
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
				fmt.Printf("writing the status of the MachineDisruptionBudget %s: %v\n", b.Metadata.Name, err)
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
			fmt.Sprintf("Created the MachineDisruptionBudget default, which allows %d machine out of service at a time. Edit the budget to change the limit", defaultBudgetLimit))
	case errors.Is(err, apiclient.ErrConflict), errors.Is(err, apiclient.ErrNotFound), errors.Is(err, apiclient.ErrForbidden):
	default:
		fmt.Printf("creating the default MachineDisruptionBudget: %v\n", err)
	}
}

// publishRequestStatus writes a Disruption's status and records the
// resourceVersion it returns, so a later read does not take an older
// version from the Disruption watch cache.
func (r *fleetReader) publishRequestStatus(d *disruption.Disruption) error {
	return r.disruptionVersions.Send(d.Metadata.Name, func() (string, error) {
		return replaceStatus(r.client, disruptionPath(d.Metadata.Name)+"/status", d)
	})
}

// publishBudgetStatus writes a budget's status and records the
// resourceVersion it returns, so a later read does not take an older
// version from the budget watch cache.
func (r *fleetReader) publishBudgetStatus(b *disruption.Budget) error {
	return r.budgetVersions.Send(b.Metadata.Name, func() (string, error) {
		return replaceStatus(r.client, budgetPath(b.Metadata.Name)+"/status", b)
	})
}

// replaceStatus replaces a status subresource with a PUT that sends
// the object's resourceVersion, so a stale write fails. It returns the
// resourceVersion that the API server stored.
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
