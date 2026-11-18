package main

import (
	"testing"
	"testing/synctest"
	"time"
)

// watching opens every watch against the fake API server until the
// test ends, and returns the caches and the change notifier that they notify.
func watching(t *testing.T, api *fakeAPI) (*stores, *changeNotifier) {
	t.Helper()
	changed := newChangeNotifier()
	return startWatches(t.Context(), api.watcher(t), changed, newMetrics("dev")), changed
}

const (
	targetsPath      = "/apis/astrophotography.liken.sh/v1alpha1/targets"
	reservationsPath = "/apis/observatory.liken.sh/v1alpha1/reservations"
	camerasPath      = "/apis/observatory.liken.sh/v1alpha1/cameras"
	podsPath         = "/api/v1/pods"
	deploymentsPath  = "/apis/apps/v1/deployments"
	jobsPath         = "/apis/batch/v1/jobs"
)

func TestEachWatchSendsItsSelectors(t *testing.T) {
	cases := []struct {
		path, labelSelector, fieldSelector string
	}{
		{targetsPath, "", ""},
		{solversPath, "", ""},
		{reservationsPath, "", "spec.holder=astrophotography-operator"},
		{camerasPath, "", ""},
		{podsPath, "app.kubernetes.io/managed-by=astrophotography-operator", ""},
		{deploymentsPath, "app.kubernetes.io/managed-by=astrophotography-operator", ""},
		{jobsPath, "app.kubernetes.io/managed-by=astrophotography-operator", ""},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				api := startFakeAPI(t)
				watching(t, api)
				synctest.Wait()
				requests := api.requested(c.path)
				if len(requests) == 0 {
					t.Fatalf("the operator sent no request to %s", c.path)
				}
				query := requests[0].Query()
				if query.Get("labelSelector") != c.labelSelector || query.Get("fieldSelector") != c.fieldSelector {
					t.Errorf("the watch of %s sent selectors %q and %q, want %q and %q",
						c.path, query.Get("labelSelector"), query.Get("fieldSelector"), c.labelSelector, c.fieldSelector)
				}
			})
		})
	}
}

func TestEveryWatchSyncs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		s, _ := watching(t, api)
		synctest.Wait()
		if !s.synced() {
			t.Error("a watch has not finished its first list, want every watch synced")
		}
	})
}

// The operator's watches sync when observatory-operator's CRDs do not
// exist. Each kind that returns 404 costs two requests, a streaming
// list and then a plain list, and client-go's default rate limit of 5
// requests a second after a burst of 10 delays them, so the test
// advances the fake clock until every watch has listed.
func TestTheWatchesSyncWithNoObservatoryOperator(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		for _, w := range watches() {
			if w.fromObservatory {
				api.drop(collectionOf(w).path)
			}
		}
		s, _ := watching(t, api)
		waitUntil(t, s.synced)
	})
}

func TestAChangeNotifiesEveryWaitingLoop(t *testing.T) {
	m42 := func(holder string) map[string]any {
		return object("observatory", "m42", map[string]any{labelManagedBy: managedBy}, map[string]any{"holder": holder})
	}
	cases := []struct {
		name   string
		path   string
		before func(api *fakeAPI, path string)
		change func(api *fakeAPI, path string)
	}{
		{"a new Target", targetsPath,
			func(*fakeAPI, string) {},
			func(api *fakeAPI, path string) { api.put(path, m42(managedBy)) }},
		{"a Reservation of this operator that changes", reservationsPath,
			func(api *fakeAPI, path string) { api.put(path, m42(managedBy)) },
			func(api *fakeAPI, path string) {
				changed := m42(managedBy)
				changed["status"] = map[string]any{"phase": "Ready"}
				api.put(path, changed)
			}},
		{"a pod of this operator that is deleted", podsPath,
			func(api *fakeAPI, path string) { api.put(path, m42(managedBy)) },
			func(api *fakeAPI, path string) { api.remove(path, "observatory", "m42") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				api := startFakeAPI(t)
				c.before(api, c.path)
				_, changed := watching(t, api)
				synctest.Wait()
				notified := changed.wait()
				c.change(api, c.path)
				synctest.Wait()
				select {
				case <-notified:
				case <-time.After(time.Second):
					t.Error("the change notification did not fire within a second of the change")
				}
			})
		})
	}
}
