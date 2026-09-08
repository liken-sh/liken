package main

// What these tests read: the Job a Catalog's one-time Jellyfin backfill
// becomes, and what the pass does with the Job it finds.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func testJellyfinBackfillJob(catalog *NamespaceCatalog) *Job {
	return buildJellyfinBackfillJob(catalog, testScannerImage, testBusAddress,
		defaultTopicBase, defaultMediaTopicBase)
}

// The Job is named for the Catalog, owned by it, and carries the worker
// labels every Job of this operator carries, so the one Job list a pass makes
// finds it.
func TestJellyfinBackfillJobBelongsToItsCatalog(t *testing.T) {
	job := testJellyfinBackfillJob(jellyfinCatalog())

	if job.Metadata.Name != "house-catalog-jellyfin-backfill" || job.Metadata.Namespace != "house" {
		t.Errorf("metadata = %+v, want the Catalog's own backfill Job", job.Metadata)
	}
	if len(job.Metadata.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences = %+v, want the Catalog", job.Metadata.OwnerReferences)
	}
	owner := job.Metadata.OwnerReferences[0]
	if owner.Kind != "Catalog" || owner.Name != "house-catalog" || !owner.Controller {
		t.Errorf("owner = %+v, want the controlling Catalog", owner)
	}
	for _, labels := range []map[string]string{job.Metadata.Labels, job.Spec.Template.Metadata.Labels} {
		if labels[scannerLabelKey] != workerLabelValue ||
			labels[libraryLabelKey] != "house-catalog" ||
			labels[workerLabelKey] != workerJellyfinBackfill {
			t.Errorf("labels = %v, want the backfill worker of the Catalog", labels)
		}
	}
}

// The Job runs once on the scan Job's own backoff and TTL, its pod runs to
// completion, and it holds no Kubernetes credential.
func TestJellyfinBackfillJobRunsOnceWithNoCredential(t *testing.T) {
	job := testJellyfinBackfillJob(jellyfinCatalog())

	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != scanBackoffLimit {
		t.Errorf("backoffLimit = %v, want %d", job.Spec.BackoffLimit, scanBackoffLimit)
	}
	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != scanJobTTL {
		t.Errorf("ttlSecondsAfterFinished = %v, want %d", job.Spec.TTLSecondsAfterFinished, scanJobTTL)
	}
	pod := job.Spec.Template.Spec
	if pod.RestartPolicy != "Never" {
		t.Errorf("restartPolicy = %q, want Never", pod.RestartPolicy)
	}
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Error("automountServiceAccountToken is not false; the pod holds no credential")
	}
	if len(pod.Containers) != 1 {
		t.Fatalf("containers = %+v, want the backfill alone", pod.Containers)
	}
}

// The container is the jellyfin role with the standing half taken off it: the
// backfill subcommand, no port, and no readiness probe, because nothing posts
// to it.
func TestJellyfinBackfillJobRunsTheRoleWithNothingListening(t *testing.T) {
	container := testJellyfinBackfillJob(jellyfinCatalog()).Spec.Template.Spec.Containers[0]

	if container.Name != jellyfinContainer || container.Image != testScannerImage {
		t.Errorf("container = %+v, want the operator image in its backfill", container)
	}
	if want := "/library-operator " + jellyfinBackfillMode; strings.Join(container.Command, " ") != want {
		t.Errorf("command = %v, want %q", container.Command, want)
	}
	if len(container.Ports) != 0 {
		t.Errorf("ports = %+v, want none", container.Ports)
	}
	if container.ReadinessProbe != nil {
		t.Errorf("readinessProbe = %+v, want none", container.ReadinessProbe)
	}
}

// The container reads the same environment the role reads, minus the address
// the role listens on.
func TestJellyfinBackfillJobReadsTheRolesEnvironment(t *testing.T) {
	container := testJellyfinBackfillJob(jellyfinCatalog()).Spec.Template.Spec.Containers[0]
	held := envOf(container)

	cases := []struct {
		variable string
		want     string
	}{
		{libraryNamespaceVariable, "house"},
		{busAddressVariable, testBusAddress},
		{topicBaseVariable, defaultTopicBase},
		{mediaTopicBaseVariable, defaultMediaTopicBase},
		{jellyfinURLVariable, "http://jellyfin.jellyfin.svc:8096"},
	}
	for _, one := range cases {
		t.Run(one.variable, func(t *testing.T) {
			if held[one.variable] != one.want {
				t.Errorf("%s = %q, want %q", one.variable, held[one.variable], one.want)
			}
		})
	}
	if _, listens := held[jellyfinListenVariable]; listens {
		t.Errorf("env = %v, want no %s on a Job that listens on nothing", held, jellyfinListenVariable)
	}
	key := valueFromOf(container)[jellyfinAPIKeyVariable]
	if key == nil || key.SecretKeyRef == nil || key.SecretKeyRef.Name != "jellyfin-api-key" {
		t.Errorf("%s = %+v, want the Secret's key", jellyfinAPIKeyVariable, key)
	}
}

