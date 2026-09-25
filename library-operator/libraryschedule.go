package main

// libraryschedule.go decides when a Library runs its Job. The operator starts
// a Job only when no other Job of the Library is unfinished, because every
// Job of a Library runs an agent on the one catalog claim, and two agents on
// one database corrupt it. The gate reads the Job list the pass read, so a
// restarted operator keeps it with no state of its own.
//
// Behind the gate the pass chooses one Job. A walk runs when
// spec.scan.schedule says a walk is due, when spec.refresh asks for one, and
// when a webhook named folders. A Job that fills gaps runs when no walk is
// due and the last report counted gaps that a cause has opened since the last
// Job.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// How many pods Kubernetes replaces before a Job itself fails, and how long
// a finished Job stays for a person to read its logs. The TTL is the hour a
// failed Job keeps. A succeeded Job goes sooner, because the operator deletes
// it after succeededJobGrace, and the TTL is the backstop when the operator
// is down.
const (
	scanBackoffLimit = 2
	scanJobTTL       = 3600
)

// The Jobs of one Library and one worker, out of the whole cluster's Jobs
// the pass listed.
func jobsOf(jobs []Job, namespace, library, worker string) []Job {
	return slices.DeleteFunc(jobsOfLibrary(jobs, namespace, library), func(job Job) bool {
		return job.Metadata.Labels[workerLabelKey] != worker
	})
}

// Every Job of one Library, whatever its worker.
func jobsOfLibrary(jobs []Job, namespace, library string) []Job {
	held := []Job{}
	for index := range jobs {
		job := &jobs[index]
		if job.Metadata.Namespace == namespace && job.Metadata.Labels[libraryLabelKey] == library {
			held = append(held, *job)
		}
	}
	return held
}

// Whether any Job of this Library is unfinished: one the controller has
// marked neither Complete nor Failed. A Job between the pods of its backoff
// counts. Every worker counts, the cleanup Job and a Job an earlier release
// created included, because each of them runs an agent on the Library's
// catalog claim.
func libraryJobUnfinished(jobs []Job, namespace, library string) bool {
	return slices.ContainsFunc(jobsOfLibrary(jobs, namespace, library), func(job Job) bool {
		return !job.finished()
	})
}

// When the operator created a Job, off the annotation it wrote, and the zero
// time for a Job that carries none.
func jobCreated(job *Job) time.Time {
	created, err := time.Parse(time.RFC3339Nano, job.Metadata.Annotations[jobCreatedAnnotation])
	if err != nil {
		return time.Time{}
	}
	return created
}

// The newest Job of one Library that the controller has ended, or nil.
func newestFinishedJob(jobs []Job, namespace, library string) *Job {
	var newest *Job
	for _, job := range jobsOfLibrary(jobs, namespace, library) {
		if !job.finished() || (newest != nil && !jobCreated(&job).After(jobCreated(newest))) {
			continue
		}
		newest = &job
	}
	return newest
}

// The library step of one Library's pass. It creates at most one Job.
func (o *operator) runLibrary(ctx context.Context, library *Library, report *libraryReport,
	jobs []Job, providers providerSet, now time.Time) error {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	if libraryJobUnfinished(jobs, namespace, name) || !o.mayFollow(jobs, namespace, name, now) {
		return nil
	}
	held := o.paths.held(namespace, name)
	plan, due := nextLibraryJob(library, report, jobs, providers, held, now)
	if !due {
		return nil
	}
	if report != nil {
		plan.sync = syncTargetFor(report.Runs)
	}
	job := buildLibraryJob(library, providers, o.languages, plan,
		jobImages{operator: o.scannerImage, ffmpeg: o.ffmpegImage, corrosion: o.corrosionImage}, now)
	if _, err := CreateJob(ctx, o.client, job); err != nil && !errors.Is(err, ErrConflict) {
		return fmt.Errorf("creating the library job %s: %w", job.Metadata.Name, err)
	}
	// A walk covers every folder the webhooks named before it, so each one
	// the pass read is released. A folder named after the read stays for
	// the next Job.
	if plan.mode == jobModeWalk {
		for _, path := range held {
			o.paths.release(namespace, name, path)
		}
	}
	return nil
}

// Whether the pass may start a Job after the newest one ended. A Job that
// failed is followed on the backoff curve a cleanup Job uses, so a cause
// nobody has repaired costs one Job per delay and not one Job per pass. A Job
// that succeeded resets the curve.
func (o *operator) mayFollow(jobs []Job, namespace, library string, now time.Time) bool {
	key := libraryKey(namespace, library) + "/job"
	newest := newestFinishedJob(jobs, namespace, library)
	if newest == nil || !newest.failed() {
		delete(o.failedStands, key)
		return true
	}
	return o.mayRestandFailed(key, now)
}

