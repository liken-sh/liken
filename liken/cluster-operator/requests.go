package main

// A Disruption requests a reboot turn for one machine, and a
// MachineDisruptionBudget limits how many of the machines it selects
// can be out of service at once. The rollout (rollout.go) grants a turn
// to a machine that has an open request: one that is approved and not
// done. The machine's entry in the Cluster's status.rebootTurns lists
// the requests that the turn covers, and a request that is opened
// during the turn joins it.

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/machine"
)

// fleetRequests is the set of Disruptions and MachineDisruptionBudgets
// that a pass read. When the cluster has no budget named default, which
// includes a cluster that does not serve the kinds, budgetsOf adds an
// unsaved default budget with defaultBudgetLimit.
type fleetRequests struct {
	disruptions []disruption.Disruption
	budgets     []disruption.Budget

	// budgetsServed is true when the API server serves the budget kind,
	// so the default budget can be created.
	budgetsServed bool
}

// forMachine returns the Disruptions for a machine, in name order.
func (q fleetRequests) forMachine(name string) []disruption.Disruption {
	var mine []disruption.Disruption
	for _, d := range q.disruptions {
		if d.Spec.Machine == name {
			mine = append(mine, d)
		}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].Metadata.Name < mine[j].Metadata.Name })
	return mine
}

// approval reports whether approve approved a request, with the
// reason and message for the request's Approved condition.
type approval struct {
	approved bool
	reason   string
	message  string
}

// approve approves every request of a machine whose rebootPolicy is
// Auto. On a Manual machine it approves a request whose
// spec.approvedHash matches its spec.hash.
func approve(d *disruption.Disruption, m *machine.Machine) approval {
	if m.Spec.RebootPolicyOrDefault() == machine.RebootAuto {
		return approval{true, "AutoPolicy", "The machine's rebootPolicy is Auto, which approves every request"}
	}
	if d.ApprovedByHash() {
		return approval{true, "HashApproved", fmt.Sprintf("spec.approvedHash matches spec.hash (%.12s)", d.Spec.Hash)}
	}
	return approval{false, "AwaitingApproval",
		fmt.Sprintf("The machine's rebootPolicy is Manual. Approve the request with liken approve-reboot, or set spec.approvedHash to spec.hash (%.12s)", d.Spec.Hash)}
}

// open returns the names of a machine's requests that are approved and
// not done, in name order.
func (q fleetRequests) open(m *machine.Machine) []string {
	bootID := machine.BootID(m.Status.Boot)
	var names []string
	for _, d := range q.forMachine(m.Metadata.Name) {
		if !d.Done(bootID) && approve(&d, m).approved {
			names = append(names, d.Metadata.Name)
		}
	}
	return names
}

// oldestOpen returns the creation time of the machine's oldest open
// request, truncated to the second, or the zero time when it has none.
// The rollout grants the machine with the oldest request first.
func (q fleetRequests) oldestOpen(m *machine.Machine) time.Time {
	open := q.open(m)
	var oldest time.Time
	for _, d := range q.forMachine(m.Metadata.Name) {
		if slices.Contains(open, d.Metadata.Name) && (oldest.IsZero() || d.Metadata.CreationTimestamp.Before(oldest)) {
			oldest = d.Metadata.CreationTimestamp
		}
	}
	return oldest.Truncate(time.Second)
}

