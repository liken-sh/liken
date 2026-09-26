package main

// What these tests read: the gate that keeps one Job of a Library running
// at a time, the walk the schedule, a request, or a webhook starts, the Job
// that fills gaps, and the backoff after a Job that failed.

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// a Job the controller ended on a pod that exited zero.
func finishedJob(name, namespace string, labels, annotations map[string]string) Job {
	return Job{
		Metadata: ObjectMeta{Name: name, Namespace: namespace,
			Labels: labels, Annotations: annotations},
		Status: JobStatus{Conditions: []JobCondition{{Type: jobComplete, Status: ConditionTrue}}},
	}
}

// a Job the controller ended on the backoff limit.
func failedJob(name, namespace string, labels map[string]string) Job {
	return Job{
		Metadata: ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Status: JobStatus{Failed: scanBackoffLimit + 1,
			Conditions: []JobCondition{failedJobCondition()}},
	}
}

// a Job the controller has not ended.
func runningJob(name, namespace string, labels map[string]string) Job {
	return Job{
		Metadata: ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Status:   JobStatus{Active: 1},
	}
}

// the runs a library reports after one walk that started a minute before
// the time given and finished at it, whose own Job ran the phases after it.
func walkedRuns(at time.Time) []libraryRun {
	return []libraryRun{
		{Worker: workerScan, Job: "movies-walk-1", Started: at.Add(-time.Minute), Finished: at},
		{Worker: workerEnrich, Job: "movies-walk-1", Started: at.Add(-time.Minute), Finished: at},
	}
}

// a library with the sources and the provider that serve identity.
func libraryWithProvider() (*Library, providerSet) {
	library := studioMovies()
	library.Spec.Sources = []string{"tmdb"}
	provider := readyProvider("tmdb", "house", factIdentity)
	return library, providerSet{libraryKey("house", "tmdb"): provider}
}

// A walk of another Library, and a walk in another namespace, hold nothing
// here. Every worker of this Library holds the gate, because each runs an
// agent on its catalog claim.
func TestTheGateReadsEveryJobOfOneLibrary(t *testing.T) {
	otherLibrary := houseJob("shows-walk-1", jobModeWalk, JobStatus{Active: 1})
	otherLibrary.Metadata.Labels[libraryLabelKey] = "shows"
	otherNamespace := houseJob("movies-walk-1", jobModeWalk, JobStatus{Active: 1})
	otherNamespace.Metadata.Namespace = "studio"

	cases := []struct {
		name    string
		jobs    []Job
		running bool
	}{
		{name: "no Job"},
		{name: "another library", jobs: []Job{otherLibrary}},
		{name: "another namespace", jobs: []Job{otherNamespace}},
		{name: "a walk", jobs: []Job{walkJob(JobStatus{Active: 1})}, running: true},
		{name: "a Job that fills gaps", jobs: []Job{houseJob("movies-gaps-1", jobModeGaps, JobStatus{Active: 1})}, running: true},
		{name: "the cleanup", jobs: []Job{houseJob("movies-cleanup", workerCleanup, JobStatus{Active: 1})}, running: true},
		{name: "an enricher of an earlier release", jobs: []Job{houseJob("movies-enrich-1", workerEnrich, JobStatus{})}, running: true},
		{name: "a walk between the pods of its backoff", jobs: []Job{walkJob(JobStatus{Failed: 1})}, running: true},
		{name: "a walk the controller marked complete", jobs: []Job{walkJob(succeededStatus(testNow))}},
		{name: "a walk the controller marked failed", jobs: []Job{walkJob(failedStatus(testNow))}},
		{name: "a failed condition that is not true", running: true, jobs: []Job{walkJob(JobStatus{Failed: 1,
			Conditions: []JobCondition{{Type: jobFailed, Status: ConditionFalse}}})}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := libraryJobUnfinished(one.jobs, "house", "movies"); got != one.running {
				t.Errorf("libraryJobUnfinished = %v, want %v", got, one.running)
			}
		})
	}
}

