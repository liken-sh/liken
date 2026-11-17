package main

import (
	"encoding/json"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// When the model of the house's movies was fitted.
var placingModel = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

// The house's movies with the titles fact on and a model in the status.
func placingHouse(cluster *fakeCluster) *Library {
	library := boundHouse(cluster)
	library.Spec.Titles.Enabled = true
	library.Status.TitlesModels = []titlesModelStatus{{Kind: titlesKindEnd, Fitted: placingModel, Videos: 110}}
	return library
}

// A report that counts the readings and placements given, after a confirmed
// run that the placing's agent must hold before it reads.
func readingsReport(readings, placements int) *libraryReport {
	return &libraryReport{
		TitlesReadings: titlesReadings{Readings: readings, Placements: placements},
		Runs: []libraryRun{{Worker: workerEnrich, Job: "movies-walk-1", Started: testNow.Add(-time.Hour),
			Finished: testNow.Add(-time.Minute), Actor: "actor-1", Version: 41}},
	}
}

// A placing Job of the movies Library, in the state given.
func placingJob(status JobStatus) Job {
	return houseJob("movies-titles-place-1", titlesPlaceMode, status)
}

// The pass starts one placing on a Library whose model has placed nothing,
// and no second one while the first is listed. The Job runs the catalog
// agent on the Library's catalog claim and mounts the library read-only.
func TestThePassStartsAPlacingForANewModel(t *testing.T) {
	cluster := newFakeCluster()
	library := placingHouse(cluster)
	operator, logged := loggingOperator(t, cluster)
	operator.subtitleLanguages = []string{"en", "fr"}

	created, err := operator.runTitlesPlace(t.Context(), library, readingsReport(1297, 0), nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	again, err := operator.runTitlesPlace(t.Context(), library, readingsReport(1297, 0), []Job{*created}, testNow)
	if err != nil {
		t.Fatal(err)
	}

	if held := cluster.heldJobs(); len(held) != 1 || again != nil {
		t.Fatalf("jobs = %+v, want the one placing", held)
	}
	wantOneLine(t, logged, "library house/movies: created the job "+created.Metadata.Name,
		"to place the end titles, because no placing has run with the model fitted at 2026-11-14T12:00:00Z")
	pod := created.Spec.Template.Spec
	container := pod.Containers[0]
	if container.Image != operator.titlesImage || container.Command[1] != titlesPlaceMode {
		t.Errorf("container = %+v, want the placing on the titles image", container)
	}
	if len(pod.InitContainers) != 1 || pod.InitContainers[0].Name != catalogContainer ||
		created.Spec.Template.Metadata.Labels[memberLabelKey] != memberLabelValue {
		t.Errorf("pod = %+v, want the catalog agent, in the catalog's member set", pod)
	}
	if !container.VolumeMounts[0].ReadOnly || !pod.Volumes[0].PersistentVolumeClaim.ReadOnly ||
		pod.Volumes[2].PersistentVolumeClaim.ClaimName != scannerCatalogClaimName("movies") {
		t.Errorf("volumes = %+v mounted at %+v, want the library read-only and the catalog claim", pod.Volumes,
			container.VolumeMounts)
	}
	env := map[string]string{}
	for _, variable := range container.Env {
		env[variable.Name] = variable.Value
	}
	if env[titlesModelFittedVariable] != "2026-11-14T12:00:00Z" || env[syncActorVariable] != "actor-1" ||
		env[syncVersionVariable] != "41" || env[librarySubtitleLanguagesVariable] != "en,fr" ||
		env[busAddressVariable] != testBusAddress {
		t.Errorf("env = %v, want the model, the sync target, the languages, and the bus", env)
	}
	var started titlesPlaced
	if err := json.Unmarshal([]byte(created.Metadata.Annotations[titlesPlacingAnnotation]), &started); err != nil ||
		!started.Model.Equal(placingModel) || started.Readings != 1297 {
		t.Errorf("annotation = %q, want the model and the readings", created.Metadata.Annotations[titlesPlacingAnnotation])
	}
}

func TestWhenThePassStartsNoPlacing(t *testing.T) {
	placed := &titlesPlacing{Kind: titlesKindEnd, titlesPlaced: titlesPlaced{At: testNow, Result: titlesPlacePlaced,
		Model: placingModel, Readings: 1297, Placements: 1290}}
	cases := []struct {
		name    string
		off     bool
		noModel bool
		report  *libraryReport
		placing *titlesPlacing
		jobs    []Job
	}{
		{name: "the fact is off", off: true, report: readingsReport(1297, 0)},
		{name: "no report"},
		{name: "no model", noModel: true, report: readingsReport(1297, 0)},
		{name: "a library Job runs", report: readingsReport(1297, 0), jobs: []Job{walkJob(JobStatus{Active: 1})}},
		{name: "a fit runs", report: readingsReport(1297, 0),
			jobs: []Job{houseJob("movies-titles-fit-1", titlesFitMode, JobStatus{Active: 1})}},
		{name: "a placing runs", report: readingsReport(1297, 0), jobs: []Job{placingJob(JobStatus{Active: 1})}},
		{name: "the model placed every reading", report: readingsReport(1297, 1290), placing: placed},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := placingHouse(cluster)
			library.Spec.Titles.Enabled = !test.off
			if test.noModel {
				library.Status.TitlesModels = nil
			}
			operator, _ := loggingOperator(t, cluster)
			if test.placing != nil {
				operator.titlesPlacings.fold("house", "movies", *test.placing)
			}

			created, err := operator.runTitlesPlace(t.Context(), library, test.report, test.jobs, testNow)

			if err != nil || created != nil || len(cluster.heldJobs()) != 0 {
				t.Errorf("created %+v (%v), want no Job", cluster.heldJobs(), err)
			}
		})
	}
}

