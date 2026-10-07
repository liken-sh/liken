package main

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"path"
	"slices"
	"strings"
	"testing"
	"time"
)

// How the pods of one worker Job split one gap: each title folder goes to
// exactly one pod, every pod computes the same split, and the split balances
// hours.

// The title folder of a path in these tests: its first element.
func firstElement(relative string) string {
	return strings.SplitN(relative, "/", 2)[0]
}

// One video of a gap in the folder given, of the length given.
func gapVideo(folder, name string, length time.Duration) workItem {
	return workItem{Path: folder + "/" + name, Size: 1, DurationMs: length.Milliseconds()}
}

// A series of 400 episodes of 45 minutes, and 60 films of 100 to 159 minutes.
func oneLongSeriesAndManyFilms() []workItem {
	var items []workItem
	for episode := range 400 {
		items = append(items, gapVideo("Harbour Lights", fmt.Sprintf("S01E%03d.mkv", episode), 45*time.Minute))
	}
	for film := range 60 {
		folder := fmt.Sprintf("Survey %02d (19%02d)", film, 40+film)
		items = append(items, gapVideo(folder, folder+".mkv", time.Duration(100+film)*time.Minute))
	}
	return items
}

// Many films of different lengths, and three series of different sizes.
func mixedGap() []workItem {
	var items []workItem
	for film := range 40 {
		folder := fmt.Sprintf("Film %02d (19%02d)", film, 40+film)
		items = append(items, gapVideo(folder, folder+".mkv", time.Duration(80+film*3)*time.Minute))
	}
	for series, episodes := range []int{10, 24, 62} {
		for episode := range episodes {
			items = append(items, gapVideo(fmt.Sprintf("Series %d", series), fmt.Sprintf("E%02d.mkv", episode),
				time.Duration(22+series*20)*time.Minute))
		}
	}
	return items
}

// The total length of each share.
func shareLengths(shares [][]workItem) []int64 {
	lengths := make([]int64, len(shares))
	for index, share := range shares {
		for _, item := range share {
			lengths[index] += item.DurationMs
		}
	}
	return lengths
}

// Every index together covers the gap exactly once.
func TestTheSharesCoverTheGapOnce(t *testing.T) {
	for _, count := range []int{1, 2, 3, 4, 7} {
		t.Run(fmt.Sprintf("%d pods", count), func(t *testing.T) {
			gap := mixedGap()

			shares := splitByHours(gap, count, firstElement)

			var all []string
			for _, share := range shares {
				all = append(all, pathsOf(share)...)
			}
			slices.Sort(all)
			if want := pathsOf(gap); !slices.Equal(all, want) {
				t.Errorf("the shares hold %d videos, want each of the %d once", len(all), len(want))
			}
		})
	}
}

// A folder never splits: every video of one title folder is in one share.
func TestAFolderNeverSplits(t *testing.T) {
	shares := splitByHours(mixedGap(), 4, firstElement)

	held := map[string]int{}
	for index, share := range shares {
		for _, item := range share {
			folder := firstElement(item.Path)
			if owner, seen := held[folder]; seen && owner != index {
				t.Fatalf("%s is in shares %d and %d", folder, owner, index)
			}
			held[folder] = index
		}
	}
}

// Every pod reads the same gap, but a pod's copy can return it in any order,
// so the split depends on the gap alone.
func TestTheSameGapGivesTheSameSplitInAnyOrder(t *testing.T) {
	gap := mixedGap()
	reversed := slices.Clone(gap)
	slices.Reverse(reversed)

	one, other := splitByHours(gap, 3, firstElement), splitByHours(reversed, 3, firstElement)

	for index := range one {
		if !slices.Equal(pathsOf(one[index]), pathsOf(other[index])) {
			t.Errorf("index %d takes %v from one order and %v from the other", index,
				pathsOf(one[index]), pathsOf(other[index]))
		}
	}
}

// A split by a hash of each folder's name, which balances the count of
// folders and not their hours: the bar the split by hours must clear.
func hashSplit(items []workItem, count int) [][]workItem {
	shares := make([][]workItem, count)
	for _, item := range items {
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(firstElement(item.Path)))
		index := int(hash.Sum32() % uint32(count))
		shares[index] = append(shares[index], item)
	}
	return shares
}

