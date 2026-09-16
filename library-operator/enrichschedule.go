package main

// enrichschedule.go decides when an enricher runs. It creates the standing
// enricher Job of a Library when a gap is open and nothing else runs, and it
// carries a webhook's folder through its chain: the folder scan, the folder
// enrich, and the folder rescan that reads what the enricher wrote. The
// operator is the only scheduler. It reads every decision off the Job list
// and the reporter's report, so it keeps no state of its own.

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"slices"
	"strconv"
	"time"
)

// A chain Job carries three marks. The pass reads every chain out of the Job
// list on every turn and keeps no record of its own, so a restarted operator
// carries on a chain it did not start.
const (
	chainAnnotation      = "library.liken.sh/chain"
	chainPathAnnotation  = "library.liken.sh/chain-path"
	chainStageAnnotation = "library.liken.sh/chain-stage"
)

// The stages of a chain, in the order they run. Each stage is the worker its
// Job runs, so a person reads the stage and the runs row as one word.
const (
	chainStageScan   = workerScan
	chainStageEnrich = workerEnrich
	chainStageRescan = workerRescan
)

// The chain's name. Every Job of the chain carries it and takes its own name
// from it. The hash covers the path and the time, so two webhooks for one
// folder are two chains, and no create collides with a Job that still runs.
func newChain(path string, now time.Time) string {
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(path))
	_, _ = sum.Write([]byte(strconv.FormatInt(now.UnixNano(), 10)))
	return strconv.FormatUint(sum.Sum64(), 36)
}

// One Job of one chain, named from the Library, the stage, and the chain.
// Every pass names the same Job, so a create that races another pass
// conflicts instead of running the stage twice.
func chainJobName(library, stage, chain string) string {
	return library + "-" + stage + "-" + chain
}

// The marks one stage's Job carries.
func chainMarks(chain, path, stage string) map[string]string {
	return map[string]string{
		chainAnnotation:      chain,
		chainPathAnnotation:  path,
		chainStageAnnotation: stage,
	}
}

// The standing enricher of one Library, named from the cause it answers: the
// walk's finish or the refresh time. One cause yields one Job however many
// passes read it, and the next cause names a new Job at once instead of
// waiting out the TTL of the finished one.
func standingEnrichJobName(library string, cause time.Time) string {
	return chainJobName(library, chainStageEnrich,
		strconv.FormatInt(cause.Unix(), 36))
}

// One chain as the cluster holds it: the folder it covers and the Job of each
// stage that has run.
type chainRun struct {
	id     string
	path   string
	stages map[string]*Job
}

// The chains of one Library, read out of the Job list alone and returned in
// chain order, so every pass reads them the same way. A Job whose TTL has
// taken it leaves its stage empty, which is what ends a chain that has run
// its course.
func chainsOf(jobs []Job, namespace, library string) []chainRun {
	held := map[string]*chainRun{}
	for index := range jobs {
		job := &jobs[index]
		if job.Metadata.Namespace != namespace || job.Metadata.Labels[libraryLabelKey] != library {
			continue
		}
		id := job.Metadata.Annotations[chainAnnotation]
		if id == "" {
			continue
		}
		chain, known := held[id]
		if !known {
			chain = &chainRun{id: id, path: job.Metadata.Annotations[chainPathAnnotation],
				stages: map[string]*Job{}}
			held[id] = chain
		}
		chain.stages[job.Metadata.Annotations[chainStageAnnotation]] = job
	}
	chains := []chainRun{}
	for _, id := range slices.Sorted(maps.Keys(held)) {
		chains = append(chains, *held[id])
	}
	return chains
}

