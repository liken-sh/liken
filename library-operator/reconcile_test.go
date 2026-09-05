package main

// what these tests read: one pass over one Library against an API
// server that answers as Kubernetes does, and what the pass wrote into
// the cluster: the claim it provisioned, the schedule it stood, and the
// status it reported.

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// conditionOf reads one condition out of a status, so a test says
// which condition it means and reads its whole verdict.
func conditionOf(t *testing.T, status LibraryStatus, conditionType string) Condition {
	t.Helper()
	for _, condition := range status.Conditions {
		if condition.Type == conditionType {
			return condition
		}
	}
	t.Fatalf("the status carries no %s condition: %+v", conditionType, status.Conditions)
	return Condition{}
}

// A claim that is missing, unbound, or bound to a volume that has gone
// is the cluster's own state and not a failure. Each one reports its
// own reason, and none of them gets a schedule, because there is
// nothing to mount.
func TestReconcileReportsStorageThatIsNotBound(t *testing.T) {
	cases := []struct {
		name   string
		claim  *PersistentVolumeClaim
		reason string
	}{
		{name: "no claim", reason: reasonClaimNotFound},
		{
			name: "a claim waiting on a volume",
			claim: &PersistentVolumeClaim{
				Metadata: ObjectMeta{Name: "movies", Namespace: "house"},
				Status:   PersistentVolumeClaimStatus{Phase: "Pending"},
			},
			reason: reasonClaimUnbound,
		},
		{
			name: "a claim the binder has not answered",
			claim: &PersistentVolumeClaim{
				Metadata: ObjectMeta{Name: "movies", Namespace: "house"},
			},
			reason: reasonClaimUnbound,
		},
		{
			name: "a claim whose volume is gone",
			claim: &PersistentVolumeClaim{
				Metadata: ObjectMeta{Name: "movies", Namespace: "house"},
				Spec:     PersistentVolumeClaimSpec{VolumeName: "pv-movies"},
				Status:   PersistentVolumeClaimStatus{Phase: claimBound},
			},
			reason: reasonVolumeNotFound,
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			delete(cluster.claims, "movies")
			delete(cluster.volumes, "pv-movies")
			if one.claim != nil {
				cluster.claims["movies"] = one.claim
			}

			if err := testOperator(t, cluster).reconcile(t.Context(), library, standingCatalog(), nil, nil, testNow); err != nil {
				t.Fatal(err)
			}

			status := cluster.heldLibrary("movies").Status
			bound := conditionOf(t, status, conditionBound)
			if bound.Status != ConditionFalse || bound.Reason != one.reason {
				t.Errorf("Bound = %+v, want False with %s", bound, one.reason)
			}
			if bound.Message == "" {
				t.Error("the Bound condition carries no message")
			}
			if ready := conditionOf(t, status, conditionReady); ready.Reason != reasonNotBound {
				t.Errorf("Ready = %+v, want NotBound", ready)
			}
			if status.Volume != nil {
				t.Errorf("volume = %+v, want none", status.Volume)
			}
			if cluster.countRequests(http.MethodPost, "cronjobs") != 0 {
				t.Error("a schedule was created for a library with no volume")
			}
		})
	}
}

// A bound claim reports the volume behind it, so whoever plays a title
// from this library does not have to chase the claim.
func TestReconcileReportsTheVolumeBehindTheClaim(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)

	if err := testOperator(t, cluster).reconcile(t.Context(), library, standingCatalog(), nil, nil, testNow); err != nil {
		t.Fatal(err)
	}

	status := cluster.heldLibrary("movies").Status
	bound := conditionOf(t, status, conditionBound)
	if bound.Status != ConditionTrue || bound.Reason != reasonBound {
		t.Errorf("Bound = %+v, want True", bound)
	}
	if bound.ObservedGeneration != 3 {
		t.Errorf("observedGeneration = %d, want the Library's generation", bound.ObservedGeneration)
	}
	if status.Volume == nil {
		t.Fatal("the status reports no volume")
	}
	want := LibraryVolume{Name: "pv-movies", Type: "nfs", Server: "syn.example", Path: "/srv/media/movies"}
	if *status.Volume != want {
		t.Errorf("volume = %+v, want %+v", *status.Volume, want)
	}
}

// a bound Library reports the address a webhook is posted to: the
// operator's own Service, with a path that names this Library.
func TestReconcileReportsTheWebhookAddress(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)

	if err := testOperator(t, cluster).reconcile(t.Context(), library, standingCatalog(), nil, nil, testNow); err != nil {
		t.Fatal(err)
	}

	want := "http://library-operator.liken-system.svc/webhook/house/movies"
	if webhook := cluster.heldLibrary("movies").Status.Webhook; webhook != want {
		t.Errorf("webhook = %q, want %q", webhook, want)
	}
}

