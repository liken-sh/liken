package main

import (
	"testing"
	"time"
)

// When the operator starts a heavy fact's worker, and the Job it starts.

// The broker every Job a test builds names.
var testJobBus = busEndpoint{address: testBusAddress, base: defaultTopicBase}

// A movies Library with trickplay on.
func trickplayMovies() *Library {
	library := studioMovies()
	library.Spec.Trickplay.Enabled = true
	return library
}

// The enrich run of movies-walk-1, which finished at the test's clock.
func enrichedRun() libraryRun {
	return libraryRun{Worker: workerEnrich, Job: "movies-walk-1", Started: testNow.Add(-time.Minute),
		Finished: testNow}
}

// A report whose last library Job is movies-walk-1.
func listedReport() *libraryReport {
	return &libraryReport{Runs: walkedRuns(testNow)}
}

// The trickplay list movies-walk-1 published.
var trickplayList = workList{namespace: "house", library: "movies", fact: factTrickplay, run: "movies-walk-1"}

// A trickplay worker of the movies Library in the state given, on the list of
// the library Job named.
func trickplayJob(status JobStatus, listed string) Job {
	job := houseJob("movies-trickplay-1", factTrickplay, status)
	job.Metadata.Annotations = map[string]string{workListAnnotation: listed}
	return job
}

// The worker starts once on a list, where the Library runs the fact, the
// library Job that published it has finished, the list holds videos, and no
// worker of the fact runs.
func TestWhenAWorkerIsDue(t *testing.T) {
	unfinished := enrichedRun()
	unfinished.Finished = time.Time{}
	cases := []struct {
		name    string
		library *Library
		listed  libraryRun
		count   int
		jobs    []Job
		taken   string
		due     bool
	}{
		{name: "a list of videos", library: trickplayMovies(), listed: enrichedRun(), count: 3, due: true},
		{name: "the fact off", library: studioMovies(), listed: enrichedRun(), count: 3},
		{name: "no run", library: trickplayMovies(), count: 3},
		{name: "no list", library: trickplayMovies(), listed: enrichedRun()},
		{name: "a library Job that has not finished", library: trickplayMovies(), listed: unfinished, count: 3},
		{name: "a list that started a worker in this process", library: trickplayMovies(), listed: enrichedRun(),
			count: 3, taken: "movies-walk-1"},
		{name: "a newer list than the one taken", library: trickplayMovies(), listed: enrichedRun(), count: 3,
			taken: "movies-walk-0", due: true},
		{name: "a list a finished worker names", library: trickplayMovies(), listed: enrichedRun(), count: 3,
			jobs: []Job{trickplayJob(succeededStatus(testNow), "movies-walk-1")}},
		{name: "a newer list than a finished worker's", library: trickplayMovies(), listed: enrichedRun(), count: 3,
			jobs: []Job{trickplayJob(succeededStatus(testNow), "movies-walk-0")}, due: true},
		{name: "a worker that runs", library: trickplayMovies(), listed: enrichedRun(), count: 3,
			jobs: []Job{trickplayJob(JobStatus{Active: 1}, "movies-walk-0")}},
		{name: "a walk that runs", library: trickplayMovies(), listed: enrichedRun(), count: 3,
			jobs: []Job{walkJob(JobStatus{Active: 1})}, due: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			due := factWorkerDue(one.library, trickplayWorker, one.listed, one.count, one.jobs, one.taken)

			if due != one.due {
				t.Errorf("due = %v, want %v", due, one.due)
			}
		})
	}
}

// One pass starts one worker on a list the bus holds, and a later pass over
// the same report starts none, because the list is taken.
func TestThePassStartsOneWorkerPerList(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	library.Spec.Trickplay.Enabled = true
	library.Spec.Trickplay.Parallelism = 2
	operator, logged := loggingOperator(t, cluster)
	operator.workLists.fold(trickplayList, []byte("3"))

	for range 2 {
		if err := operator.runFactWorkers(t.Context(), library, listedReport(), nil, nil, testNow); err != nil {
			t.Fatal(err)
		}
	}

	created := cluster.heldJobs()
	if len(created) != 1 || created[0].Metadata.Labels[workerLabelKey] != factTrickplay {
		t.Fatalf("jobs = %+v, want one trickplay worker", created)
	}
	wantOneLine(t, logged, "library house/movies: created the job "+created[0].Metadata.Name,
		"to work the trickplay list of 3 videos, 2 at once, from the job movies-walk-1")
}

