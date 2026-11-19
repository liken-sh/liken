package main

// Whole-pass tests of the status a pass writes to Disruptions and
// MachineDisruptionBudgets: a request's Approved condition, its Granted
// condition from the stored Cluster's status.rebootTurns, a budget's
// counts, the default budget the pass creates, and the deletion of a
// completed request.

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

// requestFleet serves raceFleet's fleet with the given requests and
// budgets.
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
		t.Fatalf("reading the Disruption: %v", err)
	}
	return d
}

// A request on a Manual machine with no spec.approvedHash reports
// AwaitingApproval on both its Approved and Granted conditions.
func TestAPassReportsARequestThatWaitsForApproval(t *testing.T) {
	fake := requestFleet(t, []disruption.Disruption{node2Request()}, *disruption.NewDefaultBudget(1))

	reconcileAt(t, leadingReader(t, fake))

	d := storedRequest(t, testClient(t, fake), "node-2.machine-operator")
	approved := api.FindCondition(d.Status.Conditions, disruption.ApprovedCondition)
	granted := api.FindCondition(d.Status.Conditions, disruption.GrantedCondition)
	if approved == nil || approved.Reason != "AwaitingApproval" || granted == nil || granted.Reason != "AwaitingApproval" {
		t.Errorf("the conditions are %+v, want reason AwaitingApproval on Approved and Granted", d.Status.Conditions)
	}
}

// An approved request reports Granted once the stored
// status.rebootTurns lists it in its machine's turn, and the default
// budget counts that turn.
func TestAPassReportsAGrantedRequestAndItsBudget(t *testing.T) {
	request := node2Request()
	request.Spec.ApprovedHash = request.Spec.Hash
	fake := requestFleet(t, []disruption.Disruption{request}, *disruption.NewDefaultBudget(1))
	client := testClient(t, fake)

	reconcileAt(t, leadingReader(t, fake))

	c, err := kubernetes.GetCluster(client, "lab")
	if err != nil {
		t.Fatalf("reading the Cluster: %v", err)
	}
	if len(c.Status.RebootTurns) != 1 || !slices.Equal(c.Status.RebootTurns[0].Disruptions, []string{"node-2.machine-operator"}) {
		t.Fatalf("status.rebootTurns is %+v, want one turn for node-2 that lists its request", c.Status.RebootTurns)
	}
	d := storedRequest(t, client, "node-2.machine-operator")
	if granted := api.FindCondition(d.Status.Conditions, disruption.GrantedCondition); granted == nil || granted.Status != api.ConditionTrue {
		t.Errorf("the Granted condition is %+v, want status True", granted)
	}
	b, err := apiclient.Get[disruption.Budget](client, kubernetes.BudgetsPath+"/default")
	if err != nil {
		t.Fatalf("reading the default MachineDisruptionBudget: %v", err)
	}
	if b.Status.Selected != 3 || b.Status.Unavailable != 1 || b.Status.Allowed != 0 ||
		api.FindCondition(b.Status.Conditions, disruption.AllowedCondition).Reason != "BudgetFull" {
		t.Errorf("the status of the default budget is %+v, want 3 selected, 1 unavailable, 0 allowed, and reason BudgetFull", b.Status)
	}
}

// On a cluster that serves budgets and has no default budget, the pass
// creates one from spec.disruption. On a cluster that does not serve
// budgets, it sends no create.
func TestAPassCreatesTheDefaultBudgetOnlyWhereTheKindIsServed(t *testing.T) {
	served := requestFleet(t, nil)
	reconcileAt(t, leadingReader(t, served))
	b, err := apiclient.Get[disruption.Budget](testClient(t, served), kubernetes.BudgetsPath+"/default")
	if err != nil || b.Spec.MaxUnavailable != 1 {
		t.Errorf("the default budget is %+v with error %v, want maxUnavailable 1 and no error", b, err)
	}

	unserved := raceFleet(t)
	reconcileAt(t, leadingReader(t, unserved))
	if slices.Contains(writes(unserved.Requests()), "POST "+kubernetes.BudgetsPath) {
		t.Errorf("the pass sent writes %q, want no create of a budget on a cluster that does not serve them", writes(unserved.Requests()))
	}
}

// A request that completed more than a day ago is deleted, and one that
// completed an hour ago stays.
func TestAPassDeletesARequestADayAfterItCompleted(t *testing.T) {
	complete := func(name string, ago time.Duration) disruption.Disruption {
		d := node2Request()
		d.Metadata.Name = name
		d.Spec.Requester = "kubernetes-admin"
		d.Status.Conditions = []api.Condition{{Type: disruption.CompleteCondition, Status: api.ConditionTrue,
			Reason: "BootChanged", LastTransitionTime: passTime.Add(-ago)}}
		return d
	}
	fake := requestFleet(t, []disruption.Disruption{
		complete("node-2.reboot-old", 25*time.Hour), complete("node-2.reboot-new", time.Hour),
	}, *disruption.NewDefaultBudget(1))

	reconcileAt(t, leadingReader(t, fake))

	held, err := kubernetes.List[disruption.Disruption](testClient(t, fake), kubernetes.DisruptionsPath)
	if err != nil {
		t.Fatalf("listing the Disruptions: %v", err)
	}
	if len(held) != 1 || held[0].Metadata.Name != "node-2.reboot-new" {
		t.Errorf("the remaining requests are %+v, want only node-2.reboot-new", held)
	}
}