// A Library that turns the fact off stops a placing that runs.
func TestTurningTitlesOffDeletesAPlacingThatRuns(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	operator, logged := loggingOperator(t, cluster)
	running := placingJob(JobStatus{Active: 1})
	cluster.jobs["house/"+running.Metadata.Name] = &running

	if _, err := operator.runTitlesPlace(t.Context(), library, nil, []Job{running}, testNow); err != nil {
		t.Fatal(err)
	}

	if held := cluster.heldJobs(); len(held) != 0 {
		t.Errorf("jobs = %+v, want none", held)
	}
	wantOneLine(t, logged, "deleted the placing of the end titles movies-titles-place-1", "turned titles off")
}

// The placing holds the catalog claim, so a library Job waits for it, and a
// placing that succeeds answers no failure of a walk.
func TestAPlacingHoldsTheCatalogGateAndAnswersNoFailure(t *testing.T) {
	failedWalk := walkJob(JobStatus{Failed: 3, Conditions: []JobCondition{{Type: "Failed", Status: "True"}}})
	failedWalk.Metadata.Annotations = map[string]string{jobCreatedAnnotation: testNow.Add(-time.Hour).Format(time.RFC3339Nano)}
	succeeded := placingJob(JobStatus{Succeeded: 1, Conditions: []JobCondition{{Type: "Complete", Status: "True"}}})
	succeeded.Metadata.Annotations = map[string]string{jobCreatedAnnotation: testNow.Format(time.RFC3339Nano)}
	run := libraryRun{Worker: titlesPlaceMode, Job: succeeded.Metadata.Name, Started: testNow, Finished: testNow}

	if !libraryJobUnfinished([]Job{placingJob(JobStatus{Active: 1})}, "house", "movies") {
		t.Error("a placing that runs leaves the catalog gate open, want it closed")
	}
	if failed := unansweredFailure([]Job{failedWalk, succeeded}, []libraryRun{run}); failed == nil {
		t.Error("the placing answered the failed walk, want the failure to stand")
	}
}

// The status holds the last placing the broker delivered, beside the model,
// and keeps it after a broker restart.
func TestTheStatusHoldsTheLastPlacing(t *testing.T) {
	library := titlesMovies()
	library.Status.TitlesModels = []titlesModelStatus{{Kind: titlesKindEnd, Fitted: placingModel, Videos: 110}}
	first := titlesPlacing{Kind: titlesKindEnd, titlesPlaced: titlesPlaced{At: testNow, Result: titlesPlacePlaced,
		Model: placingModel, Readings: 1297, Placements: 1290, Sure: 1100, None: 7}}

	library.Status.TitlesModels = titlesModelsStatus(library, nil, &first)
	older := first
	older.At = testNow.Add(-time.Hour)
	older.Readings = 3
	kept := titlesModelsStatus(library, nil, &older)
	restarted := titlesModelsStatus(library, nil, nil)

	for _, models := range [][]titlesModelStatus{library.Status.TitlesModels, kept, restarted} {
		if len(models) != 1 || models[0].Placed != first.titlesPlaced || models[0].Videos != 110 {
			t.Errorf("models = %+v, want the model with the first placing", models)
		}
	}
}

// A placing on the bus reaches the desk, and a payload that is not a
// placing leaves the desk as it was.
func TestAPlacingOnTheBusReachesThePass(t *testing.T) {
	operator, _ := loggingOperator(t, newFakeCluster())
	placing := titlesPlacing{Kind: titlesKindEnd, Job: "movies-titles-place-1",
		titlesPlaced: titlesPlaced{At: testNow, Result: titlesPlacePlaced, Readings: 12}}
	payload, _ := json.Marshal(placing)
	topic := titlesPlaceTopic(operator.topicBase, "house", "movies")

	operator.handleBusMessage(topic, payload)
	operator.handleBusMessage(topic, []byte("not a placing"))

	if held := operator.titlesPlacings.latestFor("house", "movies"); held == nil || *held != placing {
		t.Errorf("placing = %+v, want %+v", held, placing)
	}
}