// A walk Job the operator created, with the time it carries.
func walkCreated(name string, at time.Time) Job {
	job := finishedJob(name, "house", workerLabels("movies", jobModeWalk),
		map[string]string{jobCreatedAnnotation: at.Format(time.RFC3339Nano)})
	return job
}

// What the pass starts for one Library: a walk when the schedule, a request,
// or a webhook asks for one, a Job that fills gaps when a gap is open and a
// cause has come, and nothing while another Job of the Library runs.
func TestThePassStartsTheJobThatIsDue(t *testing.T) {
	hourAgo := testNow.Add(-time.Hour)
	halfHourAgo := testNow.Add(-30 * time.Minute)
	cases := []struct {
		name     string
		report   *libraryReport
		jobs     []Job
		held     []string
		refresh  time.Time
		schedule string
		want     string
		paths    string
	}{
		{name: "a new Library", want: jobModeWalk},
		{name: "an hour past the last walk", report: &libraryReport{Runs: walkedRuns(hourAgo)}, want: jobModeWalk},
		{name: "inside the hour", report: &libraryReport{Runs: walkedRuns(testNow.Add(time.Minute))}},
		{name: "a walk Job the report has not reached",
			report: &libraryReport{Runs: walkedRuns(hourAgo)}, jobs: []Job{walkCreated("movies-walk-2", testNow)}},
		{name: "a walk of folders is no full walk", report: &libraryReport{Runs: walkedRuns(hourAgo)},
			want: jobModeWalk, jobs: []Job{func() Job {
				job := walkCreated("movies-walk-2", testNow.Add(-time.Minute))
				job.Metadata.Annotations[jobPathsAnnotation] = `["/a"]`
				return job
			}()}},
		{name: "a schedule that does not parse", report: &libraryReport{Runs: walkedRuns(hourAgo)}, schedule: "every hour"},
		{name: "a request after the last walk", report: &libraryReport{Runs: walkedRuns(testNow.Add(time.Minute))},
			refresh: testNow.Add(30 * time.Second), want: jobModeWalk},
		{name: "a request the last walk answered", report: &libraryReport{Runs: walkedRuns(testNow.Add(time.Minute))},
			refresh: halfHourAgo},
		{name: "a webhook's folders", report: &libraryReport{Runs: walkedRuns(testNow.Add(time.Minute))},
			held: []string{"/a", "/b"}, want: jobModeWalk, paths: `["/a","/b"]`},
		{name: "a webhook that named no folder", report: &libraryReport{Runs: walkedRuns(testNow.Add(time.Minute))},
			held: []string{""}, want: jobModeWalk},
		{name: "a walk that runs", jobs: []Job{walkJob(JobStatus{Active: 1})}, held: []string{"/a"}},
		{name: "an open trickplay gap", want: jobModeGaps, report: &libraryReport{
			Runs: walkedRuns(testNow.Add(time.Minute)), Gaps: map[string]int{factTrickplay: 3}}},
		{name: "an open identity gap no cause has opened", report: &libraryReport{
			Runs: walkedRuns(testNow.Add(time.Minute)), Gaps: map[string]int{factIdentity: 3}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			library.Spec.Trickplay.Enabled = true
			library.Spec.Scan.Schedule = one.schedule
			if !one.refresh.IsZero() {
				library.Spec.Refresh = map[string]time.Time{refreshWalk: one.refresh}
			}
			operator := testOperator(t, cluster)
			for _, path := range one.held {
				operator.paths.hold("house", "movies", path)
			}

			if err := operator.runLibrary(t.Context(), library, one.report, one.jobs, providerSet{}, testNow); err != nil {
				t.Fatal(err)
			}

			created := cluster.heldJobs()
			if one.want == "" {
				if len(created) != 0 {
					t.Fatalf("jobs = %+v, want none", created)
				}
				return
			}
			if len(created) != 1 || created[0].Metadata.Labels[workerLabelKey] != one.want {
				t.Fatalf("jobs = %+v, want one %s Job", created, one.want)
			}
			if got := created[0].Metadata.Annotations[jobPathsAnnotation]; got != one.paths {
				t.Errorf("paths = %q, want %q", got, one.paths)
			}
			if one.want == jobModeWalk && len(operator.paths.held("house", "movies")) != 0 {
				t.Error("the folders stay held after the walk that covers them")
			}
		})
	}
}