// A Library with no volume reports no address. This is the same
// condition its schedule is created on.
func TestReconcileReportsNoWebhookForAnUnboundLibrary(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	delete(cluster.claims, "movies")

	if err := testOperator(t, cluster).reconcile(t.Context(), library, standingCatalog(), nil, nil, testNow); err != nil {
		t.Fatal(err)
	}

	if webhook := cluster.heldLibrary("movies").Status.Webhook; webhook != "" {
		t.Errorf("webhook = %q, want none", webhook)
	}
}

// A volume served by a driver this operator carries no type for still
// reports which driver serves it.
func TestReconcileReportsAVolumeItKnowsNothingAbout(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	cluster.volumes["pv-movies"] = `{"metadata":{"name":"pv-movies"},"spec":` +
		`{"csi":{"driver":"nas.example","volumeHandle":"movies"}}}`

	if err := testOperator(t, cluster).reconcile(t.Context(), library, standingCatalog(), nil, nil, testNow); err != nil {
		t.Fatal(err)
	}

	want := LibraryVolume{Name: "pv-movies", Type: "csi"}
	if got := *cluster.heldLibrary("movies").Status.Volume; got != want {
		t.Errorf("volume = %+v, want %+v", got, want)
	}
}

// A bound Library in a namespace with no Catalog waits: it gets no
// schedule, and Ready says the namespace has no Catalog. The storage
// is still bound, so a person sees the one thing that is missing.
func TestReconcileWaitsForACatalog(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)

	if err := testOperator(t, cluster).reconcile(t.Context(), library, singleCatalog(nil), nil, nil, testNow); err != nil {
		t.Fatal(err)
	}

	if cluster.countRequests(http.MethodPost, "cronjobs") != 0 {
		t.Error("a schedule was created for a Library with no Catalog")
	}
	status := cluster.heldLibrary("movies").Status
	if bound := conditionOf(t, status, conditionBound); bound.Status != ConditionTrue {
		t.Errorf("Bound = %+v, want True", bound)
	}
	if ready := conditionOf(t, status, conditionReady); ready.Reason != reasonNoCatalog {
		t.Errorf("Ready = %+v, want NoCatalog", ready)
	}
}

// A bound Library in a namespace with a Catalog provisions its durable
// catalog claim before it stands the schedule.
func TestReconcileProvisionsTheCatalogClaim(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)

	if err := testOperator(t, cluster).reconcile(t.Context(), library, standingCatalog(), nil, nil, testNow); err != nil {
		t.Fatal(err)
	}

	claim := cluster.heldClaim("movies-catalog")
	if claim == nil {
		t.Fatal("the reconcile provisioned no catalog claim")
	}
	if len(claim.Spec.AccessModes) != 1 || claim.Spec.AccessModes[0] != accessModeReadWriteOnce {
		t.Errorf("accessModes = %v, want ReadWriteOnce", claim.Spec.AccessModes)
	}
	if cluster.heldCronJob("house", "movies-scan") == nil {
		t.Error("the reconcile stood no scan schedule")
	}
}

// A request that fails is a failure the pass reports, because it
// cannot tell what the cluster holds.
func TestReconcileFailsWhenTheClusterCannotBeRead(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "the claim", path: "/api/v1/namespaces/house/persistentvolumeclaims/movies"},
		{name: "the volume", path: "/api/v1/persistentvolumes/pv-movies"},
		{name: "the schedule", path: "/apis/batch/v1/namespaces/house/cronjobs/movies-scan"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			cluster.broken[one.path] = http.StatusInternalServerError

			err := testOperator(t, cluster).reconcile(t.Context(), library, standingCatalog(), nil, nil, testNow)

			if err == nil {
				t.Fatal("err = nil, want the failure the pass could not read past")
			}
		})
	}
}

// A settled Library is written once. The second pass derives the same
// status and writes nothing, so the libraries watch that wakes the
// loop is not woken by the loop itself.
func TestReconcileWritesASettledStatusOnce(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	operator := testOperator(t, cluster)
	operator.reporters.mark("house", true)
	operator.reports.fold("house", "movies", libraryReport{Titles: 12, Unidentified: 2})

	if err := operator.reconcile(t.Context(), library, standingCatalog(), nil, nil, testNow); err != nil {
		t.Fatal(err)
	}
	written := cluster.heldLibrary("movies")
	if err := operator.reconcile(t.Context(), written, standingCatalog(), nil, nil, testNow); err != nil {
		t.Fatal(err)
	}

	if got := cluster.countRequests(http.MethodPut, "libraries"); got != 1 {
		t.Errorf("status writes = %d, want one", got)
	}
	ready := conditionOf(t, cluster.heldLibrary("movies").Status, conditionReady)
	if ready.Status != ConditionTrue {
		t.Errorf("Ready = %+v, want True", ready)
	}
}