// A durable copy of the progress store the kubelet reports up, as the pass
// reads it.
func readyProgressPod(catalog *NamespaceCatalog) *Pod {
	pod := testProgressPod(catalog, 0)
	pod.Status = PodStatus{
		Phase:                 podRunning,
		InitContainerStatuses: []ContainerStatus{{Name: progressContainer, Ready: true}},
		ContainerStatuses:     []ContainerStatus{{Name: recorderContainer, Ready: true}},
	}
	return pod
}

// The backfill Job as the Job controller left it, with the counts a pass
// reads on it.
func backfillJobWith(catalog *NamespaceCatalog, status JobStatus) *Job {
	job := testJellyfinBackfillJob(catalog)
	job.Status = status
	return job
}

// A Jellyfin status as the API server stores it, so a test states the whole
// status it expects and not a field of it.
func jellyfinStatusJSON(t *testing.T, status *CatalogJellyfinStatus) string {
	t.Helper()
	body, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// The time the backfill finished, in the shape the status carries.
var testBackfilled = testNow.UTC().Format(time.RFC3339)

// The whole of the stand: what the Catalog's spec, its status, the Job that
// stands, and the progress store together say the pass should do.
func TestStandJellyfinBackfillFollowsTheCatalogAndTheJob(t *testing.T) {
	cases := []struct {
		name string
		// The server the Catalog names, or none.
		server string
		status *CatalogJellyfinStatus
		// The Job the cluster holds, as the controller left it.
		job *JobStatus
		// Every durable copy of the progress store is up.
		listening bool
		// A Job stands once the pass is over.
		stands bool
		want   *CatalogJellyfinStatus
	}{
		{
			name:      "a Catalog that names no server",
			listening: true,
		},
		{
			name:   "a Catalog that dropped the server it named",
			job:    &JobStatus{Active: 1},
			status: &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillRunning},
		},
		{
			name:      "a backfill that finished and a Job the TTL took",
			server:    testJellyfinURL,
			status:    &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillFinished, Backfilled: "2026-01-01T00:00:00Z"},
			listening: true,
			want:      &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillFinished, Backfilled: "2026-01-01T00:00:00Z"},
		},
		{
			name:      "the Job that succeeded",
			server:    testJellyfinURL,
			job:       &JobStatus{Succeeded: 1},
			listening: true,
			stands:    true,
			want:      &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillFinished, Backfilled: testBackfilled},
		},
		{
			name:      "the Job that succeeded against a finish already recorded",
			server:    testJellyfinURL,
			status:    &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillFinished, Backfilled: "2026-01-01T00:00:00Z"},
			job:       &JobStatus{Succeeded: 1},
			listening: true,
			stands:    true,
			want:      &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillFinished, Backfilled: "2026-01-01T00:00:00Z"},
		},
		{
			name:      "the Job that gave up past its backoff",
			server:    testJellyfinURL,
			job:       &JobStatus{Failed: 3},
			listening: true,
			want:      &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillFailed},
		},
		{
			name:      "the Job that is still running",
			server:    testJellyfinURL,
			job:       &JobStatus{Active: 1},
			listening: true,
			stands:    true,
			want:      &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillRunning},
		},
		{
			name:   "a progress store that is not up yet",
			server: testJellyfinURL,
			want:   &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillPending},
		},
		{
			name:      "a backfill that has not run",
			server:    testJellyfinURL,
			listening: true,
			stands:    true,
			want:      &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillRunning},
		},
		{
			name:      "a backfill that failed last pass",
			server:    testJellyfinURL,
			status:    &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillFailed},
			listening: true,
			stands:    true,
			want:      &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillRunning},
		},
		{
			name:      "a server the Catalog changed",
			server:    "http://jellyfin.media.svc:8096",
			status:    &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillFinished, Backfilled: "2026-01-01T00:00:00Z"},
			listening: true,
			stands:    true,
			want:      &CatalogJellyfinStatus{Server: "http://jellyfin.media.svc:8096", Backfill: backfillRunning},
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			catalog := jellyfinCatalog()
			catalog.Status.Jellyfin = one.status
			if one.server == "" {
				catalog.Spec.Jellyfin = nil
			} else {
				catalog.Spec.Jellyfin.URL = one.server
			}
			jobs := []Job{}
			if one.job != nil {
				held := backfillJobWith(jellyfinCatalog(), *one.job)
				cluster.holdJob(held)
				jobs = append(jobs, *held)
			}
			pods := []*Pod{}
			if one.listening {
				pods = append(pods, readyProgressPod(catalog))
			}

			got := testOperator(t, cluster).standJellyfinBackfill(t.Context(), catalog, jobs, pods, testNow)

			if want := jellyfinStatusJSON(t, one.want); jellyfinStatusJSON(t, got) != want {
				t.Errorf("status = %s, want %s", jellyfinStatusJSON(t, got), want)
			}
			held := cluster.heldJob("house", "house-catalog-jellyfin-backfill") != nil
			if held != one.stands {
				t.Errorf("the backfill Job stands = %v, want %v", held, one.stands)
			}
		})
	}
}

