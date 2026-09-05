package main

// what these tests read: the schedule one Library's full walk runs on,
// the Job one webhook path becomes, and the rule that holds a folder
// scan while a walk is running.

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// the schedule the operator would stand for one Library.
func testScanCronJob(library *Library) *CronJob {
	return buildScanCronJob(library, testScannerImage, testCorrosionImage, testBusAddress, defaultTopicBase)
}

// the schedule is named for the Library, owned by it, and carries the
// marks one list of this operator's workers selects on.
func TestScanCronJobBelongsToItsLibrary(t *testing.T) {
	cronJob := testScanCronJob(studioMovies())

	if cronJob.Metadata.Name != "movies-scan" || cronJob.Metadata.Namespace != "house" {
		t.Errorf("metadata = %+v, want the Library's own schedule", cronJob.Metadata)
	}
	if len(cronJob.Metadata.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences = %+v, want the Library", cronJob.Metadata.OwnerReferences)
	}
	owner := cronJob.Metadata.OwnerReferences[0]
	if owner.Kind != "Library" || owner.Name != "movies" || !owner.Controller {
		t.Errorf("owner = %+v, want the controlling Library", owner)
	}
	if cronJob.Metadata.Labels[workerLabelKey] != workerScan {
		t.Errorf("labels = %v, want the scan worker label", cronJob.Metadata.Labels)
	}
}

// a walk that runs past its next turn skips that turn, because the
// claim admits one writer, and the Job it creates runs to completion
// and is cleaned up after an hour.
func TestScanCronJobRunsOneWalkAtATime(t *testing.T) {
	cronJob := testScanCronJob(studioMovies())

	if cronJob.Spec.ConcurrencyPolicy != forbidConcurrency {
		t.Errorf("concurrencyPolicy = %q, want %s", cronJob.Spec.ConcurrencyPolicy, forbidConcurrency)
	}
	if cronJob.Spec.SuccessfulJobsHistoryLimit == nil || *cronJob.Spec.SuccessfulJobsHistoryLimit != 1 {
		t.Errorf("successfulJobsHistoryLimit = %+v, want 1", cronJob.Spec.SuccessfulJobsHistoryLimit)
	}
	if cronJob.Spec.FailedJobsHistoryLimit == nil || *cronJob.Spec.FailedJobsHistoryLimit != 3 {
		t.Errorf("failedJobsHistoryLimit = %+v, want 3", cronJob.Spec.FailedJobsHistoryLimit)
	}
	spec := cronJob.Spec.JobTemplate.Spec
	if spec.BackoffLimit == nil || *spec.BackoffLimit != scanBackoffLimit {
		t.Errorf("backoffLimit = %+v, want %d", spec.BackoffLimit, scanBackoffLimit)
	}
	if spec.TTLSecondsAfterFinished == nil || *spec.TTLSecondsAfterFinished != scanJobTTL {
		t.Errorf("ttlSecondsAfterFinished = %+v, want %d", spec.TTLSecondsAfterFinished, scanJobTTL)
	}
}

// the schedule is the Library's own, and once an hour when it names
// none.
func TestScanCronJobTakesTheLibrarysSchedule(t *testing.T) {
	cases := []struct {
		name string
		spec string
		want string
	}{
		{name: "no schedule of its own", want: defaultScanSchedule},
		{name: "a schedule of its own", spec: "*/5 * * * *", want: "*/5 * * * *"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library := studioMovies()
			library.Spec.Scan.Schedule = one.spec

			if got := testScanCronJob(library).Spec.Schedule; got != one.want {
				t.Errorf("schedule = %q, want %q", got, one.want)
			}
		})
	}
}

// a folder scan is a Job of its own, named from the path and the time,
// carrying the path the scanner rescans.
func TestFolderScanJobCarriesItsPath(t *testing.T) {
	path := "/library/movies/Arrival (2016)"

	job := buildFolderScanJob(studioMovies(), path, testNow,
		testScannerImage, testCorrosionImage, testBusAddress, defaultTopicBase)

	if !strings.HasPrefix(job.Metadata.Name, "movies-scan-") {
		t.Errorf("name = %q, want a name under the Library's scan prefix", job.Metadata.Name)
	}
	got := containerEnvironment(job.Spec.Template.Spec.Containers[0])
	if got[scanPathVariable] != path {
		t.Errorf("%s = %q, want %q", scanPathVariable, got[scanPathVariable], path)
	}
	if job.Metadata.Labels[workerLabelKey] != workerScan {
		t.Errorf("labels = %v, want the scan worker label", job.Metadata.Labels)
	}
}

