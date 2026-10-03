package main

import (
	"testing"
	"time"
)

// When the operator starts a heavy fact's worker, and the Job it starts.

// A movies Library with trickplay on.
func trickplayMovies() *Library {
	library := studioMovies()
	library.Spec.Trickplay.Enabled = true
	return library
}

// A report whose last library Job, movies-walk-1, ended with the trickplay gap
// given.
func listedReport(gap int) *libraryReport {
	return &libraryReport{Runs: walkedRuns(testNow), Gaps: map[string]int{factTrickplay: gap}}
}

// A trickplay worker of the movies Library in the state given, working the
// list of the library Job named.
func trickplayJob(status JobStatus, listed string) Job {
	job := houseJob("movies-trickplay-1", factTrickplay, status)
	job.Metadata.Annotations = map[string]string{workListAnnotation: listed}
	return job
}

// A movies Library with trickplay on, asked again from an hour before the
// test's clock.
func refreshedTrickplayMovies() *Library {
	library := trickplayMovies()
	library.Spec.Refresh = map[string]time.Time{factTrickplay: testNow.Add(-time.Hour)}
	return library
}

// A report whose gap is closed, with the fact's oldest attempt at the time
// given. The reporter counts a gap with no refresh, so a refresh shows only
// in the oldest attempt.
func answeredReport(oldest time.Time) *libraryReport {
	report := listedReport(0)
	report.OldestAttempts = map[string]time.Time{factTrickplay: oldest}
	return report
}

// The worker starts on a list once, where the Library runs the fact, the
// gap is open or a refresh has titles left to ask about, and no worker of
// the fact runs.
func TestWhenAWorkerIsDue(t *testing.T) {
	cases := []struct {
		name    string
		library *Library
		report  *libraryReport
		jobs    []Job
		taken   string
		due     bool
	}{
		{name: "a list with work", library: trickplayMovies(), report: listedReport(3), due: true},
		{name: "the fact off", library: studioMovies(), report: listedReport(3)},
		{name: "no report", library: trickplayMovies()},
		{name: "a gap of zero", library: trickplayMovies(), report: listedReport(0)},
		{name: "a refresh with titles left to ask about", library: refreshedTrickplayMovies(),
			report: answeredReport(testNow.Add(-2 * time.Hour)), due: true},
		{name: "a refresh every title has answered", library: refreshedTrickplayMovies(),
			report: answeredReport(testNow.Add(-time.Minute))},
		{name: "a library Job that has not finished", library: trickplayMovies(),
			report: &libraryReport{Runs: []libraryRun{{Worker: workerEnrich, Job: "movies-walk-1", Started: testNow}},
				Gaps: map[string]int{factTrickplay: 3}}},
		{name: "a list this process gave a worker", library: trickplayMovies(), report: listedReport(3),
			taken: "movies-walk-1"},
		{name: "a newer list than the one taken", library: trickplayMovies(), report: listedReport(3),
			taken: "movies-walk-0", due: true},
		{name: "a list a finished worker names", library: trickplayMovies(), report: listedReport(3),
			jobs: []Job{trickplayJob(succeededStatus(testNow), "movies-walk-1")}},
		{name: "a newer list than a finished worker's", library: trickplayMovies(), report: listedReport(3),
			jobs: []Job{trickplayJob(succeededStatus(testNow), "movies-walk-0")}, due: true},
		{name: "a worker that runs", library: trickplayMovies(), report: listedReport(3),
			jobs: []Job{trickplayJob(JobStatus{Active: 1}, "movies-walk-0")}},
		{name: "a walk that runs", library: trickplayMovies(), report: listedReport(3),
			jobs: []Job{walkJob(JobStatus{Active: 1})}, due: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			listed, due := factWorkerDue(one.library, trickplayWorker, one.report, one.jobs, one.taken)

			if due != one.due {
				t.Fatalf("due = %v, want %v", due, one.due)
			}
			if due && listed != "movies-walk-1" {
				t.Errorf("list = %q, want the list of the last library Job", listed)
			}
		})
	}
}

