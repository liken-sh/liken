package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// The work lists the close container publishes before it writes the
// finished run.

// A catalog whose trickplay gap holds a feature for each path given: a
// present video with a length and no sheets.
func trickplayGapCatalog(t *testing.T, paths ...string) *Catalog {
	t.Helper()
	catalog, _ := newSQLiteCatalog(t)
	seed := &walkResult{}
	for index, path := range paths {
		seed.files = append(seed.files, fileRow{Path: path, Library: "house/movies", Present: true,
			Type: fileTypeVideo, Role: fileRolePrimary, VideoCodec: "h264", DurationMs: 100000,
			SizeBytes: int64(100 + index)})
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	return catalog
}

// The close container of movies-walk-1 over that catalog, with trickplay
// listed, on the broker the dial given reaches.
func listingJob(t *testing.T, catalog *Catalog, dial func(context.Context) (net.Conn, error)) *closeRun {
	t.Helper()
	run, _ := closingJob(t, catalog)
	run.workLists, run.base, run.dial = []string{factTrickplay}, defaultTopicBase, dial
	return run
}

// The list is on the broker, count last, by the time the finished run is in
// the catalog, so the operator that reads the run finds the list.
func TestTheCloseContainerPublishesTheListBeforeTheRun(t *testing.T) {
	catalog := trickplayGapCatalog(t, "B Film (2001)/B Film (2001).mkv", "A Film (2000)/A Film (2000).mkv")
	broker := newRetainBroker(t)
	run := listingJob(t, catalog, broker.dial)
	done := make(chan error, 1)
	go func() { done <- run.runJob(t.Context()) }()

	confirmTheRun(t, catalog, workerEnrich, run.job)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	list := trickplayListOf("movies-walk-1")
	if count, _ := broker.retainedOn(list.countTopic(defaultTopicBase)); string(count) != "2" {
		t.Errorf("count = %q, want 2", count)
	}
	first, _ := broker.retainedOn(list.itemTopic(defaultTopicBase, 0))
	if !strings.Contains(string(first), `"path":"A Film (2000)/A Film (2000).mkv"`) {
		t.Errorf("index 0 = %s, want the first video by path", first)
	}
}

// An empty gap publishes no list.
func TestAnEmptyGapPublishesNoList(t *testing.T) {
	catalog := trickplayGapCatalog(t)
	broker := newRetainBroker(t)
	run := listingJob(t, catalog, broker.dial)
	done := make(chan error, 1)
	go func() { done <- run.runJob(t.Context()) }()

	confirmTheRun(t, catalog, workerEnrich, run.job)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if held := broker.retainedUnder(defaultTopicBase); len(held) != 0 {
		t.Errorf("the broker holds %v, want nothing", held)
	}
}

// A list the broker did not take is a failure in the run, so the operator
// reads why no worker started.
func TestAListThatDidNotPublishFailsTheRun(t *testing.T) {
	catalog := trickplayGapCatalog(t, "A Film (2000)/A Film (2000).mkv")
	run := listingJob(t, catalog, func(context.Context) (net.Conn, error) {
		return nil, errors.New("connection refused")
	})
	done := make(chan error, 1)
	go func() { done <- run.runJob(t.Context()) }()

	confirmTheRun(t, catalog, workerEnrich, run.job)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	finished := awaitRun(t, catalog, func(held libraryRun) bool { return !held.Finished.IsZero() })
	if !strings.Contains(finished.Failure, "the trickplay work list") ||
		!strings.Contains(finished.Failure, "connection refused") {
		t.Errorf("failure = %q, want the list's", finished.Failure)
	}
}

// The close container of a Library that runs a heavy fact names the fact and
// the broker, and a Library that runs none names neither.
func TestTheCloseContainerNamesTheListsAndTheBroker(t *testing.T) {
	cases := []struct {
		name    string
		library *Library
		lists   string
	}{
		{name: "trickplay on", library: trickplayMovies(), lists: factTrickplay},
		{name: "appearances on", library: spreadAppearances(1), lists: factAppearances},
		{name: "no heavy fact", library: studioMovies()},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			plan := libraryJob{mode: jobModeGaps, phases: servedPhases(one.library, providerSet{})[:1]}
			job := buildLibraryJob(one.library, providerSet{}, nil, plan, testJobImages, testJobBus, testNow)

			env := containerEnvironment(*jobContainer(job, closeMode))

			if env[libraryWorkListsVariable] != one.lists {
				t.Errorf("%s = %q, want %q", libraryWorkListsVariable, env[libraryWorkListsVariable], one.lists)
			}
			if wantBus := one.lists != ""; (env[busAddressVariable] == testBusAddress) != wantBus ||
				(env[topicBaseVariable] == defaultTopicBase) != wantBus {
				t.Errorf("env = %v, want the broker named: %v", env, wantBus)
			}
		})
	}
}
