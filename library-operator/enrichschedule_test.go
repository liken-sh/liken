package main

// what these tests read: when the operator creates the enricher Job of
// one Library, and the chain a webhook's folder runs through: a folder
// scan, then a folder enrich, then a folder rescan, and no more.

import (
	"net/http"
	"slices"
	"testing"
	"time"
)

// a Job the controller has ended, which is what the pass reads as a
// finished stage.
func finishedJob(name, namespace string, labels, annotations map[string]string) Job {
	return Job{
		Metadata: ObjectMeta{Name: name, Namespace: namespace,
			Labels: labels, Annotations: annotations},
		Status: JobStatus{Conditions: []JobCondition{{Type: jobComplete, Status: ConditionTrue}}},
	}
}

// a Job the controller has not ended.
func runningJob(name, namespace string, labels map[string]string) Job {
	return Job{
		Metadata: ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Status:   JobStatus{Active: 1},
	}
}

// the runs a library reports after one walk that finished.
func walkedRuns(at time.Time) []libraryRun {
	return []libraryRun{{Worker: workerScan, Job: "movies-scan-1",
		Started: at.Add(-time.Minute), Finished: at}}
}

// a library with the sources and the provider that serve identity.
func libraryWithProvider() (*Library, providerSet) {
	library := studioMovies()
	library.Spec.Sources = []string{"tmdb"}
	provider := readyProvider("tmdb", "house", concernIdentity)
	return library, providerSet{libraryKey("house", "tmdb"): provider}
}

// the enricher Job is created when a gap is open, no run is in flight,
// and a scan has finished since the last enrich run.
func TestEnrichSchedulesOneJobPerLibrary(t *testing.T) {
	walked := testNow.Add(-time.Hour)
	scanLabels := workerLabels("movies", workerScan)
	enrichLabels := workerLabels("movies", workerEnrich)

	cases := []struct {
		name    string
		gaps    map[string]int
		runs    []libraryRun
		jobs    []Job
		sources bool
		want    bool
	}{
		{name: "a gap is open and nothing is running",
			gaps: map[string]int{concernProbe: 4}, runs: walkedRuns(walked), want: true},
		{name: "a scan Job is still running",
			gaps: map[string]int{concernProbe: 4}, runs: walkedRuns(walked),
			jobs: []Job{runningJob("movies-scan-29", "house", scanLabels)}},
		{name: "an enricher Job is still running",
			gaps: map[string]int{concernProbe: 4}, runs: walkedRuns(walked),
			jobs: []Job{runningJob("movies-enrich", "house", enrichLabels)}},
		{name: "a scan run is in flight",
			gaps: map[string]int{concernProbe: 4},
			runs: []libraryRun{{Worker: workerScan, Job: "movies-scan-2", Started: testNow}}},
		{name: "no scan has finished since the last enrich",
			gaps: map[string]int{concernProbe: 4},
			runs: append(walkedRuns(walked), libraryRun{Worker: workerEnrich, Job: "movies-enrich",
				Started: walked, Finished: walked.Add(time.Minute)})},
		{name: "a scan has finished since the last enrich",
			gaps: map[string]int{concernProbe: 4},
			runs: append(walkedRuns(walked), libraryRun{Worker: workerEnrich, Job: "movies-enrich",
				Started: walked.Add(-time.Hour), Finished: walked.Add(-time.Minute)}),
			want: true},
		{name: "every concern is filled",
			gaps: map[string]int{concernProbe: 0, concernIdentity: 0}, runs: walkedRuns(walked)},
		{name: "the report carries no gaps at all", runs: walkedRuns(walked)},
		{name: "an identity gap with no provider to fill it",
			gaps: map[string]int{concernIdentity: 7}, runs: walkedRuns(walked)},
		{name: "an identity gap with a provider that serves it",
			gaps: map[string]int{concernIdentity: 7}, runs: walkedRuns(walked),
			sources: true, want: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library, providers := libraryWithProvider()
			if !one.sources {
				library.Spec.Sources, providers = nil, providerSet{}
			}
			boundHouse(cluster)
			operator := testOperator(t, cluster)
			report := &libraryReport{Gaps: one.gaps, Runs: one.runs}

			if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
				report, one.jobs, providers); err != nil {
				t.Fatal(err)
			}

			stood := cluster.heldJob("house", standingEnrichJobName("movies", one.runs)) != nil
			if stood != one.want {
				t.Errorf("the pass stood the enricher: %v, want %v", stood, one.want)
			}
		})
	}
}

