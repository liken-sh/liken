package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
)

func TestTheWalkOfAVolumeWithPathsRecordsOnlyItsEntries(t *testing.T) {
	tree := t.TempDir()
	writeFiles(t, tree, map[string]string{
		"app/secret.yaml":  "one",
		"app/sub/run.sh":   "two",
		"other/secret.txt": "three",
		"top.sh":           "four",
	})
	for _, name := range []string{"app/secret.yaml", "other/secret.txt", "top.sh"} {
		if err := os.Chmod(filepath.Join(tree, name), 0o600); err != nil {
			t.Fatalf("chmod %s: %v", name, err)
		}
	}
	for _, c := range []struct {
		name    string
		entries []string
		want    string
	}{
		{
			name:    "a directory",
			entries: []string{"app"},
			want:    recordLine(0o600, "app/secret.yaml") + "\n",
		},
		{
			name:    "a file",
			entries: []string{"top.sh"},
			want:    recordLine(0o600, "top.sh") + "\n",
		},
		{
			name:    "a nested pair",
			entries: []string{"app", "app/sub"},
			want:    recordLine(0o600, "app/secret.yaml") + "\n",
		},
		{
			name:    "an entry that is not in the tree",
			entries: []string{"gone"},
			want:    "",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			records, err := walkTreeMetadata(tree, c.entries)
			if err != nil {
				t.Fatalf("walkTreeMetadata: %v", err)
			}
			if got := metadataContent(records); got != c.want {
				t.Errorf("the record is %q, want %q", got, c.want)
			}
		})
	}
}

func TestTheWalkReportsAnEntryItCannotRead(t *testing.T) {
	tree := t.TempDir()
	writeFiles(t, tree, map[string]string{"app/locked/a.txt": "one"})
	unreadableDir(t, filepath.Join(tree, "app", "locked"))
	if _, err := walkTreeMetadata(tree, []string{"app"}); err == nil {
		t.Error("walkTreeMetadata returned no error for an entry it cannot read")
	}
}