// compareLeader orders a worker before a leader. a and b are true for
// a leader.
func compareLeader(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// withOpenRequests returns the Disruption names of a machine's
// status.rebootTurns entry with its open requests added, sorted.
func withOpenRequests(entry []string, open []string) []string {
	names := slices.Clone(entry)
	for _, name := range open {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// requestsKeepTurn reports whether the Disruptions of a
// status.rebootTurns entry keep the entry. decideRollout asks only
// when the Machine shows no grant and its resourceVersion differs from
// the entry's, which otherwise ends the turn. An entry with no
// Disruptions is never kept this way. One with Disruptions is kept
// while the machine is unavailable, because its turn is not over, and
// while any of the entry's requests is open, because machine-operator
// can act on the entry with no grant on the Machine.
func requestsKeepTurn(turn cluster.RebootTurn, open []string, phase api.Phase) bool {
	if len(turn.Disruptions) == 0 {
		return false
	}
	if !available(phase) {
		return true
	}
	return slices.ContainsFunc(turn.Disruptions, func(name string) bool { return slices.Contains(open, name) })
}

// downloading reports whether the machine is downloading its new
// release. A turn waits for the download, so that the new release
// installs in the same drain and reboot. A download that failed and
// waits to retry has the reason DownloadRetrying and does not count, so
// an unreachable release server does not delay a turn.
func downloading(m *machine.Machine) bool {
	c := api.FindCondition(m.Status.Conditions, "VersionConverged")
	return c != nil && c.Reason == "Downloading"
}

// budgetsOf returns the budgets that limit the fleet, in name order.
// Until the cluster has a budget named default, an unsaved default
// budget with defaultBudgetLimit takes its place.
func (q fleetRequests) budgetsOf() []disruption.Budget {
	budgets := slices.Clone(q.budgets)
	if !slices.ContainsFunc(budgets, func(b disruption.Budget) bool { return b.Metadata.Name == disruption.DefaultBudget }) {
		budgets = append(budgets, *disruption.NewDefaultBudget(defaultBudgetLimit))
	}
	sort.Slice(budgets, func(i, j int) bool { return budgets[i].Metadata.Name < budgets[j].Metadata.Name })
	return budgets
}

// defaultBudgetLimit is the maxUnavailable that the default budget is
// created with.
const defaultBudgetLimit = 1

// budgetCounts counts the machines that each budget has out of service
// during one pass. It starts with the machines that are already out of
// service, and add counts each machine that the pass grants a turn.
//
// unheard lists, for each budget, the selected machines that are not
// out of service by their status but that this process has not yet
// seen renew a heartbeat Lease (heartbeatTrust.unheard). Such a machine
// may be down, so refusal counts it. The budget's status does not,
// because the status reports what is known to be out of service, and a
// new process hears every live machine within seconds.
type budgetCounts struct {
	budgets     []disruption.Budget
	selected    [][]string
	unavailable [][]string
	unheard     [][]string
}

func newBudgetCounts(budgets []disruption.Budget, machines []machine.Machine, outOfService, unheard []string) *budgetCounts {
	counts := &budgetCounts{budgets: budgets, selected: make([][]string, len(budgets)),
		unavailable: make([][]string, len(budgets)), unheard: make([][]string, len(budgets))}
	for i, b := range budgets {
		for _, m := range machines {
			if !b.Spec.Selector.Selects(m.Metadata.Labels) {
				continue
			}
			counts.selected[i] = append(counts.selected[i], m.Metadata.Name)
			switch {
			case slices.Contains(outOfService, m.Metadata.Name):
				counts.unavailable[i] = append(counts.unavailable[i], m.Metadata.Name)
			case slices.Contains(unheard, m.Metadata.Name):
				counts.unheard[i] = append(counts.unheard[i], m.Metadata.Name)
			}
		}
	}
	return counts
}

// refusal returns why a machine cannot take a turn: the first budget,
// in name order, that selects it and is full. It returns "" when every
// budget that selects the machine has room.
func (b *budgetCounts) refusal(name string) string {
	for i, budget := range b.budgets {
		out, unheard := b.unavailable[i], b.unheard[i]
		if !slices.Contains(b.selected[i], name) || len(out)+len(unheard) < budget.Spec.MaxUnavailable {
			continue
		}
		var counted []string
		if len(out) > 0 {
			counted = append(counted, strings.Join(out, ", ")+" "+isOrAre(len(out)))
		}
		if len(unheard) > 0 {
			counted = append(counted, strings.Join(unheard, ", ")+" "+countsAsOut(len(unheard)))
		}
		return fmt.Sprintf("The %s budget allows %d out of service, and %s",
			budget.Metadata.Name, budget.Spec.MaxUnavailable, strings.Join(counted, ", and "))
	}
	return ""
}

func countsAsOut(n int) string {
	if n == 1 {
		return "counts as out until cluster-operator sees its heartbeat Lease renewed"
	}
	return "count as out until cluster-operator sees their heartbeat Leases renewed"
}

func isOrAre(n int) string {
	if n == 1 {
		return "is out"
	}
	return "are out"
}

// add counts a granted machine as out of service in every budget
// that selects it.
func (b *budgetCounts) add(name string) {
	for i := range b.budgets {
		if slices.Contains(b.selected[i], name) && !slices.Contains(b.unavailable[i], name) {
			b.unavailable[i] = append(b.unavailable[i], name)
		}
	}
}

// statuses returns the status to write for each budget in served,
// after the pass's grants. The unsaved default budget of budgetsOf is
// not in served, so it gets no status.
func (b *budgetCounts) statuses(served []disruption.Budget) map[string]disruption.BudgetStatus {
	statuses := map[string]disruption.BudgetStatus{}
	for i, budget := range b.budgets {
		if !slices.ContainsFunc(served, func(s disruption.Budget) bool { return s.Metadata.Name == budget.Metadata.Name }) {
			continue
		}
		out := slices.Sorted(slices.Values(b.unavailable[i]))
		allowed := max(budget.Spec.MaxUnavailable-len(out), 0)
		condition := api.Condition{
			Type: disruption.AllowedCondition, Status: api.ConditionTrue, Reason: "BudgetHasRoom",
			ObservedGeneration: budget.Metadata.Generation,
			Message:            fmt.Sprintf("%d of %d allowed machines are out of service", len(out), budget.Spec.MaxUnavailable),
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
