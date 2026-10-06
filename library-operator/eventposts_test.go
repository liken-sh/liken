package main

// What these tests read: the Events the operator posts. Each condition
// transition of a Library, a Catalog, or a MetadataProvider posts one
// Event with the condition's reason and message, after the status write
// lands. A Job the pass creates, a walk that ends, and a stranded copy
// the heal deletes each post one Event too. Each test runs in a synctest
// bubble and waits for the recorder's queue to drain before it reads.

import (
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

// posted is the part of an Event a person reads in kubectl describe.
type posted struct {
	Type, Reason, Message string
}

// postedAbout waits for the recorder's queue to drain, and answers the
// Events about one object in the order the operator posted them.
func postedAbout(cluster *fakeCluster, kind, name string) []posted {
	synctest.Wait()
	var out []posted
	for _, event := range cluster.recorded.About(kind, name) {
		out = append(out, posted{event.Type, event.Reason, event.Message})
	}
	return out
}

// readyStatus is a Library status that carries only the Ready condition.
func readyStatus(status ConditionStatus, reason, message string) LibraryStatus {
	return LibraryStatus{Conditions: SetCondition(nil, Condition{
		Type: conditionReady, Status: status, Reason: reason, Message: message,
	}, testNow)}
}

// Each change of the Ready reason posts one Event. A new message under the
// same reason posts none, and a fault the person must act on is a Warning.
func TestALibraryPostsEachTransitionOfReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		library := boundHouse(cluster)
		operator := testOperator(t, cluster)
		steps := []LibraryStatus{
			readyStatus(ConditionFalse, reasonNoReport, "the reporter has not reported this library yet"),
			readyStatus(ConditionFalse, reasonJobFailed, "the Job movies-walk-2 failed"),
			readyStatus(ConditionFalse, reasonJobFailed, "the Job movies-walk-3 failed"),
			readyStatus(ConditionTrue, reasonReady, "the reporter reports 412 titles"),
		}

		for _, step := range steps {
			if err := writeLibraryStatus(t.Context(), operator.recorder, operator.client, nil, library,
				func(*Library) LibraryStatus { return step }); err != nil {
				t.Fatal(err)
			}
		}

		want := []posted{
			{"Normal", reasonNoReport, "the reporter has not reported this library yet"},
			{"Warning", reasonJobFailed, "the Job movies-walk-2 failed"},
			{"Normal", reasonReady, "the reporter reports 412 titles"},
		}
		assertPosted(t, postedAbout(cluster, "Library", "movies"), want)
	})
}

// A status write the API server refuses posts nothing, so the Event never
// reports a condition that kubectl get does not show.
func TestARefusedLibraryStatusWritePostsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		library := boundHouse(cluster)
		cluster.broken[http.MethodPut+" "+libraryPath("house", "movies")+"/status"] = http.StatusInternalServerError
		operator := testOperator(t, cluster)

		err := writeLibraryStatus(t.Context(), operator.recorder, operator.client, nil, library, func(*Library) LibraryStatus {
			return readyStatus(ConditionFalse, reasonJobFailed, "the Job movies-walk-2 failed")
		})

		if err == nil {
			t.Fatal("the refused write answered no error")
		}
		assertPosted(t, postedAbout(cluster, "Library", "movies"), nil)
	})
}

// The operator reads the end of a walk from the run the reporter
// publishes, and posts it once: the titles it found, or the failure.
func TestTheEndOfAWalkPostsOneEvent(t *testing.T) {
	started := testNow.Add(-time.Hour)
	finished := testNow.Add(-time.Minute)
	walk := libraryStatusRun{Worker: workerScan, Job: "movies-walk-2", Started: started}
	ended := walk
	ended.Finished = finished
	failed := ended
	failed.Failure = "the root /movies does not exist"
	cases := []struct {
		name   string
		before []libraryStatusRun
		after  []libraryStatusRun
		want   []posted
	}{
		{name: "a walk that runs", after: []libraryStatusRun{walk}},
		{name: "a walk that finished", before: []libraryStatusRun{walk}, after: []libraryStatusRun{ended},
			want: []posted{{"Normal", reasonScanCompleted, "the walk of the Job movies-walk-2 found 412 titles in 400 files"}}},
		{name: "a walk that failed", before: []libraryStatusRun{walk}, after: []libraryStatusRun{failed},
			want: []posted{{"Warning", reasonScanFailed, "the walk of the Job movies-walk-2 failed: the root /movies does not exist"}}},
		{name: "a walk the status already holds", before: []libraryStatusRun{ended}, after: []libraryStatusRun{ended}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := newFakeCluster()
				library := boundHouse(cluster)
				library.Status.Runs = one.before
				operator := testOperator(t, cluster)

				err := writeLibraryStatus(t.Context(), operator.recorder, operator.client, nil, library, func(*Library) LibraryStatus {
					return LibraryStatus{Titles: 412, Files: 400, Runs: one.after, LastWalk: finished}
				})

				if err != nil {
					t.Fatal(err)
				}
				assertPosted(t, postedAbout(cluster, "Library", "movies"), one.want)
			})
		})
	}
}

