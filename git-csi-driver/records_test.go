package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RecordOf reads the record a volume's directory carries.
func recordOf(t *testing.T, answering *node, id string) *record {
	t.Helper()
	held, err := readRecord(filepath.Join(answering.store.volumeDir(id), recordFile))
	if err != nil {
		t.Fatalf("reading the record of %s: %v", id, err)
	}
	return held
}

// Restarted is a second driver on the same store, which is what a rollout
// of the DaemonSet leaves.
func restarted(t *testing.T, answering *node, mounted bool) *node {
	t.Helper()
	again, _ := testNode(t, io.Discard)
	again.store = answering.store
	again.mounted = func(string) bool { return mounted }
	again.resume(t.Context())
	return again
}

func TestTheRecordIsWrittenAtPublishAndGoneAtUnpublish(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	request.Secrets = map[string]string{tokenKey: "a token"}
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	held := recordOf(t, answering, "csi-1")
	if held.Target != request.TargetPath || !held.Ephemeral || held.Staging != "" {
		t.Errorf("the record is %+v, want an inline volume at %s", held, request.TargetPath)
	}
	if held.Attributes["url"] != fileURL(source) {
		t.Errorf("the record names %q, want the URL", held.Attributes["url"])
	}
	if !held.Credentials {
		t.Error("the record does not say the volume had a credential")
	}
	content, err := os.ReadFile(filepath.Join(answering.store.volumeDir("csi-1"), recordFile))
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	if strings.Contains(string(content), "a token") {
		t.Errorf("the record carries the credential: %s", content)
	}
}

func TestADriverThatRestartsTakesBackItsVolumes(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	inline := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "1h"})
	if _, err := answering.NodePublishVolume(t.Context(), inline); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	published, _ := writeableVolume(t, answering, "config", fileURL(source))
	writeFiles(t, published.tree, map[string]string{"new.txt": "the pod wrote this"})

	again := restarted(t, answering, true)
	again.mu.Lock()
	defer again.mu.Unlock()
	if len(again.volumes) != 2 {
		t.Fatalf("the driver took back %d volumes, want 2", len(again.volumes))
	}
	if len(again.staged) != 1 || len(again.watchers) != 1 || len(again.armings) != 1 {
		t.Errorf("the driver holds %d staged, %d watches, %d arming loops, want one of each",
			len(again.staged), len(again.watchers), len(again.armings))
	}
	if len(again.followers) != 1 {
		t.Errorf("the driver holds %d fetch loops, want 1", len(again.followers))
	}
	resumed := again.volumes["config"]
	if resumed.commit == "" {
		t.Error("the writeable volume forgot the commit its tree stands on")
	}
	if resumed.target != published.target || !resumed.writeable {
		t.Errorf("the volume is %+v, want the writeable volume at %s", resumed, published.target)
	}
}

func TestAStaleRecordOfAReadOnlyVolumeGoesWithItsDirectory(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	again := restarted(t, answering, false)
	if _, err := os.Stat(answering.store.volumeDir("csi-1")); err == nil {
		t.Error("the directory of a volume that is no longer mounted stayed")
	}
	again.mu.Lock()
	defer again.mu.Unlock()
	if len(again.volumes) != 0 {
		t.Errorf("the driver took back %d volumes, want 0", len(again.volumes))
	}
}

func TestAStaleRecordOfAWriteableVolumeKeepsItsTree(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := writeableVolume(t, answering, "config", fileURL(source))
	writeFiles(t, published.tree, map[string]string{"new.txt": "the pod wrote this"})

	again := restarted(t, answering, false)
	want := map[string]string{"a.txt": "one", "new.txt": "the pod wrote this"}
	if got := readTree(t, published.tree); !sameTree(got, want) {
		t.Errorf("the work tree holds %v, want %v", got, want)
	}
	again.mu.Lock()
	defer again.mu.Unlock()
	if len(again.volumes) != 0 {
		t.Errorf("the driver took back %d volumes, want 0", len(again.volumes))
	}
}

func TestAResumedVolumeWithACredentialSaysSo(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	request.Secrets = map[string]string{tokenKey: "a token"}
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	again := restarted(t, answering, true)
	again.mu.Lock()
	resumed := again.volumes["csi-1"]
	again.mu.Unlock()
	abnormal, message := resumed.report()
	if !abnormal || !strings.Contains(message, "no credential") {
		t.Errorf("the condition says %q, want the credential the driver lost", message)
	}
}

func TestResumeSkipsWhatItCannotRead(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	answering.resume(t.Context())

	volumes := filepath.Join(answering.store.root, "volumes")
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

	answering.mounted = func(string) bool { return true }
	answering.resume(t.Context())
	answering.mu.Lock()
	defer answering.mu.Unlock()
	if len(answering.volumes) != 0 {
		t.Errorf("the driver took back %d volumes, want 0", len(answering.volumes))
	}
	if !strings.Contains(logs.String(), "the volume's record was not read") {
		t.Errorf("the log is %q, want the refused record in it", logs)
	}
}

func TestTheRecordReportsWhatItCannotWrite(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	directory := answering.store.volumeDir("csi-1")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("making the volume directory: %v", err)
	}
	held := &volume{id: "csi-1", directory: directory}
	readOnlyDir(t, directory)

	answering.record(t.Context(), held, map[string]string{})
	answering.forget(held)
	if !strings.Contains(logs.String(), "the volume's record was not written") {
		t.Errorf("the log is %q, want the record it could not write in it", logs)
	}
	if !strings.Contains(logs.String(), "the volume's record was not removed") {
		t.Errorf("the log is %q, want the record it could not remove in it", logs)
	}
}

func TestAStaleDirectoryThatStaysIsReported(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	volumes := filepath.Join(answering.store.root, "volumes")
	directory := filepath.Join(volumes, "csi-1")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("making the volume directory: %v", err)
	}
	writeFiles(t, directory, map[string]string{"tree/a.txt": "one"})

	// A writeable volume keeps its tree, whatever the mount says.
	answering.drop(t.Context(), &record{VolumeID: "csi-1"}, directory)
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("the writeable volume's directory went: %v", err)
	}

	readOnlyDir(t, volumes)
	answering.drop(t.Context(), &record{VolumeID: "csi-1", Ephemeral: true}, directory)
	if !strings.Contains(logs.String(), "the volume's directory stayed") {
		t.Errorf("the log is %q, want the directory it could not remove in it", logs)
	}
}

func TestTheMountTableSaysWhatIsStillMounted(t *testing.T) {
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
				t.Errorf("mountedIn(%q) answered %v, want %v", c.path, got, c.want)
			}
		})
	}
}

func TestTheKernelsOwnMountTableAnswers(t *testing.T) {
	if !mountedNow("/") {
		t.Error("the root is not a mount, and the kernel says it is")
	}
	if mountedNow(t.TempDir()) {
		t.Error("a temporary directory is a mount")
	}
}
