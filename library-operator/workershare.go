package main

// workershare.go splits one heavy fact's gap between the pods of a worker
// Job. A Library can run a heavy fact's worker as several pods at once, so the
// decode of a large library runs on several nodes' GPUs (plan 76). Every pod
// reads the same gap from its own copy of the catalog and runs the same rule
// on it, so every pod computes the same split, and no pod writes anything to
// coordinate.
//
// The split goes by title folder and not by video. The title folder is the
// folder a rescan names, and it holds the .liken ledgers of its videos: the
// season folders of a series and the extras of a movie. So no two pods write
// one title's ledgers, and no two pods ask for a rescan of one folder. The
// library root holds no title and no ledger, so a folder is never the root.
//
// The split balances hours, not folders, because a decode takes time in
// proportion to the video's length. Each folder weighs the sum of its videos'
// lengths. The folders go out longest first, each to the pod whose total is
// lowest so far, which is the longest-processing-time rule. A series of many
// episodes is still one pod's work, because its ledger is one file.

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The variables that carry a pod's share. The Job controller sets the index
// in each pod of an Indexed Job, and the operator sets the count in the pod
// template. A pod of a Job of one pod has neither.
const (
	completionIndexVariable   = "JOB_COMPLETION_INDEX"
	workerParallelismVariable = "LIBRARY_WORKER_PARALLELISM"
)

// One pod's share of a gap: its index, from 0, and the count of pods.
type workerShare struct {
	index, count int
}

// The share a pod reads from its environment. A Job of one pod sets neither
// variable, and its pod takes the whole gap. An index outside the count is
// a Job to repair, so the pod fails before it reads the volume, and no title
// is left in no share without a log line.
func workerShareOf(index, count string) (workerShare, error) {
	share := workerShare{index: 0, count: 1}
	var err error
	if count != "" {
		if share.count, err = strconv.Atoi(count); err != nil || share.count < 1 {
			return workerShare{}, fmt.Errorf("%s is %q, which is not a count of pods", workerParallelismVariable, count)
		}
	}
	if index != "" {
		if share.index, err = strconv.Atoi(index); err != nil || share.index < 0 || share.index >= share.count {
			return workerShare{}, fmt.Errorf("%s is %q, which is no index of %d pods",
				completionIndexVariable, index, share.count)
		}
	}
	return share, nil
}

// The videos of one title folder and the hours they run.
type shareFolder struct {
	path  string
	items []workItem
	ms    int64
}

// The gap grouped by title folder, longest first, with the path as the
// tie-break, so the order depends on the gap alone and not on the order the
// gap arrived in.
func shareFolders(items []workItem, folderOf func(string) string) []shareFolder {
	byPath := map[string]*shareFolder{}
	for _, item := range items {
		path := folderOf(item.Path)
		folder := byPath[path]
		if folder == nil {
			folder = &shareFolder{path: path}
			byPath[path] = folder
		}
		folder.items = append(folder.items, item)
		folder.ms += item.DurationMs
	}
	folders := make([]shareFolder, 0, len(byPath))
	for _, folder := range byPath {
		folders = append(folders, *folder)
	}
	slices.SortFunc(folders, func(a, b shareFolder) int {
		return cmp.Or(cmp.Compare(b.ms, a.ms), strings.Compare(a.path, b.path))
	})
	return folders
}

// The split of a gap into count shares: each folder, longest first, goes to
// the share whose total is lowest so far, and the lowest index takes a tie.
// Each share holds its videos in path order, the order the worker reads them
// in.
func splitByHours(items []workItem, count int, folderOf func(string) string) [][]workItem {
	shares := make([][]workItem, count)
	totals := make([]int64, count)
	for _, folder := range shareFolders(items, folderOf) {
		lowest := 0
		for index := range totals {
			if totals[index] < totals[lowest] {
				lowest = index
			}
		}
		shares[lowest] = append(shares[lowest], folder.items...)
		totals[lowest] += folder.ms
	}
	for _, share := range shares {
		slices.SortFunc(share, func(a, b workItem) int { return strings.Compare(a.Path, b.Path) })
	}
	return shares
}

// The videos of the gap this pod works, with one line that names its share
// in folders, videos, and hours. A Job of one pod takes the whole gap.
func (w *factWorkerRun) shareOf(items []workItem) []workItem {
	count := max(w.share.count, 1)
	mine := splitByHours(items, count, w.titleFolder)[w.share.index]
	folders := map[string]bool{}
	var length time.Duration
	for _, item := range mine {
		folders[w.titleFolder(item.Path)] = true
		length += item.duration()
	}
	w.logf("index %d of %d takes %s in %s, %.1f hours, of the %s in the gap", w.share.index, count,
		counted(len(mine), "video"), counted(len(folders), "folder"), length.Hours(),
		counted(len(items), "video"))
	return mine
}

// The name a pod's temporaries carry. The pods of one Job share the Job's
// name, so each pod of a Job of several adds its index. A video that no title
// folder holds goes by its own path, so two pods can write ledgers in one
// folder, and the index keeps either pod from renaming the other's temporary.
func (s workerShare) writerName(job string) string {
	if s.count <= 1 {
		return job
	}
	return job + "-" + strconv.Itoa(s.index)
}