// A gap of one long series and many films: the series is one pod's work,
// and the split by hours puts the films on the other pods, so the longest
// pod runs no longer than the series alone or an even share of the total,
// whichever is longer, and never longer than the hash split's longest pod.
func TestTheSplitByHoursBalancesOneLongSeries(t *testing.T) {
	for _, count := range []int{2, 3, 4} {
		t.Run(fmt.Sprintf("%d pods", count), func(t *testing.T) {
			gap := oneLongSeriesAndManyFilms()

			byHours := slices.Max(shareLengths(splitByHours(gap, count, firstElement)))
			byHash := slices.Max(shareLengths(hashSplit(gap, count)))

			var total int64
			for _, item := range gap {
				total += item.DurationMs
			}
			series := (400 * 45 * time.Minute).Milliseconds()
			film := (159 * time.Minute).Milliseconds()
			bound := max(series, total/int64(count)+film)
			if byHours > byHash || byHours > bound {
				t.Errorf("the longest pod runs %d ms, want at most %d and no more than the hash split's %d",
					byHours, bound, byHash)
			}
		})
	}
}

// A gap of many films of different lengths splits close to an even share:
// the longest pod runs no more than one film past the total over the pods.
func TestTheSplitByHoursIsCloseToEven(t *testing.T) {
	var gap []workItem
	for film := range 50 {
		folder := fmt.Sprintf("Film %02d (19%02d)", film, 40+film)
		gap = append(gap, gapVideo(folder, folder+".mkv", time.Duration(80+film*2)*time.Minute))
	}
	var total int64
	for _, item := range gap {
		total += item.DurationMs
	}

	longest := slices.Max(shareLengths(splitByHours(gap, 4, firstElement)))

	if bound := total/4 + (178 * time.Minute).Milliseconds(); longest > bound {
		t.Errorf("the longest pod runs %d ms, want at most %d", longest, bound)
	}
}

// One pod is the worker of a Job of one pod: it takes the whole gap.
func TestOnePodTakesTheWholeGap(t *testing.T) {
	gap := mixedGap()

	shares := splitByHours(gap, 1, firstElement)

	if len(shares) != 1 || !slices.Equal(pathsOf(shares[0]), pathsOf(gap)) {
		t.Errorf("the one pod takes %d of the %d videos", len(shares[0]), len(gap))
	}
}

// The pods of one Job share its name, so each pod of several marks its
// temporaries with its index, and two pods that write ledgers in one folder
// never rename each other's temporary. A pod of a Job of one keeps the name
// it had.
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

// A series of two seasons and three other series, as one gap on the volume.
func sharedSeriesGap(t *testing.T, root string) []workItem {
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
		items = append(items, listedVideo(t, root, video, video, time.Time{}))
	}
	return items
}

// Two pods over one gap: each works its own share, the two together work
// every video once, a series goes whole to one pod, each pod asks for
// rescans only of its own title folders, and each names its share.
func TestThePodsOfAWorkerWorkTheGapOnce(t *testing.T) {
	root := t.TempDir()
	items := sharedSeriesGap(t, root)
	worked := map[int][]string{}
	rescans := map[int][]string{}
	for index := range 2 {
		var paths []string
		work, log := gapWorker(t, recordingWorker(&paths), libraryKindSeries, root, items)
		work.share = workerShare{index: index, count: 2}
		webhooks, address := recordWebhooks(t, http.StatusNoContent)
		work.webhook = address

		if err := work.work(t.Context()); err != nil {
			t.Fatal(err)
		}

		worked[index], rescans[index] = paths, webhooks.named()
		want := fmt.Sprintf("index %d of 2 takes %s in ", index, counted(len(paths), "video"))
		if !strings.Contains(log.String(), want) || !strings.Contains(log.String(), "hours, of the 7 videos in the gap") {
			t.Errorf("log = %q, want %q with its hours", log, want)
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
			folder := firstElement(video)
			if !slices.Contains(rescans[index], folder) || slices.Contains(rescans[1-index], folder) {
				t.Errorf("%s: rescans %v, want %q only in the rescans of index %d", video, rescans, folder, index)
			}
		}
	}
}

// The paths of a gap, sorted.
func pathsOf(items []workItem) []string {
	var paths []string
	for _, item := range items {
		paths = append(paths, path.Clean(item.Path))
	}
	slices.Sort(paths)
	return paths
}
