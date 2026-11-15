package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
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

// publishedAt publishes the handle once for each pod, each at its own
// target, and returns the targets in the order of the pods.
func publishedAt(t *testing.T, answering *driver, handle string, pods ...podReference) []string {
	t.Helper()
	targets := make([]string, 0, len(pods))
	for _, pod := range pods {
		target := filepath.Join(t.TempDir(), pod.name)
		if _, err := answering.NodePublishVolume(t.Context(),
			publishing(handle, target, pod)); err != nil {
			t.Fatalf("NodePublishVolume for %s: %v", pod.name, err)
		}
		targets = append(targets, target)
	}
	return targets
}

func TestEveryHoldSurvivesARestartWhileItsTargetIsStillAMount(t *testing.T) {
	answering := testDriver(t)
	targets := publishedAt(t, answering, "example-store",
		aPod("first", "pod-uid-1"), aPod("second", "pod-uid-2"))

	restarted := driverIn(t, answering.store.root)
	restarted.mountinfo = mountTableWith(t, targets...)
	restarted.resume(t.Context())

	held := restarted.holdsOf("example-store")
	if len(held) != 2 {
		t.Fatalf("the restarted driver has holds %+v, want both back", held)
	}
	if held[0].PodUID != "pod-uid-1" || held[0].Target != targets[0] ||
		held[1].PodUID != "pod-uid-2" || held[1].Target != targets[1] {
		t.Errorf("the holds name %+v, want pod-uid-1 at %s and pod-uid-2 at %s",
			held, targets[0], targets[1])
	}
}

func TestARestartGivesUpOnlyTheHoldsWhoseTargetsAreNoLongerMounts(t *testing.T) {
	answering := testDriver(t)
	targets := publishedAt(t, answering, "example-store",
		aPod("first", "pod-uid-1"), aPod("second", "pod-uid-2"))

	restarted := driverIn(t, answering.store.root)
	restarted.mountinfo = mountTableWith(t, targets[1])
	restarted.resume(t.Context())

	held := restarted.holdsOf("example-store")
	if len(held) != 1 || held[0].Target != targets[1] {
		t.Errorf("the restarted driver has holds %+v, want the one at %s alone", held, targets[1])
	}
	if _, err := os.Stat(restarted.store.holdPath("example-store", targets[0])); !os.IsNotExist(err) {
		t.Errorf("the hold file of the unmounted target answered %v, want it removed", err)
	}
}

func TestARestartWithNoMountsLeftRemovesTheHandlesHolds(t *testing.T) {
	answering := testDriver(t)
	publishedAt(t, answering, "example-store", aPod("reader", "pod-uid-1"))

	restarted := driverIn(t, answering.store.root)
	restarted.resume(t.Context())

	if held := restarted.holdsOf("example-store"); len(held) != 0 {
		t.Errorf("the restarted driver has holds %+v, want none", held)
	}
	if _, err := os.Stat(restarted.store.holdsPath("example-store")); !os.IsNotExist(err) {
		t.Errorf("the handle's holds directory answered %v, want it removed", err)
	}
}