// The Job this pass would start, and whether one is due. A walk comes first,
// because it covers what a Job that fills gaps would do.
func nextLibraryJob(library *Library, report *libraryReport, jobs []Job, providers providerSet,
	held []string, now time.Time) (libraryJob, bool) {
	served := servedPhases(library, providers)
	last := lastWalkStart(report, jobs, library.Metadata.Namespace, library.Metadata.Name)
	if walkDue(library, last, now) || walkRequested(library, last) || slices.Contains(held, "") {
		return libraryJob{mode: jobModeWalk, phases: served}, true
	}
	if len(held) > 0 {
		return libraryJob{mode: jobModeWalk, paths: held, phases: served}, true
	}
	if report == nil {
		return libraryJob{}, false
	}
	gaps := gapPhases(library, report, providers, served, now)
	return libraryJob{mode: jobModeGaps, phases: gaps}, len(gaps) > 0
}

// When the last full walk of this Library started: the later of the scan run
// the report carries and the newest full walk Job the pass listed. The Job is
// read as well, because a Job that ended can reach the pass before its row
// reaches the report.
func lastWalkStart(report *libraryReport, jobs []Job, namespace, library string) time.Time {
	var last time.Time
	if report != nil {
		if walk, ran := runOf(report.Runs, workerScan); ran {
			last = walk.Started
		}
	}
	for _, job := range jobsOf(jobs, namespace, library, jobModeWalk) {
		if job.Metadata.Annotations[jobPathsAnnotation] != "" {
			continue
		}
		if created := jobCreated(&job); created.After(last) {
			last = created
		}
	}
	return last
}

// Whether spec.scan.schedule has a time between the last walk's start and
// now. A Library with no walk at all is due, so a new Library has rows
// before its schedule's first time. A schedule that does not parse is never
// due, and the Ready condition says why.
func walkDue(library *Library, last, now time.Time) bool {
	if last.IsZero() {
		return true
	}
	schedule, err := parseScanSchedule(library.Spec.scanSchedule())
	if err != nil {
		return false
	}
	next := nextWalk(schedule, last)
	return !next.IsZero() && !next.After(now)
}

// Whether spec.refresh asks for a walk that has not started. A walk that
// started before the request does not answer it, because that walk may have
// read the volume before the person asked.
func walkRequested(library *Library, last time.Time) bool {
	requested, named := library.Spec.Refresh[refreshWalk]
	return named && requested.After(last)
}

// The phases a Job that fills gaps runs. Trickplay and the trailer files run
// while their gap is open, because a run stops at its time limit and leaves
// the rest for the next Job. Every other phase runs while its gap is open and
// a cause has come that no Job has answered, so a gap that no phase can close
// does not start a Job on every pass.
func gapPhases(library *Library, report *libraryReport, providers providerSet,
	served []servedPhase, now time.Time) []servedPhase {
	due := enrichDue(enrichCause(library, report.Runs, providers, now), report.Runs)
	var phases []servedPhase
	for _, phase := range served {
		if !phaseGapOpen(library, report, phase.served) {
			continue
		}
		if timeLimitedPhases[phase.name] || due {
			phases = append(phases, phase)
		}
	}
	return phases
}

// The cause of the next Job that fills gaps: the newest of a refresh time
// that has come, a source provider that turned Ready, and a walk whose own
// Job ran no phases. A walk Job runs every phase after its walk, so its walk
// is answered already.
func enrichCause(library *Library, runs []libraryRun, providers providerSet, now time.Time) time.Time {
	var cause time.Time
	enrich, _ := runOf(runs, workerEnrich)
	for _, worker := range []string{workerScan, workerRescan} {
		if walk, ran := runOf(runs, worker); ran && walk.Job != enrich.Job && walk.Finished.After(cause) {
			cause = walk.Finished
		}
	}
	for fact, refresh := range library.Spec.Refresh {
		if isContainerFact(fact) && !refresh.After(now) && refresh.After(cause) {
			cause = refresh
		}
	}
	for _, name := range library.Spec.Sources {
		provider, held := providers[libraryKey(library.Metadata.Namespace, name)]
		if !held || !provider.ready() {
			continue
		}
		if ready := provider.readyCondition().LastTransitionTime; ready.After(cause) {
			cause = ready
		}
	}
	return cause
}

// Whether a cause has come that no Job has answered. A Job reads the
// providers and spec.refresh when the operator creates it, so a Job that
// started before the cause never saw it. A run that wrote no finish
// answered nothing.
func enrichDue(cause time.Time, runs []libraryRun) bool {
	if cause.IsZero() {
		return false
	}
	enrich, held := runOf(runs, workerEnrich)
	return !held || enrich.Finished.IsZero() || cause.After(enrich.Started)
}

// Whether one fact's refresh time has titles left to ask about. The
// reporter counts a gap with no refresh, so a title whose file and rows
// are there counts as filled; the oldest attempt of the fact is what
// says the refresh still has work, and the fact's own run moves that
// attempt past the refresh, which is what ends the work.
func refreshHasWork(library *Library, report *libraryReport, fact string) bool {
	refresh, named := library.Spec.Refresh[fact]
	if !named {
		return false
	}
	oldest, held := report.OldestAttempts[fact]
	return held && refresh.After(oldest) && !refresh.After(time.Now())
}