// The enrichment step of one Library's pass. It creates at most one Job,
// because every enricher of a Library runs on the one claim its agent keeps.
func (o *operator) enrich(ctx context.Context, library *Library, catalog *NamespaceCatalog,
	report *libraryReport, jobs []Job, providers providerSet, now time.Time) error {
	if report == nil {
		return nil
	}
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	if libraryBusy(report, jobs, namespace, name) {
		return nil
	}
	served, err := o.serveChain(ctx, library, catalog, report, jobs, providers)
	if err != nil || served {
		return err
	}
	cause := enrichCause(library, report.Runs, now)
	if !enrichDue(cause, report.Runs) || !gapOpen(library, report, providers) {
		return nil
	}
	return o.createEnrichJob(ctx, library, catalog, providers,
		standingEnrichJobName(name, cause), "", nil)
}

// The next stage of the first chain that has one, or false when every chain
// has run its course. Nothing of this Library runs while this is called,
// because the caller has already checked.
func (o *operator) serveChain(ctx context.Context, library *Library, catalog *NamespaceCatalog,
	report *libraryReport, jobs []Job, providers providerSet) (bool, error) {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	for _, chain := range chainsOf(jobs, namespace, name) {
		if chain.stages[chainStageRescan] != nil {
			continue
		}
		if chain.stages[chainStageEnrich] != nil {
			// The rescan reads what both stages wrote, so a trickplay stage the
			// controller has not ended holds the chain here. A stage whose TTL
			// has taken it leaves the chain to carry on.
			if tiles := chain.stages[chainStageTrickplay]; tiles != nil && !tiles.finished() {
				continue
			}
			return true, o.createChainScan(ctx, library, chain)
		}
		if chain.stages[chainStageScan] == nil || !gapOpen(library, report, providers) {
			continue
		}
		return true, o.createChainStages(ctx, library, catalog, providers, chain)
	}
	return false, nil
}

// The middle of one chain: the enricher of the folder, and beside it the
// trickplay Job of the same folder where the Library turns the fact on. A new
// title arrives by webhook, so this is the path that has to win the race for
// its tiles.
func (o *operator) createChainStages(ctx context.Context, library *Library, catalog *NamespaceCatalog,
	providers providerSet, chain chainRun) error {
	name := library.Metadata.Name
	if err := o.createEnrichJob(ctx, library, catalog, providers,
		chainJobName(name, chainStageEnrich, chain.id), chain.path,
		chainMarks(chain.id, chain.path, chainStageEnrich)); err != nil {
		return err
	}
	if !library.Spec.Trickplay.Enabled {
		return nil
	}
	return o.createTrickplayJob(ctx, library, catalog,
		chainJobName(name, chainStageTrickplay, chain.id), chain.path,
		chainMarks(chain.id, chain.path, chainStageTrickplay))
}

// The enricher Job and the claim it runs on. The claim stands first, because
// a pod that names a claim nothing has created waits Pending until the next
// pass.
func (o *operator) createEnrichJob(ctx context.Context, library *Library, catalog *NamespaceCatalog,
	providers providerSet, name, path string, marks map[string]string) error {
	if err := o.standEnrichClaim(ctx, library, catalog); err != nil {
		return err
	}
	job := buildEnrichJob(library, providers, o.languages, name, path,
		o.scannerImage, o.ffmpegImage, o.corrosionImage)
	job.Metadata.Annotations = marks

	if _, err := CreateJob(ctx, o.client, job); err != nil && !errors.Is(err, ErrConflict) {
		return fmt.Errorf("creating the enrich job %s: %w", name, err)
	}
	return nil
}

// The last stage of a chain: a scan of the same folder, which reads into the
// catalog what the enricher wrote onto the volume.
func (o *operator) createChainScan(ctx context.Context, library *Library, chain chainRun) error {
	job := buildChainScanJob(library, chain, o.scannerImage, o.corrosionImage)
	if _, err := CreateJob(ctx, o.client, job); err != nil && !errors.Is(err, ErrConflict) {
		return fmt.Errorf("creating the rescan job for %s: %w", chain.path, err)
	}
	return nil
}

