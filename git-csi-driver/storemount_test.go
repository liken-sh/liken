package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The store path every fixture table is read for. It is the default, so
// the tables read the way a node plugin with no --store reads its own.
const fixtureStore = defaultStore

func TestTheMountTableSaysWhetherTheStoreSurvivesAReboot(t *testing.T) {
	for _, c := range []struct {
		table string
		// refused is a word the reason has to name, or empty when the
		// store survives a reboot.
		refused string
	}{
		// A copy of what a liken-1 node plugin reads: the hostPath is a
		// bind of the pod-storage partition, an ext4 disk.
		{table: "liken-node.mountinfo"},
		{table: "bind.mountinfo"},
		{table: "overlay-on-disk.mountinfo"},
		{table: "no-entry-for-the-store.mountinfo"},
		{table: "tmpfs.mountinfo", refused: "tmpfs"},
		{table: "ramfs.mountinfo", refused: "ramfs"},
		{table: "rootfs.mountinfo", refused: "rootfs"},
		{table: "tmpfs-over-a-disk.mountinfo", refused: "tmpfs"},
		{table: "overlay-on-tmpfs.mountinfo", refused: "/run/writes/upper"},
		{table: "overlay-with-an-escaped-upper.mountinfo", refused: "/run/a,b/upper"},
		// A liken node with no pod-storage partition: the hostPath is a
		// bind of the root overlay, and the tmpfs under it is not in
		// the pod's table. The store's line is written from how liken's
		// init mounts the root overlay, not copied from a node.
		{table: "liken-node-without-pod-storage.mountinfo", refused: "/liken-boot/upper"},
		{table: "read-only-overlay.mountinfo", refused: "no upper directory"},
		{table: "empty.mountinfo", refused: "no mount"},
		{table: "not-there.mountinfo", refused: "not-there.mountinfo"},
	} {
		t.Run(c.table, func(t *testing.T) {
			got := storeRefusal(filepath.Join("testdata", "mountinfo", c.table), fixtureStore)
			if c.refused == "" && got != "" {
				t.Errorf("the store was refused: %s", got)
			}
			if c.refused != "" && !strings.Contains(got, c.refused) {
				t.Errorf("the reason %q does not name %q", got, c.refused)
			}
		})
	}
}

func TestAMountPointReadsWithItsEscapes(t *testing.T) {
	table := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(table, []byte(
		"1 0 8:1 / / rw - ext4 /dev/sda1 rw\n"+
			`40 1 0:40 / /srv/git\040csi rw - tmpfs tmpfs rw`+"\n"), 0o600); err != nil {
		t.Fatalf("writing the mount table: %v", err)
	}
	if got := storeRefusal(table, "/srv/git csi"); !strings.Contains(got, "tmpfs") {
		t.Errorf("a store under an escaped mount point was not refused: %q", got)
	}
}

// memoryStore is a node whose mount table puts its store on tmpfs.
func memoryStore(t *testing.T, logs io.Writer) *node {
	t.Helper()
	answering, _ := testNode(t, logs)
	moveStoreToMemory(t, answering)
	return answering
}

// moveStoreToMemory gives the node a mount table that puts its store
// on tmpfs, and runs the check the node runs at start.
func moveStoreToMemory(t *testing.T, answering *node) {
	t.Helper()
	table := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(table, []byte(
		"1 0 8:1 / / rw - ext4 /dev/sda1 rw\n"+
			"40 1 0:40 / "+answering.store.root+" rw - tmpfs tmpfs rw\n"), 0o600); err != nil {
		t.Fatalf("writing the mount table: %v", err)
	}
	answering.mountinfo = table
	answering.checkStore(t.Context())
}

func TestAStoreInMemoryIsReportedAtStart(t *testing.T) {
	logs := &bytes.Buffer{}
	answering := memoryStore(t, logs)
	if !strings.Contains(logs.String(), "refuses writeable volumes") {
		t.Errorf("the log does not say the node refuses writeable volumes:\n%s", logs)
	}
	if got := nodeGaugeOf(t, answering.readings, "git_csi_store_refuses_writeable"); got != 1 {
		t.Errorf("git_csi_store_refuses_writeable is %v, want 1", got)
	}
}

func TestAStoreOnADiskIsNotReported(t *testing.T) {
	logs := &bytes.Buffer{}
	answering, _ := testNode(t, logs)
	answering.mountinfo = filepath.Join("testdata", "mountinfo", "bind.mountinfo")
	answering.store = newStore(fixtureStore)
	answering.checkStore(t.Context())
	if strings.Contains(logs.String(), "refuses") {
		t.Errorf("the log reports a store on a disk:\n%s", logs)
	}
	if got := nodeGaugeOf(t, answering.readings, "git_csi_store_refuses_writeable"); got != 0 {
		t.Errorf("git_csi_store_refuses_writeable is %v, want 0", got)
	}
}

func TestAStoreInMemoryRefusesAWriteableVolume(t *testing.T) {
	answering := memoryStore(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	_, err := answering.NodeStageVolume(t.Context(), stageRequest(t, "config", fileURL(source), nil))
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("NodeStageVolume answered %v, want %v", got, codes.FailedPrecondition)
	}
	if !strings.Contains(status.Convert(err).Message(), "tmpfs") {
		t.Errorf("the refusal does not name the filesystem: %v", err)
	}
}

func TestAStoreInMemoryStillServesAReadOnlyClaim(t *testing.T) {
	answering := memoryStore(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	_, staged := stagedReadOnly(t, answering, "config", fileURL(source), nil)
	if got := readTree(t, staged.tree); !sameTree(got, map[string]string{"a.txt": "one"}) {
		t.Errorf("the read-only claim's tree holds %v", got)
	}
}

func TestAStoreInMemoryStillStagesATreeItHolds(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := stageRequest(t, "config", fileURL(source), nil)
	if _, err := answering.NodeStageVolume(t.Context(), request); err != nil {
		t.Fatalf("NodeStageVolume: %v", err)
	}
	if _, err := answering.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
		VolumeId: "config", StagingTargetPath: request.StagingTargetPath,
	}); err != nil {
		t.Fatalf("NodeUnstageVolume: %v", err)
	}

	// The work tree holds work that may not have pushed, and only a
	// stage with the pod's Secret can push it.
	moveStoreToMemory(t, answering)
	if _, err := answering.NodeStageVolume(t.Context(), request); err != nil {
		t.Errorf("NodeStageVolume refused a volume whose tree the node holds: %v", err)
	}
}