// The server the Catalog names in every test here.
const testJellyfinURL = "http://jellyfin.jellyfin.svc:8096"

// A copy that is not up holds the backfill back, whatever the shape of what
// the kubelet reports.
func TestTheBackfillWaitsForEveryCopyOfTheProgressStore(t *testing.T) {
	catalog := jellyfinCatalog()

	cases := []struct {
		name      string
		pods      []*Pod
		listening bool
	}{
		{name: "a namespace with no copy"},
		{name: "a copy the pass could not stand", pods: []*Pod{nil}},
		{name: "a copy that has not started", pods: []*Pod{testProgressPod(catalog, 0)}},
		{name: "a copy up beside one that is not", listening: false, pods: []*Pod{
			readyProgressPod(catalog), testProgressPod(catalog, 1),
		}},
		{name: "every copy up", listening: true, pods: []*Pod{readyProgressPod(catalog)}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := progressStoreListening(one.pods); got != one.listening {
				t.Errorf("listening = %v, want %v", got, one.listening)
			}
		})
	}
}

// A backfill Job that keeps failing is created again on a wait that doubles,
// so a server the backfill cannot read does not cost a Job every pass.
func TestTheBackfillWaitsBeforeItStandsTheJobAgain(t *testing.T) {
	cluster := newFakeCluster()
	catalog := jellyfinCatalog()
	pods := []*Pod{readyProgressPod(catalog)}
	operator := testOperator(t, cluster)

	first := operator.standJellyfinBackfill(t.Context(), catalog, nil, pods, testNow)
	held := backfillJobWith(catalog, JobStatus{Failed: 3})
	catalog.Status.Jellyfin = operator.standJellyfinBackfill(t.Context(), catalog, []Job{*held}, pods, testNow)
	waiting := operator.standJellyfinBackfill(t.Context(), catalog, nil, pods, testNow)
	again := operator.standJellyfinBackfill(t.Context(), catalog, nil, pods, testNow.Add(time.Hour))

	if first.Backfill != backfillRunning || waiting.Backfill != backfillPending || again.Backfill != backfillRunning {
		t.Errorf("the three stands were %q, %q, and %q, want Running, Pending, and Running",
			first.Backfill, waiting.Backfill, again.Backfill)
	}
	if got := cluster.countRequests(http.MethodPost, "jobs"); got != 2 {
		t.Errorf("the passes created %d Jobs, want the first and the one past the wait", got)
	}
}

// A finished backfill drops the wait it built up, so a Catalog that names
// another server later stands its Job at once.
func TestAFinishedBackfillDropsItsWait(t *testing.T) {
	cluster := newFakeCluster()
	catalog := jellyfinCatalog()
	operator := testOperator(t, cluster)
	operator.backfillStands["house/house-catalog"] = cleanupStand{count: 4, next: testNow.Add(time.Hour)}
	held := backfillJobWith(catalog, JobStatus{Succeeded: 1})

	operator.standJellyfinBackfill(t.Context(), catalog, []Job{*held}, nil, testNow)

	if _, waiting := operator.backfillStands["house/house-catalog"]; waiting {
		t.Error("a finished backfill still holds a wait")
	}
}

