package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A pass that finds everything standing reads the claims, the volumes, and
// the pods it stands from one list of each, and never one object by name.
// A read by name per object costs one request per claim and per pod on
// every pass, which is most of what an idle operator sends.
func TestAPassReadsTheObjectsItStandsFromOneListOfEach(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	seedPlayer(cluster, "den", testLibraryNamespace, screenController)
	operator := testOperator(t, cluster)
	// The first pass creates what the second finds standing.
	operator.pass()
	before := len(cluster.requestLines())

	operator.pass()

	for _, kind := range []string{"persistentvolumeclaims", "persistentvolumes", "pods"} {
		if got := countObjectReads(cluster.requestLines()[before:], kind); got != 0 {
			t.Errorf("the pass read %d %s by name, want none", got, kind)
		}
	}
}

// Without the claims, the volumes, or the pods, a pass cannot tell what
// stands from what it must create, so a list that fails ends the pass
// before it reconciles a Library.
func TestAPassThatCannotListWhatItStandsEnds(t *testing.T) {
	for _, path := range []string{claimsAllPath, volumesPath, podsAllPath + "?labelSelector=" + url.QueryEscape(stoodPodsSelector)} {
		t.Run(path, func(t *testing.T) {
			cluster := newFakeCluster()
			boundHouse(cluster)
			cluster.broken[path] = http.StatusInternalServerError

			testOperator(t, cluster).pass()

			if cluster.heldLibrary("movies").Status.Conditions != nil {
				t.Error("the pass reconciled a Library it could not read the storage of")
			}
		})
	}
}

// The GETs of one object of a kind, which a path with a name after the
// kind is.
func countObjectReads(lines []string, kind string) int {
	count := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "GET ") && strings.Contains(line, "/"+kind+"/") {
			count++
		}
	}
	return count
}

// The request lines the cluster has served, as a copy.
func (f *fakeCluster) requestLines() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]string(nil), f.requests...)
}
