package main

// These tests cover the grant ledger in the rollout's decision: which
// entries take a slot, which are confirmed, which leave, and which
// grants enter it. Every Machine here is at resourceVersion 7, the
// version the sweep read from the API server for each entry whose
// Machine holds no grant (fleetReader.readTurns).

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// atVersion7 sets every Machine's resourceVersion to 7.
func atVersion7(machines []machine.Machine) []machine.Machine {
	for i := range machines {
		machines[i].Metadata.ResourceVersion = "7"
	}
	return machines
}

// withTurns is labCluster with a budget and a ledger.
func withTurns(maxUnavailable int, turns ...cluster.RebootTurn) *cluster.Cluster {
	c := labCluster(maxUnavailable)
	c.Status.RebootTurns = turns
	return c
}

func turnAt(name, version string) cluster.RebootTurn {
	return cluster.RebootTurn{Machine: name, MachineResourceVersion: version, Since: sweepNow.Add(-time.Minute)}
}

// turnNames answers the machines that a ledger names, in its order.
func turnNames(turns []cluster.RebootTurn) []string {
	var names []string
	for _, turn := range turns {
		names = append(names, turn.Machine)
	}
	return names
}

func TestTheLedgerInTheDecision(t *testing.T) {
	const down = 5 * time.Minute
	cases := []struct {
		name       string
		machines   []rolloutEntry
		cluster    *cluster.Cluster
		heardSince time.Time
		grant      []string
		confirm    []string
		turns      []string
	}{
		{
			name: "an entry with no grant takes a slot, and a leader's entry sets the leader floor",
			machines: []rolloutEntry{
				{"node-1", api.PhaseReady, fresh, false, -1},
				{"node-2", api.PhaseUpdatePending, fresh, true, -1},
				{"node-3", api.PhaseUpdatePending, fresh, true, -1},
				{"node-4", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster: withTurns(2, turnAt("node-1", "7")),
			grant:   []string{"node-3"},
			confirm: []string{"node-1"},
			turns:   []string{"node-1", "node-3"},
		},
		{
			name: "an entry whose machine is available with no grant at the recorded version is confirmed",
			machines: []rolloutEntry{
				{"node-3", api.PhaseUpdatePending, fresh, true, -1},
				{"node-4", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster: withTurns(1, turnAt("node-3", "7")),
			confirm: []string{"node-3"},
			turns:   []string{"node-3"},
		},
		{
			name: "a confirmed machine that no longer asks for a turn is still confirmed",
			machines: []rolloutEntry{
				{"node-3", api.PhaseReady, fresh, false, -1},
			},
			cluster: withTurns(1, turnAt("node-3", "7")),
			confirm: []string{"node-3"},
			turns:   []string{"node-3"},
		},
		{
			name: "the same entry on a machine that is down is not confirmed, and keeps its slot",
			machines: []rolloutEntry{
				{"node-3", api.PhaseReady, down, true, -1},
				{"node-4", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster: withTurns(2, turnAt("node-3", "7")),
			grant:   []string{"node-4"},
			turns:   []string{"node-3", "node-4"},
		},
		{
			name: "an entry at another version with no grant leaves, and its slot goes to the next waiting machine",
			machines: []rolloutEntry{
				{"node-3", api.PhaseReady, fresh, false, -1},
				{"node-4", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster: withTurns(1, turnAt("node-3", "6")),
			grant:   []string{"node-4"},
			turns:   []string{"node-4"},
		},
		{
			name: "an entry at another version leaves and enters again for a machine that still asks",
			machines: []rolloutEntry{
				{"node-3", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster: withTurns(1, turnAt("node-3", "6")),
			grant:   []string{"node-3"},
			turns:   []string{"node-3"},
		},
		{
			name: "an entry for a Machine that is gone leaves",
			machines: []rolloutEntry{
				{"node-4", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster: withTurns(1, turnAt("node-3", "7")),
			grant:   []string{"node-4"},
			turns:   []string{"node-4"},
		},
		{
			name: "grants that no entry names are adopted over the budget, and block new grants",
			machines: []rolloutEntry{
				{"node-3", api.PhaseUpdating, fresh, true, time.Minute},
				{"node-4", api.PhaseUpdating, fresh, true, time.Minute},
				{"node-5", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster: withTurns(1),
			turns:   []string{"node-3", "node-4"},
		},
		{
			name: "while the heartbeats are young, grants and confirms wait, and adoption and removal go on",
			machines: []rolloutEntry{
				{"node-3", api.PhaseUpdatePending, fresh, true, -1},
				{"node-4", api.PhaseUpdating, fresh, true, time.Minute},
				{"node-5", api.PhaseReady, fresh, false, -1},
				{"node-6", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster:    withTurns(3, turnAt("node-3", "7"), turnAt("node-5", "6")),
			heardSince: sweepNow.Add(-fresh),
			turns:      []string{"node-3", "node-4"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			machines, renewals := rolloutInputs(c.machines...)

			r := decideFor(atVersion7(machines), renewals, c.heardSince, c.cluster, "", sweepNow)

			if !slices.Equal(r.grant, c.grant) {
				t.Errorf("grant = %q, want %q", r.grant, c.grant)
			}
			if !slices.Equal(r.confirm, c.confirm) {
				t.Errorf("confirm = %q, want %q", r.confirm, c.confirm)
			}
			if got := turnNames(r.turns); !slices.Equal(got, c.turns) {
				t.Errorf("ledger = %q, want %q", got, c.turns)
			}
		})
	}
}

// A new grant's entry records the version of the Machine copy the grant
// write sends, and the time of the sweep. An entry the ledger already
// holds keeps its time, so a settled ledger causes no write.
func TestANewEntryRecordsTheVersionTheGrantNames(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-3", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
	)
	machines[1].Metadata.ResourceVersion = "184467"
	kept := turnAt("node-3", "")

	r := decideFor(machines, renewals, time.Time{}, withTurns(2, kept), "", sweepNow)

	want := []cluster.RebootTurn{kept, {Machine: "node-4", MachineResourceVersion: "184467", Since: sweepNow}}
	if !sameSlots(r.turns, want) {
		t.Errorf("ledger = %+v, want %+v", r.turns, want)
	}
}

// A sweep that cannot read the machine-operator DaemonSet holds its new
// turns and takes them out of the ledger, and still confirms an entry
// that an earlier sweep recorded.
func TestWithoutGrantsKeepsTheConfirms(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-3", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
	)

	r := decideFor(atVersion7(machines), renewals, time.Time{}, withTurns(2, turnAt("node-3", "7")), "", sweepNow).
		withoutGrants(errTemplateRead)

	if len(r.grant) != 0 || !slices.Equal(r.confirm, []string{"node-3"}) {
		t.Errorf("grant = %q and confirm = %q, want no grant and node-3 confirmed", r.grant, r.confirm)
	}
	if got := turnNames(r.turns); !slices.Equal(got, []string{"node-3"}) {
		t.Errorf("ledger = %q, want only node-3", got)
	}
}

var errTemplateRead = errors.New("the API server refused the read")