// The folders a webhook names while a Job runs stay held, and the next Job
// walks every one of them.
func TestFoldersNamedWhileAJobRunsWaitForTheNextJob(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	operator := testOperator(t, cluster)
	report := &libraryReport{Runs: walkedRuns(testNow.Add(time.Minute))}
	running := []Job{walkJob(JobStatus{Active: 1})}
	for _, path := range []string{"/a", "/b", "/c"} {
		operator.paths.hold("house", "movies", path)
		if err := operator.runLibrary(t.Context(), library, report, running, providerSet{}, testNow); err != nil {
			t.Fatal(err)
		}
	}
	if len(cluster.heldJobs()) != 0 {
		t.Fatal("a Job started beside the one that runs")
	}

	if err := operator.runLibrary(t.Context(), library, report, nil, providerSet{}, testNow); err != nil {
		t.Fatal(err)
	}

	created := cluster.heldJobs()
	if len(created) != 1 || created[0].Metadata.Annotations[jobPathsAnnotation] != `["/a","/b","/c"]` {
		t.Errorf("jobs = %+v, want one walk of the three folders", created)
	}
}

// A Job that fills gaps runs the phases whose gaps are open and that a cause
// has opened since the last Job, and the time-limited phases whenever their
// gap is open.
func TestAGapJobRunsThePhasesWithWork(t *testing.T) {
	library, providers := libraryWithProvider()
	library.Spec.Trickplay.Enabled = true
	// The provider turned Ready after the last Job started.
	providers[libraryKey("house", "tmdb")].Status.Conditions[0].LastTransitionTime = testNow.Add(30 * time.Second)
	report := &libraryReport{Runs: walkedRuns(testNow.Add(time.Minute)),
		Gaps: map[string]int{factIdentity: 2, factProbe: 0, factTrickplay: 1}}

	plan, due := nextLibraryJob(library, report, nil, providers, nil, testNow)

	names := []string{}
	for _, phase := range plan.phases {
		names = append(names, phase.name)
	}
	if !due || plan.mode != jobModeGaps || !slices.Equal(names, []string{factIdentity, trickplayContainerName}) {
		t.Errorf("plan = %s %v, due %v, want the identity and trickplay phases", plan.mode, names, due)
	}
}

// The cause of a Job that fills gaps: a refresh that has come, a provider
// that turned Ready, and a walk whose own Job ran no phases. A walk Job ran
// every phase after its walk, so its walk opens nothing.
func TestTheCauseOfAGapJob(t *testing.T) {
	library, providers := libraryWithProvider()
	provider := providers[libraryKey("house", "tmdb")]
	earlier := testNow.Add(-2 * time.Hour)
	provider.Status.Conditions[0].LastTransitionTime = earlier
	legacyWalk := []libraryRun{
		{Worker: workerScan, Job: "movies-scan-1", Finished: testNow},
		{Worker: workerEnrich, Job: "movies-enrich-1", Started: earlier, Finished: earlier},
	}
	cases := []struct {
		name    string
		runs    []libraryRun
		refresh map[string]time.Time
		want    time.Time
	}{
		{name: "a walk Job's own walk", runs: walkedRuns(testNow), want: earlier},
		{name: "a walk with no phases", runs: legacyWalk, want: testNow},
		{name: "a refresh that has come", runs: walkedRuns(testNow.Add(-time.Hour)),
			refresh: map[string]time.Time{factCredits: testNow.Add(-time.Minute)}, want: testNow.Add(-time.Minute)},
		{name: "a refresh still to come", runs: walkedRuns(testNow.Add(-time.Hour)),
			refresh: map[string]time.Time{factCredits: testNow.Add(time.Hour)}, want: earlier},
		{name: "a walk request", runs: walkedRuns(testNow.Add(-time.Hour)),
			refresh: map[string]time.Time{refreshWalk: testNow}, want: earlier},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library.Spec.Refresh = one.refresh

			if got := enrichCause(library, one.runs, providers, testNow); !got.Equal(one.want) {
				t.Errorf("cause = %v, want %v", got, one.want)
			}
		})
	}
}

