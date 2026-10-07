package main

// factworkers.go is the table of the heavy facts: the facts that read a whole
// video and write only files onto the library volume. Each one runs in a
// worker Job of its own, outside the library Job, because one Library runs
// one library Job at a time and a fact that decodes for minutes per title
// would hold every walk and every webhook rescan behind it.
//
// Each pod of a worker Job runs a catalog agent on a copy of its own, and
// reads the fact's gap from that copy (workgap.go). It writes no catalog row.
// The volume holds every fact and the catalog derives from the volume, so a
// worker writes the fact's outputs and its .liken ledger, and the next walk
// of the folder reads them into the catalog. The operator starts the worker
// when a library Job has ended with the gap open (factworkerjob.go), and the
// worker works through its share of the gap (factworkerrun.go).

import "context"

// One kind of worker Job.
type factWorker struct {
	// The fact. It is also the worker label of the fact's Jobs and the name
	// of the worker's container.
	fact string
	// Whether a Library runs the fact.
	enabled func(library *Library) bool
	// The image the container runs.
	image func(images jobImages) string
	// What the container asks for and the memory it may take.
	resources func() ResourceRequirements
	// The ResourceClaimTemplate the pod claims its GPU from, and empty where
	// the Library names none (gpuclaim.go).
	gpuClaimTemplate func(library *Library) string
	// How many pods the worker Job runs, each on its own share of the gap
	// (workershare.go). It is never below 1.
	parallelism func(library *Library) int
	// Where the container gets a directory of its own that is not the volume,
	// as an emptyDir the pod takes with it, and empty where it needs none.
	scratch string
	// The work on one video of the gap: the fact's outputs and its attempt
	// in the ledger, whatever the outcome.
	work func(ctx context.Context, run *factWorkerRun, item workItem)
	// Whether one video of the gap is quick to work, and nil where every
	// video takes the same work. The worker takes the quick videos first,
	// so a gap that a refresh filled with them answers them in minutes
	// and not after the slow ones.
	quick func(run *factWorkerRun, item workItem) bool
}

// The heavy facts, in the order the operator considers them.
var factWorkers = []factWorker{trickplayWorker, appearancesWorker}

// The worker of one fact, or false where the fact runs in the library Job.
func factWorkerOf(fact string) (factWorker, bool) {
	for _, worker := range factWorkers {
		if worker.fact == fact {
			return worker, true
		}
	}
	return factWorker{}, false
}

// Whether a Job's worker label names a heavy fact. Such a Job's agent runs on
// a copy of its own, so it holds no gate that the Library's catalog claim
// needs.
func isFactWorker(worker string) bool {
	_, held := factWorkerOf(worker)
	return held
}

// The count of pods a Library's block asks for. A Library the API server
// served carries the CRD's default of 1. A Library built in this program can
// carry 0, which is one pod too.
func podsOf(parallelism int) int {
	return max(parallelism, 1)
}
