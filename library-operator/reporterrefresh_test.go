package main

// What these tests prove: the operator publishes each Library's refresh
// times on the bus, the reporter counts each gap with the refresh times of
// its Library, and a change on the bus republishes the report with no restart
// of the catalog pod.

import (
	"encoding/json"
	"testing"
	"testing/synctest"
	"time"
)

// A catalog whose credits gap holds two titles, and a third title whose
// credits attempt is an hour old, which a refresh after that attempt reopens.
func catalogWithACreditsAttempt(t *testing.T) (*Catalog, time.Time) {
	t.Helper()
	catalog, _ := newSQLiteCatalog(t)
	seedNFOFactRows(t, catalog)
	attempted := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	if _, err := catalog.UpsertCredits(t.Context(), []creditRow{{
		Library: "house/movies", Item: "movie:tmdb:2", Billing: 0,
		Name: "Nora Vance", Contributor: ".contributors/no/nora-vance",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.UpsertAttempts(t.Context(), []attemptRow{{
		Library: "house/movies", Item: "movie:tmdb:2", Fact: factCredits,
		At: attempted.Unix(), Result: attemptFound, Provider: "tmdb",
	}}); err != nil {
		t.Fatal(err)
	}
	return catalog, attempted
}

func TestTheReportCountsEachGapWithTheLibrarysRefreshTimes(t *testing.T) {
	catalog, attempted := catalogWithACreditsAttempt(t)
	cases := []struct {
		name    string
		refresh map[string]refreshTimes
		want    int
	}{
		{name: "no refresh", want: 2},
		{name: "a refresh of another library",
			refresh: map[string]refreshTimes{"studio/movies": {factCredits: attempted.Add(time.Minute)}}, want: 2},
		{name: "a refresh earlier than the attempt",
			refresh: map[string]refreshTimes{"house/movies": {factCredits: attempted.Add(-time.Minute)}}, want: 2},
		{name: "a refresh later than the attempt",
			refresh: map[string]refreshTimes{"house/movies": {factCredits: attempted.Add(time.Minute)}}, want: 3},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			report := &reporter{catalog: catalog, published: map[string]libraryReport{}, refresh: test.refresh}

			built, err := report.buildReport(t.Context(), "house/movies")
			if err != nil {
				t.Fatal(err)
			}

			if built.Gaps[factCredits] != test.want {
				t.Errorf("credits gap = %d, want %d", built.Gaps[factCredits], test.want)
			}
		})
	}
}

// The reporter subscribes to the refresh times of its own namespace, and a
// refresh that arrives, or one that is cleared, republishes the report.
func TestARefreshOnTheBusRepublishesTheReport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, attempted := catalogWithACreditsAttempt(t)
		_, accepted, _ := servingReporter(t, catalog)
		broker := waitForBroker(t, accepted)
		if got := waitForString(t, broker.subs); got != libraryRefreshFilter(defaultTopicBase, "house") {
			t.Errorf("subscription = %q, want the refresh times of the namespace", got)
		}
		topic := libraryStatusTopic(defaultTopicBase, "house", "movies")
		waitForReport(t, broker, topic, func(held libraryReport) bool { return held.Gaps[factCredits] == 2 })

		refresh, _ := json.Marshal(refreshTimes{factCredits: attempted.Add(time.Minute)})
		broker.push(libraryRefreshTopic(defaultTopicBase, "house", "movies"), refresh)
		waitForReport(t, broker, topic, func(held libraryReport) bool { return held.Gaps[factCredits] == 3 })

		broker.push(libraryRefreshTopic(defaultTopicBase, "house", "movies"), nil)
		waitForReport(t, broker, topic, func(held libraryReport) bool { return held.Gaps[factCredits] == 2 })
	})
}

// The operator publishes the refresh times of each fact a Library names,
// retained, so a reporter that starts later reads them. The walk is not a
// gap, so it does not travel.
func TestAPassPublishesTheRefreshTimesOfALibrary(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	library.Spec.Refresh = map[string]time.Time{factCredits: testNow, refreshWalk: testNow}
	operator, broker := operatorOnTheBus(t, cluster)

	operator.pass()

	message := waitForTopic(t, broker, libraryRefreshTopic(defaultTopicBase, "house", "movies"))
	if !message.retained {
		t.Error("the refresh times were not retained")
	}
	sent := parseRefresh(string(message.payload))
	if !sent[factCredits].Equal(testNow) {
		t.Errorf("refresh = %v, want the credits time", sent)
	}
	if _, held := sent[refreshWalk]; held {
		t.Errorf("refresh = %v, want no walk", sent)
	}
}