// Each placing posts one Event when the status first holds it: its counts,
// or the reason it failed.
func TestAPlacingPostsOneEvent(t *testing.T) {
	placed := titlesPlaced{At: testNow, Result: titlesPlacePlaced, Model: placingModel, Readings: 1300,
		Missing: 3, Placements: 1290, Sure: 1100, None: 7}
	failed := titlesPlaced{At: testNow, Result: titlesPlaceFailed, Model: placingModel, Readings: 1300,
		Reason: "the volume holds no model"}
	entry := func(placed titlesPlaced) []titlesModelStatus {
		return []titlesModelStatus{{Kind: titlesKindEnd, Fitted: placingModel, Placed: placed}}
	}
	cases := []struct {
		name          string
		before, after []titlesModelStatus
		want          []posted
	}{
		{name: "no placing yet", after: entry(titlesPlaced{})},
		{name: "a placing", after: entry(placed), want: []posted{{"Normal", reasonEndTitlesPlaced,
			"placed the end titles of 1297 readings with the model fitted at 2026-11-14T12:00:00Z: the catalog " +
				"holds 1290 placements, 1100 of them with a confidence of 0.6 or more, and 7 of the readings hold " +
				"no end titles"}}},
		{name: "a placing that failed", after: entry(failed), want: []posted{{"Warning", reasonEndTitlesPlacingFailed,
			"could not place the end titles of 1300 readings: the volume holds no model"}}},
		{name: "a placing the status already holds", before: entry(placed), after: entry(placed)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := newFakeCluster()
				library := boundHouse(cluster)
				library.Status.TitlesModels = test.before
				operator := testOperator(t, cluster)

				err := writeLibraryStatus(t.Context(), operator.recorder, operator.client, nil, library,
					func(*Library) LibraryStatus { return LibraryStatus{Titles: 1, TitlesModels: test.after} })

				if err != nil {
					t.Fatal(err)
				}
				assertPosted(t, postedAbout(cluster, "Library", "movies"), test.want)
			})
		})
	}
}

// The gauge holds the counts of the last placing, and none for a Library
// with no placing.
func TestThePlacingsCountsAreAGauge(t *testing.T) {
	m := newMetrics("test")
	library := titlesMovies()
	library.Status.TitlesModels = []titlesModelStatus{{Kind: titlesKindEnd, Fitted: testNow,
		Placed: titlesPlaced{At: testNow, Placements: 1290, Sure: 1100, None: 7}}}

	m.observeTitlesModels(library)

	if got := testutil.ToFloat64(m.titlesPlacements.WithLabelValues("movies", titlesKindEnd, "sure")); got != 1100 {
		t.Errorf("sure = %v, want 1100", got)
	}
	library.Status.TitlesModels[0].Placed = titlesPlaced{}
	m.observeTitlesModels(library)
	if left := testutil.CollectAndCount(m.titlesPlacements); left != 0 {
		t.Errorf("the gauge holds %d series, want none for a Library with no placing", left)
	}
}

// A report whose placements fall, or whose readings pass a step, wakes a
// pass, and one more reading inside a step does not.
func TestWhichReadingsWakeAPass(t *testing.T) {
	cases := []struct {
		name   string
		after  titlesReadings
		wakes  bool
		before titlesReadings
	}{
		{name: "one more reading", before: titlesReadings{Readings: 60}, after: titlesReadings{Readings: 61}},
		{name: "a step of readings", before: titlesReadings{Readings: 99}, after: titlesReadings{Readings: 100}, wakes: true},
		{name: "fewer placements", before: titlesReadings{Placements: 10}, after: titlesReadings{Placements: 0}, wakes: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			woke := actsOn(libraryReport{TitlesReadings: test.before}, libraryReport{TitlesReadings: test.after})

			if woke != test.wakes {
				t.Errorf("wakes = %v, want %v", woke, test.wakes)
			}
		})
	}
}

// A pass that starts a placing starts no library Job beside it, because both
// run an agent on the one catalog claim. The walk that is due waits for the
// placing to finish.
func TestAPassThatStartsAPlacingStartsNoLibraryJob(t *testing.T) {
	cluster := newFakeCluster()
	library := placingHouse(cluster)
	operator, _ := loggingOperator(t, cluster)
	operator.reports.fold("house", "movies", *readingsReport(1297, 0))

	if err := operator.reconcile(t.Context(), library, standingCatalog(), nil, nil, nil, testNow); err != nil {
		t.Fatal(err)
	}

	if held := cluster.heldJobs(); len(held) != 1 || held[0].Metadata.Labels[workerLabelKey] != titlesPlaceMode {
		t.Errorf("jobs = %+v, want the placing alone", held)
	}
}
