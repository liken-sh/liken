package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// unwatched stops the volume's own loops, so a test drives one pass of
// the driver's work with nothing running beside it.
func unwatched(t *testing.T, answering *node, held *volume) {
	t.Helper()
	answering.mu.Lock()
	defer answering.mu.Unlock()
	answering.unwatch(held)
}

// commitOnce arms a volume with the parameters, writes the files into its
// tree, and runs one pass of the driver's own work.
func commitOnce(
	t *testing.T, parameters, files map[string]string,
) (*node, *volume) {
	t.Helper()
	answering, held := writtenVolume(t, io.Discard, parameters, files)
	answering.commit(t.Context(), held, held.policyNow())
	return answering, held
}

// writtenVolume is an armed volume whose watch is stopped and whose tree
// holds the files.
func writtenVolume(
	t *testing.T, logs io.Writer, parameters, files map[string]string,
) (*node, *volume) {
	t.Helper()
	answering, _ := testNode(t, logs)
	held := armedVolume(t, answering, "config", fileURL(bareRemote(t,
		map[string]string{"a.txt": "one"})), parameters)
	unwatched(t, answering, held)
	writeFiles(t, held.tree, files)
	return answering, held
}

func TestAnArmedVolumeCommitsWhatThePodWrote(t *testing.T) {
	_, held := commitOnce(t,
		map[string]string{authorParameter: "Home Assistant <homeassistant@home.example>"},
		map[string]string{"one.yaml": "1", "docs/two.yaml": "2"})

	if got := gitIn(t, held.work, "log", "--format=%an <%ae>", "-1"); strings.TrimSpace(got) !=
		"Home Assistant <homeassistant@home.example>" {
		t.Errorf("the commit is by %q, want the class's author", strings.TrimSpace(got))
	}
	message := gitIn(t, held.work, "log", "--format=%B", "-1")
	want := "Update 2 paths\n\ndocs/two.yaml\none.yaml\n"
	if message != want+"\n" {
		t.Errorf("the commit says %q, want %q", message, want)
	}
	if got := strings.TrimSpace(gitIn(t, held.work, "status", "--porcelain")); got != "" {
		t.Errorf("the tree still holds %q, want nothing pending", got)
	}
}

func TestTheSizeGuardLeavesABigFileOut(t *testing.T) {
	answering, held := commitOnce(t,
		map[string]string{maxFileSizeParameter: "16"},
		map[string]string{"small.yaml": "1", "big.yaml": strings.Repeat("x", 64)})

	if got := gitIn(t, held.work, "log", "--format=%B", "-1"); !strings.Contains(got, "small.yaml") {
		t.Errorf("the commit says %q, want small.yaml in it", got)
	}
	if got := gitIn(t, held.work, "log", "--format=%B", "-1"); strings.Contains(got, "big.yaml") {
		t.Errorf("the commit says %q, want big.yaml left out", got)
	}

	abnormal, message := held.report()
	if !abnormal || message != "1 files over commit.maxFileSize: big.yaml" {
		t.Errorf("the condition is %v, %q, want the skipped file named", abnormal, message)
	}
	skipped := eventsWithReason(t, answering, reasonSkipped)
	if len(skipped) != 2 {
		t.Fatalf("the skip posted %v, want one Event on the pod and one on the claim", skipped)
	}
	if skipped[0].Message != "big.yaml is 64 bytes, over commit.maxFileSize" {
		t.Errorf("the event says %q", skipped[0].Message)
	}
	answering.readings.record(held)
	got, found := gaugeOf(t, answering.readings, "git_csi_skipped_files", "home", "config")
	if !found || got != 1 {
		t.Errorf("the gauge reports %v skipped files (found %v), want 1", got, found)
	}
}

// eventsWithReason is every Event the node posted for one reason.
func eventsWithReason(t *testing.T, answering *node, reason string) []corev1.Event {
	t.Helper()
	found := []corev1.Event{}
	for _, posted := range eventsOf(t, answering) {
		if posted.Reason == reason {
			found = append(found, posted)
		}
	}
	return found
}