// the enricher Job runs the identity concern only for a Library whose
// sources name a ready provider that serves it.
func TestEnrichJobTakesTheProviderTheSourcesName(t *testing.T) {
	cluster := newFakeCluster()
	library, providers := libraryWithProvider()
	boundHouse(cluster)
	operator := testOperator(t, cluster)
	report := &libraryReport{Gaps: map[string]int{concernIdentity: 2}, Runs: walkedRuns(testNow)}

	if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
		report, nil, providers); err != nil {
		t.Fatal(err)
	}

	job := cluster.heldJob("house", standingEnrichJobName("movies", report.Runs))
	if job == nil {
		t.Fatal("the pass stood no enricher")
	}
	if len(job.Spec.Template.Spec.InitContainers) != 3 {
		t.Errorf("initContainers = %+v, want the agent, the probe, and identity",
			job.Spec.Template.Spec.InitContainers)
	}
	if cluster.heldClaim("movies-enrich-catalog") == nil {
		t.Error("the pass stood the Job without its claim")
	}
}

// the enricher of one walk stays until its TTL, and the walk after it
// is answered by a Job of its own rather than by a create that
// collides with the finished one.
func TestEnrichNamesTheJobAfterTheWalkItAnswers(t *testing.T) {
	cluster := newFakeCluster()
	library, providers := libraryWithProvider()
	boundHouse(cluster)
	operator := testOperator(t, cluster)

	first := testNow.Add(-time.Hour)
	runs := []libraryRun{
		{Worker: workerScan, Job: "movies-scan-2", Started: testNow.Add(-time.Minute), Finished: testNow},
		{Worker: workerEnrich, Job: standingEnrichJobName("movies", walkedRuns(first)),
			Started: first, Finished: first.Add(time.Minute)},
	}
	// The enricher of the walk before this one has finished and its TTL
	// has not taken it yet.
	jobs := []Job{finishedJob(standingEnrichJobName("movies", walkedRuns(first)), "house",
		workerLabels("movies", workerEnrich), nil)}
	report := &libraryReport{Gaps: map[string]int{concernProbe: 2}, Runs: runs}

	if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
		report, jobs, providers); err != nil {
		t.Fatal(err)
	}

	if cluster.heldJob("house", standingEnrichJobName("movies", runs)) == nil {
		t.Errorf("the newer walk stood no enricher of its own, jobs = %v", cluster.heldJobs())
	}
}