// The hash reads the spec alone, so stamping the annotation does not
// change it, and a second build of the same Library stamps the same
// value.
func TestTemplateHashIgnoresTheAnnotationItStamps(t *testing.T) {
	pod := testScanPod(studioMovies())

	before, err := templateHash(pod.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := stampTemplateHash(&pod.Metadata, pod.Spec); err != nil {
		t.Fatal(err)
	}

	after, err := templateHash(pod.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("hash = %q, want %q after the stamp", after, before)
	}
	if pod.Metadata.Annotations[templateHashAnnotation] != before {
		t.Errorf("stamp = %q, want %q", pod.Metadata.Annotations[templateHashAnnotation], before)
	}
}

// A release that changes an image, and a person who changes the root,
// the schedule, or the scanner image, each change the hash, which is
// what rolls the schedule the Jobs are built from.
func TestTemplateHashFollowsTheSchedule(t *testing.T) {
	base, err := templateHash(testScanCronJob(studioMovies()).Spec)
	if err != nil {
		t.Fatal(err)
	}

	newRoot := studioMovies()
	newRoot.Spec.Storage.Root = "/kids-movies"
	ownScanner := studioMovies()
	ownScanner.Spec.Movies.Image = "registry.example/my-scanner:1"
	newSchedule := studioMovies()
	newSchedule.Spec.Scan.Schedule = "*/15 * * * *"
	cases := []struct {
		name    string
		cronJob *CronJob
	}{
		{"the scanner image", buildScanCronJob(studioMovies(), testScannerImage+"-next",
			testCorrosionImage, testBusAddress, defaultTopicBase)},
		{"the catalog image", buildScanCronJob(studioMovies(), testScannerImage,
			testCorrosionImage+"-next", testBusAddress, defaultTopicBase)},
		{"the root", testScanCronJob(newRoot)},
		{"a scanner of one's own", testScanCronJob(ownScanner)},
		{"the schedule", testScanCronJob(newSchedule)},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			hash, err := templateHash(one.cronJob.Spec)
			if err != nil {
				t.Fatal(err)
			}
			if hash == base {
				t.Errorf("%s changed and the hash stayed %q", one.name, hash)
			}
		})
	}
}

// A Library that loses a precondition loses its schedule. The pass is
// level-triggered, so the schedule that stood for a Library whose
// Catalog went away is deleted on the next pass, rather than left to
// keep walking a volume the Library no longer reports.
func TestReconcileStopsTheScheduleWhenThePreconditionGoes(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	operator := testOperator(t, cluster)

	if err := operator.reconcile(t.Context(), library, standingCatalog(), nil, nil, testNow); err != nil {
		t.Fatal(err)
	}
	if cluster.heldCronJob("house", "movies-scan") == nil {
		t.Fatal("the first pass stood no schedule")
	}

	if err := operator.reconcile(t.Context(), library, singleCatalog(nil), nil, nil, testNow); err != nil {
		t.Fatal(err)
	}

	if cluster.heldCronJob("house", "movies-scan") != nil {
		t.Error("the schedule stands for a Library with no Catalog")
	}
	// The conditions still report the precondition, not the delete.
	if ready := conditionOf(t, cluster.heldLibrary("movies").Status, conditionReady); ready.Reason != reasonNoCatalog {
		t.Errorf("Ready = %+v, want NoCatalog", ready)
	}
}

// A Library that never stood a schedule asks for the delete anyway,
// because the pass reads the whole state every time. An absent CronJob
// is success, so the pass reports no error.
func TestReconcileWithNoScheduleToStopReportsNoError(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)

	if err := testOperator(t, cluster).reconcile(t.Context(), library, singleCatalog(nil), nil, nil, testNow); err != nil {
		t.Fatalf("a pass with no schedule to stop failed: %v", err)
	}
}

// a failure to create a held path's Job ends the pass, so the next pass
// creates it again in place of reporting a status the cluster does not
// hold.
func TestReconcileReportsAFailedFolderScan(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	operator := testOperator(t, cluster)
	operator.paths.hold("house", "movies", "/library/movies/Arrival (2016)")
	cluster.broken["/apis/batch/v1/namespaces/house/jobs"] = http.StatusInternalServerError

	err := operator.reconcile(t.Context(), library, standingCatalog(), nil, nil, testNow)

	if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
		t.Fatalf("err = %v, want the server's own message", err)
	}
}

