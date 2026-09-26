package main

// These tests read the lines the progress half leaves: a Person the operator
// holds, a Person whose progress a delete removes, and a Play whose last
// position the store recorded. Each prints once across passes.

import (
	"testing"
)

// A Person as a pass meets it: new, or deleting with the finalizer held.
func deletingPerson(cluster *fakeCluster) {
	person := seedPerson(cluster, "person-a")
	person.Metadata.Finalizers = []string{progressFinalizer}
	person.Metadata.DeletionTimestamp = "2026-09-06T21:14:02Z"
}

func TestAPersonLeavesOneLinePerStepAcrossPasses(t *testing.T) {
	cases := []struct {
		name      string
		arrange   func(cluster *fakeCluster)
		forgotten bool
		want      string
	}{
		{
			name:    "a new Person",
			arrange: func(c *fakeCluster) { seedPerson(c, "person-a") },
			want:    "person person-a: holding the finalizer " + progressFinalizer + ", so a delete waits for the progress stores",
		},
		{
			name:    "a deleting Person",
			arrange: deletingPerson,
			want:    "person person-a is deleting: asked the progress stores of 1 namespace to forget the person's progress",
		},
		{
			name:      "a Person every store forgot",
			arrange:   deletingPerson,
			forgotten: true,
			want:      "person person-a: released the finalizer, because the progress stores of 1 namespace forgot the person's progress",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			operator, cluster, logged := loggingHouse(t)
			c.arrange(cluster)
			if c.forgotten {
				publishForgotten(operator, "person-a", testLibraryNamespace)
			}

			operator.pass()
			operator.pass()

			wantOneLine(t, logged, c.want)
		})
	}
}

// A Play the store recorded the end of is released once, and the line names
// it by its uid and its work, never by its name, which carries the title.
func TestARecordedPlayLeavesOneLine(t *testing.T) {
	operator, cluster, logged := loggingHouse(t)
	play := testPlay("den-some-film")
	play.Metadata.UID = "play-uid-1"
	play.Metadata.Finalizers = []string{progressFinalizer}
	play.Metadata.DeletionTimestamp = "2026-09-06T21:14:02Z"
	play.Metadata.Annotations = map[string]string{aliasAnnotationPrefix + "tmdb": "1001"}
	play.Status = PlayStatus{Phase: playPhaseFinished, Item: 1, Position: "1:38:12"}
	cluster.plays = append(cluster.plays, play)
	publishRecorded(operator, "den-some-film", playRecorded{
		Item: 1, Position: "1:38:12", Ended: true, At: "2026-09-06T21:14:02Z",
	})

	operator.pass()
	operator.pass()

	wantOneLine(t, logged, "released the Play with uid play-uid-1 of tmdb:1001, "+
		"because the progress store recorded its end at item 1, position 1:38:12")
	if found := linesWith(logged, "some-film"); found != nil {
		t.Errorf("a line names the Play by its name: %q", found)
	}
}

// A Play in a namespace with no progress store is released with that reason.
func TestAPlayNoStoreRecordsLeavesOneLine(t *testing.T) {
	operator, cluster, logged := loggingHouse(t)
	play := testPlay("den-some-film")
	play.Metadata.UID = "play-uid-1"
	play.Metadata.Finalizers = []string{progressFinalizer}
	cluster.plays = append(cluster.plays, play)
	delete(cluster.catalogs, "house")

	operator.pass()

	wantOneLine(t, logged, "released the Play with uid play-uid-1", "because namespace house holds no progress store")
}