func TestASkippedFilePostsItsEventOnce(t *testing.T) {
	answering, held := commitOnce(t,
		map[string]string{maxFileSizeParameter: "16"},
		map[string]string{"big.yaml": strings.Repeat("x", 64)})
	answering.commit(t.Context(), held, held.policyNow())
	if got := len(eventsWithReason(t, answering, reasonSkipped)); got != 2 {
		t.Errorf("the skip posted %d events, want the two of the first pass", got)
	}
}

func TestNoLimitCommitsEveryFile(t *testing.T) {
	_, held := commitOnce(t,
		map[string]string{maxFileSizeParameter: "0"},
		map[string]string{"big.yaml": strings.Repeat("x", 4096)})
	if got := gitIn(t, held.work, "log", "--format=%B", "-1"); !strings.Contains(got, "big.yaml") {
		t.Errorf("the commit says %q, want big.yaml in it", got)
	}
}

func TestTheIgnoreListKeepsAPathOut(t *testing.T) {
	_, held := commitOnce(t,
		map[string]string{ignoreParameter: ".storage/,*.log"},
		map[string]string{
			"one.yaml": "1", "app.log": "2", ".storage/db": "3",
		})

	exclude, err := os.ReadFile(filepath.Join(held.work.gitDir, excludeFile))
	if err != nil {
		t.Fatalf("reading the exclude file: %v", err)
	}
	if string(exclude) != ".storage/\n*.log\n" {
		t.Errorf("the exclude file holds %q, want the class's patterns", exclude)
	}
	message := gitIn(t, held.work, "log", "--format=%B", "-1")
	if !strings.Contains(message, "one.yaml") {
		t.Errorf("the commit says %q, want one.yaml in it", message)
	}
	for _, ignored := range []string{"app.log", ".storage/db"} {
		if strings.Contains(message, ignored) {
			t.Errorf("the commit says %q, want %s left out", message, ignored)
		}
	}
}

func TestTheIgnoreListIsWrittenOnlyWhenItChanges(t *testing.T) {
	work := createdWorkTree(t, map[string]string{"a.txt": "one"})
	path := filepath.Join(work.gitDir, excludeFile)
	if err := work.exclude([]string{"*.log"}); err != nil {
		t.Fatalf("exclude: %v", err)
	}
	written, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if err := work.exclude([]string{"*.log"}); err != nil {
		t.Fatalf("exclude: %v", err)
	}
	again, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !again.ModTime().Equal(written.ModTime()) {
		t.Error("the exclude file was written again with the same patterns")
	}
	if err := work.exclude(nil); err != nil {
		t.Fatalf("exclude: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the exclude file: %v", err)
	}
	if len(content) != 0 {
		t.Errorf("the exclude file holds %q, want nothing", content)
	}
}

func TestAClassThatRecordsNoMetadataMakesNoRef(t *testing.T) {
	_, held := commitOnce(t,
		map[string]string{metadataParameter: "false"},
		map[string]string{"one.yaml": "1"})
	if got := held.work.refCommit(t.Context(), metadataRef); got != "" {
		t.Errorf("the metadata ref names %s, want nothing", got)
	}
}

