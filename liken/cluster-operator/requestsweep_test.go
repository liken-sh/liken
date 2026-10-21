package main

// The conductor's reports through whole sweeps: a request's Approved
// and Granted conditions from the stored ledger, a budget's counts,
// the default budget it creates, and the complete request it deletes.

import (
	"slices"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
	"github.com/liken-sh/liken/liken/machine"
)

// requestFleet is the race's fleet with node-2's request and the
// given budgets served.
func requestFleet(t *testing.T, requests []disruption.Disruption, budgets ...disruption.Budget) *fakeapi.Server {
	t.Helper()
	var requestItems, budgetItems []map[string]any
	for _, d := range requests {
		d.Metadata.ResourceVersion = "5"
		requestItems = append(requestItems, asObject(t, d))
	}
	for _, b := range budgets {
		b.Metadata.ResourceVersion = "5"
		budgetItems = append(budgetItems, asObject(t, b))
	}
	return raceFleetWith(t, map[string]*fakeapi.Collection{
		kubernetes.DisruptionsPath: {APIVersion: api.APIVersion, Kind: "Disruption", Items: requestItems},
		kubernetes.BudgetsPath:     {APIVersion: api.APIVersion, Kind: "MachineDisruptionBudget", Items: budgetItems},
	})
}

func node2Request() disruption.Disruption {
	d := disruption.New("node-2.machine-operator", &machine.Machine{Metadata: api.ObjectMeta{Name: "node-2", UID: "uid-node-2"}},
		disruption.Spec{Machine: "node-2", Requester: disruption.MachineOperator, Kind: disruption.KindReboot, Hash: "6f2a91c04b7e5d"})
	return *d
}

func storedRequest(t *testing.T, c *apiclient.Client, name string) *disruption.Disruption {
	t.Helper()
	d, err := apiclient.Get[disruption.Disruption](c, kubernetes.DisruptionsPath+"/"+name)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A request on a Manual machine reads AwaitingApproval. It holds no
// turn of its own: node-2's AwaitingTurn is what asks for the turn, as
// a machine-operator of every release writes it.
func TestASweepReportsARequestThatWaitsForApproval(t *testing.T) {
	fake := requestFleet(t, []disruption.Disruption{node2Request()}, *disruption.NewDefaultBudget(1))

	sweepAt(t, leadingReader(t, fake))

	d := storedRequest(t, testClient(t, fake), "node-2.machine-operator")
	approved := api.FindCondition(d.Status.Conditions, disruption.ApprovedCondition)
	granted := api.FindCondition(d.Status.Conditions, disruption.GrantedCondition)
	if approved == nil || approved.Reason != "AwaitingApproval" || granted == nil || granted.Reason != "AwaitingApproval" {
		t.Errorf("conditions = %+v", d.Status.Conditions)
	}
}

// An approved request reads Granted once the stored ledger names it,
// and the budget counts the turn.
func TestASweepReportsAGrantedRequestAndItsBudget(t *testing.T) {
	request := node2Request()
	request.Spec.ApprovedHash = request.Spec.Hash
	fake := requestFleet(t, []disruption.Disruption{request}, *disruption.NewDefaultBudget(1))
	client := testClient(t, fake)

	sweepAt(t, leadingReader(t, fake))

	c, err := kubernetes.GetCluster(client, "lab")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Status.RebootTurns) != 1 || !slices.Equal(c.Status.RebootTurns[0].Disruptions, []string{"node-2.machine-operator"}) {
		t.Fatalf("ledger = %+v, want node-2's turn naming its request", c.Status.RebootTurns)
	}
	d := storedRequest(t, client, "node-2.machine-operator")
	if granted := api.FindCondition(d.Status.Conditions, disruption.GrantedCondition); granted == nil || granted.Status != api.ConditionTrue {
		t.Errorf("Granted = %+v", granted)
	}
	b, err := apiclient.Get[disruption.Budget](client, kubernetes.BudgetsPath+"/default")
	if err != nil {
		t.Fatal(err)
	}
	if b.Status.Selected != 3 || b.Status.Unavailable != 1 || b.Status.Allowed != 0 ||
		api.FindCondition(b.Status.Conditions, disruption.AllowedCondition).Reason != "BudgetFull" {
		t.Errorf("default budget status = %+v", b.Status)
	}
}

// A cluster that serves the budgets and has no default gets one, from
// spec.disruption. A cluster that does not serve them gets no create.
func TestASweepCreatesTheDefaultBudgetOnlyWhereTheKindIsServed(t *testing.T) {
	served := requestFleet(t, nil)
	sweepAt(t, leadingReader(t, served))
	b, err := apiclient.Get[disruption.Budget](testClient(t, served), kubernetes.BudgetsPath+"/default")
	if err != nil || b.Spec.MaxUnavailable != 1 {
		t.Errorf("default budget = %+v, %v; want one with maxUnavailable 1", b, err)
	}

	unserved := raceFleet(t)
	sweepAt(t, leadingReader(t, unserved))
	if slices.Contains(writes(unserved.Requests()), "POST "+kubernetes.BudgetsPath) {
		t.Errorf("writes = %q, want no create of a budget the cluster does not serve", writes(unserved.Requests()))
	}
}

// A request that completed more than a day ago is deleted, and one that
// completed an hour ago stays.
func TestASweepDeletesARequestADayAfterItCompleted(t *testing.T) {
	complete := func(name string, ago time.Duration) disruption.Disruption {
		d := node2Request()
		d.Metadata.Name = name
		d.Spec.Requester = "kubernetes-admin"
		d.Status.Conditions = []api.Condition{{Type: disruption.CompleteCondition, Status: api.ConditionTrue,
			Reason: "BootChanged", LastTransitionTime: sweepNow.Add(-ago)}}
		return d
	}
	fake := requestFleet(t, []disruption.Disruption{
		complete("node-2.reboot-old", 25*time.Hour), complete("node-2.reboot-new", time.Hour),
	}, *disruption.NewDefaultBudget(1))

	sweepAt(t, leadingReader(t, fake))

	held, err := kubernetes.List[disruption.Disruption](testClient(t, fake), kubernetes.DisruptionsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 1 || held[0].Metadata.Name != "node-2.reboot-new" {
		t.Errorf("requests left = %+v, want node-2.reboot-new", held)
	}
}