// A Catalog that names no server drops the wait as well, because the pass
// that names one again is a first stand.
func TestACatalogWithNoServerDropsItsWait(t *testing.T) {
	cluster := newFakeCluster()
	catalog := housekeepingCatalog()
	operator := testOperator(t, cluster)
	operator.backfillStands["house/house-catalog"] = cleanupStand{count: 4, next: testNow.Add(time.Hour)}

	operator.standJellyfinBackfill(t.Context(), catalog, nil, nil, testNow)

	if _, waiting := operator.backfillStands["house/house-catalog"]; waiting {
		t.Error("a Catalog that names no server still holds a wait")
	}
}

// The paths the backfill Job is written and deleted through, so a test breaks
// one of them and reads what the pass reports.
const (
	backfillJobsPath = "/apis/batch/v1/namespaces/house/jobs"
	backfillJobPath  = backfillJobsPath + "/house-catalog-jellyfin-backfill"
)

// A write the API server refuses costs the pass its Job and nothing else: the
// backfill is still Pending, and the next pass stands it again.
func TestTheBackfillReportsAFailedCreate(t *testing.T) {
	cluster := newFakeCluster()
	cluster.broken[http.MethodPost+" "+backfillJobsPath] = http.StatusInternalServerError
	catalog := jellyfinCatalog()

	got := testOperator(t, cluster).standJellyfinBackfill(t.Context(), catalog, nil,
		[]*Pod{readyProgressPod(catalog)}, testNow)

	if got.Backfill != backfillPending {
		t.Errorf("backfill = %q, want %q", got.Backfill, backfillPending)
	}
}

// A create another writer got to first is a conflict, which is success: the
// next pass reads the Job that writer left.
func TestTheBackfillTakesAConflictAsSuccess(t *testing.T) {
	cluster := newFakeCluster()
	cluster.refuseCreate = true
	catalog := jellyfinCatalog()

	got := testOperator(t, cluster).standJellyfinBackfill(t.Context(), catalog, nil,
		[]*Pod{readyProgressPod(catalog)}, testNow)

	if got.Backfill != backfillRunning {
		t.Errorf("backfill = %q, want %q", got.Backfill, backfillRunning)
	}
}

// A delete the API server refuses is reported, and the pass reports the
// backfill the way it stands.
func TestTheBackfillReportsAFailedDelete(t *testing.T) {
	cases := []struct {
		name   string
		server string
		job    *JobStatus
		want   *CatalogJellyfinStatus
	}{
		{
			name: "a Catalog that names no server",
			job:  &JobStatus{Active: 1},
		},
		{
			name:   "a Job that gave up",
			server: testJellyfinURL,
			job:    &JobStatus{Failed: 3},
			want:   &CatalogJellyfinStatus{Server: testJellyfinURL, Backfill: backfillFailed},
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			cluster.broken[http.MethodDelete+" "+backfillJobPath] = http.StatusInternalServerError
			catalog := jellyfinCatalog()
			if one.server == "" {
				catalog.Spec.Jellyfin = nil
			}
			held := backfillJobWith(jellyfinCatalog(), *one.job)

			got := testOperator(t, cluster).standJellyfinBackfill(t.Context(), catalog,
				[]Job{*held}, nil, testNow)

			if want := jellyfinStatusJSON(t, one.want); jellyfinStatusJSON(t, got) != want {
				t.Errorf("status = %s, want %s", jellyfinStatusJSON(t, got), want)
			}
		})
	}
}

// The pass writes what the backfill says onto the Catalog's own status, so a
// person reads one object to see where it stands.
func TestReconcileCatalogsReportsTheBackfillOnTheCatalog(t *testing.T) {
	cluster := newFakeCluster()
	catalog := seedCatalog(cluster, "house-catalog", "house")
	catalog.Spec.Jellyfin = jellyfinCatalog().Spec.Jellyfin

	testOperator(t, cluster).reconcileCatalogs(t.Context(), oneNamespace("house", catalog), nil, nil, testNow)

	status := cluster.heldCatalog("house-catalog").Status.Jellyfin
	if status == nil {
		t.Fatal("the Catalog reports no backfill")
	}
	if status.Server != testJellyfinURL || status.Backfill != backfillPending {
		t.Errorf("status.jellyfin = %+v, want the server Pending on a store that is not up", status)
	}
}
