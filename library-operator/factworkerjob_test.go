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

// A trickplay worker of the movies Library in the state given, started after
// the enrich run of the library Job named.
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

// The worker starts once after an enrich run, where the Library runs the
// fact, the gap is open or a refresh has titles left to ask about, and no
// worker of the fact runs.
func TestWhenAWorkerIsDue(t *testing.T) {
	cases := []struct {
		name    string
		library *Library
		report  *libraryReport
		jobs    []Job
		taken   string
		due     bool
	}{
		{name: "a run with work", library: trickplayMovies(), report: listedReport(3), due: true},
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
		{name: "a run that started a worker in this process", library: trickplayMovies(), report: listedReport(3),
			taken: "movies-walk-1"},
		{name: "a newer run than the one taken", library: trickplayMovies(), report: listedReport(3),
			taken: "movies-walk-0", due: true},
		{name: "a run a finished worker names", library: trickplayMovies(), report: listedReport(3),
			jobs: []Job{trickplayJob(succeededStatus(testNow), "movies-walk-1")}},
		{name: "a newer run than a finished worker's", library: trickplayMovies(), report: listedReport(3),
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
			if due && listed.Job != "movies-walk-1" {
				t.Errorf("run = %+v, want the enrich run of the last library Job", listed)
			}
		})
	}
}

// One pass starts one worker after a run, and a later pass over the same
// report starts none, because the run is taken.
func TestThePassStartsOneWorkerPerRun(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	library.Spec.Trickplay.Enabled = true
	operator, logged := loggingOperator(t, cluster)

	for range 2 {
		if err := operator.runFactWorkers(t.Context(), library, testNamespaceCatalog(), listedReport(3), nil, nil,
			testNow); err != nil {
			t.Fatal(err)
		}
	}

	created := cluster.heldJobs()
	if len(created) != 1 || created[0].Metadata.Labels[workerLabelKey] != factTrickplay {
		t.Fatalf("jobs = %+v, want one trickplay worker", created)
	}
	wantOneLine(t, logged, "library house/movies: created the job "+created[0].Metadata.Name,
		"to work the trickplay gap of 3 videos after the job movies-walk-1")
}

// The enrich run of movies-walk-1, confirmed by another agent's write.
func enrichedRun() libraryRun {
	return libraryRun{Worker: workerEnrich, Job: "movies-walk-1", Started: testNow.Add(-time.Minute),
		Finished: testNow, Actor: otherAgent, Version: 40}
}

// The trickplay worker of the movies Library after enrichedRun, in the
// Catalog given and the count of pods given.
func trickplayWorkerJob(catalog *NamespaceCatalog, pods int) *Job {
	library := trickplayMovies()
	library.Spec.Trickplay.Parallelism = pods
	return buildFactWorkerJob(library, catalog, trickplayWorker,
		jobImages{operator: testScannerImage, ffmpeg: testFFmpegImage, corrosion: testCorrosionImage},
		"http://library-operator.liken-system.svc/webhook/house/movies", enrichedRun(), testNow)
}

// The Catalog of the house namespace with a class for the worker copies.
func workersOnClass(class string) *NamespaceCatalog {
	catalog := testNamespaceCatalog()
	catalog.Spec.Workers.StorageClassName = class
	return catalog
}