// A list the bus does not hold for the last library Job starts no worker,
// even where it holds an older one.
func TestAWorkerStartsOnlyOnTheLastJobsList(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	library.Spec.Trickplay.Enabled = true
	operator := testOperator(t, cluster)
	older := trickplayList
	older.run = "movies-walk-0"
	operator.workLists.fold(older, []byte("3"))

	if err := operator.runFactWorkers(t.Context(), library, listedReport(), nil, nil, testNow); err != nil {
		t.Fatal(err)
	}

	if created := cluster.heldJobs(); len(created) != 0 {
		t.Errorf("jobs = %+v, want none", created)
	}
}

// The trickplay worker of the movies Library on a list of the count given,
// at the parallelism given.
func trickplayWorkerJob(count, parallelism int) *Job {
	library := trickplayMovies()
	library.Spec.Trickplay.Parallelism = parallelism
	return buildFactWorkerJob(library, trickplayWorker,
		jobImages{operator: testScannerImage, ffmpeg: testFFmpegImage, corrosion: testCorrosionImage}, testJobBus,
		"http://library-operator.liken-system.svc/webhook/house/movies", enrichedRun(), count, testNow)
}

// The worker is an Indexed Job of one completion per video. Each index is
// retried on its own, and the deadline stays the whole Job's.
func TestAWorkerIsAnIndexedJobOfItsList(t *testing.T) {
	job := trickplayWorkerJob(40, 4)
	spec := job.Spec

	if spec.CompletionMode != indexedCompletion || *spec.Completions != 40 || *spec.Parallelism != 4 {
		t.Errorf("job = %s with %d completions and %d at once, want Indexed, 40 and 4",
			spec.CompletionMode, *spec.Completions, *spec.Parallelism)
	}
	if spec.BackoffLimit != nil || *spec.BackoffLimitPerIndex != scanBackoffLimit {
		t.Errorf("backoff = %v, per index %d, want none for the Job and the worker's for each index",
			spec.BackoffLimit, *spec.BackoffLimitPerIndex)
	}
	if *spec.ActiveDeadlineSeconds != int64(factWorkerDeadline/time.Second) {
		t.Errorf("deadline = %d, want the worker's for the whole Job", *spec.ActiveDeadlineSeconds)
	}
	if job.Metadata.Annotations[workListAnnotation] != "movies-walk-1" {
		t.Errorf("annotations = %v, want the library Job whose list it works", job.Metadata.Annotations)
	}
}

// A list shorter than the parallelism runs every video at once and starts no
// pod with nothing to do.
func TestAShortListRunsEveryVideoAtOnce(t *testing.T) {
	if got := *trickplayWorkerJob(2, 4).Spec.Parallelism; got != 2 {
		t.Errorf("parallelism = %d, want the 2 videos of the list", got)
	}
}

// The worker's pod holds no catalog: one container, no agent, and no member
// label. It reads its video from the bus, writes the volume the phases
// write, and carries the address it asks for rescans at.
func TestAWorkerPodReadsItsVideoFromTheBus(t *testing.T) {
	pod := trickplayWorkerJob(3, 1).Spec.Template

	if _, held := pod.Metadata.Labels[memberLabelKey]; held {
		t.Error("the worker's pod carries the catalog member label")
	}
	if len(pod.Spec.InitContainers) != 0 || len(pod.Spec.Containers) != 1 {
		t.Fatalf("pod runs %d init containers and %d containers, want the worker alone",
			len(pod.Spec.InitContainers), len(pod.Spec.Containers))
	}
	phases := pod.Spec.Volumes[0].PersistentVolumeClaim
	if len(pod.Spec.Volumes) != 1 || phases == nil || phases.ClaimName != "movies" || phases.ReadOnly {
		t.Errorf("volumes = %+v, want the storage claim alone, writable", pod.Spec.Volumes)
	}
	container := pod.Spec.Containers[0]
	env := envOf(container)
	want := map[string]string{
		libraryFactVariable: factTrickplay,
		workListVariable:    "movies-walk-1",
		gapSinceVariable:    testNow.UTC().Format(time.RFC3339Nano),
		busAddressVariable:  testBusAddress,
		topicBaseVariable:   defaultTopicBase,
	}
	for name, value := range want {
		if env[name] != value {
			t.Errorf("%s = %q, want %q", name, env[name], value)
		}
	}
	if container.Image != testFFmpegImage || container.Command[1] != workerMode || env[libraryWebhookVariable] == "" {
		t.Errorf("container = %s %v with %v, want the ffmpeg image in worker mode", container.Image,
			container.Command, env)
	}
	if container.Resources.Limits["memory"] != trickplayMemoryLimit ||
		container.Resources.Requests["cpu"] != trickplayCPURequest {
		t.Errorf("resources = %+v, want the trickplay request and limit", container.Resources)
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
