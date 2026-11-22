package main

// These tests cover how decideRollout reads and writes the reboot
// turns in the Cluster's status.rebootTurns.

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// atVersion7 sets every Machine's resourceVersion to 7, as if the pass
// had just read it from the API server (fleetReader.readTurns).
func atVersion7(machines []machine.Machine) []machine.Machine {
	for i := range machines {
		machines[i].Metadata.ResourceVersion = "7"
	}
	return machines
}

// withTurns returns labCluster with maxUnavailable and the given
// entries in status.rebootTurns.
func withTurns(maxUnavailable int, turns ...cluster.RebootTurn) *cluster.Cluster {
	c := labCluster(maxUnavailable)
	c.Status.RebootTurns = turns
	return c
}

func turnAt(name, version string) cluster.RebootTurn {
	return cluster.RebootTurn{Machine: name, MachineResourceVersion: version, Since: passTime.Add(-time.Minute)}
}

func turnNames(turns []cluster.RebootTurn) []string {
	var names []string
	for _, turn := range turns {
		names = append(names, turn.Machine)
	}
	return names
}

// heard returns a heartbeat record that started reading the Leases one
// renewal ago and has seen the named machines renew since.
func heard(names ...string) heartbeatTrust {
	t := heartbeatTrust{firstRead: passTime.Add(-fresh), heard: map[string]bool{}}
	for _, name := range names {
		t.heard[name] = true
	}
	return t
}

