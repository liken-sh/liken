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

// mountTableWith writes a mount table that lists the targets as mounts,
// as the kernel shows them to a driver that restarted while they stayed
// mounted.
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

// loggingDriver returns a test driver and its log, for a failure that
// only writes a log line.
func loggingDriver(t *testing.T) (*driver, *bytes.Buffer) {
	t.Helper()
	written := &bytes.Buffer{}
	d := testDriver(t)
	d.logger = slog.New(slog.NewTextHandler(written, nil))
	return d, written
}

// publishedAt publishes the handle once for each pod, each at its own
// target, and returns the targets in the order of the pods.
func publishedAt(t *testing.T, d *driver, handle string, pods ...podReference) []string {
	t.Helper()
	targets := make([]string, 0, len(pods))
	for _, pod := range pods {
		target := filepath.Join(t.TempDir(), pod.name)
		if _, err := d.NodePublishVolume(t.Context(),
			publishing(handle, target, pod)); err != nil {
			t.Fatalf("NodePublishVolume for %s: %v", pod.name, err)
		}
		targets = append(targets, target)
	}
	return targets
}

func TestEveryRecordIsLoadedAfterARestartWhileItsTargetIsStillAMount(t *testing.T) {
	d := testDriver(t)
	targets := publishedAt(t, d, "example-store",
		aPod("first", "pod-uid-1"), aPod("second", "pod-uid-2"))

	restarted := driverIn(t, d.store.root)
	restarted.mountinfo = mountTableWith(t, targets...)
	restarted.loadRecords(t.Context())

	records := restarted.recordsOf("example-store")
	if len(records) != 2 {
		t.Fatalf("the restarted driver has records %+v, want both back", records)
	}
	if records[0].PodUID != "pod-uid-1" || records[0].Target != targets[0] ||
		records[1].PodUID != "pod-uid-2" || records[1].Target != targets[1] {
		t.Errorf("the records are %+v, want pod-uid-1 at %s and pod-uid-2 at %s",
			records, targets[0], targets[1])
	}
}

func TestARestartDeletesOnlyTheRecordsWhoseTargetsAreNoLongerMounts(t *testing.T) {
	d := testDriver(t)
	targets := publishedAt(t, d, "example-store",
		aPod("first", "pod-uid-1"), aPod("second", "pod-uid-2"))

	restarted := driverIn(t, d.store.root)
	restarted.mountinfo = mountTableWith(t, targets[1])
	restarted.loadRecords(t.Context())

	records := restarted.recordsOf("example-store")
	if len(records) != 1 || records[0].Target != targets[1] {
		t.Errorf("the restarted driver has records %+v, want only the record at %s", records, targets[1])
	}
	if _, err := os.Stat(restarted.store.recordPath("example-store", targets[0])); !os.IsNotExist(err) {
		t.Errorf("the record file of the unmounted target returned %v, want it removed", err)
	}
}

func TestARestartWithNoMountsLeftDeletesTheHandlesRecordsDirectory(t *testing.T) {
	d := testDriver(t)
	publishedAt(t, d, "example-store", aPod("reader", "pod-uid-1"))

	restarted := driverIn(t, d.store.root)
	restarted.loadRecords(t.Context())

	if records := restarted.recordsOf("example-store"); len(records) != 0 {
		t.Errorf("the restarted driver has records %+v, want none", records)
	}
	if _, err := os.Stat(restarted.store.recordsPath("example-store")); !os.IsNotExist(err) {
		t.Errorf("the handle's records directory returned %v, want it removed", err)
	}
}

// writeSingleFileRecord writes a record as one file at holds/<handle>,
// the layout that earlier versions of the driver wrote.
func writeSingleFileRecord(t *testing.T, root, handle string, record mountRecord) {
	t.Helper()
	content, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("encoding the record: %v", err)
	}
	dir := filepath.Join(root, recordsDirectory)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("making the records directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, handle), content, 0o600); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
}

func TestARecordWrittenAsOneFilePerHandleIsLoadedAfterARestart(t *testing.T) {
	d := testDriver(t)
	target := filepath.Join(t.TempDir(), "mount")
	writeSingleFileRecord(t, d.store.root, "example-store",
		mountRecord{PodUID: "pod-uid-1", PodName: "reader", PodNamespace: "example", Target: target})
	d.mountinfo = mountTableWith(t, target)
	d.loadRecords(t.Context())

	records := d.recordsOf("example-store")
	if len(records) != 1 || records[0].PodUID != "pod-uid-1" || records[0].Target != target {
		t.Fatalf("the driver has records %+v, want pod-uid-1 at %s", records, target)
	}
	restarted := driverIn(t, d.store.root)
	restarted.mountinfo = mountTableWith(t, target)
	restarted.loadRecords(t.Context())
	if records := restarted.recordsOf("example-store"); len(records) != 1 {
		t.Errorf("a second restart has records %+v, want the record kept on disk", records)
	}
}

func TestARecordWrittenAsOneFilePerHandleIsDeletedWhenItsTargetIsNotMounted(t *testing.T) {
	d := testDriver(t)
	writeSingleFileRecord(t, d.store.root, "example-store",
		mountRecord{PodUID: "pod-uid-1", Target: filepath.Join(t.TempDir(), "mount")})
	d.loadRecords(t.Context())

	if records := d.recordsOf("example-store"); len(records) != 0 {
		t.Errorf("the driver has records %+v, want none", records)
	}
	if _, err := os.Stat(d.store.recordsPath("example-store")); !os.IsNotExist(err) {
		t.Errorf("the record file returned %v, want it removed", err)
	}
}