func TestARecordOfAVolumeWithPathsKeepsTheLinesOutsideThem(t *testing.T) {
	work := sparseTree(t, map[string]string{"app/a.yaml": "a", "other/b.yaml": "b"}, []string{"app"})
	recordOnRef(t, work, recordLine(0o700, "other/")+"\n"+recordLine(0o600, "app/old.yaml")+"\n")
	if err := os.Chmod(filepath.Join(work.tree, "app/a.yaml"), 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// The checkout makes the directory with the process's umask, so the
	// test sets the default mode, which the record leaves out.
	if err := os.Chmod(filepath.Join(work.tree, "app"), defaultDirMode); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if err := work.recordMetadata(t.Context(), defaultPolicy()); err != nil {
		t.Fatalf("recording the metadata: %v", err)
	}

	got, err := work.metadataRecord(t.Context(), metadataRef)
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	want := recordLine(0o700, "other/") + "\n" + recordLine(0o600, "app/a.yaml") + "\n"
	if got != want {
		t.Errorf("the record is %q, want the other volume's line and this volume's own", got)
	}
}

func TestAReplayOfAVolumeWithPathsChangesNothingOutsideThem(t *testing.T) {
	work := sparseTree(t, map[string]string{"app/a.yaml": "a", "other/b.yaml": "b"}, []string{"app"})
	recordOnRef(t, work, strings.Join([]string{
		recordLine(0o600, "app/a.yaml"),
		recordLine(0o700, "app/cache/"),
		recordLine(0o700, "other/"),
		recordLine(0o700, "other/cache/"),
	}, "\n")+"\n")

	if err := work.replayMetadata(t.Context(), slog.New(slog.DiscardHandler), false); err != nil {
		t.Fatalf("replaying the record: %v", err)
	}

	if got := modeOf(t, work.tree, "app/a.yaml"); got != 0o600 {
		t.Errorf("app/a.yaml is %v, want the recorded mode", got)
	}
	if got := modeOf(t, work.tree, "app/cache"); got != 0o700 {
		t.Errorf("app/cache is %v, want the recorded empty directory", got)
	}
	if work.present("other") {
		t.Error("the replay created other, want nothing outside the paths")
	}
}

// diverging gives the work tree a record on remoteMetadataRef and a
// record of its own on metadataRef that does not descend from it, as
// after another volume of the URL pushed its record.
func diverging(t *testing.T, work *workTree, theirs, ours string) string {
	t.Helper()
	recordOnRef(t, work, theirs)
	remote := work.refCommit(t.Context(), metadataRef)
	gitIn(t, work, "update-ref", remoteMetadataRef, remote)
	gitIn(t, work, "update-ref", "-d", metadataRef)
	recordOnRef(t, work, ours)
	return remote
}

func TestARebasedRecordOfAVolumeWithPathsMergesTheRemoteRecord(t *testing.T) {
	work := sparseTree(t, map[string]string{"app/a.yaml": "a"}, []string{"app"})
	remote := diverging(t, work,
		recordLine(0o600, "app/theirs.yaml")+"\n"+recordLine(0o600, "other/b.yaml")+"\n",
		recordLine(0o600, "app/a.yaml")+"\n"+recordLine(0o600, "stale/c.yaml")+"\n")

	if err := work.reparentMetadata(t.Context(), defaultPolicy().author()); err != nil {
		t.Fatalf("rebasing the record: %v", err)
	}

	got, err := work.metadataRecord(t.Context(), metadataRef)
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	want := recordLine(0o600, "other/b.yaml") + "\n" + recordLine(0o600, "app/a.yaml") + "\n"
	if got != want {
		t.Errorf("the record is %q, want the remote's lines outside app and this volume's inside", got)
	}
	if parent := strings.TrimSpace(gitIn(t, work, "rev-parse", metadataRef+"^")); parent != remote {
		t.Errorf("the record's parent is %s, want the remote's record %s", parent, remote)
	}
}

func TestARebasedRecordReportsARecordItCannotRead(t *testing.T) {
	for _, ref := range []string{remoteMetadataRef, metadataRef} {
		t.Run(ref, func(t *testing.T) {
			work := sparseTree(t, map[string]string{"app/a.yaml": "a"}, []string{"app"})
			diverging(t, work, recordLine(0o600, "other/b.yaml")+"\n", recordLine(0o600, "app/a.yaml")+"\n")
			// A commit of the branch has no metadata file, so git show
			// cannot read a record from it.
			gitIn(t, work, "update-ref", ref, "HEAD")

			if err := work.reparentMetadata(t.Context(), defaultPolicy().author()); err == nil {
				t.Errorf("reparentMetadata returned no error with no record on %s", ref)
			}
		})
	}
}

// Two writers on two nodes, each with its own application of one
// repository, push in turn. Each push after the first is rejected and
// rebased, and the record that reaches the remote keeps the modes of
// both applications.
func TestTwoVolumesWithOtherPathsKeepEachOthersMetadata(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		remote := appRemote(t)
		first, _ := testNode(t, io.Discard)
		second, _ := testNode(t, io.Discard)
		a, _ := armedPaths(t, first, fileURL(remote), "app-a", nil)
		b, _ := armedPaths(t, second, fileURL(remote), "app-b", nil)

		writeAndPush(t, first, a, "app-a/config.yaml", "app-a/new.yaml")
		writeAndPush(t, second, b, "app-b/config.yaml", "app-b/new.yaml")
		writeAndPush(t, first, a, "app-a/config.yaml", "app-a/more.yaml")

		record := git(t, remote, "show", "--no-textconv", metadataRef+":"+metadataFile)
		for _, line := range []string{recordLine(0o600, "app-a/config.yaml"), recordLine(0o600, "app-b/config.yaml")} {
			if !strings.Contains(record, line+"\n") {
				t.Errorf("the remote's record is %q, want %q in it", record, line)
			}
		}
		if a.work.present("app-b") || b.work.present("app-a") {
			t.Error("a tree has the other application's directory, want each tree to keep to its paths")
		}
		if got := len(eventsWithReason(t, second, reasonDiverged)); got != 0 {
			t.Errorf("the second writer posted %d divergences, want none", got)
		}
	})
}

// writeAndPush makes one file private, writes another, and commits and
// pushes the volume.
func writeAndPush(t *testing.T, n *node, vol *volume, private, written string) {
	t.Helper()
	if err := os.Chmod(filepath.Join(vol.tree, filepath.FromSlash(private)), 0o600); err != nil {
		t.Fatalf("chmod %s: %v", private, err)
	}
	writeFiles(t, vol.tree, map[string]string{written: "written"})
	n.commit(t.Context(), vol, vol.policyNow())
	n.push(t.Context(), vol)
	if count, _, _ := vol.work.unpushed(t.Context(), "main"); count != 0 {
		t.Fatalf("the volume has %d unpushed commits after the push of %s, want none", count, written)
	}
}
