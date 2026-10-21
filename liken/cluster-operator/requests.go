package main

// What the fleet asks for, and the limits it keeps: the Disruptions
// and the MachineDisruptionBudgets that the rollout conductor reads on
// each sweep (rollout.go).
//
// A machine still asks for a turn with the reason AwaitingTurn on one
// of its conditions, and the conductor grants the turn on that reason.
// machine-operator sets it for every request it acts on: its own
// staged documents, and a person's Disruption or request annotation. A
// grant on a Disruption alone would go to a machine-operator of the
// release before, which ignores a Disruption, and the grant would hold
// a slot of the budget with no reboot to spend it. So a Disruption
// tells the conductor what a turn covers, not whether to grant one:
// the ledger entry names each approved Disruption that is still open,
// and the entry stays while one of them is open.

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/machine"
)

// fleetRequests is what a sweep read of the Disruptions and the
// budgets. A cluster that does not serve the kinds, because its leaders
// run a release from before them, reads as none of either, and the
// conductor keeps the one budget that spec.disruption declares.
type fleetRequests struct {
	disruptions []disruption.Disruption
	budgets     []disruption.Budget

	// budgetsServed says the cluster serves the budgets, and lets this
	// program read them, so the conductor can create the default one.
	budgetsServed bool
}

// of answers the Disruptions that name a machine, in name order.
func (q fleetRequests) of(name string) []disruption.Disruption {
	var mine []disruption.Disruption
	for _, d := range q.disruptions {
		if d.Spec.Machine == name {
			mine = append(mine, d)
		}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].Metadata.Name < mine[j].Metadata.Name })
	return mine
}

// An approval is whether a request may take a turn, and the reason a
// person reads on the request's Approved condition.
type approval struct {
	approved bool
	reason   string
	message  string
}

// approve judges one request against its machine. A machine on
// rebootPolicy Auto approves every request. A Manual machine's request
// is approved by its approvedHash, and also by the approve annotation
// that machine-operator still honors: for a person's request, the
// annotation names the request's hash, and for machine-operator's own,
// the annotation names one of the staged documents in status.pending
// that the request covers.
func approve(d *disruption.Disruption, m *machine.Machine) approval {
	if m.Spec.RebootPolicyOrDefault() == machine.RebootAuto {
		return approval{true, "AutoPolicy", "the machine's rebootPolicy is Auto, which approves every request"}
	}
	if d.ApprovedByHash() {
		return approval{true, "HashApproved", fmt.Sprintf("spec.approvedHash names spec.hash (%.12s)", d.Spec.Hash)}
	}
	annotation := m.Metadata.Annotations[machine.ApproveDisruptionAnnotation]
	if annotation != "" && approvedByAnnotation(d, m, annotation) {
		return approval{true, "AnnotationApproved",
			fmt.Sprintf("the %s annotation names %s, which this request covers", machine.ApproveDisruptionAnnotation, annotation)}
	}
	return approval{false, "AwaitingApproval",
		fmt.Sprintf("the machine's rebootPolicy is Manual; approve the request with `liken approve-reboot`, or set spec.approvedHash to spec.hash (%.12s)", d.Spec.Hash)}
}

func approvedByAnnotation(d *disruption.Disruption, m *machine.Machine, annotation string) bool {
	if !d.Own() {
		return machine.ApprovalGrants(annotation, d.Spec.Hash)
	}
	for _, p := range m.Status.Pending {
		if p.Condition != rebootRequestCondition && machine.ApprovalGrants(annotation, p.Hash) {
			return true
		}
	}
	return false
}

// rebootRequestCondition is the condition machine-operator reports a
// person's reboot request on. Its status.pending entry is the person's
// request, not one of the documents machine-operator's own Disruption
// covers.
const rebootRequestCondition = "RebootRequestHonored"

// open answers the names of a machine's requests that are approved and
// not done, in name order. These are the requests a turn grants.
func (q fleetRequests) open(m *machine.Machine) []string {
	bootID := machine.BootID(m.Status.Boot)
	var names []string
	for _, d := range q.of(m.Metadata.Name) {
		if !d.Done(bootID) && approve(&d, m).approved {
			names = append(names, d.Metadata.Name)
		}
	}
	return names
}

