package main

// factworkers.go is the table of the heavy facts: the facts that read a whole
// video and write only files onto the library volume. Each one runs in a
// worker Job of its own, outside the library Job, because one Library runs
// one library Job at a time and a fact that decodes for minutes per title
// would hold every walk and every webhook rescan behind it.
//
// The library Job publishes each heavy fact's gap on the bus as a work list
// (worklist.go). The operator starts the fact's worker on that list as an
// Indexed Job (factworkerjob.go), and each pod of the worker works the one
// video at its index (factworkerrun.go). A worker writes no catalog row. The
// volume holds every fact and the catalog derives from the volume, so a
// worker writes the fact's outputs and its .liken ledger, and the walk of the
// folder that the worker asks for reads them into the catalog.

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
	// How many videos of the list the worker Job runs at once, one pod per
	// video. It is never below 1.
	parallelism func(library *Library) int
	// Where the container gets a directory of its own that is not the volume,
	// as an emptyDir the pod takes with it, and empty where it needs none.
	scratch string
	// The work on one video of the gap: the fact's outputs and its attempt
	// in the ledger, whatever the outcome.
	work func(ctx context.Context, run *factWorkerRun, item workItem)
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

// Whether a Job's worker label names a heavy fact. Such a Job runs no catalog
// agent, so it holds no gate that the Library's catalog claim needs.
func isFactWorker(worker string) bool {
	_, held := factWorkerOf(worker)
	return held
}

// The count of videos at once a Library's block asks for. A Library the API
// server served carries the CRD's default of 1. A Library built in this
// program can carry 0, which is one too.
func podsOf(parallelism int) int {
	return max(parallelism, 1)
}