// The chain's rescan Job. It runs the scanner the webhook's own Job ran, on
// the same folder, and it carries the marks that say the chain is over.
func buildChainScanJob(library *Library, chain chainRun,
	scannerImage, corrosionImage string) *Job {
	return &Job{
		APIVersion: batchAPIVersion,
		Kind:       "Job",
		Metadata: ObjectMeta{
			Name:            chainJobName(library.Metadata.Name, chainStageRescan, chain.id),
			Namespace:       library.Metadata.Namespace,
			Labels:          workerLabels(library.Metadata.Name, workerScan),
			Annotations:     chainMarks(chain.id, chain.path, chainStageRescan),
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: scanJobSpec(library, chain.path, scannerImage, corrosionImage),
	}
}

// Whether any work of this Library is in flight, by the Jobs the pass listed
// and by the runs the reporter published. Both are read, because a Job the
// controller has not started has written no run.
//
// An open run row counts only while the Job that wrote it is still listed. A
// Job that died mid-run holds nothing back.
func libraryBusy(report *libraryReport, jobs []Job, namespace, library string) bool {
	if scanUnfinished(jobs, namespace, library) || enrichUnfinished(jobs, namespace, library) {
		return true
	}
	for _, worker := range []string{workerScan, workerRescan, workerEnrich} {
		run, held := runOf(report.Runs, worker)
		if held && run.Finished.IsZero() && !runAbandoned(run, jobs, namespace, library) {
			return true
		}
	}
	return false
}

// Whether any enricher Job of this Library is still open, by the rule
// scanUnfinished applies to a scan.
func enrichUnfinished(jobs []Job, namespace, library string) bool {
	for _, job := range jobsOf(jobs, namespace, library, workerEnrich) {
		if !job.finished() {
			return true
		}
	}
	return false
}

// The cause of the next enricher: the later of the last walk's finish and the
// newest refresh time that has come. A refresh in the future waits for its
// time, the way refreshSeconds makes it wait.
func enrichCause(library *Library, runs []libraryRun, now time.Time) time.Time {
	cause := lastScanFinish(runs)
	for _, refresh := range library.Spec.Refresh {
		if !refresh.After(now) && refresh.After(cause) {
			cause = refresh
		}
	}
	return cause
}

// Whether an enricher is due: a cause has come that no enrich run has
// answered. An enricher reads spec.refresh when its Job is created, so a run
// that started before the cause never saw it, and a refresh set during a run
// stands one more run after it. A run that wrote no finish answered nothing.
func enrichDue(cause time.Time, runs []libraryRun) bool {
	if cause.IsZero() {
		return false
	}
	enrich, held := runOf(runs, workerEnrich)
	return !held || enrich.Finished.IsZero() || cause.After(enrich.Started)
}

// When a walk of this library last finished, whether it was the full walk or
// one folder.
func lastScanFinish(runs []libraryRun) time.Time {
	latest := time.Time{}
	for _, worker := range []string{workerScan, workerRescan} {
		if run, held := runOf(runs, worker); held && run.Finished.After(latest) {
			latest = run.Finished
		}
	}
	return latest
}

// Whether any fact has work left that this Library can do. A fact that
// needs a provider counts only where the Library's sources name one that is
// Ready and serves it, so a library with no key never runs a Job that has
// nothing to ask.
func gapOpen(library *Library, report *libraryReport, providers providerSet) bool {
	for fact, count := range report.Gaps {
		// The trickplay gap and the trailerfile gap are their own Jobs', and an
		// enricher that counted one would run for ever with nothing to do.
		if fact == factTrickplay || fact == factTrailerFile {
			continue
		}
		if count <= 0 && !refreshHasWork(library, report, fact) {
			continue
		}
		if fact != factProbe && fact != factArrival &&
			providers.serving(library.Metadata.Namespace, library.Spec.Sources, fact) == nil {
			continue
		}
		return true
	}
	return false
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