// A cause is due until a Job starts after it, and a run that died before
// its finish answered nothing.
func TestACauseIsDueUntilAJobStartsAfterIt(t *testing.T) {
	cases := []struct {
		name  string
		cause time.Time
		runs  []libraryRun
		want  bool
	}{
		{name: "no cause", runs: walkedRuns(testNow)},
		{name: "no Job yet", cause: testNow, want: true},
		{name: "a Job after the cause", cause: testNow.Add(-time.Hour), runs: walkedRuns(testNow)},
		{name: "a Job before the cause", cause: testNow.Add(time.Hour), runs: walkedRuns(testNow), want: true},
		{name: "a Job that never finished", cause: testNow.Add(-time.Hour), want: true,
			runs: []libraryRun{{Worker: workerEnrich, Job: "movies-walk-1", Started: testNow}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := enrichDue(one.cause, one.runs); got != one.want {
				t.Errorf("enrichDue = %v, want %v", got, one.want)
			}
		})
	}
}

// After a Job that failed, the next waits on the backoff curve, so a cause
// nobody has repaired costs one Job per delay. A Job that succeeded after it
// resets the curve.
func TestAFailedJobIsFollowedOnTheBackoff(t *testing.T) {
	operator := testOperator(t, newFakeCluster())
	failed := failedJob("movies-walk-1", "house", workerLabels("movies", jobModeWalk))
	failed.Metadata.Annotations = map[string]string{jobCreatedAnnotation: testNow.Format(time.RFC3339Nano)}
	jobs := []Job{failed}

	if !operator.mayFollow(jobs, nil, "house", "movies", testNow) {
		t.Fatal("the first Job after a failure waited")
	}
	if operator.mayFollow(jobs, nil, "house", "movies", testNow.Add(time.Second)) {
		t.Error("a second Job followed inside the backoff")
	}
	succeeded := walkCreated("movies-walk-2", testNow.Add(time.Minute))
	if !operator.mayFollow(append(jobs, succeeded), nil, "house", "movies", testNow.Add(2*time.Second)) {
		t.Error("a Job after a success waited on the backoff")
	}
	if !operator.mayFollow(jobs, nil, "house", "movies", testNow.Add(3*time.Second)) {
		t.Error("the curve held after the success reset it")
	}
}

// A create another writer got to first is success, and the folders it
// covers are released.
func TestTheJobCreateAcceptsAConflict(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	cluster.refuseCreate = true
	operator := testOperator(t, cluster)
	operator.paths.hold("house", "movies", "/a")

	if err := operator.runLibrary(t.Context(), library, nil, nil, providerSet{}, testNow); err != nil {
		t.Fatalf("err = %v, want a conflict to read as success", err)
	}
	if len(operator.paths.held("house", "movies")) != 0 {
		t.Error("the folder is still held after another writer created its Job")
	}
}

// The Job carries the write its copy must hold: the newest confirmed run the
// report carries.
func TestTheJobCarriesTheSyncTargetOfTheReport(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	report := &libraryReport{Runs: []libraryRun{{Worker: workerEnrich, Job: "movies-walk-1",
		Started: testNow.Add(-2 * time.Hour), Finished: testNow.Add(-2 * time.Hour),
		Actor: sqliteAgentActor, Version: 77}}}

	if err := testOperator(t, cluster).runLibrary(t.Context(), library, report, nil, providerSet{}, testNow); err != nil {
		t.Fatal(err)
	}

	created := cluster.heldJobs()
	if len(created) != 1 {
		t.Fatalf("jobs = %+v, want the walk", created)
	}
	closing := jobContainer(&created[0], closeMode)
	got := containerEnvironment(*closing)
	if got[syncActorVariable] != sqliteAgentActor || got[syncVersionVariable] != "77" {
		t.Errorf("close reads %v, want the report's newest confirmed run", got)
	}
	if !strings.HasPrefix(created[0].Metadata.Name, "movies-walk-") {
		t.Errorf("name = %q, want a walk of movies", created[0].Metadata.Name)
	}
}

