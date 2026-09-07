package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mountTableWith writes a mount table that lists the targets, which is
// what the kernel shows a driver that restarted under them.
func mountTableWith(t *testing.T, targets ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mountinfo")
	lines := &bytes.Buffer{}
	for number, target := range targets {
		fmt.Fprintf(lines, "%d 25 0:23 / %s rw,relatime shared:4 - tmpfs tmpfs rw\n",
			26+number, target)
	}
	if err := os.WriteFile(path, lines.Bytes(), 0o644); err != nil {
		t.Fatalf("writing the mount table: %v", err)
	}
	return path
}

// loggingDriver is a test driver whose log the test reads, for the
// cases where a failure is a log line and nothing more.
func loggingDriver(t *testing.T) (*driver, *bytes.Buffer) {
	t.Helper()
	written := &bytes.Buffer{}
	answering := testDriver(t)
	answering.logger = slog.New(slog.NewTextHandler(written, nil))
	return answering, written
}

func TestAHoldSurvivesARestartWhileItsTargetIsStillAMount(t *testing.T) {
	answering := testDriver(t)
	target := filepath.Join(t.TempDir(), "mount")
	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", target, aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	restarted := driverIn(t, answering.store.root)
	restarted.mountinfo = mountTableWith(t, target)
	restarted.resume(t.Context())

	standing, held := restarted.holder("example-store")
	if !held {
		t.Fatal("the restarted driver holds nothing, want the hold back")
	}
	if standing.PodUID != "pod-uid-1" || standing.Target != target {
		t.Errorf("the hold names %+v, want pod-uid-1 at %s", standing, target)
	}
}

func TestAHoldWhoseTargetIsNoLongerAMountIsGivenUp(t *testing.T) {
	answering := testDriver(t)
	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "mount"),
			aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	restarted := driverIn(t, answering.store.root)
	restarted.resume(t.Context())

	if _, held := restarted.holder("example-store"); held {
		t.Error("the restarted driver took the hold back, want it given up")
	}
	if _, err := os.Stat(restarted.store.holdPath("example-store")); !os.IsNotExist(err) {
		t.Errorf("the hold file answered %v, want it removed", err)
	}
}

func TestAHoldTheDriverCannotReadIsGivenUp(t *testing.T) {
	answering, written := loggingDriver(t)
	holds := filepath.Join(answering.store.root, holdsDirectory)
	if err := os.MkdirAll(holds, 0o755); err != nil {
		t.Fatalf("making the holds directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(holds, "example-store"),
		[]byte("not json\n"), 0o600); err != nil {
		t.Fatalf("writing the hold: %v", err)
	}
	answering.resume(t.Context())
	if _, held := answering.holder("example-store"); held {
		t.Error("the driver took a hold it could not read, want it given up")
	}
	if !strings.Contains(written.String(), "the hold was not read") {
		t.Errorf("the log reads %q, want it to say the hold was not read", written)
	}
}

func TestAStoreWithNoHoldsResumesNothing(t *testing.T) {
	answering := testDriver(t)
	answering.resume(t.Context())
	if held := answering.heldHandles(); len(held) != 0 {
		t.Errorf("the driver holds %v, want nothing", held)
	}
}

func TestAHoldTheDriverCannotWriteIsLoggedAndTheMountStands(t *testing.T) {
	answering, written := loggingDriver(t)
	// A directory where the hold file goes is a write the kernel refuses.
	if err := os.MkdirAll(answering.store.holdPath("example-store"), 0o755); err != nil {
		t.Fatalf("making the directory: %v", err)
	}
	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "mount"),
			aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if _, held := answering.holder("example-store"); !held {
		t.Error("the driver holds nothing, want the hold in memory")
	}
	if !strings.Contains(written.String(), "the hold was not written") {
		t.Errorf("the log reads %q, want it to say the hold was not written", written)
	}
}

func TestAHoldTheDriverCannotRemoveIsLogged(t *testing.T) {
	answering, written := loggingDriver(t)
	if err := os.MkdirAll(filepath.Join(answering.store.holdPath("example-store"), "inner"),
		0o755); err != nil {
		t.Fatalf("making the directory: %v", err)
	}
	answering.dropHold(t.Context(), "example-store")
	if !strings.Contains(written.String(), "the hold was not removed") {
		t.Errorf("the log reads %q, want it to say the hold was not removed", written)
	}
}

func TestTheHeldHandlesAreTheOnesAPodHolds(t *testing.T) {
	answering := testDriver(t)
	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", filepath.Join(t.TempDir(), "mount"),
			aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	held := answering.heldHandles()
	if len(held) != 1 || !held["example-store"] {
		t.Errorf("the driver holds %v, want example-store alone", held)
	}
}

func TestAHoldFileThatIsNotThereReadsNothing(t *testing.T) {
	if _, err := readHold(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("readHold answered no error, want one")
	}
}

func TestTheKernelsMountTableSaysWhichTargetsAreStillMounts(t *testing.T) {
	table := "26 25 0:23 / /kubelet/mount rw,relatime shared:4 - tmpfs tmpfs rw\n" +
		"27 25 0:24 / /some\\040path rw,relatime shared:5 - tmpfs tmpfs rw\n" +
		"short line\n"
	for _, c := range []struct {
		name    string
		path    string
		mounted bool
	}{
		{name: "a target the kernel holds", path: "/kubelet/mount", mounted: true},
		{name: "a target with an escaped space", path: "/some path", mounted: true},
		{name: "a target the kernel does not hold", path: "/kubelet/other"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := mountedIn(strings.NewReader(table), c.path); got != c.mounted {
				t.Errorf("mountedIn(%q) answered %v, want %v", c.path, got, c.mounted)
			}
		})
	}
}

func TestAMountTableTheDriverCannotOpenHoldsNothing(t *testing.T) {
	if mountedNow(filepath.Join(t.TempDir(), "absent"), "/kubelet/mount") {
		t.Error("mountedNow answered true, want false with no table to read")
	}
}
