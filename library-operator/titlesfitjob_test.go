package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// A movies Library with the titles fact on.
func titlesMovies() *Library {
	library := studioMovies()
	library.Spec.Titles.Enabled = true
	return library
}

// A report that counts the marked videos given.
func markedReport(videos, groups int) *libraryReport {
	return &libraryReport{TitlesMarked: titlesMarked{Videos: videos, Groups: groups}}
}

// A fit of the movies Library that left a model, on the count given.
func fittedOn(marked int, at time.Time) titlesFit {
	return titlesFit{Kind: titlesKindEnd, Result: titlesFitFitted, Job: "movies-titles-fit-1", At: at,
		Marked: titlesMarked{Videos: marked, Groups: 40}, Videos: marked - 10, LeftOut: 10,
		HeldOut: marked, Within3: 0.469, Within10: 0.722, Within30: 0.849, Median: 3.5}
}

func fitOf(fit titlesFit) *titlesFit { return &fit }

// A fit Job of the movies Library, started on the count given.
func titlesFitJobOn(marked string, status JobStatus) Job {
	job := houseJob("movies-titles-fit-1", titlesFitMode, status)
	job.Metadata.Annotations = map[string]string{titlesMarkedAnnotation: marked}
	return job
}

// The pass starts one fit when the count passes the floors, and no second
// fit while the Job of the first is listed.
func TestThePassStartsAFitWhenTheCountIsDue(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	library.Spec.Titles.Enabled = true
	operator, logged := loggingOperator(t, cluster)

	if err := operator.runTitlesFit(t.Context(), library, markedReport(120, 35), nil, testNow); err != nil {
		t.Fatal(err)
	}
	created := cluster.heldJobs()
	if len(created) != 1 || created[0].Metadata.Labels[workerLabelKey] != titlesFitMode {
		t.Fatalf("jobs = %+v, want one titles fit", created)
	}
	if err := operator.runTitlesFit(t.Context(), library, markedReport(121, 35), created, testNow); err != nil {
		t.Fatal(err)
	}

	if held := cluster.heldJobs(); len(held) != 1 {
		t.Errorf("jobs = %d, want the one fit", len(held))
	}
	wantOneLine(t, logged, "library house/movies: created the job "+created[0].Metadata.Name,
		"to fit the titles model, because 120 videos of 35 films and series have a reading")
	job := created[0]
	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != operator.titlesImage || container.Command[1] != titlesFitMode ||
		job.Metadata.Annotations[titlesMarkedAnnotation] != "120" {
		t.Errorf("job = %+v, want the fit on the titles image, annotated with the count", job)
	}
	if container.Resources.Limits["memory"] != titlesFitMemoryLimit {
		t.Errorf("memory limit = %q, want %s", container.Resources.Limits["memory"], titlesFitMemoryLimit)
	}
	volumes := job.Spec.Template.Spec.Volumes
	if len(volumes) != 2 || volumes[1].EmptyDir == nil || len(container.VolumeMounts) != 2 ||
		container.VolumeMounts[1].MountPath != titlesFitScratch {
		t.Errorf("volumes = %+v mounted at %+v, want the scratch emptyDir at %s", volumes,
			container.VolumeMounts, titlesFitScratch)
	}
}

func TestWhenThePassStartsNoFit(t *testing.T) {
	cases := []struct {
		name   string
		off    bool
		report *libraryReport
		fit    *titlesFit
		status []titlesModelStatus
		jobs   []Job
	}{
		{name: "the fact is off", off: true, report: markedReport(500, 90)},
		{name: "no report", report: nil},
		{name: "too few videos", report: markedReport(99, 60)},
		{name: "a fit runs", report: markedReport(500, 90), jobs: []Job{titlesFitJobOn("120", JobStatus{Active: 1})}},
		{name: "the broker's fit had nearly as many", report: markedReport(170, 60),
			fit: fitOf(fittedOn(120, testNow))},
		{name: "the status's fit had nearly as many", report: markedReport(170, 60),
			status: []titlesModelStatus{{Kind: titlesKindEnd, MarkedVideos: 120}}},
		{name: "a finished fit that never reported had nearly as many", report: markedReport(170, 60),
			jobs: []Job{titlesFitJobOn("120", JobStatus{Failed: 3,
				Conditions: []JobCondition{{Type: "Failed", Status: "True"}}})}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			library.Spec.Titles.Enabled = !test.off
			library.Status.TitlesModels = test.status
			operator, _ := loggingOperator(t, cluster)
			if test.fit != nil {
				operator.titlesFits.fold("house", "movies", *test.fit)
			}

			if err := operator.runTitlesFit(t.Context(), library, test.report, test.jobs, testNow); err != nil {
				t.Fatal(err)
			}

			if created := cluster.heldJobs(); len(created) != 0 {
				t.Errorf("jobs = %+v, want none", created)
			}
		})
	}
}

