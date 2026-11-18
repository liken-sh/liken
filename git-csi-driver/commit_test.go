package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"

	kevents "github.com/liken-sh/liken/kubernetes/events"
)

// unwatched stops the volume's background loops, so a test can run
// one pass of the driver's work with nothing else running.
func unwatched(t testing.TB, n *node, vol *volume) {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	n.unwatch(vol)
}

// commitOnce arms a volume with the parameters, writes the files into
// its work tree, and runs one commit.
func commitOnce(
	t *testing.T, parameters, files map[string]string,
) (*node, *volume) {
	t.Helper()
	n, vol := writtenVolume(t, io.Discard, parameters, files)
	n.commit(t.Context(), vol, vol.policyNow())
	return n, vol
}

// writtenVolume is an armed volume with its loops stopped and the files
// written into its work tree.
func writtenVolume(
	t *testing.T, logs io.Writer, parameters, files map[string]string,
) (*node, *volume) {
	t.Helper()
	n, _ := testNode(t, logs)
	vol := armedVolume(t, n, "config", fileURL(bareRemote(t,
		map[string]string{"a.txt": "one"})), parameters)
	unwatched(t, n, vol)
	writeFiles(t, vol.tree, files)
	return n, vol
}

func TestAnArmedVolumeCommitsWhatThePodWrote(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, vol := commitOnce(t,
			map[string]string{authorParameter: "Home Assistant <homeassistant@home.example>"},
			map[string]string{"one.yaml": "1", "docs/two.yaml": "2"})

		if got := gitIn(t, vol.work, "log", "--format=%an <%ae>", "-1"); strings.TrimSpace(got) !=
			"Home Assistant <homeassistant@home.example>" {
			t.Errorf("the commit is by %q, want the class's author", strings.TrimSpace(got))
		}
		message := gitIn(t, vol.work, "log", "--format=%B", "-1")
		want := "Update 2 paths\n\ndocs/two.yaml\none.yaml\n"
		if message != want+"\n" {
			t.Errorf("the commit message is %q, want %q", message, want)
		}
		if got := strings.TrimSpace(gitIn(t, vol.work, "status", "--porcelain")); got != "" {
			t.Errorf("the tree still has %q, want nothing pending", got)
		}
	})
}

func TestMaxFileSizeLeavesABigFileOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol := commitOnce(t,
			map[string]string{maxFileSizeParameter: "16"},
			map[string]string{"small.yaml": "1", "big.yaml": strings.Repeat("x", 64)})

		if got := gitIn(t, vol.work, "log", "--format=%B", "-1"); !strings.Contains(got, "small.yaml") {
			t.Errorf("the commit message is %q, want small.yaml in it", got)
		}
		if got := gitIn(t, vol.work, "log", "--format=%B", "-1"); strings.Contains(got, "big.yaml") {
			t.Errorf("the commit message is %q, want big.yaml left out", got)
		}

		abnormal, message := vol.health()
		if !abnormal || message != "1 files over commit.maxFileSize: big.yaml" {
			t.Errorf("the health report is %v, %q, want the skipped file named", abnormal, message)
		}
		skipped := eventsWithReason(t, n, reasonSkipped)
		if len(skipped) != 2 {
			t.Fatalf("the skip posted %v, want one Event on the pod and one on the claim", skipped)
		}
		if want := "Skipped big.yaml because it is 64 bytes, over the commit.maxFileSize of 16 bytes"; skipped[0].Message != want {
			t.Errorf("the Event message is %q, want %q", skipped[0].Message, want)
		}
		n.metrics.record(vol)
		got, found := gaugeOf(t, n.metrics, "git_csi_skipped_files", "home", "config")
		if !found || got != 1 {
			t.Errorf("the gauge reports %v skipped files (found %v), want 1", got, found)
		}
	})
}

// eventsWithReason returns every Event the node posted with the reason.
func eventsWithReason(t *testing.T, n *node, reason string) []kevents.Event {
	t.Helper()
	found := []kevents.Event{}
	for _, posted := range eventsOf(t, n) {
		if posted.Reason == reason {
			found = append(found, posted)
		}
	}
	return found
}

func TestASkippedFilePostsItsEventOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol := commitOnce(t,
			map[string]string{maxFileSizeParameter: "16"},
			map[string]string{"big.yaml": strings.Repeat("x", 64)})
		n.commit(t.Context(), vol, vol.policyNow())
		if got := len(eventsWithReason(t, n, reasonSkipped)); got != 2 {
			t.Errorf("the skip posted %d events, want the two of the first pass", got)
		}
	})
}

func TestNoLimitCommitsEveryFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, vol := commitOnce(t,
			map[string]string{maxFileSizeParameter: "0"},
			map[string]string{"big.yaml": strings.Repeat("x", 4096)})
		if got := gitIn(t, vol.work, "log", "--format=%B", "-1"); !strings.Contains(got, "big.yaml") {
			t.Errorf("the commit message is %q, want big.yaml in it", got)
		}
	})
}

func TestTheIgnoreListKeepsAPathOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, vol := commitOnce(t,
			map[string]string{ignoreParameter: ".storage/,*.log"},
			map[string]string{
				"one.yaml": "1", "app.log": "2", ".storage/db": "3",
			})

		exclude, err := os.ReadFile(filepath.Join(vol.work.gitDir, excludeFile))
		if err != nil {
			t.Fatalf("reading the exclude file: %v", err)
		}
		if string(exclude) != ".storage/\n*.log\n" {
			t.Errorf("the exclude file has %q, want the class's patterns", exclude)
		}
		message := gitIn(t, vol.work, "log", "--format=%B", "-1")
		if !strings.Contains(message, "one.yaml") {
			t.Errorf("the commit message is %q, want one.yaml in it", message)
		}
		for _, ignored := range []string{"app.log", ".storage/db"} {
			if strings.Contains(message, ignored) {
				t.Errorf("the commit message is %q, want %s left out", message, ignored)
			}
		}
	})
}

func TestTheIgnoreListIsWrittenOnlyWhenItChanges(t *testing.T) {
	work := createdWorkTree(t, map[string]string{"a.txt": "one"})
	path := filepath.Join(work.gitDir, excludeFile)
	if err := work.exclude([]string{"*.log"}); err != nil {
		t.Fatalf("writing the exclude file: %v", err)
	}
	written, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the status of the exclude file: %v", err)
	}
	if err := work.exclude([]string{"*.log"}); err != nil {
		t.Fatalf("writing the exclude file: %v", err)
	}
	again, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the status of the exclude file: %v", err)
	}
	if !again.ModTime().Equal(written.ModTime()) {
		t.Error("the exclude file was written again with the same patterns")
	}
	if err := work.exclude(nil); err != nil {
		t.Fatalf("writing the exclude file: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the exclude file: %v", err)
	}
	if len(content) != 0 {
		t.Errorf("the exclude file has %q, want nothing", content)
	}
}

func TestAClassThatRecordsNoMetadataMakesNoRef(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, vol := commitOnce(t,
			map[string]string{metadataParameter: "false"},
			map[string]string{"one.yaml": "1"})
		if got := vol.work.refCommit(t.Context(), metadataRef); got != "" {
			t.Errorf("the metadata ref names %s, want nothing", got)
		}
	})
}

func TestAClassThatRecordsMetadataMakesTheRef(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol := writtenVolume(t, io.Discard, nil, map[string]string{"secret.yaml": "1"})
		if err := os.Chmod(filepath.Join(vol.tree, "secret.yaml"), 0o600); err != nil {
			t.Fatalf("changing the mode of secret.yaml: %v", err)
		}
		n.commit(t.Context(), vol, vol.policyNow())

		content, err := vol.work.metadataRecord(t.Context(), metadataRef)
		if err != nil {
			t.Fatalf("reading the metadata record: %v", err)
		}
		if !strings.Contains(content, " secret.yaml\n") {
			t.Errorf("the record is %q, want secret.yaml in it", content)
		}
	})
}

