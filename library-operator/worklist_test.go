package main

import (
	"slices"
	"testing"
)

// A work list on the broker: published by one session, read back one index
// at a time by others, and cleared.

// The list of the series Library's appearances after the Job series-walk-1.
var testWorkList = workList{namespace: "house", library: "series", fact: factAppearances, run: "series-walk-1"}

// Three videos of a gap, in the order the gap query returns them.
func threeVideos() []workItem {
	return []workItem{
		{Path: "Harbour Lights/Season 01/Harbour Lights - S01E01.mkv", Size: 101, DurationMs: 2700000},
		{Path: "Harbour Lights/Season 01/Harbour Lights - S01E02.mkv", Size: 102, DurationMs: 2710000},
		{Path: "Quiet Field/Season 01/Quiet Field - S01E01.mkv", Size: 103, DurationMs: 1500000},
	}
}

// The broker with the three videos published, by a session that has closed.
func publishedList(t *testing.T) *retainBroker {
	t.Helper()
	broker := newRetainBroker(t)
	session, err := openBusSession(t.Context(), broker.dial, "close-series-walk-1")
	if err != nil {
		t.Fatal(err)
	}
	defer session.close()
	if err := publishWorkList(session, defaultTopicBase, testWorkList, threeVideos()); err != nil {
		t.Fatal(err)
	}
	return broker
}

// Each index of the list reads back the video at that place in the gap, in
// a session of its own, after the publisher has gone.
func TestEachIndexReadsItsOwnVideo(t *testing.T) {
	broker := publishedList(t)

	for index, want := range threeVideos() {
		item, found, err := readWorkItem(testSession(t, broker), defaultTopicBase, testWorkList, index)

		if err != nil || !found || item != want {
			t.Errorf("index %d = %+v, %v, %v, want %+v", index, item, found, err, want)
		}
	}
}

// The count goes out after every video, so a reader that sees the count
// finds every video.
func TestTheCountGoesOutLast(t *testing.T) {
	broker := publishedList(t)

	count := slices.Index(broker.published, testWorkList.countTopic(defaultTopicBase))
	if count != 3 {
		t.Errorf("published %v, want the three videos and then the count", broker.published)
	}
	if held, _ := broker.retainedOn(testWorkList.countTopic(defaultTopicBase)); string(held) != "3" {
		t.Errorf("count = %q, want 3", held)
	}
}

// An index the broker holds nothing for, past the list or after a broker
// restart, reads as no video.
func TestAMissingIndexReadsAsNoVideo(t *testing.T) {
	broker := publishedList(t)

	_, found, err := readWorkItem(testSession(t, broker), defaultTopicBase, testWorkList, 3)

	if err != nil || found {
		t.Errorf("index 3 = %v, %v, want no video and no error", found, err)
	}
}

// A message that is not a video is an error and not an absence, so the pod
// fails, and the person who reads its log finds the payload.
func TestAnItemThatIsNotAVideoIsAnError(t *testing.T) {
	broker := newRetainBroker(t)
	broker.retain(testWorkList.itemTopic(defaultTopicBase, 0), []byte(`{"size":3}`))

	if _, _, err := readWorkItem(testSession(t, broker), defaultTopicBase, testWorkList, 0); err == nil {
		t.Error("read a payload with no path as a video")
	}
}

// A clear leaves nothing of the list on the broker.
func TestAClearRemovesEveryTopicOfTheList(t *testing.T) {
	broker := publishedList(t)

	if err := clearWorkList(testSession(t, broker), defaultTopicBase, testWorkList, 3); err != nil {
		t.Fatal(err)
	}

	if held := broker.retainedUnder(testWorkList.prefix(defaultTopicBase)); len(held) != 0 {
		t.Errorf("the broker still holds %v", held)
	}
}