// A Library that holds half as many marked videos again as its last fit
// fits again.
func TestALibraryThatGrewFitsAgain(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	library.Spec.Titles.Enabled = true
	operator, logged := loggingOperator(t, cluster)
	operator.titlesFits.fold("house", "movies", fittedOn(120, testNow))

	if err := operator.runTitlesFit(t.Context(), library, markedReport(180, 60), nil, testNow); err != nil {
		t.Fatal(err)
	}

	if created := cluster.heldJobs(); len(created) != 1 {
		t.Fatalf("jobs = %+v, want one fit", created)
	}
	wantOneLine(t, logged, "180 videos of 60 films and series", "and the last fit had 120")
}

// A Library that turns the fact off stops a fit that runs.
func TestTurningTitlesOffDeletesAFitThatRuns(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	operator, logged := loggingOperator(t, cluster)
	running := titlesFitJobOn("120", JobStatus{Active: 1})
	cluster.jobs["house/"+running.Metadata.Name] = &running
	finished := houseJob("movies-titles-fit-0", titlesFitMode, JobStatus{Succeeded: 1,
		Conditions: []JobCondition{{Type: "Complete", Status: "True"}}})
	cluster.jobs["house/"+finished.Metadata.Name] = &finished

	if err := operator.runTitlesFit(t.Context(), library, nil, []Job{running, finished}, testNow); err != nil {
		t.Fatal(err)
	}

	if held := cluster.heldJobs(); len(held) != 1 || held[0].Metadata.Name != "movies-titles-fit-0" {
		t.Errorf("jobs = %+v, want the finished fit alone", held)
	}
	wantOneLine(t, logged, "deleted the titles fit movies-titles-fit-1", "turned titles off")
}

// The status holds the model of the last fitted result, and a later fit
// that left no model keeps that model's numbers and records itself.
func TestTheStatusKeepsTheModelThroughAFitThatLeftNone(t *testing.T) {
	library := titlesMovies()
	fitted := fittedOn(120, testNow)

	library.Status.TitlesModels = titlesModelsStatus(library, &fitted)
	rejected := titlesFit{Kind: titlesKindEnd, Result: titlesFitRejected, Reason: "under the floor",
		At: testNow.Add(time.Hour), Marked: titlesMarked{Videos: 190, Groups: 60}}
	after := titlesModelsStatus(library, &rejected)

	want := titlesModelStatus{Kind: titlesKindEnd, Fitted: testNow, Videos: 110, LeftOut: 10, HeldOut: 120,
		Within3: 47, Within10: 72, Within30: 85, LastFit: testNow.Add(time.Hour), LastResult: titlesFitRejected,
		LastReason: "under the floor", MarkedVideos: 190, MarkedGroups: 60}
	if len(after) != 1 || after[0] != want {
		t.Errorf("status = %+v, want %+v", after, want)
	}
}

func TestWhatTheStatusHoldsOfTheModel(t *testing.T) {
	held := []titlesModelStatus{{Kind: titlesKindEnd, Fitted: testNow, LastFit: testNow, Videos: 110}}
	cases := []struct {
		name string
		off  bool
		held []titlesModelStatus
		fit  *titlesFit
		want int
	}{
		{name: "the fact is off", off: true, held: held, fit: fitOf(fittedOn(120, testNow.Add(time.Hour)))},
		{name: "no fit and no status"},
		{name: "the status alone, after a broker restart", held: held, want: 110},
		{name: "a result the status already holds", held: held, fit: fitOf(fittedOn(500, testNow)), want: 110},
		{name: "a later result", held: held, fit: fitOf(fittedOn(500, testNow.Add(time.Hour))), want: 490},
		{name: "a result of another kind", fit: &titlesFit{Kind: "main-titles", Result: titlesFitFitted, At: testNow}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			library := titlesMovies()
			library.Spec.Titles.Enabled = !test.off
			library.Status.TitlesModels = test.held

			models := titlesModelsStatus(library, test.fit)

			if got := endTitlesModel(models); (got == nil) != (test.want == 0) || (got != nil && got.Videos != test.want) {
				t.Errorf("models = %+v, want a model of %d videos", models, test.want)
			}
		})
	}
}