func TestARecordFileTheDriverCannotReadIsDeleted(t *testing.T) {
	d, written := loggingDriver(t)
	dir := d.store.recordsPath("example-store")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("making the records directory: %v", err)
	}
	unreadable := filepath.Join(dir, "0123456789abcdef0123456789abcdef")
	if err := os.WriteFile(unreadable, []byte("not json\n"), 0o600); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	d.loadRecords(t.Context())
	if records := d.recordsOf("example-store"); len(records) != 0 {
		t.Errorf("the driver loaded a record it could not read: %+v", records)
	}
	if _, err := os.Stat(unreadable); !os.IsNotExist(err) {
		t.Errorf("the record file returned %v, want it removed", err)
	}
	if !strings.Contains(written.String(), "record file was not read") {
		t.Errorf("the log reads %q, want it to say the record file was not read", written)
	}
}

func TestARecordsDirectoryTheDriverCannotListIsLogged(t *testing.T) {
	d, written := loggingDriver(t)
	dir := d.store.recordsPath("example-store")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("making the records directory: %v", err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatalf("closing the records directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	d.loadRecords(t.Context())
	if !strings.Contains(written.String(), "records directory of the volume was not read") {
		t.Errorf("the log reads %q, want it to say the records directory was not read", written)
	}
}

func TestASingleFileRecordTheDriverCannotReadOrDeleteIsLogged(t *testing.T) {
	d, written := loggingDriver(t)
	dir := filepath.Join(d.store.root, recordsDirectory)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("making the records directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "example-store"), []byte("not json\n"), 0o600); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	// The kernel refuses to remove a file from a directory that the
	// driver cannot write.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("closing the records directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	d.loadRecords(t.Context())
	if records := d.recordsOf("example-store"); len(records) != 0 {
		t.Errorf("the driver loaded a record it could not read: %+v", records)
	}
	for _, line := range []string{"record file was not read", "record file was not deleted"} {
		if !strings.Contains(written.String(), line) {
			t.Errorf("the log reads %q, want it to say %s", written, line)
		}
	}
}

func TestAStoreWithNoRecordsLoadsNothing(t *testing.T) {
	d := testDriver(t)
	d.loadRecords(t.Context())
	if records := d.publishedHandles(); len(records) != 0 {
		t.Errorf("the published handles are %v, want none", records)
	}
}

func TestARecordTheDriverCannotWriteIsLoggedAndThePublishSucceeds(t *testing.T) {
	d, written := loggingDriver(t)
	target := filepath.Join(t.TempDir(), "mount")
	// A directory at the record file's path makes the write fail.
	if err := os.MkdirAll(d.store.recordPath("example-store", target), 0o755); err != nil {
		t.Fatalf("making the directory: %v", err)
	}
	if _, err := d.NodePublishVolume(t.Context(),
		publishing("example-store", target, aPod("reader", "pod-uid-1"))); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if records := d.recordsOf("example-store"); len(records) != 1 {
		t.Errorf("the driver has records %+v, want the record in memory", records)
	}
	if !strings.Contains(written.String(), "was not written to disk") {
		t.Errorf("the log reads %q, want it to say the record was not written", written)
	}
}

func TestARecordFileTheDriverCannotDeleteIsLogged(t *testing.T) {
	d, written := loggingDriver(t)
	target := filepath.Join(t.TempDir(), "mount")
	if err := os.MkdirAll(filepath.Join(d.store.recordPath("example-store", target), "inner"),
		0o755); err != nil {
		t.Fatalf("making the directory: %v", err)
	}
	d.deleteRecord(t.Context(), "example-store", target)
	if !strings.Contains(written.String(), "record file was not deleted") {
		t.Errorf("the log reads %q, want it to say the record file was not deleted", written)
	}
}

func TestPublishedHandlesListsAHandleOnceForAllItsPods(t *testing.T) {
	d := testDriver(t)
	publishedAt(t, d, "example-store",
		aPod("first", "pod-uid-1"), aPod("second", "pod-uid-2"))
	records := d.publishedHandles()
	if len(records) != 1 || !records["example-store"] {
		t.Errorf("the published handles are %v, want example-store alone", records)
	}
}

func TestTheVolumesGaugeCountsAHandleOnceForAllThePodsThatMountIt(t *testing.T) {
	d := testDriver(t)
	publishedAt(t, d, "example-store",
		aPod("first", "pod-uid-1"), aPod("second", "pod-uid-2"))
	publishedAt(t, d, "some-cache", aPod("third", "pod-uid-3"))
	if got := testutil.ToFloat64(d.metrics.volumes); got != 2 {
		t.Errorf("per_node_csi_volumes reads %v, want 2", got)
	}
}

func TestReadRecordFailsForAMissingFile(t *testing.T) {
	if _, err := readRecord(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("readRecord returned no error, want one")
	}
}

func TestMountedInFindsTheMountPointsOfTheMountTable(t *testing.T) {
	table := "26 25 0:23 / /kubelet/mount rw,relatime shared:4 - tmpfs tmpfs rw\n" +
		"27 25 0:24 / /some\\040path rw,relatime shared:5 - tmpfs tmpfs rw\n" +
		"short line\n"
	for _, c := range []struct {
		name    string
		path    string
		mounted bool
	}{
		{name: "a mount point", path: "/kubelet/mount", mounted: true},
		{name: "a target with an escaped space", path: "/some path", mounted: true},
		{name: "a path that is not a mount point", path: "/kubelet/other"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := mountedIn(strings.NewReader(table), c.path); got != c.mounted {
				t.Errorf("mountedIn(%q) returned %v, want %v", c.path, got, c.mounted)
			}
		})
	}
}

func TestMountedNowReturnsFalseWhenTheMountTableCannotBeOpened(t *testing.T) {
	if mountedNow(filepath.Join(t.TempDir(), "absent"), "/kubelet/mount") {
		t.Error("mountedNow returned true, want false with no table to read")
	}
}