// a webhook's folder runs through three Jobs and then stops.
func TestWebhookChainRunsScanEnrichRescan(t *testing.T) {
	cluster := newFakeCluster()
	library, providers := libraryWithProvider()
	boundHouse(cluster)
	operator := testOperator(t, cluster)
	report := &libraryReport{Gaps: map[string]int{concernIdentity: 1}, Runs: walkedRuns(testNow)}

	chain := newChain("/library/movies/Arrival (2016)", testNow)
	folder := "/library/movies/Arrival (2016)"
	jobs := []Job{finishedJob(chainJobName("movies", chainStageScan, chain), "house",
		workerLabels("movies", workerScan), chainMarks(chain, folder, chainStageScan))}

	// The scan of the folder has finished, so the enricher of the same
	// folder follows it.
	if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
		report, jobs, providers); err != nil {
		t.Fatal(err)
	}
	enrichJob := cluster.heldJob("house", chainJobName("movies", chainStageEnrich, chain))
	if enrichJob == nil {
		t.Fatalf("the chain stood no enricher, jobs = %v", cluster.heldJobs())
	}
	if got := containerEnvironment(enrichJob.Spec.Template.Spec.Containers[0]); got[scanPathVariable] != folder {
		t.Errorf("%s = %q, want the folder the chain carries", scanPathVariable, got[scanPathVariable])
	}
	if cluster.heldJob("house", standingEnrichJobName("movies", report.Runs)) != nil {
		t.Error("the chain stood the standing enricher as well")
	}

	// The enricher has finished, so the rescan of the same folder
	// follows it and reads what the enricher wrote.
	jobs = append(jobs, finishedJob(enrichJob.Metadata.Name, "house",
		enrichJob.Metadata.Labels, enrichJob.Metadata.Annotations))
	if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
		report, jobs, providers); err != nil {
		t.Fatal(err)
	}
	rescan := cluster.heldJob("house", chainJobName("movies", chainStageRescan, chain))
	if rescan == nil {
		t.Fatalf("the chain stood no rescan, jobs = %v", cluster.heldJobs())
	}
	if rescan.Metadata.Labels[workerLabelKey] != workerScan {
		t.Errorf("labels = %v, want the scan worker label", rescan.Metadata.Labels)
	}
	if got := containerEnvironment(rescan.Spec.Template.Spec.Containers[0]); got[scanPathVariable] != folder {
		t.Errorf("%s = %q, want the folder the chain carries", scanPathVariable, got[scanPathVariable])
	}

	// The rescan ends the chain. What follows is the standing
	// enricher of the whole Library, which the open gap earns.
	jobs = append(jobs, finishedJob(rescan.Metadata.Name, "house",
		rescan.Metadata.Labels, rescan.Metadata.Annotations))
	if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
		report, jobs, providers); err != nil {
		t.Fatal(err)
	}
	if got := chainJobsStood(cluster, chain); len(got) != 2 {
		t.Errorf("the chain stood %v, want the enrich and the rescan", got)
	}
	if cluster.heldJob("house", standingEnrichJobName("movies", report.Runs)) == nil {
		t.Error("the open gap stood no enricher for the whole Library")
	}
}

// the Jobs of one chain the cluster holds, in name order, so a test reads
// which stages a pass stood.
func chainJobsStood(cluster *fakeCluster, chain string) []string {
	stood := []string{}
	for _, job := range cluster.heldJobs() {
		if job.Metadata.Annotations[chainAnnotation] == chain {
			stood = append(stood, job.Metadata.Name)
		}
	}
	return stood
}

// the TTL takes a chain's finished stages one at a time, and the pass reads
// the stage that is left: an enrich with no scan behind it still gets its
// rescan, and a rescan alone is a chain that is over.
func TestChainReadsTheStageThatIsLeft(t *testing.T) {
	folder := "/library/movies/Arrival (2016)"
	chain := newChain(folder, testNow)
	cases := []struct {
		name   string
		stage  string
		worker string
		want   []string
	}{
		{name: "the enricher alone", stage: chainStageEnrich, worker: workerEnrich,
			want: []string{chainJobName("movies", chainStageRescan, chain)}},
		{name: "the rescan alone", stage: chainStageRescan, worker: workerScan},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library, providers := libraryWithProvider()
			boundHouse(cluster)
			operator := testOperator(t, cluster)
			report := &libraryReport{Gaps: map[string]int{concernIdentity: 1}, Runs: walkedRuns(testNow)}
			jobs := []Job{finishedJob(chainJobName("movies", one.stage, chain), "house",
				workerLabels("movies", one.worker), chainMarks(chain, folder, one.stage))}

			if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
				report, jobs, providers); err != nil {
				t.Fatal(err)
			}

			if got := chainJobsStood(cluster, chain); !slices.Equal(got, one.want) {
				t.Errorf("the chain stood %v, want %v", got, one.want)
			}
		})
	}
}