// singleFileHold writes a hold as one file at holds/<handle>, the layout
// in which a driver that refused a second pod recorded its one hold.
func singleFileHold(t *testing.T, root, handle string, taken hold) {
	t.Helper()
	content, err := json.Marshal(taken)
	if err != nil {
		t.Fatalf("encoding the hold: %v", err)
	}
	holds := filepath.Join(root, holdsDirectory)
	if err := os.MkdirAll(holds, 0o755); err != nil {
		t.Fatalf("making the holds directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(holds, handle), content, 0o600); err != nil {
		t.Fatalf("writing the hold: %v", err)
	}
}

func TestAHoldWrittenAsOneFilePerHandleSurvivesARestart(t *testing.T) {
	answering := testDriver(t)
	target := filepath.Join(t.TempDir(), "mount")
	singleFileHold(t, answering.store.root, "example-store",
		hold{PodUID: "pod-uid-1", PodName: "reader", PodNamespace: "example", Target: target})
	answering.mountinfo = mountTableWith(t, target)
	answering.resume(t.Context())

	held := answering.holdsOf("example-store")
	if len(held) != 1 || held[0].PodUID != "pod-uid-1" || held[0].Target != target {
		t.Fatalf("the driver has holds %+v, want pod-uid-1 at %s", held, target)
	}
	restarted := driverIn(t, answering.store.root)
	restarted.mountinfo = mountTableWith(t, target)
	restarted.resume(t.Context())
	if held := restarted.holdsOf("example-store"); len(held) != 1 {
		t.Errorf("a second restart has holds %+v, want the hold kept on the disk", held)
	}
}

func TestAHoldWrittenAsOneFilePerHandleIsGivenUpWithItsMount(t *testing.T) {
	answering := testDriver(t)
	singleFileHold(t, answering.store.root, "example-store",
		hold{PodUID: "pod-uid-1", Target: filepath.Join(t.TempDir(), "mount")})
	answering.resume(t.Context())

	if held := answering.holdsOf("example-store"); len(held) != 0 {
		t.Errorf("the driver has holds %+v, want none", held)
	}
	if _, err := os.Stat(answering.store.holdsPath("example-store")); !os.IsNotExist(err) {
		t.Errorf("the hold file answered %v, want it removed", err)
	}
}

func TestAHoldTheDriverCannotReadIsGivenUp(t *testing.T) {
	answering, written := loggingDriver(t)
	holds := answering.store.holdsPath("example-store")
	if err := os.MkdirAll(holds, 0o755); err != nil {
		t.Fatalf("making the holds directory: %v", err)
	}
	unreadable := filepath.Join(holds, "0123456789abcdef0123456789abcdef")
	if err := os.WriteFile(unreadable, []byte("not json\n"), 0o600); err != nil {
		t.Fatalf("writing the hold: %v", err)
	}
	answering.resume(t.Context())
	if held := answering.holdsOf("example-store"); len(held) != 0 {
		t.Errorf("the driver took a hold it could not read: %+v", held)
	}
	if _, err := os.Stat(unreadable); !os.IsNotExist(err) {
		t.Errorf("the hold file answered %v, want it removed", err)
	}
	if !strings.Contains(written.String(), "the hold was not read") {
		t.Errorf("the log reads %q, want it to say the hold was not read", written)
	}
}

func TestAHandlesHoldsTheDriverCannotListAreLogged(t *testing.T) {
	answering, written := loggingDriver(t)
	holds := answering.store.holdsPath("example-store")
	if err := os.MkdirAll(holds, 0o755); err != nil {
		t.Fatalf("making the holds directory: %v", err)
	}
	if err := os.Chmod(holds, 0); err != nil {
		t.Fatalf("closing the holds directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(holds, 0o755) })
	answering.resume(t.Context())
	if !strings.Contains(written.String(), "the holds were not read") {
		t.Errorf("the log reads %q, want it to say the holds were not read", written)
	}
}

func TestASingleFileHoldTheDriverCannotReadOrRemoveIsLogged(t *testing.T) {
	answering, written := loggingDriver(t)
	holds := filepath.Join(answering.store.root, holdsDirectory)
	if err := os.MkdirAll(holds, 0o755); err != nil {
		t.Fatalf("making the holds directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(holds, "example-store"), []byte("not json\n"), 0o600); err != nil {
		t.Fatalf("writing the hold: %v", err)
	}
	// A directory the driver cannot write is a removal the kernel refuses.
	if err := os.Chmod(holds, 0o555); err != nil {
		t.Fatalf("closing the holds directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(holds, 0o755) })
	answering.resume(t.Context())
	if held := answering.holdsOf("example-store"); len(held) != 0 {
		t.Errorf("the driver took a hold it could not read: %+v", held)
	}
	for _, line := range []string{"the hold was not read", "the hold was not removed"} {
		if !strings.Contains(written.String(), line) {
			t.Errorf("the log reads %q, want it to say %s", written, line)
		}
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
	target := filepath.Join(t.TempDir(), "mount")
	// A directory where the hold file goes is a write the kernel refuses.
	if err := os.MkdirAll(answering.store.holdPath("example-store", target), 0o755); err != nil {
		t.Fatalf("making the directory: %v", err)
	}
	if _, err := answering.NodePublishVolume(t.Context(),
		publishing("example-store", target, aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if held := answering.holdsOf("example-store"); len(held) != 1 {
		t.Errorf("the driver has holds %+v, want the hold in memory", held)
	}
	if !strings.Contains(written.String(), "the hold was not written") {
		t.Errorf("the log reads %q, want it to say the hold was not written", written)
	}
}

func TestAHoldTheDriverCannotRemoveIsLogged(t *testing.T) {
	answering, written := loggingDriver(t)
	target := filepath.Join(t.TempDir(), "mount")
	if err := os.MkdirAll(filepath.Join(answering.store.holdPath("example-store", target), "inner"),
		0o755); err != nil {
		t.Fatalf("making the directory: %v", err)
	}
	answering.dropHold(t.Context(), "example-store", target)
	if !strings.Contains(written.String(), "the hold was not removed") {
		t.Errorf("the log reads %q, want it to say the hold was not removed", written)
	}
}

func TestTheHeldHandlesAreTheOnesAPodHolds(t *testing.T) {
	answering := testDriver(t)
	publishedAt(t, answering, "example-store",
		aPod("first", "pod-uid-1"), aPod("second", "pod-uid-2"))
	held := answering.heldHandles()
	if len(held) != 1 || !held["example-store"] {
		t.Errorf("the driver holds %v, want example-store alone", held)
	}
}

func TestTheVolumesGaugeCountsAHandleOnceForAllThePodsThatHoldIt(t *testing.T) {
	answering := testDriver(t)
	publishedAt(t, answering, "example-store",
		aPod("first", "pod-uid-1"), aPod("second", "pod-uid-2"))
	publishedAt(t, answering, "some-cache", aPod("third", "pod-uid-3"))
	if got := testutil.ToFloat64(answering.readings.volumes); got != 2 {
		t.Errorf("per_node_csi_volumes reads %v, want 2", got)
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