func TestAClassThatRecordsMetadataMakesTheRef(t *testing.T) {
	answering, held := writtenVolume(t, io.Discard, nil, map[string]string{"secret.yaml": "1"})
	if err := os.Chmod(filepath.Join(held.tree, "secret.yaml"), 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	answering.commit(t.Context(), held, held.policyNow())

	content, err := held.work.metadataRecord(t.Context(), metadataRef)
	if err != nil {
		t.Fatalf("metadataRecord: %v", err)
	}
	if !strings.Contains(content, " secret.yaml\n") {
		t.Errorf("the record is %q, want secret.yaml in it", content)
	}
}

func TestOneAddCarriesEveryPathABatchHolds(t *testing.T) {
	files := map[string]string{}
	for i := range stageBatch + 5 {
		files["file-"+strconv.Itoa(i)+".yaml"] = "one"
	}
	_, held := commitOnce(t, nil, files)
	message := gitIn(t, held.work, "log", "--format=%s", "-1")
	if want := fmt.Sprintf("Update %d paths", len(files)); strings.TrimSpace(message) != want {
		t.Errorf("the commit says %q, want %q", strings.TrimSpace(message), want)
	}
}

func TestAPathOutsideTheTreeIsNotStaged(t *testing.T) {
	work := createdWorkTree(t, map[string]string{"a.txt": "one"})
	_, _, err := work.stageChanges(t.Context(),
		[]change{{path: "../escape.yaml"}}, defaultPolicy())
	if err == nil {
		t.Error("stageChanges answered no error for a path outside the tree")
	}
}

func TestTheCommitReportsWhatGitWouldNotDo(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, held *volume)
	}{
		{
			name: "an exclude file the driver cannot write",
			stand: func(t *testing.T, held *volume) {
				replaceWithDirectory(t, filepath.Join(held.work.gitDir, excludeFile))
			},
		},
		{
			name: "an index the driver cannot read",
			stand: func(t *testing.T, held *volume) {
				replaceWithDirectory(t, filepath.Join(held.work.gitDir, "index"))
			},
		},
		{
			name: "a commit message file the driver cannot write",
			stand: func(t *testing.T, held *volume) {
				replaceWithDirectory(t, filepath.Join(held.work.gitDir, "COMMIT_EDITMSG"))
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := &logbook{}
			answering, held := writtenVolume(t, logs,
				map[string]string{metadataParameter: "false"},
				map[string]string{"one.yaml": "1"})
			c.stand(t, held)

			answering.commit(t.Context(), held, held.policyNow())
			if !strings.Contains(logs.String(), "the tree was not committed") {
				t.Errorf("the log is %q, want the failure in it", logs)
			}
		})
	}
}

// replaceWithDirectory puts a directory where a file git writes stands,
// so the next write of it fails.
func replaceWithDirectory(t *testing.T, path string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("removing %s: %v", path, err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("making %s a directory: %v", path, err)
	}
}

func TestTheCommitReportsAMetadataRecordItCannotMake(t *testing.T) {
	logs := &logbook{}
	answering, held := writtenVolume(t, logs, nil, map[string]string{"one.yaml": "1"})
	replaceWithDirectory(t, filepath.Join(held.work.directory, metadataRecordFile))

	answering.commit(t.Context(), held, held.policyNow())
	if !strings.Contains(logs.String(), "the metadata was not recorded") {
		t.Errorf("the log is %q, want the failure in it", logs)
	}
	if got := strings.TrimSpace(gitIn(t, held.work, "log", "--format=%s", "-1")); got != "Update 1 paths" {
		t.Errorf("the last commit is %q, want the tree committed anyway", got)
	}
}

func TestATreeWithNothingPendingIsNotCommitted(t *testing.T) {
	answering, held := commitOnce(t, nil, nil)
	before := strings.TrimSpace(gitIn(t, held.work, "rev-parse", "HEAD"))
	answering.commit(t.Context(), held, held.policyNow())
	if after := strings.TrimSpace(gitIn(t, held.work, "rev-parse", "HEAD")); after != before {
		t.Errorf("a tree with nothing pending moved from %s to %s", before, after)
	}
}

func TestGitsZeroSeparatedReportHoldsNoEmptyPath(t *testing.T) {
	if got := splitZero("one.yaml\x00two.yaml\x00"); strings.Join(got, "|") != "one.yaml|two.yaml" {
		t.Errorf("splitZero answered %v", got)
	}
	if got := splitZero(""); len(got) != 0 {
		t.Errorf("splitZero answered %v, want nothing", got)
	}
}

func TestTheIgnoreListReportsWhereItCannotBeWritten(t *testing.T) {
	work := createdWorkTree(t, map[string]string{"a.txt": "one"})
	if err := os.RemoveAll(filepath.Join(work.gitDir, "info")); err != nil {
		t.Fatalf("removing the info directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work.gitDir, "info"), nil, 0o600); err != nil {
		t.Fatalf("writing info as a file: %v", err)
	}
	if err := work.exclude([]string{"*.log"}); err == nil {
		t.Error("exclude answered no error")
	}
}

func TestTheIndexIsNotReadFromAGitDirectoryThatIsGone(t *testing.T) {
	work := createdWorkTree(t, map[string]string{"a.txt": "one"})
	if err := os.RemoveAll(work.gitDir); err != nil {
		t.Fatalf("removing the git directory: %v", err)
	}
	if _, err := work.staged(t.Context()); err == nil {
		t.Error("staged answered no error")
	}
}