// a folder whose scan filled every gap ends the chain there, with no
// enricher and no rescan.
func TestWebhookChainStopsWhenNoGapIsOpen(t *testing.T) {
	cluster := newFakeCluster()
	library, providers := libraryWithProvider()
	boundHouse(cluster)
	operator := testOperator(t, cluster)
	report := &libraryReport{Gaps: map[string]int{concernIdentity: 0}, Runs: walkedRuns(testNow)}

	chain := newChain("/library/movies/Arrival (2016)", testNow)
	jobs := []Job{finishedJob(chainJobName("movies", chainStageScan, chain), "house",
		workerLabels("movies", workerScan), chainMarks(chain, "/library/movies/Arrival (2016)", chainStageScan))}

	if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
		report, jobs, providers); err != nil {
		t.Fatal(err)
	}

	if got := cluster.heldJobs(); len(got) != 0 {
		t.Errorf("the chain stood %+v, want nothing", got)
	}
}

// the folder scan a webhook creates opens the chain, so the pass that
// reads it knows which folder it covered and how far the chain reached.
func TestFolderScanJobOpensTheChain(t *testing.T) {
	job := buildFolderScanJob(studioMovies(), "/library/movies/Arrival (2016)", testNow,
		testScannerImage, testCorrosionImage, testBusAddress, defaultTopicBase)

	marks := job.Metadata.Annotations
	if marks[chainStageAnnotation] != chainStageScan {
		t.Errorf("stage = %q, want %q", marks[chainStageAnnotation], chainStageScan)
	}
	if marks[chainPathAnnotation] != "/library/movies/Arrival (2016)" {
		t.Errorf("path = %q, want the folder the webhook named", marks[chainPathAnnotation])
	}
	if marks[chainAnnotation] == "" || job.Metadata.Name != chainJobName("movies", chainStageScan, marks[chainAnnotation]) {
		t.Errorf("name = %q, chain = %q, want the one from the other",
			job.Metadata.Name, marks[chainAnnotation])
	}
}

// a Library with no report is one the operator schedules nothing for,
// because it has no gap counts to read.
func TestEnrichWaitsForAReport(t *testing.T) {
	cluster := newFakeCluster()
	library, providers := libraryWithProvider()
	boundHouse(cluster)
	operator := testOperator(t, cluster)

	if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
		nil, nil, providers); err != nil {
		t.Fatal(err)
	}

	if got := cluster.heldJobs(); len(got) != 0 {
		t.Errorf("the pass stood %+v, want nothing", got)
	}
}

// what the rest of these tests read: what the operator reports about
// enrichment on the Library itself.

// the report's gap counts and its two identity counts reach the
// Library, so a person reads on the resource what the operator
// schedules on.
func TestLibraryStatusCarriesTheGaps(t *testing.T) {
	report := &libraryReport{
		Gaps:       map[string]int{concernProbe: 3, concernIdentity: 1},
		Waiting:    2,
		Unresolved: 5,
	}

	status := deriveLibraryStatus(studioMovies(), libraryObservation{
		bound: binding{volume: &LibraryVolume{Name: "pv-movies"}}, report: report,
	}, testNow)

	if status.Gaps[concernProbe] != 3 || status.Gaps[concernIdentity] != 1 {
		t.Errorf("gaps = %v, want the counts the reporter published", status.Gaps)
	}
	if status.Waiting != 2 || status.Unresolved != 5 {
		t.Errorf("waiting = %d and unresolved = %d, want 2 and 5", status.Waiting, status.Unresolved)
	}
}