// joined answers an entry's Disruptions with each open request of its
// machine added, so a request that arrives during the turn shares it.
func joined(entry []string, open []string) []string {
	names := slices.Clone(entry)
	for _, name := range open {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// holdsEntry reports whether an entry that names Disruptions stays in
// the ledger although its Machine holds no grant at the recorded
// version: the machine is unavailable, so its turn is not over, or one
// of the entry's requests is still open. A release whose
// machine-operator acts on the entry alone needs the entry to stay
// for as long as it works on the turn, with no grant on the Machine to
// show it.
func holdsEntry(turn cluster.RebootTurn, open []string, phase api.Phase) bool {
	if len(turn.Disruptions) == 0 {
		return false
	}
	if !available(phase) {
		return true
	}
	return slices.ContainsFunc(turn.Disruptions, func(name string) bool { return slices.Contains(open, name) })
}

// downloading reports whether the machine's release is still on its
// way to the machine. A turn waits for it, so the release joins the
// turn's one drain and one reboot. A fetch that failed and waits to
// retry reports the reason DownloadRetrying, so a release server that
// is down holds no turn.
func downloading(m *machine.Machine) bool {
	c := api.FindCondition(m.Status.Conditions, "VersionConverged")
	return c != nil && c.Reason == "Downloading"
}

// budgetsOf answers the budgets that limit the fleet. The default
// budget always limits it: when the cluster serves no budget named
// default, because the kind is not served yet or the conductor has not
// created it yet, a stand-in from spec.disruption takes its place.
func (q fleetRequests) budgetsOf(clusterDoc *cluster.Cluster) []disruption.Budget {
	budgets := slices.Clone(q.budgets)
	if !slices.ContainsFunc(budgets, func(b disruption.Budget) bool { return b.Metadata.Name == disruption.DefaultBudget }) {
		budgets = append(budgets, *disruption.NewDefaultBudget(clusterDoc.Spec.Disruption.MaxUnavailableOrDefault()))
	}
	sort.Slice(budgets, func(i, j int) bool { return budgets[i].Metadata.Name < budgets[j].Metadata.Name })
	return budgets
}

// A budgetBook counts each budget's room through one sweep. It starts
// from the machines that are already out of service, and each grant
// takes a slot of every budget that selects the machine.
type budgetBook struct {
	budgets  []disruption.Budget
	selected [][]string
	out      [][]string
}

func newBudgetBook(budgets []disruption.Budget, machines []machine.Machine, occupying []string) *budgetBook {
	book := &budgetBook{budgets: budgets, selected: make([][]string, len(budgets)), out: make([][]string, len(budgets))}
	for i, b := range budgets {
		for _, m := range machines {
			if !b.Spec.Selector.Selects(m.Metadata.Labels) {
				continue
			}
			book.selected[i] = append(book.selected[i], m.Metadata.Name)
			if slices.Contains(occupying, m.Metadata.Name) {
				book.out[i] = append(book.out[i], m.Metadata.Name)
			}
		}
	}
	return book
}

// refusal answers why a machine cannot take a turn now: the first
// budget, in name order, that selects it and has no room. It answers
// the empty string when every budget that selects the machine has
// room.
func (b *budgetBook) refusal(name string) string {
	for i, budget := range b.budgets {
		if !slices.Contains(b.selected[i], name) || len(b.out[i]) < budget.Spec.MaxUnavailable {
			continue
		}
		return fmt.Sprintf("the %s budget allows %d out of service, and %s %s",
			budget.Metadata.Name, budget.Spec.MaxUnavailable, strings.Join(b.out[i], ", "), isOrAre(len(b.out[i])))
	}
	return ""
}

func isOrAre(n int) string {
	if n == 1 {
		return "is out"
	}
	return "are out"
}

// take records a grant in every budget that selects the machine.
func (b *budgetBook) take(name string) {
	for i := range b.budgets {
		if slices.Contains(b.selected[i], name) && !slices.Contains(b.out[i], name) {
			b.out[i] = append(b.out[i], name)
		}
	}
}

// statuses answers what each budget the cluster serves reports after
// the sweep. The stand-in for a missing default budget reports
// nothing, because there is no object to report on.
func (b *budgetBook) statuses(served []disruption.Budget) map[string]disruption.BudgetStatus {
	statuses := map[string]disruption.BudgetStatus{}
	for i, budget := range b.budgets {
		if !slices.ContainsFunc(served, func(s disruption.Budget) bool { return s.Metadata.Name == budget.Metadata.Name }) {
			continue
		}
		out := slices.Sorted(slices.Values(b.out[i]))
		allowed := max(budget.Spec.MaxUnavailable-len(out), 0)
		condition := api.Condition{
			Type: disruption.AllowedCondition, Status: api.ConditionTrue, Reason: "BudgetHasRoom",
			ObservedGeneration: budget.Metadata.Generation,
			Message:            fmt.Sprintf("%d of %d out of service", len(out), budget.Spec.MaxUnavailable),
		}
		if len(out) > 0 {
			condition.Message += ": " + strings.Join(out, ", ")
		}
		if allowed == 0 {
			condition.Status, condition.Reason = api.ConditionFalse, "BudgetFull"
		}
		statuses[budget.Metadata.Name] = disruption.BudgetStatus{
			ObservedGeneration: budget.Metadata.Generation,
			Selected:           len(b.selected[i]),
			Unavailable:        len(out),
			Allowed:            allowed,
			Conditions:         []api.Condition{condition},
		}
	}
	return statuses
}
