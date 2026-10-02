package main

import (
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"testing"
	"time"
)

// How the pods of one worker Job split one work list: each title folder goes
// to exactly one pod, and every pod computes the same split.

// Title folders enough that each share of a small count holds several.
func shareFolders() []string {
	var folders []string
	for n := range 60 {
		folders = append(folders, fmt.Sprintf("Survey %02d (19%02d)", n, 40+n))
	}
	return folders
}

// Every folder goes to exactly one index of the count, so the shares together
// cover the list once.
func TestTheSharesCoverEveryFolderOnce(t *testing.T) {
	cases := []int{1, 2, 3, 4, 7}
	for _, count := range cases {
		t.Run(fmt.Sprintf("%d pods", count), func(t *testing.T) {
			taken := map[string]int{}
			for index := range count {
				for _, folder := range shareFolders() {
					if (workerShare{index: index, count: count}).takes(folder) {
						taken[folder]++
					}
				}
			}

			for _, folder := range shareFolders() {
				if taken[folder] != 1 {
					t.Errorf("%s went to %d pods, want 1", folder, taken[folder])
				}
			}
		})
	}
}

// The split is FNV-1a over the folder, so a pod on another node, in another
// process, or on another release computes the same share. The pinned indexes
// fail this test if the hash changes, because pods of one Job that ran two
// hashes would leave some folders in no share.
func TestTheSplitIsTheSameInEveryPod(t *testing.T) {
	cases := []struct {
		folder string
		index  int
	}{
		{folder: "A Quiet Field (1950)", index: 1},
		{folder: "The Long Survey (1982)", index: 0},
		{folder: "Harbour Lights", index: 1},
		{folder: "Quiet Field", index: 2},
	}
	for _, one := range cases {
		t.Run(one.folder, func(t *testing.T) {
			share := workerShare{index: one.index, count: 3}

			if !share.takes(one.folder) {
				t.Errorf("index %d of 3 does not take %q", one.index, one.folder)
			}
		})
	}
}

// The pods of one Job share its name, so each pod of several marks its
// temporaries with its index, and two pods that write the root's ledger at
// once never rename each other's temporary. A pod of a Job of one keeps the
// name it had.
func TestEachPodWritesItsOwnTemporaries(t *testing.T) {
	cases := []struct {
		share workerShare
		want  string
	}{
		{share: workerShare{index: 0, count: 1}, want: "movies-appearances-1-appearances"},
		{share: workerShare{index: 2, count: 4}, want: "movies-appearances-1-appearances-2"},
	}
	for _, one := range cases {
		t.Run(one.want, func(t *testing.T) {
			if got := one.share.writerName("movies-appearances-1-appearances"); got != one.want {
				t.Errorf("writer = %q, want %q", got, one.want)
			}
		})
	}
}

// One pod is the worker of a Job of one pod: it takes the whole list.
func TestOnePodTakesTheWholeList(t *testing.T) {
	share := workerShare{index: 0, count: 1}

	for _, folder := range shareFolders() {
		if !share.takes(folder) {
			t.Fatalf("the one pod does not take %q", folder)
		}
	}
}

// The Job controller gives each pod of an Indexed Job its index, and the
// operator gives every pod the count. A pod of a Job of one pod has neither.
func TestAPodReadsItsShareOutOfTheJob(t *testing.T) {
	cases := []struct {
		name  string
		index string
		count string
		want  workerShare
		ok    bool
	}{
		{name: "a Job of one pod", want: workerShare{index: 0, count: 1}, ok: true},
		{name: "the third pod of four", index: "2", count: "4", want: workerShare{index: 2, count: 4}, ok: true},
		{name: "an index past the count", index: "4", count: "4"},
		{name: "a negative index", index: "-1", count: "4"},
		{name: "an index that is not a number", index: "two", count: "4"},
		{name: "a count of zero", index: "0", count: "0"},
		{name: "a count that is not a number", index: "0", count: "four"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			share, err := workerShareOf(one.index, one.count)

			if (err == nil) != one.ok {
				t.Fatalf("workerShareOf = %v, want ok: %v", err, one.ok)
			}
			if one.ok && share != one.want {
				t.Errorf("share = %+v, want %+v", share, one.want)
			}
		})
	}
}

// A series of two seasons and three films, as one list.
func sharedSeriesList(t *testing.T, root string, listed time.Time) []workItem {
	t.Helper()
	var items []workItem
	for _, video := range []string{
		"Harbour Lights/Season 01/Harbour Lights - S01E01.mkv",
		"Harbour Lights/Season 01/Harbour Lights - S01E02.mkv",
		"Harbour Lights/Season 02/Harbour Lights - S02E01.mkv",
		"Quiet Field/Season 01/Quiet Field - S01E01.mkv",
		"The Long Survey/Season 01/The Long Survey - S01E01.mkv",
		"The Long Survey/Season 03/The Long Survey - S03E04.mkv",
		"Night Ferry/Season 01/Night Ferry - S01E01.mkv",
	} {
		items = append(items, listedVideo(t, root, video, video, listed))
	}
	return items
}

// Two pods over one list: each works its own share, the two together work
// every video once, a series goes whole to one pod, and each pod asks for
// rescans only of its own title folders.
func TestThePodsOfAWorkerWorkTheListOnce(t *testing.T) {
	root := t.TempDir()
	items := sharedSeriesList(t, root, time.Now().UTC())
	if err := newVolumeWriter("shows-close").writeWorkList(root, testWorkLibrary, factTrickplay, items); err != nil {
		t.Fatal(err)
	}
	worked := map[int][]string{}
	rescans := map[int][]string{}
	for index := range 2 {
		var paths []string
		work, log := testFactWorker(t, recordingWorker(&paths), libraryKindSeries, root)
		work.share = workerShare{index: index, count: 2}
		webhooks, address := recordWebhooks(t, http.StatusNoContent)
		work.webhook = address

		if err := work.work(t.Context()); err != nil {
			t.Fatal(err)
		}

		worked[index], rescans[index] = paths, webhooks.named()
		want := fmt.Sprintf("index %d of 2 takes %d of the 7 videos", index, len(paths))
		if !strings.Contains(log.String(), want) {
			t.Errorf("log = %q, want %q", log, want)
		}
	}

	all := slices.Sorted(slices.Values(append(slices.Clone(worked[0]), worked[1]...)))
	if want := pathsOf(items); !slices.Equal(all, want) {
		t.Errorf("the pods worked on %v, want each of %v once", all, want)
	}
	if len(worked[0]) == 0 || len(worked[1]) == 0 {
		t.Errorf("worked = %v, want work in both shares", worked)
	}
	for index, paths := range worked {
		for _, video := range paths {
			folder := strings.SplitN(video, "/", 2)[0]
			if !slices.Contains(rescans[index], folder) || slices.Contains(rescans[1-index], folder) {
				t.Errorf("%s: rescans %v, want %q only in the rescans of index %d", video, rescans, folder, index)
			}
		}
	}
}

// The paths of a list, sorted.
func pathsOf(items []workItem) []string {
	var paths []string
	for _, item := range items {
		paths = append(paths, path.Clean(item.Path))
	}
	slices.Sort(paths)
	return paths
}
