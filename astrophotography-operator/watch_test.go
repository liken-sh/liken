package main

// The watches against the fake API server, through the real reflector:
// what each watch asks the API server for, and the wake of the loops on
// each change.

import (
	"testing"
	"testing/synctest"
	"time"
)

// watching opens every watch against the fake API server until the
// test ends, and answers the stores and the bell they ring.
func watching(t *testing.T, api *fakeAPI) (*stores, *bell) {
	t.Helper()
	changed := newBell()
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

// Each watch reads only what the operator owns or holds: the
// Reservations whose holder is this operator, and the objects that
// carry its label. The other kinds are read whole.
func TestEachWatchAsksForItsCollection(t *testing.T) {
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
					t.Fatal("no request")
				}
				query := requests[0].Query()
				if query.Get("labelSelector") != c.labelSelector || query.Get("fieldSelector") != c.fieldSelector {
					t.Errorf("selectors %q and %q, want %q and %q",
						query.Get("labelSelector"), query.Get("fieldSelector"), c.labelSelector, c.fieldSelector)
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
			t.Error("a watch has not read its collection")
		}
	})
}

// A cluster can run this operator before observatory-operator's CRDs
// exist. Its kinds then answer 404, and the operator's own watches
// still read their collections. Each kind that answers 404 costs two
// reads, a streaming list and a list, and client-go's rate limit of 5
// requests a second after a burst of 10 spaces them, so the test lets
// the clock run until every watch has read.
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

// The loops wait on the bell, so each change to a collection rings it:
// an object that arrives, one that changes, and one that goes.
func TestAChangeRingsTheBell(t *testing.T) {
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
		{"a pod of this operator that goes", podsPath,
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
				rang := changed.wait()
				c.change(api, c.path)
				synctest.Wait()
				select {
				case <-rang:
				case <-time.After(time.Second):
					t.Error("the bell did not ring")
				}
			})
		})
	}
}
