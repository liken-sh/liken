package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordOf reads the record file in a volume's directory.
func recordOf(t *testing.T, n *node, id string) *record {
	t.Helper()
	rec, err := readRecord(filepath.Join(n.store.volumeDir(id), recordFile))
	if err != nil {
		t.Fatalf("reading the record of %s: %v", id, err)
	}
	return rec
}

// restarted returns a new driver on the same store, as after a rollout
// of the DaemonSet. Every target counts as mounted when mounted is true.
func restarted(t *testing.T, n *node, mounted bool) *node {
	t.Helper()
	again, _ := testNode(t, io.Discard)
	again.store = n.store
	again.mounted = func(string) bool { return mounted }
	again.resume(t.Context())
	return again
}

func TestTheRecordIsWrittenAtPublishAndGoneAtUnpublish(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	request.Secrets = map[string]string{tokenKey: "a token"}
	if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("publishing the volume: %v", err)
	}

	rec := recordOf(t, n, "csi-1")
	if rec.Target != request.TargetPath || !rec.Ephemeral || rec.Staging != "" {
		t.Errorf("the record is %+v, want an inline volume at %s", rec, request.TargetPath)
	}
	if rec.Attributes["url"] != fileURL(source) {
		t.Errorf("the record has the URL %q, want the URL in the volume attributes", rec.Attributes["url"])
	}
	if !rec.Credentials {
		t.Error("the record does not mark that the volume had a credential, want it marked")
	}
	content, err := os.ReadFile(filepath.Join(n.store.volumeDir("csi-1"), recordFile))
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	if strings.Contains(string(content), "a token") {
		t.Errorf("the record file has the credential: %s, want none", content)
	}
}

func TestARestartedDriverResumesItsMountedVolumes(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	inline := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "1h"})
	if _, err := n.NodePublishVolume(t.Context(), inline); err != nil {
		t.Fatalf("publishing the volume: %v", err)
	}
	published, _ := stagedWriteable(t, n, "config", fileURL(source))
	writeFiles(t, published.tree, map[string]string{"new.txt": "the pod wrote this"})

	again := restarted(t, n, true)
	again.mu.Lock()
	defer again.mu.Unlock()
	if len(again.volumes) != 2 {
		t.Fatalf("the restarted driver resumed %d volumes, want 2", len(again.volumes))
	}
	if len(again.staged) != 1 || len(again.watchers) != 1 || len(again.armingCancels) != 1 {
		t.Errorf("the driver has %d staged volumes, %d watches, and %d arming loops, want 1 of each",
			len(again.staged), len(again.watchers), len(again.armingCancels))
	}
	if len(again.followers) != 1 {
		t.Errorf("the driver has %d fetch loops, want 1", len(again.followers))
	}
	resumed := again.volumes["config"]
	if resumed.commit == "" {
		t.Error("the resumed writeable volume has no record of the commit of its tree, want one")
	}
	if resumed.target != published.target || !resumed.writeable() {
		t.Errorf("the volume is %+v, want the writeable volume at %s", resumed, published.target)
	}
}

func TestAStaleRecordOfAReadOnlyVolumeGoesWithItsDirectory(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("publishing the volume: %v", err)
	}

	again := restarted(t, n, false)
	if _, err := os.Stat(n.store.volumeDir("csi-1")); err == nil {
		t.Error("the directory of a read-only volume with no mount is still there after resume, want it deleted")
	}
	again.mu.Lock()
	defer again.mu.Unlock()
	if len(again.volumes) != 0 {
		t.Errorf("the restarted driver resumed %d volumes, want 0", len(again.volumes))
	}
}

func TestAStaleRecordOfAWriteableVolumeKeepsItsTree(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := stagedWriteable(t, n, "config", fileURL(source))
	writeFiles(t, published.tree, map[string]string{"new.txt": "the pod wrote this"})

	again := restarted(t, n, false)
	want := map[string]string{"a.txt": "one", "new.txt": "the pod wrote this"}
	if got := readTree(t, published.tree); !sameTree(got, want) {
		t.Errorf("the work tree has %v, want %v", got, want)
	}
	again.mu.Lock()
	defer again.mu.Unlock()
	if len(again.volumes) != 0 {
		t.Errorf("the restarted driver resumed %d volumes, want 0", len(again.volumes))
	}
}

func TestAResumedVolumeThatHadACredentialReportsItLost(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	request.Secrets = map[string]string{tokenKey: "a token"}
	if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("publishing the volume: %v", err)
	}

	again := restarted(t, n, true)
	again.mu.Lock()
	resumed := again.volumes["csi-1"]
	again.mu.Unlock()
	abnormal, message := resumed.health()
	if !abnormal || !strings.Contains(message, "no credential") {
		t.Errorf("the health report message is %q, want it to say the credential is missing", message)
	}
}

