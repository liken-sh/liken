package main

// What these tests read: when the operator stands the trickplay Job of one
// Library, that it stands beside the enricher rather than behind it, and the
// trickplay stage a webhook's chain gains.

import (
	"testing"
	"time"
)

// A movies Library that turns the trickplay fact on.
func libraryWithTrickplay() *Library {
	library := studioMovies()
	library.Spec.Trickplay.Enabled = true
	return library
}

// The trickplay Job stands when the fact is on, the gap is open, a walk has
// finished, and no trickplay Job of the Library is still open. An enricher Job
// that runs is none of its business.
func TestTrickplaySchedulesOneJobPerLibrary(t *testing.T) {
	walked := testNow.Add(-time.Hour)
	cases := []struct {
		name    string
		off     bool
		gaps    map[string]int
		runs    []libraryRun
		jobs    []Job
		want    bool
		refresh time.Time
	}{
		{name: "the gap is open and nothing is running",
			gaps: map[string]int{factTrickplay: 4}, runs: walkedRuns(walked), want: true},
		{name: "the Library leaves the fact off", off: true,
			gaps: map[string]int{factTrickplay: 4}, runs: walkedRuns(walked)},
		{name: "every video has its tiles",
			gaps: map[string]int{factTrickplay: 0}, runs: walkedRuns(walked)},
		{name: "a refresh has work with the gap closed",
			gaps: map[string]int{factTrickplay: 0}, runs: walkedRuns(walked),
			refresh: testNow.Add(-time.Minute), want: true},
		{name: "no walk has finished", gaps: map[string]int{factTrickplay: 4}},
		{name: "a trickplay Job is still running",
			gaps: map[string]int{factTrickplay: 4}, runs: walkedRuns(walked),
			jobs: []Job{runningJob("movies-trickplay-29", "house",
				workerLabels("movies", workerTrickplay))}},
		{name: "an enricher Job is still running",
			gaps: map[string]int{factTrickplay: 4}, runs: walkedRuns(walked),
			jobs: []Job{runningJob("movies-enrich-29", "house",
				workerLabels("movies", workerEnrich))}, want: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := libraryWithTrickplay()
			library.Spec.Trickplay.Enabled = !test.off
			library.Spec.Refresh = map[string]time.Time{factTrickplay: test.refresh}
			boundHouse(cluster)
			operator := testOperator(t, cluster)
			report := &libraryReport{Gaps: test.gaps, Runs: test.runs,
				OldestAttempts: map[string]time.Time{factTrickplay: walked}}

			if err := operator.trickplay(t.Context(), library, testNamespaceCatalog(),
				report, test.jobs); err != nil {
				t.Fatal(err)
			}

			stood := cluster.heldJob("house", standingTrickplayJobName("movies", test.runs)) != nil
			if stood != test.want {
				t.Errorf("the pass stood the trickplay Job: %v, want %v", stood, test.want)
			}
		})
	}
}

// One pass stands both Jobs, each on the claim its own agent keeps.
func TestTheTrickplayJobAndTheEnricherStandTogether(t *testing.T) {
	cluster := newFakeCluster()
	library, providers := libraryWithProvider()
	library.Spec.Trickplay.Enabled = true
	boundHouse(cluster)
	operator := testOperator(t, cluster)
	report := &libraryReport{
		Gaps: map[string]int{factIdentity: 2, factTrickplay: 5},
		Runs: walkedRuns(testNow),
	}

	if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
		report, nil, providers); err != nil {
		t.Fatal(err)
	}
	if err := operator.trickplay(t.Context(), library, testNamespaceCatalog(),
		report, nil); err != nil {
		t.Fatal(err)
	}

	if cluster.heldJob("house", standingEnrichJobName("movies", report.Runs)) == nil {
		t.Errorf("the pass stood no enricher, jobs = %v", cluster.heldJobs())
	}
	if cluster.heldJob("house", standingTrickplayJobName("movies", report.Runs)) == nil {
		t.Errorf("the pass stood no trickplay Job, jobs = %v", cluster.heldJobs())
	}
	if cluster.heldClaim("movies-trickplay-catalog") == nil {
		t.Error("the pass stood the trickplay Job without its claim")
	}
}

