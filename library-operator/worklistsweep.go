package main

// worklistsweep.go clears the work lists the operator holds no reason to
// keep. A list costs the broker memory until someone publishes an empty
// payload on each of its topics, and no pod does that: the library Job ends
// before its worker starts, and each pod of the worker reads one video. So
// the pass clears a list when the worker that took it has finished, when a
// newer list of the same fact replaces a list no worker took, and when its
// Library is gone. The desk holds every count the broker retains, so a
// restarted operator clears the lists an earlier process left.

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// How long one pass may take to clear lists. A clear of the longest list is
// 10,001 small publishes and one ping, which a broker reads in well under a
// second.
const workListClearTimeout = time.Minute

// Whether the pass keeps one list on the bus. library is nil where the
// Library is gone.
//
// A worker that names the list keeps it while it runs and clears it when it
// has finished. A list that the last worker took and whose Job is gone has
// been worked. A list no worker took is kept while it is the newest work of
// its fact, so the worker can start on it: no newer list of the fact, and no
// newer finished library Job, which would have listed the fact again or found
// its gap empty.
func (o *operator) keepsWorkList(list workList, library *Library, jobs []Job, held []workList) bool {
	if library == nil || library.Metadata.deleting() {
		return false
	}
	for _, job := range jobsOf(jobs, list.namespace, list.library, list.fact) {
		if job.Metadata.Annotations[workListAnnotation] == list.run {
			return !job.finished()
		}
	}
	worker, known := factWorkerOf(list.fact)
	if !known || !worker.enabled(library) {
		return false
	}
	if o.workListsTaken[libraryKey(list.namespace, list.library)+"/"+list.fact] == list.run {
		return false
	}
	for _, other := range held {
		if other.namespace == list.namespace && other.library == list.library && other.fact == list.fact &&
			jobNewer(other.run, list.run) {
			return false
		}
	}
	listed := listedRun(o.reports.latestFor(list.namespace, list.library))
	return listed.Finished.IsZero() || !jobNewer(listed.Job, list.run)
}

// The sweep of one pass, after every Library's reconcile, so a worker the
// pass started keeps its list. A broker the pass cannot reach leaves every
// list on the desk, and the next pass tries again.
func (o *operator) sweepWorkLists(ctx context.Context, libraries []Library, jobs []Job) {
	held := o.workLists.held()
	var stale []workList
	for _, list := range held {
		if !o.keepsWorkList(list, libraryIn(libraries, list.namespace, list.library), jobs, held) {
			stale = append(stale, list)
		}
	}
	if len(stale) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, workListClearTimeout)
	defer cancel()
	session, err := openBusSession(ctx, o.bus.dial, "library-operator-sweep")
	if err != nil {
		o.logf("could not clear %s from the bus: %v", counted(len(stale), "work list"), err)
		return
	}
	defer session.close()
	for _, list := range stale {
		count, _ := o.workLists.countOf(list)
		if err := clearWorkList(session, o.topicBase, list, count); err != nil {
			o.logf("library %s/%s: could not clear the %s list from the job %s: %v", list.namespace,
				list.library, list.fact, list.run, err)
			return
		}
		o.workLists.drop(list)
		o.logf("library %s/%s: cleared the %s list of %s from the job %s", list.namespace, list.library,
			list.fact, counted(count, "video"), list.run)
	}
}

// The Library of one name in the pass's collection, and nil where there is
// none.
func libraryIn(libraries []Library, namespace, name string) *Library {
	for index := range libraries {
		if libraries[index].Metadata.Namespace == namespace && libraries[index].Metadata.Name == name {
			return &libraries[index]
		}
	}
	return nil
}

// Whether the Job of one name was created after the Job of another. A Job's
// name ends in its creation time in nanoseconds, in base 36
// (libraryJobName), so a name that does not parse is newer than nothing.
func jobNewer(name, than string) bool {
	created, ok := jobCreatedOf(name)
	before, held := jobCreatedOf(than)
	return ok && held && created > before
}

// The creation time a Job's name ends in, and false where it ends in none.
func jobCreatedOf(name string) (int64, bool) {
	index := strings.LastIndex(name, "-")
	if index < 0 {
		return 0, false
	}
	created, err := strconv.ParseInt(name[index+1:], 36, 64)
	return created, err == nil
}