func TestResumeSkipsWhatItCannotRead(t *testing.T) {
	logs := &logbook{}
	n, _ := testNode(t, logs)
	n.resume(t.Context())

	volumes := filepath.Join(n.store.root, "volumes")
	if err := os.MkdirAll(filepath.Join(volumes, "csi-1"), 0o700); err != nil {
		t.Fatalf("making the volume directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(volumes, "csi-2"), 0o700); err != nil {
		t.Fatalf("making the volume directory: %v", err)
	}
	writeFiles(t, filepath.Join(volumes, "csi-2"), map[string]string{recordFile: "{"})

	refused := &record{VolumeID: "csi-3", Target: "/mount", Attributes: map[string]string{}}
	content, err := json.Marshal(refused)
	if err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(volumes, "csi-3"), 0o700); err != nil {
		t.Fatalf("making the volume directory: %v", err)
	}
	writeFiles(t, filepath.Join(volumes, "csi-3"), map[string]string{recordFile: string(content)})

	n.mounted = func(string) bool { return true }
	n.resume(t.Context())
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.volumes) != 0 {
		t.Errorf("the restarted driver resumed %d volumes, want 0", len(n.volumes))
	}
	if !strings.Contains(logs.String(), "The attributes in the record file of the volume were not parsed") {
		t.Errorf("the log is %q, want the line for the record file that was not parsed", logs)
	}
}

func TestTheRecordReportsWhatItCannotWrite(t *testing.T) {
	logs := &logbook{}
	n, _ := testNode(t, logs)
	directory := n.store.volumeDir("csi-1")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("making the volume directory: %v", err)
	}
	vol := &volume{id: "csi-1", directory: directory}
	readOnlyDir(t, directory)

	n.record(t.Context(), vol)
	n.forget(vol)
	if !strings.Contains(logs.String(), "The record file of the volume was not written") {
		t.Errorf("the log is %q, want the line for the record file that was not written", logs)
	}
	if !strings.Contains(logs.String(), "The record file of the volume was not removed") {
		t.Errorf("the log is %q, want the line for the record file that was not removed", logs)
	}
}

func TestAStaleDirectoryThatStaysIsReported(t *testing.T) {
	logs := &logbook{}
	n, _ := testNode(t, logs)
	volumes := filepath.Join(n.store.root, "volumes")
	directory := filepath.Join(volumes, "csi-1")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("making the volume directory: %v", err)
	}
	writeFiles(t, directory, map[string]string{"tree/a.txt": "one"})

	// A writeable volume keeps its tree even when nothing is mounted.
	n.drop(t.Context(), &record{VolumeID: "csi-1"}, directory)
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("the directory of a writeable volume is missing after drop: %v, want it kept", err)
	}

	readOnlyDir(t, volumes)
	n.drop(t.Context(), &record{VolumeID: "csi-1", Ephemeral: true}, directory)
	if !strings.Contains(logs.String(), "The volume directory was not removed") {
		t.Errorf("the log is %q, want the line for the directory that was not removed", logs)
	}
}

func TestMountedInFindsAPathInAMountinfoTable(t *testing.T) {
	table := strings.Join([]string{
		"24 30 0:22 / /sys rw,nosuid shared:7 - sysfs sysfs rw",
		`26 30 0:5 / /var/lib/kubelet/pods/9b1c/volumes/a\040path rw shared:9 - tmpfs tmpfs rw`,
		"too few fields",
	}, "\n")
	for _, c := range []struct {
		path string
		want bool
	}{
		{path: "/sys", want: true},
		{path: "/var/lib/kubelet/pods/9b1c/volumes/a path", want: true},
		{path: "/var/lib/kubelet/pods/9b1c/volumes/other", want: false},
	} {
		t.Run(c.path, func(t *testing.T) {
			if got := mountedIn(strings.NewReader(table), c.path); got != c.want {
				t.Errorf("mountedIn(%q) returned %v, want %v", c.path, got, c.want)
			}
		})
	}
}

func TestMountedNowReadsTheKernelMountTable(t *testing.T) {
	if !mountedNow(mountTable, "/") {
		t.Error("the root is not a mount, want true from mountedNow")
	}
	if mountedNow(mountTable, t.TempDir()) {
		t.Error("a temporary directory is a mount, want false from mountedNow")
	}
}

func TestMountedNowReportsNoMountWhenTheTableCannotBeRead(t *testing.T) {
	if mountedNow(filepath.Join(t.TempDir(), "mountinfo"), "/") {
		t.Error("a mount table that is not there returned that the root is a mount, want false")
	}
}

func TestTheNodeReadsTheMountTableAtItsMountinfoPath(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	table := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(table, []byte(
		"36 35 0:32 / /var/lib/kubelet/pods/9b1c/volumes/config rw - tmpfs tmpfs rw\n"), 0o600); err != nil {
		t.Fatalf("writing the mount table: %v", err)
	}
	n.mountinfo = table
	if !n.mounted("/var/lib/kubelet/pods/9b1c/volumes/config") {
		t.Error("no mount was read from the table, want one")
	}
	if n.mounted("/var/lib/kubelet/pods/9b1c/volumes/other") {
		t.Error("a mount was read that the table does not contain, want none")
	}
}
