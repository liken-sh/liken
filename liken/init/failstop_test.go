package main

// Tests for the fail-stop record's two boot-time halves: whether a
// refusal has anywhere to write itself, and what the next boot makes
// of what it finds. Both run against a temporary machineState root.
// machineStateWritable is a package variable, like every other seam
// here, so these tests must not run in parallel.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/liken-sh/liken/machine"
)

var failStopAt = time.Date(2026, 7, 27, 4, 15, 0, 0, time.UTC)

// fakeMachineState gives the test a machineState root and states
// whether that root is mounted, restoring the real flag afterward.
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
		// The status field the record feeds is bounded, and the record
		// and the field must carry the same words.
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
				t.Fatal(err)
			}
			if got == nil {
				t.Fatal("the refusal left no record")
			}
			if got.Reason != tc.want {
				t.Errorf("reason:\nwant %q\ngot  %q", tc.want, got.Reason)
			}
			if !got.Time.Equal(failStopAt) {
				t.Errorf("time: want %s, got %s", failStopAt, got.Time)
			}
		})
	}
}

func TestRecordFailStopKeepsNothingWhereItWouldNotBelong(t *testing.T) {
	// Two refusals leave no record, for two different reasons. A
	// machineState that is not mounted goes out with the power, so a
	// record written there would say nothing to the next boot. An
	// install boot has a person at the console already, and it must
	// leave nothing behind on a machine it did not finish installing:
	// the first successful boot would read such a record back and
	// report the installer's refusal as the machine's own history.
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
				t.Fatal(err)
			}
			if got != nil {
				t.Fatalf("nothing should have been recorded: %+v", got)
			}
		})
	}
}

func TestCapReasonCutsOnARuneBoundary(t *testing.T) {
	// A message cut mid-rune is invalid UTF-8, and the API server
	// refuses the status write that carries it.
	reason := strings.Repeat("x", failStopReasonCap-1) + "é and more"
	got := capReason(reason)
	if len(got) != failStopReasonCap-1 {
		t.Errorf("the cut backs up to the rune boundary, got %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("the cut string is not valid UTF-8: %q", got[len(got)-4:])
	}
}

func TestRecordFailStopSurvivesAnUnwritableMachineState(t *testing.T) {
	// The machine is already stopping. A record that cannot be written
	// is reported, and the power-off goes ahead.
	dir := fakeMachineState(t, true)
	sealed := filepath.Join(dir, "sealed")
	if err := os.Mkdir(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })
	recordFailStop(sealed, "storage: role clusterState has no device", failStopAt)

	got, err := machine.ReadFailStop(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("nothing could have been written: %+v", got)
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
					t.Fatal(err)
				}
			}
			got := reportFailStop(dir)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("nothing was refused: %+v", got)
				}
				return
			}
			if got == nil || got.Reason != tc.want {
				t.Fatalf("want %q, got %+v", tc.want, got)
			}
			// The record is evidence, not a queue. Reading it leaves it
			// where it is, so every later boot reports the same refusal
			// until another one replaces it.
			again := reportFailStop(dir)
			if again == nil || again.Reason != tc.want {
				t.Errorf("the report cleared the record: %+v", again)
			}
		})
	}
}

func TestReportFailStopSurvivesAnUnreadableRecord(t *testing.T) {
	// A corrupt record is a reporting gap, not a reason to stop a boot
	// that is otherwise fine.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "failstop.yaml"), []byte("{not yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := reportFailStop(dir); got != nil {
		t.Errorf("an unreadable record reports nothing: %+v", got)
	}
}

func TestPublishBootFactsCarriesTheLastFailStop(t *testing.T) {
	// Console parity: what reportFailStop prints must also reach the
	// Machine's status, and the facts tree is the whole path there.
	tree, _ := fakeFactsMachine(t)
	reason := "machine identity: /etc/liken/cluster.yaml: expected kind Cluster, got \"Machine\""

	publishBootFacts(tree, bootFacts{
		storage:      machine.AllRolesInMemory(),
		time:         timeStatus(nil, nil),
		lastFailStop: &machine.FailStop{Reason: reason, Time: failStopAt},
	})

	facts, err := tree.Read()
	if err != nil {
		t.Fatal(err)
	}
	if facts.LastFailStop == nil {
		t.Fatal("the fail-stop record never reached the facts tree")
	}
	if facts.LastFailStop.Reason != reason || !facts.LastFailStop.Time.Equal(failStopAt) {
		t.Errorf("the record round-trips whole: %+v", facts.LastFailStop)
	}
	// cat parity: the reason is its own file under lastFailStop/.
	raw, err := os.ReadFile(filepath.Join(tree.Dir, "lastFailStop", "reason"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != reason {
		t.Errorf("lastFailStop/reason holds %q", string(raw))
	}
}