func TestDecideRolloutWithRecordedRebootTurns(t *testing.T) {
	const down = 5 * time.Minute
	cases := []struct {
		name       string
		machines   []rolloutEntry
		cluster    *cluster.Cluster
		heartbeats heartbeatTrust
		grant      []string
		confirm    []string
		revoke     []string
		turns      []string
	}{
		{
			name: "an entry with no grant counts against the budget, and a leader's entry keeps the other leader from a turn",
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
			name: "the same entry on a machine that is down is not confirmed, and still counts against the budget",
			machines: []rolloutEntry{
				{"node-3", api.PhaseReady, down, true, -1},
				{"node-4", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster: withTurns(2, turnAt("node-3", "7")),
			grant:   []string{"node-4"},
			turns:   []string{"node-3", "node-4"},
		},
		{
			name: "an entry at another version with no grant is removed, and the next waiting machine is granted",
			machines: []rolloutEntry{
				{"node-3", api.PhaseReady, fresh, false, -1},
				{"node-4", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster: withTurns(1, turnAt("node-3", "6")),
			grant:   []string{"node-4"},
			turns:   []string{"node-4"},
		},
		{
			name: "an entry at another version is removed and added again for a machine that still awaits a turn",
			machines: []rolloutEntry{
				{"node-3", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster: withTurns(1, turnAt("node-3", "6")),
			grant:   []string{"node-3"},
			turns:   []string{"node-3"},
		},
		{
			name: "an entry for a Machine that is gone is removed",
			machines: []rolloutEntry{
				{"node-4", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster: withTurns(1, turnAt("node-3", "7")),
			grant:   []string{"node-4"},
			turns:   []string{"node-4"},
		},
		{
			name: "grants with no entry get one even over the budget, and block new grants",
			machines: []rolloutEntry{
				{"node-3", api.PhaseUpdating, fresh, true, time.Minute},
				{"node-4", api.PhaseUpdating, fresh, true, time.Minute},
				{"node-5", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster: withTurns(1),
			turns:   []string{"node-3", "node-4"},
		},
		{
			name: "a young heartbeat record grants and confirms nothing for machines it has not heard renew, but entries are still added and removed",
			machines: []rolloutEntry{
				{"node-3", api.PhaseUpdatePending, fresh, true, -1},
				{"node-4", api.PhaseUpdating, fresh, true, time.Minute},
				{"node-5", api.PhaseReady, fresh, false, -1},
				{"node-6", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster:    withTurns(3, turnAt("node-3", "7"), turnAt("node-5", "6")),
			heartbeats: heartbeatTrust{firstRead: passTime.Add(-fresh)},
			turns:      []string{"node-3", "node-4"},
		},
		{
			name: "a young heartbeat record reclaims, confirms, and grants for machines it has heard renew",
			machines: []rolloutEntry{
				{"node-3", api.PhaseReady, fresh, false, time.Minute},
				{"node-4", api.PhaseUpdatePending, fresh, true, -1},
				{"node-5", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster:    withTurns(3, turnAt("node-4", "7")),
			heartbeats: heard("node-3", "node-4", "node-5"),
			grant:      []string{"node-5"},
			confirm:    []string{"node-4"},
			revoke:     []string{"node-3"},
			turns:      []string{"node-3", "node-4", "node-5"},
		},
		{
			name: "a machine that a young heartbeat record has not heard renew keeps its grant, and another fills the budget",
			machines: []rolloutEntry{
				{"node-3", api.PhaseReady, fresh, false, time.Minute},
				{"node-4", api.PhaseReady, fresh, false, -1},
				{"node-5", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster:    withTurns(2),
			heartbeats: heard("node-5"),
			turns:      []string{"node-3"},
		},
		{
			name: "a leader that a young heartbeat record has not heard renew keeps the other leader from a turn",
			machines: []rolloutEntry{
				{"node-1", api.PhaseReady, fresh, false, -1},
				{"node-2", api.PhaseUpdatePending, fresh, true, -1},
			},
			cluster:    withTurns(3),
			heartbeats: heard("node-2"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			machines, renewals := rolloutInputs(c.machines...)

			r := decideFor(atVersion7(machines), renewals, c.heartbeats, c.cluster, "", passTime)

			if !slices.Equal(r.grant, c.grant) {
				t.Errorf("grant = %q, want %q", r.grant, c.grant)
			}
			if !slices.Equal(r.confirm, c.confirm) {
				t.Errorf("confirm = %q, want %q", r.confirm, c.confirm)
			}
			if !slices.Equal(r.revoke, c.revoke) {
				t.Errorf("revoke = %q, want %q", r.revoke, c.revoke)
			}
			if got := turnNames(r.turns); !slices.Equal(got, c.turns) {
				t.Errorf("turns = %q, want %q", got, c.turns)
			}
		})
	}
}

// A new grant's entry records the resourceVersion of the Machine that
// the grant write is based on, and the time of the pass. An existing
// entry keeps its time, so unchanged turns cause no write.
func TestANewEntryRecordsTheVersionOfTheGrantWrite(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-3", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
	)
	machines[1].Metadata.ResourceVersion = "184467"
	kept := turnAt("node-3", "")

	r := decideFor(machines, renewals, heartbeatTrust{}, withTurns(2, kept), "", passTime)

	want := []cluster.RebootTurn{kept, {Machine: "node-4", MachineResourceVersion: "184467", Since: passTime}}
	if !sameTurnsIgnoringDisruptions(r.turns, want) {
		t.Errorf("turns = %+v, want %+v", r.turns, want)
	}
}

// A pass that cannot read the machine-operator DaemonSet makes no new
// grants and drops their entries, but still confirms the entries that
// an earlier pass recorded.
func TestWithoutGrantsKeepsTheConfirms(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-3", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
	)

	r := decideFor(atVersion7(machines), renewals, heartbeatTrust{}, withTurns(2, turnAt("node-3", "7")), "", passTime).
		withoutGrants(errTemplateRead)

	if len(r.grant) != 0 || !slices.Equal(r.confirm, []string{"node-3"}) {
		t.Errorf("grant = %q and confirm = %q, want no grant and node-3 confirmed", r.grant, r.confirm)
	}
	if got := turnNames(r.turns); !slices.Equal(got, []string{"node-3"}) {
		t.Errorf("turns = %q, want [node-3]", got)
	}
}

var errTemplateRead = errors.New("the API server refused the read")