// the chain is a hash of the path and the time, so two webhooks for one
// folder are two chains and no create collides with a Job that runs.
func TestFolderScanChainsDifferByPathAndTime(t *testing.T) {
	later := testNow.Add(time.Second)

	cases := []struct{ name, first, second string }{
		{name: "another path",
			first:  newChain("/one", testNow),
			second: newChain("/two", testNow)},
		{name: "another time",
			first:  newChain("/one", testNow),
			second: newChain("/one", later)},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if one.first == one.second {
				t.Errorf("both runs are named %q", one.first)
			}
		})
	}
	if newChain("/one", testNow) != newChain("/one", testNow) {
		t.Error("one path at one time is named two ways")
	}
}

// the schedule is created when there is none, and rewritten when the
// pass builds a different one, which is how a changed schedule reaches
// the cluster.
func TestStandScanCronJobCreatesThenUpdatesOnDivergence(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	operator := testOperator(t, cluster)

	if _, err := operator.standScanCronJob(t.Context(), library); err != nil {
		t.Fatal(err)
	}
	if cluster.heldCronJob("house", "movies-scan") == nil {
		t.Fatal("the pass stood no schedule")
	}
	if _, err := operator.standScanCronJob(t.Context(), library); err != nil {
		t.Fatal(err)
	}
	if got := cluster.countRequests(http.MethodPut, "cronjobs"); got != 0 {
		t.Errorf("updates = %d, want none over an unchanged schedule", got)
	}

	library.Spec.Scan.Schedule = "*/15 * * * *"
	if _, err := operator.standScanCronJob(t.Context(), library); err != nil {
		t.Fatal(err)
	}

	if got := cluster.heldCronJob("house", "movies-scan").Spec.Schedule; got != "*/15 * * * *" {
		t.Errorf("schedule = %q, want the one the Library now names", got)
	}
}

// a create another writer got to first is success, and so is an update
// another writer raced: the next pass reads what they wrote.
func TestStandScanCronJobAcceptsAConflict(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	cluster.refuseCreate = true

	cronJob, err := testOperator(t, cluster).standScanCronJob(t.Context(), library)

	if err != nil {
		t.Fatalf("err = %v, want a conflict to read as success", err)
	}
	if cronJob != nil {
		t.Errorf("cronJob = %+v, want none until the next pass reads it", cronJob)
	}
}

// a held path becomes a Job on the next pass, and it is released once
// that Job exists, so one webhook makes one Job.
func TestServeHeldPathsCreatesOneJobPerPath(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	operator := testOperator(t, cluster)
	operator.paths.hold("house", "movies", "/library/movies/Arrival (2016)")

	if err := operator.serveHeldPaths(t.Context(), library, nil, testNow); err != nil {
		t.Fatal(err)
	}

	jobs := cluster.heldJobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v, want the one folder scan", jobs)
	}
	if len(operator.paths.held("house", "movies")) != 0 {
		t.Error("the path is still held after its job was created")
	}

	if err := operator.serveHeldPaths(t.Context(), library, nil, testNow); err != nil {
		t.Fatal(err)
	}
	if got := len(cluster.heldJobs()); got != 1 {
		t.Errorf("jobs = %d, want no second job for a path already served", got)
	}
}

// a folder scan waits out a full walk, because the walk covers the same
// folder and the claim admits one writer. A folder scan running beside
// it does not hold the next one.
func TestServeHeldPathsWaitsForAFullWalk(t *testing.T) {
	cases := []struct {
		name   string
		jobs   []Job
		served bool
	}{
		{name: "a full walk is running", jobs: []Job{walkJob(JobStatus{Active: 1})}},
		{name: "the walk has finished", jobs: []Job{walkJob(JobStatus{Succeeded: 1})}, served: true},
		{
			name:   "another folder scan is running",
			jobs:   []Job{houseJob("movies-scan-abc", workerScan, JobStatus{Active: 1})},
			served: true,
		},
		{
			name:   "a cleanup is running",
			jobs:   []Job{houseJob("movies-cleanup", workerCleanup, JobStatus{Active: 1})},
			served: true,
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			operator := testOperator(t, cluster)
			operator.paths.hold("house", "movies", "/library/movies/Arrival (2016)")

			if err := operator.serveHeldPaths(t.Context(), library, one.jobs, testNow); err != nil {
				t.Fatal(err)
			}

			if served := len(cluster.heldJobs()) == 1; served != one.served {
				t.Errorf("a job was created: %v, want %v", served, one.served)
			}
			if held := len(operator.paths.held("house", "movies")) == 1; held == one.served {
				t.Errorf("the path is still held: %v, want %v", held, !one.served)
			}
		})
	}
}