// The worker is a peer of the namespace's catalog cluster: the agent runs as
// a native sidecar, the pod carries the member label, and the worker
// container reads the catalog on loopback after the run it was started
// after. It writes the volume the phases write, and it carries the address
// it asks for rescans at.
func TestAWorkerIsACatalogPeer(t *testing.T) {
	job := trickplayWorkerJob(testNamespaceCatalog(), 1)
	pod := job.Spec.Template

	if pod.Metadata.Labels[memberLabelKey] != memberLabelValue {
		t.Error("the worker's pod carries no catalog member label")
	}
	if len(pod.Spec.InitContainers) != 1 || len(pod.Spec.Containers) != 1 {
		t.Fatalf("pod runs %d init containers and %d containers, want the agent and the worker",
			len(pod.Spec.InitContainers), len(pod.Spec.Containers))
	}
	agent := pod.Spec.InitContainers[0]
	if agent.Image != testCorrosionImage || agent.RestartPolicy != "Always" || agent.StartupProbe == nil {
		t.Errorf("agent = %s with restart %q, want the catalog agent as a native sidecar", agent.Image,
			agent.RestartPolicy)
	}
	phases := pod.Spec.Volumes[0].PersistentVolumeClaim
	if phases == nil || phases.ClaimName != "movies" || phases.ReadOnly {
		t.Errorf("volumes = %+v, want the storage claim first, writable", pod.Spec.Volumes)
	}
	container := pod.Spec.Containers[0]
	env := envOf(container)
	want := map[string]string{
		libraryFactVariable: factTrickplay,
		catalogAPIVariable:  defaultCatalogAPI,
		syncActorVariable:   otherAgent,
		syncVersionVariable: "40",
		syncTimeoutVariable: defaultSyncTimeout.String(),
		gapSinceVariable:    testNow.UTC().Format(time.RFC3339Nano),
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
	if *job.Spec.ActiveDeadlineSeconds != int64(factWorkerDeadline/time.Second) {
		t.Errorf("deadline = %d, want the worker's", *job.Spec.ActiveDeadlineSeconds)
	}
	if job.Metadata.Annotations[workListAnnotation] != "movies-walk-1" {
		t.Errorf("annotations = %v, want the library Job it was started after", job.Metadata.Annotations)
	}
}

// The worker reads its fact's refresh time the way a library Job's
// containers read theirs.
func TestAWorkerCarriesTheRefreshTimes(t *testing.T) {
	library := refreshedTrickplayMovies()

	pod := buildFactWorkerJob(library, testNamespaceCatalog(), trickplayWorker, jobImages{}, "", enrichedRun(),
		testNow).Spec.Template

	if got := envOf(pod.Spec.Containers[0])[libraryRefreshVariable]; got != refreshValue(library) || got == "" {
		t.Errorf("%s = %q, want %q", libraryRefreshVariable, got, refreshValue(library))
	}
}

// Where the agent keeps its copy: an emptyDir where the Catalog names no
// class, and the workers claim where it names one, in the directory of the
// Library, the fact, and the pod's index. A Job of one pod has no index, so
// its pod names the directory of index 0.
func TestWhereAWorkerKeepsItsCopy(t *testing.T) {
	cases := []struct {
		name        string
		catalog     *NamespaceCatalog
		pods        int
		claim       string
		subPath     string
		subPathExpr string
	}{
		{name: "no class", catalog: testNamespaceCatalog(), pods: 1},
		{name: "no class and three pods", catalog: testNamespaceCatalog(), pods: 3},
		{name: "a class and one pod", catalog: workersOnClass("per-node"), pods: 1, claim: "house-workers",
			subPath: "movies-trickplay-0"},
		{name: "a class and three pods", catalog: workersOnClass("per-node"), pods: 3, claim: "house-workers",
			subPathExpr: "movies-trickplay-$(JOB_COMPLETION_INDEX)"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			pod := trickplayWorkerJob(one.catalog, one.pods).Spec.Template.Spec

			copy := pod.Volumes[1]
			if copy.Name != catalogVolumeName || (one.claim == "") != (copy.EmptyDir != nil) ||
				(one.claim != "" && copy.PersistentVolumeClaim.ClaimName != one.claim) {
				t.Errorf("copy = %+v, want the claim %q or an emptyDir", copy, one.claim)
			}
			mount := pod.InitContainers[0].VolumeMounts[0]
			if mount.Name != catalogVolumeName || mount.MountPath != catalogStatePath ||
				mount.SubPath != one.subPath || mount.SubPathExpr != one.subPathExpr {
				t.Errorf("mount = %+v, want subPath %q and subPathExpr %q", mount, one.subPath, one.subPathExpr)
			}
		})
	}
}

// The agent of an Indexed Job reads its index from the annotation the Job
// controller writes on the pod, so its subPathExpr expands in the init
// container where Kubernetes runs a native sidecar. A Job of one pod has no
// index to read.
func TestTheAgentOfAnIndexedWorkerReadsItsIndex(t *testing.T) {
	cases := []struct {
		pods int
		want string
	}{
		{pods: 1},
		{pods: 3, want: completionIndexFieldPath},
	}
	for _, one := range cases {
		t.Run(counted(one.pods, "pod"), func(t *testing.T) {
			agent := trickplayWorkerJob(workersOnClass("per-node"), one.pods).Spec.Template.Spec.InitContainers[0]

			got := ""
			for _, variable := range agent.Env {
				if variable.Name == completionIndexVariable {
					got = variable.ValueFrom.FieldRef.FieldPath
				}
			}
			if got != one.want {
				t.Errorf("%s comes from %q, want %q", completionIndexVariable, got, one.want)
			}
		})
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
