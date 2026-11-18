package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/liken-sh/liken/liken/machine"
)

var failStopAt = time.Date(2026, 7, 27, 4, 15, 0, 0, time.UTC)

// fakeMachineState returns a temporary machineState root and sets
// machineStateWritable to mounted until the test ends. The flag is a
// package variable, so tests that call this must not run in parallel.
func fakeMachineState(t *testing.T, mounted bool) string {
	t.Helper()
	dir := t.TempDir()
	old := machineStateWritable
	machineStateWritable = mounted
	t.Cleanup(func() { machineStateWritable = old })
	return dir
}

func TestRecordFailStopKeepsTheReason(t *testing.T) {
	cases := map[string]struct {
		reason string
		want   string
	}{
		"the console's own words": {
			reason: "machine identity: /etc/liken/cluster.yaml: spec.registries.mirrors: registry.example lists no endpoints",
			want:   "machine identity: /etc/liken/cluster.yaml: spec.registries.mirrors: registry.example lists no endpoints",
		},
		"a reason past the cap is cut": {
			reason: strings.Repeat("x", failStopReasonCap+50),
			want:   strings.Repeat("x", failStopReasonCap),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := fakeMachineState(t, true)
			recordFailStop(dir, tc.reason, failStopAt)

			got, err := machine.ReadFailStop(dir)
			if err != nil {
				t.Fatalf("reading the failstop record: %v", err)
			}
			if got == nil {
				t.Fatal("the refusal wrote no record, want one")
			}
			if got.Reason != tc.want {
				t.Errorf("the reason is %q, want %q", got.Reason, tc.want)
			}
			if !got.Time.Equal(failStopAt) {
				t.Errorf("the time is %s, want %s", got.Time, failStopAt)
			}
		})
	}
}

func TestRecordFailStopWritesNothingUnmountedOrInstalling(t *testing.T) {
	// A record on a machineState that is not mounted is lost at
	// power-off.
	cases := map[string]struct {
		mounted bool
		cmdline string
	}{
		"machineState is not mounted": {mounted: false, cmdline: "liken.slot=A"},
		"an install boot":             {mounted: true, cmdline: "liken.install"},
		"a reinstall boot":            {mounted: true, cmdline: "liken.reinstall"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fakeCmdline(t, tc.cmdline)
			dir := fakeMachineState(t, tc.mounted)
			recordFailStop(dir, "role clusterState: two partitions claim to be liken:clusterState", failStopAt)

			got, err := machine.ReadFailStop(dir)
			if err != nil {
				t.Fatalf("reading the failstop record: %v", err)
			}
			if got != nil {
				t.Fatalf("the record is %+v, want none", got)
			}
		})
	}
}

func TestCapReasonCutsOnARuneBoundary(t *testing.T) {
	reason := strings.Repeat("x", failStopReasonCap-1) + "é and more"
	got := capReason(reason)
	if len(got) != failStopReasonCap-1 {
		t.Errorf("the cut string is %d bytes, want %d to end before the split rune", len(got), failStopReasonCap-1)
	}
	if !utf8.ValidString(got) {
		t.Errorf("the cut string ends in %q, which is not valid UTF-8, want a cut on a character boundary", got[len(got)-4:])
	}
}

func TestRecordFailStopSurvivesAnUnwritableMachineState(t *testing.T) {
	dir := fakeMachineState(t, true)
	sealed := filepath.Join(dir, "sealed")
	if err := os.Mkdir(sealed, 0o555); err != nil {
		t.Fatalf("creating the read-only directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })
	recordFailStop(sealed, "storage: role clusterState has no device", failStopAt)

	got, err := machine.ReadFailStop(sealed)
	if err != nil {
		t.Fatalf("reading the failstop record: %v", err)
	}
	if got != nil {
		t.Errorf("the record is %+v, want none", got)
	}
}

func TestReportFailStopReadsWhatTheLastRefusalLeft(t *testing.T) {
	cases := map[string]struct {
		record *machine.FailStop
		want   string
	}{
		"a machine that has never refused reports nothing": {},
		"a machine that refused reports the reason": {
			record: &machine.FailStop{Reason: "machine identity: node-9 names no manifest", Time: failStopAt},
			want:   "machine identity: node-9 names no manifest",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.record != nil {
				if err := machine.WriteFailStop(dir, *tc.record); err != nil {
					t.Fatalf("writing the failstop record: %v", err)
				}
			}
			got := reportFailStop(dir)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("the record is %+v, want none", got)
				}
				return
			}
			if got == nil || got.Reason != tc.want {
				t.Fatalf("the record is %+v, want reason %q", got, tc.want)
			}
			// Reading the record does not delete it.
			again := reportFailStop(dir)
			if again == nil || again.Reason != tc.want {
				t.Errorf("the second report is %+v, want reason %q", again, tc.want)
			}
		})
	}
}

func TestReportFailStopSurvivesAnUnreadableRecord(t *testing.T) {
	// A corrupt record must not stop a boot that is otherwise fine.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "failstop.yaml"), []byte("{not yaml\n"), 0o644); err != nil {
		t.Fatalf("writing the corrupt failstop record: %v", err)
	}
	if got := reportFailStop(dir); got != nil {
		t.Errorf("the report is %+v, want none", got)
	}
}

func TestPublishBootFactsIncludesTheLastFailStop(t *testing.T) {
	tree, _ := fakeFactsMachine(t)
	reason := "machine identity: /etc/liken/cluster.yaml: expected kind Cluster, got \"Machine\""

	publishBootFacts(tree, bootFacts{
		storage:      machine.AllRolesInMemory(),
		time:         timeStatus(nil, nil),
		lastFailStop: &machine.FailStop{Reason: reason, Time: failStopAt},
	})

	facts, err := tree.Read()
	if err != nil {
		t.Fatalf("reading the facts tree: %v", err)
	}
	if facts.LastFailStop == nil {
		t.Fatal("the facts tree has no fail-stop record, want one")
	}
	if facts.LastFailStop.Reason != reason || !facts.LastFailStop.Time.Equal(failStopAt) {
		t.Errorf("the record is %+v, want reason %q and time %s", facts.LastFailStop, reason, failStopAt)
	}
	// The reason is its own file under lastFailStop/, so cat can read it.
	raw, err := os.ReadFile(filepath.Join(tree.Dir, "lastFailStop", "reason"))
	if err != nil {
		t.Fatalf("reading lastFailStop/reason: %v", err)
	}
	if strings.TrimSpace(string(raw)) != reason {
		t.Errorf("lastFailStop/reason is %q, want %q", string(raw), reason)
	}
}
