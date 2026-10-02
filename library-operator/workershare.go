package main

// workershare.go splits one work list between the pods of a worker Job. A
// Library can run a heavy fact's worker as several pods at once, so the
// decode of a large library runs on several nodes' GPUs (plan 76). Every pod
// reads the same list, and each one takes the videos of the title folders
// that hash to its index. The split needs no store that the pods share and no
// lease, because each pod computes it alone from the list and the volume.
//
// The split goes by title folder and not by video. The title folder is the
// folder a rescan names, and it holds the .liken ledgers of its videos: the
// season folders of a series and the extras of a movie. So no two pods write
// one title's ledgers, and no two pods ask for a rescan of one folder. A
// video at the root has no title folder, so it goes by its own path, and two
// pods can write the root's ledger. The update door (volumeupdate.go) keeps
// that ledger whole, as it does for two clusters on one volume.

import (
	"fmt"
	"hash/fnv"
	"strconv"
)

// The variables that carry a pod's share. The Job controller sets the index
// in each pod of an Indexed Job, and the operator sets the count in the pod
// template. A pod of a Job of one pod has neither.
const (
	completionIndexVariable   = "JOB_COMPLETION_INDEX"
	workerParallelismVariable = "LIBRARY_WORKER_PARALLELISM"
)

// One pod's share of a list: its index, from 0, and the count of pods.
type workerShare struct {
	index, count int
}

// The share a pod reads from its environment. A Job of one pod sets neither
// variable, and its pod takes the whole list. An index outside the count is
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

// Whether this pod takes the videos of one title folder. The hash is FNV-1a,
// whose value is fixed by its definition, so every pod of a Job computes the
// same split on any node and in any release. Go's map order and its own
// string hash change from one process to the next.
func (s workerShare) takes(folder string) bool {
	if s.count <= 1 {
		return true
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(folder))
	return int(hash.Sum32()%uint32(s.count)) == s.index
}

// The videos of the list this pod works, in the list's order, with one line
// that names its share.
func (w *factWorkerRun) shareOf(items []workItem) []workItem {
	if w.share.count <= 1 {
		return items
	}
	var mine []workItem
	for _, item := range items {
		if w.share.takes(w.titleFolder(item.Path)) {
			mine = append(mine, item)
		}
	}
	w.logf("index %d of %d takes %d of the %s", w.share.index, w.share.count, len(mine),
		counted(len(items), "video"))
	return mine
}

// The name a pod's temporaries carry. The pods of one Job share the Job's
// name, and two of them can write the root's ledger at once, so each pod of a
// Job of several adds its index, and no pod renames another's temporary.
func (s workerShare) writerName(job string) string {
	if s.count <= 1 {
		return job
	}
	return job + "-" + strconv.Itoa(s.index)
}