// the phase reads Enriching while an enrich run is in flight, the way
// it reads Scanning while a walk runs.
func TestLibraryPhaseReadsEnriching(t *testing.T) {
	cases := []struct {
		name string
		seen libraryReport
		want string
	}{
		{name: "a walk is running", seen: libraryReport{Walking: true}, want: phaseScanning},
		{name: "an enricher is running", want: phaseEnriching,
			seen: libraryReport{Runs: []libraryRun{{Worker: workerEnrich, Started: testNow}}}},
		{name: "the enricher has finished", want: phaseIdle,
			seen: libraryReport{Runs: []libraryRun{{Worker: workerEnrich,
				Started: testNow, Finished: testNow.Add(time.Minute)}}}},
		{name: "nothing is running", want: phaseIdle},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			ready := Condition{Type: conditionReady, Status: ConditionTrue, Reason: reasonReady}

			if got := libraryPhase(ready, &one.seen); got != one.want {
				t.Errorf("phase = %q, want %q", got, one.want)
			}
		})
	}
}

// the Library reports the providers its sources name, and a Library
// that names none carries no such condition.
func TestLibraryReportsItsSources(t *testing.T) {
	cases := []struct {
		name    string
		verdict sourcesVerdict
		want    ConditionStatus
	}{
		{name: "a library that names no source"},
		{name: "a source that does not exist", want: ConditionFalse,
			verdict: sourcesVerdict{reason: reasonProviderNotFound, message: "no such provider"}},
		{name: "sources that serve identity", want: ConditionTrue,
			verdict: sourcesVerdict{reason: reasonSourcesReady, message: "the sources serve it"}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			status := deriveLibraryStatus(studioMovies(), libraryObservation{
				bound: binding{volume: &LibraryVolume{Name: "pv-movies"}}, sources: one.verdict,
			}, testNow)

			got := conditionNamed(status.Conditions, conditionSources)
			if got.Status != one.want {
				t.Errorf("Sources = %q, want %q", got.Status, one.want)
			}
			if got.Reason != one.verdict.reason {
				t.Errorf("reason = %q, want %q", got.Reason, one.verdict.reason)
			}
		})
	}
}

// an API server that refuses a write is reported and never swallowed,
// whichever step of the enrichment the pass is on.
func TestEnrichReportsWhatTheAPIServerRefuses(t *testing.T) {
	chain := newChain("/library/movies/Arrival (2016)", testNow)
	scanned := []Job{finishedJob(chainJobName("movies", chainStageScan, chain), "house",
		workerLabels("movies", workerScan), chainMarks(chain, "/movies/Arrival (2016)", chainStageScan))}
	enriched := append(scanned, finishedJob(chainJobName("movies", chainStageEnrich, chain), "house",
		workerLabels("movies", workerEnrich), chainMarks(chain, "/movies/Arrival (2016)", chainStageEnrich)))

	cases := []struct {
		name   string
		broken string
		jobs   []Job
	}{
		{name: "the claim the enricher runs on", broken: claimPath("house", "movies-enrich-catalog")},
		{name: "the enricher Job", broken: jobsPath("house")},
		{name: "the rescan Job of a chain", broken: jobsPath("house"), jobs: enriched},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library, providers := libraryWithProvider()
			boundHouse(cluster)
			cluster.broken[one.broken] = http.StatusInternalServerError
			operator := testOperator(t, cluster)
			report := &libraryReport{Gaps: map[string]int{concernIdentity: 1}, Runs: walkedRuns(testNow)}

			err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
				report, one.jobs, providers)

			if err == nil {
				t.Error("the pass reported no failure")
			}
		})
	}
}

// a chain of another Library, or of another namespace, is not this
// Library's to carry on.
func TestChainsOfReadsOneLibrarysOwn(t *testing.T) {
	chain := newChain("/one", testNow)
	jobs := []Job{
		finishedJob("series-scan", "house", workerLabels("series", workerScan),
			chainMarks(chain, "/one", chainStageScan)),
		finishedJob("movies-scan", "studio", workerLabels("movies", workerScan),
			chainMarks(chain, "/one", chainStageScan)),
		finishedJob("movies-cleanup", "house", workerLabels("movies", workerCleanup), nil),
	}

	if got := chainsOf(jobs, "house", "movies"); len(got) != 0 {
		t.Errorf("chains = %+v, want none of this Library's own", got)
	}
}