// A walk of another Library, and a walk in another namespace, hold nothing
// here. The departure reads the Job and not its pods, so a Job between the
// pods of its backoff counts as open.
func TestScanUnfinishedReadsOneLibraryOfOneNamespace(t *testing.T) {
	otherLibrary := houseJob("shows-scan-1", workerScan, JobStatus{Active: 1})
	otherLibrary.Metadata.Labels[libraryLabelKey] = "shows"
	otherNamespace := houseJob("movies-scan-1", workerScan, JobStatus{Active: 1})
	otherNamespace.Metadata.Namespace = "studio"

	cases := []struct {
		name    string
		jobs    []Job
		running bool
	}{
		{name: "another library", jobs: []Job{otherLibrary}},
		{name: "another namespace", jobs: []Job{otherNamespace}},
		{name: "this library", jobs: []Job{walkJob(JobStatus{Active: 1})}, running: true},
		{name: "this library between the pods of its backoff",
			jobs: []Job{walkJob(JobStatus{Active: 0, Failed: 1})}, running: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := scanUnfinished(one.jobs, "house", "movies"); got != one.running {
				t.Errorf("scanUnfinished = %v, want %v", got, one.running)
			}
		})
	}
}

// A scan is unfinished until the Job controller marks it Complete or
// Failed with status True. The counts a Job carries between its pods
// say nothing here, and a condition of any other type or status says
// nothing either.
func TestScanUnfinishedReadsTheControllersVerdict(t *testing.T) {
	cases := []struct {
		name       string
		status     JobStatus
		unfinished bool
	}{
		{name: "a pod is running", status: JobStatus{Active: 1}, unfinished: true},
		{name: "between the pods of a backoff", status: JobStatus{Failed: 1}, unfinished: true},
		{
			name:   "the controller marked it complete",
			status: JobStatus{Succeeded: 1, Conditions: []JobCondition{{Type: jobComplete, Status: ConditionTrue}}},
		},
		{
			name:   "the controller marked it failed",
			status: JobStatus{Failed: 3, Conditions: []JobCondition{failedJobCondition()}},
		},
		{
			name:       "a terminal type that is not true",
			status:     JobStatus{Failed: 1, Conditions: []JobCondition{{Type: jobFailed, Status: ConditionFalse}}},
			unfinished: true,
		},
		{
			name:       "a condition of another type",
			status:     JobStatus{Active: 1, Conditions: []JobCondition{{Type: "Suspended", Status: ConditionTrue}}},
			unfinished: true,
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := scanUnfinished([]Job{walkJob(one.status)}, "house", "movies"); got != one.unfinished {
				t.Errorf("scanUnfinished = %v, want %v", got, one.unfinished)
			}
		})
	}
	if scanUnfinished(nil, "house", "movies") {
		t.Error("a library with no jobs reads as unfinished")
	}
}

// a create another writer got to first is success, and any other
// failure ends the pass so the next one tries again.
func TestServeHeldPathsAcceptsAConflict(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	cluster.refuseCreate = true
	operator := testOperator(t, cluster)
	operator.paths.hold("house", "movies", "/library/movies/Arrival (2016)")

	if err := operator.serveHeldPaths(t.Context(), library, nil, testNow); err != nil {
		t.Fatalf("err = %v, want a conflict to read as success", err)
	}
	if len(operator.paths.held("house", "movies")) != 0 {
		t.Error("the path is still held after another writer created its job")
	}
}

// an update another writer raced is success: the next pass reads what
// they wrote. A refusal for any other reason is a failure.
func TestStandScanCronJobAnswersARacedUpdate(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "a conflict is success", status: http.StatusConflict},
		{name: "any other refusal is a failure", status: http.StatusInternalServerError, wantErr: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			cluster.cronJobs["house/movies-scan"] = &CronJob{
				Metadata: ObjectMeta{Name: "movies-scan", Namespace: "house"},
			}
			cluster.broken[http.MethodPut+" /apis/batch/v1/namespaces/house/cronjobs/movies-scan"] = one.status

			_, err := testOperator(t, cluster).standScanCronJob(t.Context(), library)

			if one.wantErr && err == nil {
				t.Fatal("err = nil, want the server's refusal")
			}
			if !one.wantErr && err != nil {
				t.Fatalf("err = %v, want a conflict to read as success", err)
			}
		})
	}
}