// A Job's pod that has not started posts a Warning with the words of the
// kubelet's own Warning about the pod, which the operator lists from the
// same events collection it posts to.
func TestAJobThatHasNotStartedPostsTheKubeletsWords(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		library := boundHouse(cluster)
		late := testNow.Add(-jobStartGrace - time.Minute)
		jobs := []Job{moviesJob("movies-walk-2", late)}
		pods := []Pod{jobPod("movies-walk-2", podPending, placedAt(late))}
		cluster.events = []Event{
			warningAbout("movies-walk-2-pod", "GitVolumeRefused", "readOnly: mount the claim read-only", testNow),
		}

		if err := testOperator(t, cluster).reconcile(t.Context(), library, standingCatalog(),
			jobs, pods, nil, testNow); err != nil {
			t.Fatal(err)
		}

		var got []posted
		for _, one := range postedAbout(cluster, "Library", "movies") {
			if one.Reason == reasonJobNotStarted {
				got = append(got, one)
			}
		}
		want := []posted{{"Warning", reasonJobNotStarted, "the pod movies-walk-2-pod of the Job movies-walk-2 " +
			"has not started: GitVolumeRefused: readOnly: mount the claim read-only"}}
		assertPosted(t, got, want)
	})
}

// The Job the pass creates posts one Event that names it and the cause.
func TestThePassPostsTheJobItCreates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		library := boundHouse(cluster)
		operator := testOperator(t, cluster)

		if err := operator.runLibrary(t.Context(), library, nil, nil, providerSet{}, testNow); err != nil {
			t.Fatal(err)
		}

		created := cluster.heldJobs()
		if len(created) != 1 {
			t.Fatalf("jobs = %+v, want one", created)
		}
		got := postedAbout(cluster, "Library", "movies")
		if len(got) != 1 || got[0].Type != "Normal" || got[0].Reason != reasonJobCreated {
			t.Fatalf("Events = %+v, want one Normal %s", got, reasonJobCreated)
		}
		if want := "created the Job " + created[0].Metadata.Name + ", "; got[0].Message[:len(want)] != want {
			t.Errorf("message = %q, want it to start %q", got[0].Message, want)
		}
	})
}

// Every Catalog of a namespace that holds two posts ManyCatalogs as a
// Warning, because a person must delete one of them.
func TestEveryCatalogOfANamespaceWithTwoPostsAWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		first := seedCatalog(cluster, "house", "house")
		second := seedCatalog(cluster, "second", "house")

		testOperator(t, cluster).reconcileCatalogs(t.Context(),
			oneNamespace("house", first, second), nil, nil, testNow)

		for _, name := range []string{"house", "second"} {
			got := postedAbout(cluster, "Catalog", name)
			if len(got) != 1 || got[0].Type != "Warning" || got[0].Reason != catalogReasonManyCatalogs {
				t.Errorf("Events about %s = %+v, want one Warning %s", name, got, catalogReasonManyCatalogs)
			}
		}
	})
}

// A stranded copy the heal deletes posts a Warning on the Catalog that
// owns it, because the pod it names is gone by the time a person looks.
func TestAHealedCopyPostsAWarningOnItsCatalog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		catalog := seedCatalog(cluster, "house", "house")
		stranded := copyOnNode(cluster, catalog, 0, "nuc-2")
		cluster.nodes["nuc-2"] = nodeAt("nuc-2", "False", 11*time.Minute)

		testOperator(t, cluster).reconcileCatalogs(t.Context(),
			oneNamespace("house", catalog), []Pod{*stranded}, nil, testNow)

		var got []posted
		for _, one := range postedAbout(cluster, "Catalog", "house") {
			if one.Reason == reasonStoreCopyHealed {
				got = append(got, one)
			}
		}
		want := []posted{{"Warning", reasonStoreCopyHealed, "deleted the copy house-catalog-0 and its claim, " +
			"because its node nuc-2 has not been Ready for more than " + strandedNodeGrace.String()}}
		assertPosted(t, got, want)
	})
}

// A provider whose check changes its verdict posts the new verdict once.
// A check that keeps the verdict posts nothing.
func TestAProviderPostsAChangeOfVerdict(t *testing.T) {
	cases := []struct {
		name  string
		prior []Condition
		want  []posted
	}{
		{name: "a provider that was Reachable",
			prior: []Condition{{Type: conditionReady, Status: ConditionTrue, Reason: reasonReachable, Message: "the provider answered"}},
			want:  []posted{{"Warning", reasonNoSecret, "the Secret tmdb-key does not exist in namespace house"}}},
		{name: "a provider that had no Secret already",
			prior: []Condition{{Type: conditionReady, Status: ConditionFalse, Reason: reasonNoSecret, Message: "an older message"}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := newFakeCluster()
				provider := seedProvider(cluster, "tmdb", "house", factIdentity)
				provider.Status.Conditions = one.prior

				testOperator(t, cluster).checkProviders(t.Context(), []MetadataProvider{*provider}, testNow)

				assertPosted(t, postedAbout(cluster, "MetadataProvider", "tmdb"), one.want)
			})
		})
	}
}

func assertPosted(t *testing.T, got, want []posted) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Events = %+v, want %+v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("Event %d = %+v, want %+v", index, got[index], want[index])
		}
	}
}