func TestTheEndTitlesModelCondition(t *testing.T) {
	fitted := titlesModelStatus{Kind: titlesKindEnd, Fitted: testNow, Videos: 110, HeldOut: 120, Within10: 72, Within30: 85}
	cases := []struct {
		name    string
		off     bool
		models  []titlesModelStatus
		fitting bool
		status  ConditionStatus
		reason  string
		message string
	}{
		{name: "the fact is off", off: true},
		{name: "a model", models: []titlesModelStatus{fitted}, status: ConditionTrue, reason: reasonModelFitted,
			message: "fitted on 110 videos, and it placed 72% of the 120 videos"},
		{name: "a model while another fit runs", models: []titlesModelStatus{fitted}, fitting: true,
			status: ConditionTrue, reason: reasonModelFitted, message: "fitted on 110 videos"},
		{name: "the first fit runs", fitting: true, status: ConditionFalse, reason: reasonModelFitting,
			message: "the first fit"},
		{name: "the first fit was rejected", status: ConditionFalse, reason: reasonModelRejected, message: "the floor",
			models: []titlesModelStatus{{Kind: titlesKindEnd, LastResult: titlesFitRejected, LastReason: "under the floor"}}},
		{name: "the first fit failed", status: ConditionFalse, reason: reasonModelFitFailed, message: "too few",
			models: []titlesModelStatus{{Kind: titlesKindEnd, LastResult: titlesFitFailed, LastReason: "too few videos"}}},
		{name: "too few marks", status: ConditionFalse, reason: reasonWaitingForMarks,
			message: "40 videos of 12 films and series have a reading and a provider's mark, and the first fit starts at 100 videos of 30"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			library := titlesMovies()
			library.Spec.Titles.Enabled = !test.off

			condition, held := endTitlesModelCondition(library, test.models, titlesMarked{Videos: 40, Groups: 12},
				test.fitting, 7)

			if held == test.off {
				t.Fatalf("held = %v, want a condition only where the fact is on", held)
			}
			if !held {
				return
			}
			if condition.Type != conditionEndTitlesModel || condition.Status != test.status ||
				condition.Reason != test.reason || !strings.Contains(condition.Message, test.message) ||
				condition.ObservedGeneration != 7 {
				t.Errorf("condition = %+v, want %s %s with %q", condition, test.status, test.reason, test.message)
			}
		})
	}
}

// The Library's status carries the model and its condition from a pass, and
// loses both when the fact is turned off.
func TestTheLibraryStatusHoldsTheModelAndItsCondition(t *testing.T) {
	library := titlesMovies()
	fit := fittedOn(120, testNow)
	seen := libraryObservation{titlesFit: &fit, report: markedReport(120, 40)}

	status := deriveLibraryStatus(library, seen, testNow)

	condition, held := conditions.Find(status.Conditions, conditionEndTitlesModel)
	if len(status.TitlesModels) != 1 || !held || condition.Status != ConditionTrue {
		t.Fatalf("status = %+v, want the model and a true EndTitlesModel", status)
	}
	library.Status = status
	library.Spec.Titles.Enabled = false
	off := deriveLibraryStatus(library, seen, testNow)
	if _, held := conditions.Find(off.Conditions, conditionEndTitlesModel); len(off.TitlesModels) != 0 || held {
		t.Errorf("status = %+v, want no model and no EndTitlesModel", off)
	}
}

// A fit on the bus reaches the desk, and wakes the loop once.
func TestAFitOnTheBusReachesThePass(t *testing.T) {
	cluster := newFakeCluster()
	operator, logged := loggingOperator(t, cluster)
	fit := fittedOn(120, testNow)
	payload, _ := json.Marshal(fit)
	topic := titlesFitTopic(operator.topicBase, "house", "movies")

	operator.handleBusMessage(topic, payload)
	operator.handleBusMessage(topic, payload)
	operator.handleBusMessage(topic, []byte("not a fit"))
	operator.handleBusMessage(topic, nil)

	held := operator.titlesFits.latestFor("house", "movies")
	if held == nil || *held != fit {
		t.Errorf("fit = %+v, want %+v", held, fit)
	}
	if operator.titlesFits.latestFor("house", "series") != nil {
		t.Error("the desk holds a fit of a Library that published none")
	}
	if strings.Contains(logged.String(), "fit") {
		t.Errorf("log = %q, want no line for a fit on the bus", logged)
	}
}

// The gauges hold the score of a Library's model, and none for a Library
// with no model.
func TestTheModelsScoreIsAGauge(t *testing.T) {
	m := newMetrics("test")
	library := titlesMovies()
	library.Status.TitlesModels = []titlesModelStatus{{Kind: titlesKindEnd, Fitted: testNow, Videos: 110,
		Within3: 47, Within10: 72, Within30: 85}}

	m.observeTitlesModels(library)

	if got := testutil.ToFloat64(m.titlesModel.WithLabelValues("movies", titlesKindEnd, "10")); got != 0.72 {
		t.Errorf("within 10 seconds = %v, want 0.72", got)
	}
	if got := testutil.ToFloat64(m.titlesModelVideos.WithLabelValues("movies", titlesKindEnd)); got != 110 {
		t.Errorf("videos = %v, want 110", got)
	}
	library.Status.TitlesModels[0].Fitted = time.Time{}
	m.observeTitlesModels(library)
	if left := testutil.CollectAndCount(m.titlesModel); left != 0 {
		t.Errorf("the gauge holds %d series, want none for a Library with no model", left)
	}
	var none *metrics
	none.observeTitlesModels(library)
}