func TestMoreThanOneBatchOfPathsIsCommitted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		files := map[string]string{}
		for i := range stageBatch + 5 {
			files["file-"+strconv.Itoa(i)+".yaml"] = "one"
		}
		_, vol := commitOnce(t, nil, files)
		message := gitIn(t, vol.work, "log", "--format=%s", "-1")
		if want := fmt.Sprintf("Update %d paths", len(files)); strings.TrimSpace(message) != want {
			t.Errorf("the commit message is %q, want %q", strings.TrimSpace(message), want)
		}
	})
}

func TestAPathOutsideTheTreeIsNotStaged(t *testing.T) {
	work := createdWorkTree(t, map[string]string{"a.txt": "one"})
	_, _, err := work.stageChanges(t.Context(),
		[]change{{path: "../escape.yaml"}}, defaultPolicy())
	if err == nil {
		t.Error("stageChanges returned no error for a path outside the tree, want one")
	}
}

func TestTheCommitLogsAGitFailure(t *testing.T) {
	for _, c := range []struct {
		name     string
		breakGit func(t *testing.T, vol *volume)
	}{
		{
			name: "an exclude file the driver cannot write",
			breakGit: func(t *testing.T, vol *volume) {
				replaceWithDirectory(t, filepath.Join(vol.work.gitDir, excludeFile))
			},
		},
		{
			name: "an index the driver cannot read",
			breakGit: func(t *testing.T, vol *volume) {
				replaceWithDirectory(t, filepath.Join(vol.work.gitDir, "index"))
			},
		},
		{
			name: "a commit message file the driver cannot write",
			breakGit: func(t *testing.T, vol *volume) {
				replaceWithDirectory(t, filepath.Join(vol.work.gitDir, "COMMIT_EDITMSG"))
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				logs := &logbook{}
				n, vol := writtenVolume(t, logs,
					map[string]string{metadataParameter: "false"},
					map[string]string{"one.yaml": "1"})
				c.breakGit(t, vol)

				n.commit(t.Context(), vol, vol.policyNow())
				if !strings.Contains(logs.String(), "The work tree was not committed") {
					t.Errorf("the log is %q, want the line that reports the work tree was not committed", logs)
				}
			})
		})
	}
}

// replaceWithDirectory replaces a file that git writes with a
// directory, so the next write of that file fails.
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
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		n, vol := writtenVolume(t, logs, nil, map[string]string{"one.yaml": "1"})
		replaceWithDirectory(t, filepath.Join(vol.work.directory, metadataRecordFile))

		n.commit(t.Context(), vol, vol.policyNow())
		if !strings.Contains(logs.String(), "The file metadata was not recorded") {
			t.Errorf("the log is %q, want the line that reports the file metadata was not recorded", logs)
		}
		if got := strings.TrimSpace(gitIn(t, vol.work, "log", "--format=%s", "-1")); got != "Update 1 paths" {
			t.Errorf("the last commit is %q, want the tree committed anyway", got)
		}
	})
}

func TestATreeWithNothingPendingIsNotCommitted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol := commitOnce(t, nil, nil)
		before := strings.TrimSpace(gitIn(t, vol.work, "rev-parse", "HEAD"))
		n.commit(t.Context(), vol, vol.policyNow())
		if after := strings.TrimSpace(gitIn(t, vol.work, "rev-parse", "HEAD")); after != before {
			t.Errorf("a tree with nothing pending moved from %s to %s", before, after)
		}
	})
}

func TestSplitZeroDropsEmptyPaths(t *testing.T) {
	if got := splitZero("one.yaml\x00two.yaml\x00"); strings.Join(got, "|") != "one.yaml|two.yaml" {
		t.Errorf("splitZero returns %v, want one.yaml and two.yaml", got)
	}
	if got := splitZero(""); len(got) != 0 {
		t.Errorf("splitZero of an empty string returns %v, want nothing", got)
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
		t.Error("exclude returned no error, want one because info is a file")
	}
}

func TestTheIndexIsNotReadFromAGitDirectoryThatIsGone(t *testing.T) {
	work := createdWorkTree(t, map[string]string{"a.txt": "one"})
	if err := os.RemoveAll(work.gitDir); err != nil {
		t.Fatalf("removing the git directory: %v", err)
	}
	if _, err := work.staged(t.Context()); err == nil {
		t.Error("staged returned no error, want one because the git directory is gone")
	}
}
