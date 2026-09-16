package main

// What these tests read: when the operator stands the trailers Job, the Job
// and the claim it stands, and that it stands beside the enricher.

import (
	"encoding/json"
	"testing"
	"time"
)

// A movies Library that turns the trailerfile fact on, with the source that
// names its trailers.
func libraryWithTrailers() (*Library, providerSet) {
	library, providers := libraryWithProvider()
	library.Spec.Trailers.Enabled = true
	return library, providers
}

// The trailers Job stands when the fact is on, the gap is open, a walk has
// finished, and no trailers Job of the Library is still open.
func TestTrailersSchedulesOneJobPerLibrary(t *testing.T) {
	walked := testNow.Add(-time.Hour)
	cases := []struct {
		name string
		off  bool
		gaps map[string]int
		runs []libraryRun
		jobs []Job
		want bool
	}{
		{name: "the gap is open and nothing is running",
			gaps: map[string]int{factTrailerFile: 4}, runs: walkedRuns(walked), want: true},
		{name: "the Library leaves the fact off", off: true,
			gaps: map[string]int{factTrailerFile: 4}, runs: walkedRuns(walked)},
		{name: "every title has its trailer",
			gaps: map[string]int{factTrailerFile: 0}, runs: walkedRuns(walked)},
		{name: "no walk has finished", gaps: map[string]int{factTrailerFile: 4}},
		{name: "a trailers Job is still running",
			gaps: map[string]int{factTrailerFile: 4}, runs: walkedRuns(walked),
			jobs: []Job{runningJob("movies-trailers-29", "house",
				workerLabels("movies", workerTrailers))}},
		{name: "an enricher Job is still running",
			gaps: map[string]int{factTrailerFile: 4}, runs: walkedRuns(walked),
			jobs: []Job{runningJob("movies-enrich-29", "house",
				workerLabels("movies", workerEnrich))}, want: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library, providers := libraryWithTrailers()
			library.Spec.Trailers.Enabled = !test.off
			boundHouse(cluster)
			operator := testOperator(t, cluster)
			report := &libraryReport{Gaps: test.gaps, Runs: test.runs}

			if err := operator.trailers(t.Context(), library, testNamespaceCatalog(),
				report, test.jobs, providers); err != nil {
				t.Fatal(err)
			}

			stood := cluster.heldJob("house", standingTrailersJobName("movies", test.runs)) != nil
			if stood != test.want {
				t.Errorf("the pass stood the trailers Job: %v, want %v", stood, test.want)
			}
		})
	}
}

// One pass stands the enricher and the trailers Job, each on the claim its
// own agent keeps.
func TestTheTrailersJobAndTheEnricherStandTogether(t *testing.T) {
	cluster := newFakeCluster()
	library, providers := libraryWithTrailers()
	boundHouse(cluster)
	operator := testOperator(t, cluster)
	report := &libraryReport{
		Gaps: map[string]int{factIdentity: 2, factTrailerFile: 5},
		Runs: walkedRuns(testNow),
	}

	if err := operator.enrich(t.Context(), library, testNamespaceCatalog(),
		report, nil, providers, testNow); err != nil {
		t.Fatal(err)
	}
	if err := operator.trailers(t.Context(), library, testNamespaceCatalog(),
		report, nil, providers); err != nil {
		t.Fatal(err)
	}

	if cluster.heldJob("house", standingEnrichJobName("movies", testNow)) == nil {
		t.Errorf("the pass stood no enricher, jobs = %v", cluster.heldJobs())
	}
	if cluster.heldJob("house", standingTrailersJobName("movies", report.Runs)) == nil {
		t.Errorf("the pass stood no trailers Job, jobs = %v", cluster.heldJobs())
	}
	if cluster.heldClaim("movies-trailers-catalog") == nil {
		t.Error("the pass stood the trailers Job without its claim")
	}
}

// The trailerfile gap is the trailers Job's own, so an enricher never counts
// it.
func TestTheTrailerFileGapStandsNoEnricher(t *testing.T) {
	library, providers := libraryWithTrailers()
	report := &libraryReport{Gaps: map[string]int{factTrailerFile: 9},
		Runs: walkedRuns(testNow)}

	if gapOpen(library, report, providers) {
		t.Error("the trailerfile gap stood an enricher, want the trailers Job alone")
	}
}

// The enricher Job never runs this fact, because the trailers Job holds it.
func TestTheEnricherNeverRunsTheTrailerFileFact(t *testing.T) {
	library, providers := libraryWithTrailers()

	job := buildEnrichJob(library, providers, nil, enrichJobName("movies"), "",
		testScannerImage, testFFmpegImage, testCorrosionImage)

	for _, container := range job.Spec.Template.Spec.InitContainers {
		if containerEnvironment(container)[libraryFactsVariable] == factTrailerFile {
			t.Errorf("the enricher runs %s in %s, want the trailers Job alone",
				factTrailerFile, container.Name)
		}
	}
}

// The trailers block the API server admits is the block the operator reads.
func TestTheOperatorReadsATrailersBlockTheSchemaAdmits(t *testing.T) {
	trailers := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties",
		"spec", "properties", "trailers").(map[string]any)

	enabled := trailers["properties"].(map[string]any)["enabled"].(map[string]any)
	if enabled["type"] != "boolean" || enabled["default"] != false {
		t.Errorf("enabled reads %+v, want a boolean that is off by default", enabled)
	}
	spec := LibrarySpec{}
	if err := json.Unmarshal([]byte(`{"trailers":{"enabled":true}}`), &spec); err != nil {
		t.Fatal(err)
	}
	if !spec.Trailers.Enabled {
		t.Error("the operator read no trailers block, want the one the schema admits")
	}
}