// One pass starts one worker for a list, and a later pass over the same
// report starts none, because the list is taken.
func TestThePassStartsOneWorkerPerList(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	library.Spec.Trickplay.Enabled = true
	operator, logged := loggingOperator(t, cluster)

	for range 2 {
		if err := operator.runFactWorkers(t.Context(), library, listedReport(3), nil, nil, testNow); err != nil {
			t.Fatal(err)
		}
	}

	created := cluster.heldJobs()
	if len(created) != 1 || created[0].Metadata.Labels[workerLabelKey] != factTrickplay {
		t.Fatalf("jobs = %+v, want one trickplay worker", created)
	}
	wantOneLine(t, logged, "library house/movies: created the job "+created[0].Metadata.Name,
		"to work the trickplay gap of 3 videos from the list of the job movies-walk-1")
}

// The worker holds no catalog: no agent, no catalog claim, and no member
// label, so it joins no catalog cluster. It writes the volume the phases
// write, and it carries the address it asks for rescans at.
func TestATrickplayWorkerHoldsNoCatalog(t *testing.T) {
	job := buildFactWorkerJob(trickplayMovies(), trickplayWorker,
		jobImages{operator: testScannerImage, ffmpeg: testFFmpegImage, corrosion: testCorrosionImage},
		"http://library-operator.liken-system.svc/webhook/house/movies", "movies-walk-1", testNow)
	pod := job.Spec.Template

	if _, member := pod.Metadata.Labels[memberLabelKey]; member {
		t.Error("the worker's pod carries the catalog member label")
	}
	if len(pod.Spec.InitContainers) != 0 || len(pod.Spec.Containers) != 1 {
		t.Fatalf("pod runs %d init containers and %d containers, want one container alone",
			len(pod.Spec.InitContainers), len(pod.Spec.Containers))
	}
	if len(pod.Spec.Volumes) != 1 || pod.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "movies" ||
		pod.Spec.Volumes[0].PersistentVolumeClaim.ReadOnly {
		t.Errorf("volumes = %+v, want the storage claim alone, writable", pod.Spec.Volumes)
	}
	container := pod.Spec.Containers[0]
	env := envOf(container)
	if container.Image != testFFmpegImage || container.Command[1] != workerMode ||
		env[libraryFactVariable] != factTrickplay || env[libraryWebhookVariable] == "" {
		t.Errorf("container = %s %v with %v, want the ffmpeg image in worker mode", container.Image,
			container.Command, env)
	}
	if container.Resources.Limits["memory"] != trickplayMemoryLimit ||
		container.Resources.Requests["cpu"] != trickplayCPURequest {
		t.Errorf("resources = %+v, want the trickplay request and limit", container.Resources)
	}
	if *job.Spec.ActiveDeadlineSeconds != int64(factWorkerDeadline/time.Second) {
		t.Errorf("deadline = %d, want the worker's", *job.Spec.ActiveDeadlineSeconds)
	}
	if job.Metadata.Annotations[workListAnnotation] != "movies-walk-1" {
		t.Errorf("annotations = %v, want the list it works", job.Metadata.Annotations)
	}
}

// A worker that failed holds no fault on the Library: no later run can answer
// it, and the next list starts another worker.
func TestAFailedWorkerIsNoFaultOfTheLibrary(t *testing.T) {
	failed := trickplayJob(failedStatus(testNow), "movies-walk-1")

	if fault := libraryJobFault([]Job{failed}, nil, nil, "house", "movies", testNow); fault != nil {
		t.Errorf("fault = %+v, want none", fault)
	}
}

// A worker writes no row, so it holds no departure back.
func TestARunningWorkerHoldsNoDeparture(t *testing.T) {
	running := trickplayJob(JobStatus{Active: 1}, "movies-walk-1")

	if blocker := departureBlocker([]Job{running}, nil, "house", "movies"); blocker.reason != "" {
		t.Errorf("departure = %+v, want nothing held back", blocker)
	}
}
