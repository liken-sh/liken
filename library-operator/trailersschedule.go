package main

// trailersschedule.go is when the trailers Job runs. The Job stands beside the
// enricher rather than behind it: the enricher's own gap counts leave the
// trailerfile gap out, and nothing here waits on an enricher Job.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// The standing trailers Job of one Library, named from the walk it answers,
// so one walk yields one Job however many passes read it.
func standingTrailersJobName(library string, runs []libraryRun) string {
	return chainJobName(library, workerTrailers,
		strconv.FormatInt(lastScanFinish(runs).Unix(), 36))
}

// Whether any trailers Job of this Library is still open, by the rule
// scanUnfinished applies to a scan. libraryBusy never reads this, because the
// trailers Job runs beside the enricher.
func trailersUnfinished(jobs []Job, namespace, library string) bool {
	for _, job := range jobsOf(jobs, namespace, library, workerTrailers) {
		if !job.finished() {
			return true
		}
	}
	return false
}

// Whether the trailerfile fact has work left. The fact takes no refresh, so
// the gap count is the whole answer, and a Library that leaves the fact off
// never closes that gap.
func trailersGapOpen(library *Library, report *libraryReport) bool {
	if !library.Spec.Trailers.Enabled {
		return false
	}
	return report.Gaps[factTrailerFile] > 0
}

// Whether any Ready source of this Library names a block that one of the
// trailerFetchers serves. LIBRARY_SOURCES carries those blocks into the
// container, and a gap with no site to fetch from stands no Job, which is the
// rule the enricher applies to a fact no source serves. A provider that turns
// Ready one pass later stands the Job on that pass.
func trailersFetchable(library *Library, providers providerSet) bool {
	blocks := trailerFetchBlocks()
	for _, block := range sourceBlocks(library, providers) {
		if blocks[block] {
			return true
		}
	}
	return false
}

// The trailers step of one Library's pass. It creates at most one Job,
// because every trailers Job of a Library runs on the one claim its agent
// keeps.
func (o *operator) trailers(ctx context.Context, library *Library, catalog *NamespaceCatalog,
	report *libraryReport, jobs []Job, providers providerSet, now time.Time) error {
	if report == nil || !trailersGapOpen(library, report) ||
		!trailersFetchable(library, providers) {
		return nil
	}
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	if trailersUnfinished(jobs, namespace, name) {
		return nil
	}
	// A library no walk has finished for has no counts to schedule on, and no
	// walk to name the Job after.
	if lastScanFinish(report.Runs).IsZero() {
		return nil
	}
	standing := standingTrailersJobName(name, report.Runs)
	if held := jobNamed(jobs, namespace, standing); held != nil {
		return o.retireFailedJob(ctx, held, libraryKey(namespace, name)+"/"+workerTrailers, now)
	}
	return o.createTrailersJob(ctx, library, catalog, providers, standing, "", nil)
}

// The trailers Job and the claim it runs on. The claim is created first,
// because a pod that names a claim nothing has created waits Pending until
// the next pass.
func (o *operator) createTrailersJob(ctx context.Context, library *Library, catalog *NamespaceCatalog,
	providers providerSet, name, path string, marks map[string]string) error {
	if err := o.standTrailersClaim(ctx, library, catalog); err != nil {
		return err
	}
	job := buildTrailersJob(library, providers, o.languages, name, path,
		o.ffmpegImage, o.corrosionImage)
	job.Metadata.Annotations = marks

	if _, err := CreateJob(ctx, o.client, job); err != nil && !errors.Is(err, ErrConflict) {
		return fmt.Errorf("creating the trailers job %s: %w", name, err)
	}
	return nil
}
