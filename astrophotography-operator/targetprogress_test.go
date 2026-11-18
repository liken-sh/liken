package main

// The promotion loop writes a target's progress as soon as the target
// exists, before any reservation takes it.

import (
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/conditions"
)

// lrgb is a target of six frames in two sequences.
func lrgb() astrophotography.Target {
	t := m42()
	t.Spec.Sequences = []astrophotography.Sequence{
		{Filter: "Luminance", Exposure: "300s", Count: 4},
		{Filter: "Red", Exposure: "300s", Count: 2},
	}
	return t
}

func TestANewTargetShowsItsProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putTarget(t, api, lrgb())
		startOperator(t, api)
		synctest.Wait()

		status := readTarget(api).Status
		wantSequences := []astrophotography.SequenceProgress{{Sequence: lrgb().Spec.Sequences[0]}, {Sequence: lrgb().Spec.Sequences[1]}}
		if status.Progress != "0/6" || !reflect.DeepEqual(status.Sequences, wantSequences) {
			t.Errorf("the status is %+v, want progress 0/6 and sequences %+v", status, wantSequences)
		}
		complete, _ := conditions.Find(status.Conditions, astrophotography.ConditionComplete)
		if complete.Status != conditions.False || complete.Reason != astrophotography.ReasonWorkLeft ||
			complete.Message != "0 frames of 6 are in the archive" {
			t.Errorf("Complete is %+v, want False with reason WorkLeft and message \"0 frames of 6 are in the archive\"", complete)
		}
		if got, want := targetEvents(api), []string{"Normal WorkLeft"}; !slices.Equal(got, want) {
			t.Errorf("the Events are %q, want %q", got, want)
		}
	})
}

// An edit of the sequences changes the number of frames wanted, and
// keeps each sequence's archived count.
func TestAnEditOfTheSequencesChangesTheProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putTarget(t, api, lrgb())
		startOperator(t, api)
		synctest.Wait()

		edited := readTarget(api)
		edited.Status.Sequences[0].Archived = 3
		edited.Spec.Sequences[1].Count = 5
		edited.Metadata.Generation++
		putTarget(t, api, edited)
		synctest.Wait()

		status := readTarget(api).Status
		if status.Progress != "3/9" || status.Sequences[1].Count != 5 || status.Sequences[0].Archived != 3 {
			t.Errorf("the status is %+v, want progress 3/9, a second sequence of count 5, and 3 frames archived in the first", status)
		}
	})
}

// A failed write of the progress posts no Event, and the loop retries
// it after its first backoff.
func TestAFailedWriteOfTheProgressIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		api.failStatus = 1
		putTarget(t, api, lrgb())
		startOperator(t, api)

		time.Sleep(firstRetry)
		synctest.Wait()

		if got := readTarget(api).Status.Progress; got != "0/6" {
			t.Errorf("the progress is %q, want 0/6", got)
		}
		if got, want := targetEvents(api), []string{"Normal WorkLeft"}; !slices.Equal(got, want) {
			t.Errorf("the Events are %q, want %q", got, want)
		}
	})
}