// A refresh gives a fact work while its oldest attempt is older than the
// refresh and the refresh time has come, and a phase with that work runs in
// a Job that fills gaps even with no gap counted.
func TestARefreshGivesAPhaseWork(t *testing.T) {
	oldest := testNow.Add(-24 * time.Hour)
	cases := []struct {
		name    string
		refresh map[string]time.Time
		want    bool
	}{
		{name: "no refresh"},
		{name: "a refresh after the oldest attempt", refresh: map[string]time.Time{factIdentity: oldest.Add(time.Hour)}, want: true},
		{name: "a refresh before the oldest attempt", refresh: map[string]time.Time{factIdentity: oldest.Add(-time.Hour)}},
		{name: "a refresh still to come", refresh: map[string]time.Time{factIdentity: time.Now().Add(time.Hour)}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library := studioMovies()
			library.Spec.Refresh = one.refresh
			report := &libraryReport{OldestAttempts: map[string]time.Time{factIdentity: oldest}}

			if got := phaseGapOpen(library, report, providerSet{}, []string{factIdentity}, time.Now()); got != one.want {
				t.Errorf("phaseGapOpen = %v, want %v", got, one.want)
			}
		})
	}
}

// A delete the API server refuses ends the pass, and the next pass asks
// again, because the Library is not marked done.
func TestAFailedDeleteOfAnEarlierReleasesObjectsIsAskedAgain(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	cluster.broken["/api/v1/namespaces/house/persistentvolumeclaims/movies-enrich-catalog"] = 500
	operator := testOperator(t, cluster)

	if err := operator.retireLegacyWorkers(t.Context(), library); err == nil {
		t.Fatal("retireLegacyWorkers = nil error on a refused delete")
	}
	delete(cluster.broken, "/api/v1/namespaces/house/persistentvolumeclaims/movies-enrich-catalog")
	if err := operator.retireLegacyWorkers(t.Context(), library); err != nil {
		t.Fatal(err)
	}
	if got := cluster.countRequests("DELETE", "cronjobs"); got != 2 {
		t.Errorf("CronJob deletes = %d, want one for each pass that had not finished", got)
	}
}

// The backoff after a failure ends once a later Job succeeded, whether that
// Job is still listed or only its finished run row remains after the operator
// deleted it. The status reads the same rule, so the gate and the status agree.
func TestTheBackoffEndsAtALaterSuccess(t *testing.T) {
	failed := failedJob("movies-walk-1", "house", workerLabels("movies", jobModeWalk))
	failed.Metadata.Annotations = map[string]string{jobCreatedAnnotation: testNow.Format(time.RFC3339Nano)}
	later := testNow.Add(time.Minute)

	cases := []struct {
		name string
		jobs []Job
		runs []libraryRun
		open bool
	}{
		{name: "a failed Job with no later success", jobs: []Job{failed}},
		{
			name: "a failed Job and a later Job that succeeded",
			jobs: []Job{failed, walkCreated("movies-walk-2", later)},
			open: true,
		},
		{
			name: "a failed Job and the run row of a later Job the operator deleted",
			jobs: []Job{failed},
			runs: []libraryRun{{Worker: workerEnrich, Job: "movies-walk-2", Started: later, Finished: later}},
			open: true,
		},
		{
			name: "a failed Job and a run row from before it",
			jobs: []Job{failed},
			runs: []libraryRun{{Worker: workerEnrich, Job: "movies-walk-0",
				Started: testNow.Add(-time.Hour), Finished: testNow.Add(-time.Minute)}},
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			operator := testOperator(t, newFakeCluster())
			// The first Job after a failure starts at once, so the second call
			// is the one the backoff decides.
			operator.mayFollow(one.jobs, one.runs, "house", "movies", testNow)

			if got := operator.mayFollow(one.jobs, one.runs, "house", "movies", testNow.Add(time.Second)); got != one.open {
				t.Errorf("mayFollow = %v, want %v", got, one.open)
			}
		})
	}
}
