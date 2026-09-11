package main

// When the trickplay Job runs. It stands beside the enricher rather than behind
// it: the enricher's own gap counts leave the trickplay gap out, and nothing
// here waits on an enricher Job.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// The stage a chain's trickplay Job carries, which is the worker it runs, as
// every other stage is.
const chainStageTrickplay = workerTrickplay

// The standing trickplay Job of one Library, named from the walk it answers, so
// one walk yields one Job however many passes read it.
func standingTrickplayJobName(library string, runs []libraryRun) string {
	return chainJobName(library, chainStageTrickplay,
		strconv.FormatInt(lastScanFinish(runs).Unix(), 36))
}

// Whether any trickplay Job of this Library is still open, by the rule
// scanUnfinished applies to a scan. libraryBusy never reads this, because the
// trickplay Job runs beside the enricher.
func trickplayUnfinished(jobs []Job, namespace, library string) bool {
	for _, job := range jobsOf(jobs, namespace, library, workerTrickplay) {
		if !job.finished() {
			return true
		}
	}
	return false
}

// Whether the trickplay fact has work left. A Library that leaves the fact off
// never closes that gap, so it counts only where the fact is on.
func trickplayGapOpen(library *Library, report *libraryReport) bool {
	if !library.Spec.Trickplay.Enabled {
		return false
	}
	return report.Gaps[factTrickplay] > 0 || refreshHasWork(library, report, factTrickplay)
}

// The trickplay step of one Library's pass. It creates at most one Job, because
// every trickplay Job of a Library runs on the one claim its agent keeps.
func (o *operator) trickplay(ctx context.Context, library *Library, catalog *NamespaceCatalog,
	report *libraryReport, jobs []Job) error {
	if report == nil || !trickplayGapOpen(library, report) {
		return nil
	}
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	if trickplayUnfinished(jobs, namespace, name) {
		return nil
	}
	// A library no walk has finished for has no counts to schedule on, and no
	// walk to name the Job after.
	if lastScanFinish(report.Runs).IsZero() {
		return nil
	}
	return o.createTrickplayJob(ctx, library, catalog,
		standingTrickplayJobName(name, report.Runs), "", nil)
}

// The trickplay Job and the claim it runs on. The claim stands first, because a
// pod that names a claim nothing has created waits Pending until the next pass.
func (o *operator) createTrickplayJob(ctx context.Context, library *Library, catalog *NamespaceCatalog,
	name, path string, marks map[string]string) error {
	if err := o.standTrickplayClaim(ctx, library, catalog); err != nil {
		return err
	}
	job := buildTrickplayJob(library, name, path,
		o.ffmpegImage, o.corrosionImage, o.busAddress, o.topicBase)
	job.Metadata.Annotations = marks

	if _, err := CreateJob(ctx, o.client, job); err != nil && !errors.Is(err, ErrConflict) {
		return fmt.Errorf("creating the trickplay job %s: %w", name, err)
	}
	return nil
}