// A Library with no reported scan run gets one full walk on its first
// pass, and no second one on the pass after it while that Job's pod is
// active.
func TestReconcileStartsTheFirstWalk(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	operator := testOperator(t, cluster)

	if err := operator.reconcile(t.Context(), library, standingCatalog(), nil, nil, testNow); err != nil {
		t.Fatal(err)
	}

	jobs := cluster.heldJobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v, want the one full walk", jobs)
	}
	environment := containerEnvironment(jobs[0].Spec.Template.Spec.Containers[0])
	if path := environment[scanPathVariable]; path != "" {
		t.Errorf("%s = %q, want the full walk's empty path", scanPathVariable, path)
	}
	if len(operator.paths.held("house", "movies")) != 0 {
		t.Error("the path is still held after the walk's job was created")
	}

	walking := jobs[0]
	walking.Status = JobStatus{Active: 1}
	if err := operator.reconcile(t.Context(), cluster.heldLibrary("movies"), standingCatalog(),
		[]Job{walking}, nil, testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	if got := len(cluster.heldJobs()); got != 1 {
		t.Errorf("jobs = %d, want no second walk while the first one runs", got)
	}
}

// failedJobCondition is the condition the Job controller writes onto a
// Job that has given up.
func failedJobCondition() JobCondition {
	return JobCondition{Type: jobFailed, Status: ConditionTrue, Reason: "BackoffLimitExceeded"}
}

// scanRunReport is the report of a library that has already had one
// full walk.
func scanRunReport() *libraryReport {
	return &libraryReport{Runs: []libraryRun{
		{Worker: workerScan, Job: "movies-scan-29380000", Started: testNow, Finished: testNow},
	}}
}

// Two states start no first walk: a report that carries a scan run,
// and a scan Job the controller has not marked Complete or Failed,
// which covers a Job between the pods of its backoff. Every other
// state starts one.
func TestReconcileStartsNoWalkWhenAScanHasRun(t *testing.T) {
	cases := []struct {
		name    string
		report  *libraryReport
		jobs    []Job
		started bool
	}{
		{name: "no report yet", started: true},
		{
			name: "a report with another worker's run",
			report: &libraryReport{Runs: []libraryRun{
				{Worker: workerCleanup, Job: "movies-cleanup", Started: testNow},
			}},
			started: true,
		},
		{name: "a report carrying a scan run", report: scanRunReport()},
		{name: "a scan is running", jobs: []Job{walkJob(JobStatus{Active: 1})}},
		{name: "a scan between its pods", jobs: []Job{walkJob(JobStatus{Failed: 1})}},
		{
			name:    "a scan the controller marked failed",
			jobs:    []Job{walkJob(JobStatus{Failed: 3, Conditions: []JobCondition{failedJobCondition()}})},
			started: true,
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			operator := testOperator(t, cluster)
			if one.report != nil {
				operator.reports.fold("house", "movies", *one.report)
			}

			if err := operator.reconcile(t.Context(), library, standingCatalog(), one.jobs, nil, testNow); err != nil {
				t.Fatal(err)
			}

			if started := len(cluster.heldJobs()) == 1; started != one.started {
				t.Errorf("a walk was started: %v, want %v", started, one.started)
			}
		})
	}
}

// A franchises Library binds its claim the way every other kind does, and
// reports the volume behind it. The repository is the scan Job's own work, and
// the operator never reads it.
func TestReconcileBindsTheClaimOfALibraryThatNamesARepository(t *testing.T) {
	cluster := newFakeCluster()
	library := studioFranchises()
	cluster.libraries["franchises"] = library
	seedCatalog(cluster, "house-catalog", "house")
	cluster.claims["franchise-art"] = &PersistentVolumeClaim{
		Metadata: ObjectMeta{Name: "franchise-art", Namespace: "house"},
		Spec:     PersistentVolumeClaimSpec{VolumeName: "pv-franchise-art"},
		Status:   PersistentVolumeClaimStatus{Phase: claimBound},
	}
	cluster.volumes["pv-franchise-art"] = `{"metadata":{"name":"pv-franchise-art"},"spec":` +
		`{"capacity":{"storage":"1Gi"},"accessModes":["ReadWriteOnce"],` +
		`"local":{"path":"/srv/franchise-art"}}}`

	if err := testOperator(t, cluster).reconcile(t.Context(), library,
		standingCatalog(), nil, nil, testNow); err != nil {
		t.Fatal(err)
	}

	status := cluster.heldLibrary("franchises").Status
	bound := conditionOf(t, status, conditionBound)
	if bound.Status != ConditionTrue || bound.Reason != reasonBound {
		t.Errorf("Bound = %+v, want True with the reason %s", bound, reasonBound)
	}
	if status.Volume == nil || status.Volume.Name != "pv-franchise-art" {
		t.Errorf("volume = %+v, want the one behind the claim", status.Volume)
	}
	if cluster.heldCronJob("house", scanCronJobName("franchises")) == nil {
		t.Error("the pass stood no schedule for a library that names a repository")
	}
}