// A webhook's folder gains a trickplay stage beside its enrich stage, and the
// rescan waits for both, because it reads what both of them wrote.
func TestTheChainStandsATrickplayStageTheRescanWaitsFor(t *testing.T) {
	cluster := newFakeCluster()
	library, providers := libraryWithProvider()
	library.Spec.Trickplay.Enabled = true
	boundHouse(cluster)
	operator := testOperator(t, cluster)
	report := &libraryReport{Gaps: map[string]int{factIdentity: 1, factTrickplay: 1},
		Runs: walkedRuns(testNow)}

	folder := "/library/movies/Arrival (2016)"
	chain := newChain(folder, testNow)
	jobs := []Job{finishedJob(chainJobName("movies", chainStageScan, chain), "house",
		workerLabels("movies", workerScan), chainMarks(chain, folder, chainStageScan))}

	if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
		report, jobs, providers); err != nil {
		t.Fatal(err)
	}
	enriched := cluster.heldJob("house", chainJobName("movies", chainStageEnrich, chain))
	tiles := cluster.heldJob("house", chainJobName("movies", chainStageTrickplay, chain))
	if enriched == nil || tiles == nil {
		t.Fatalf("the chain stood %v, want an enrich stage and a trickplay stage", cluster.heldJobs())
	}
	if got := containerEnvironment(tiles.Spec.Template.Spec.Containers[0]); got[scanPathVariable] != folder {
		t.Errorf("%s = %q, want the folder the chain carries", scanPathVariable, got[scanPathVariable])
	}

	// The enricher has finished and the decode has not, so the rescan waits.
	jobs = append(jobs,
		finishedJob(enriched.Metadata.Name, "house", enriched.Metadata.Labels, enriched.Metadata.Annotations),
		Job{Metadata: tiles.Metadata, Status: JobStatus{Active: 1}})
	if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
		report, jobs, providers); err != nil {
		t.Fatal(err)
	}
	if cluster.heldJob("house", chainJobName("movies", chainStageRescan, chain)) != nil {
		t.Fatal("the rescan ran while the trickplay stage was still open")
	}

	// Both stages have finished, so the rescan reads what they wrote.
	jobs[len(jobs)-1] = finishedJob(tiles.Metadata.Name, "house",
		tiles.Metadata.Labels, tiles.Metadata.Annotations)
	if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
		report, jobs, providers); err != nil {
		t.Fatal(err)
	}
	if cluster.heldJob("house", chainJobName("movies", chainStageRescan, chain)) == nil {
		t.Errorf("the chain stood no rescan, jobs = %v", cluster.heldJobs())
	}
}

// A Library that leaves the fact off gains no trickplay stage, and its chain
// runs the three stages it always ran.
func TestAChainWithTheFactOffStandsNoTrickplayStage(t *testing.T) {
	cluster := newFakeCluster()
	library, providers := libraryWithProvider()
	boundHouse(cluster)
	operator := testOperator(t, cluster)
	report := &libraryReport{Gaps: map[string]int{factIdentity: 1}, Runs: walkedRuns(testNow)}

	folder := "/library/movies/Arrival (2016)"
	chain := newChain(folder, testNow)
	jobs := []Job{finishedJob(chainJobName("movies", chainStageScan, chain), "house",
		workerLabels("movies", workerScan), chainMarks(chain, folder, chainStageScan))}

	if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
		report, jobs, providers); err != nil {
		t.Fatal(err)
	}

	if cluster.heldJob("house", chainJobName("movies", chainStageTrickplay, chain)) != nil {
		t.Error("the chain stood a trickplay stage for a Library that leaves the fact off")
	}
}
