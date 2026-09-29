package main

// What these tests prove. A mark that lists several episodes, which Pick up
// here publishes, reaches Jellyfin as one write for each episode and each
// person, and leaves one sent record. An item Jellyfin already holds as
// played takes no write from a watched mark. A pass that failed on one
// episode sends the mark again, and the episodes that landed are left
// alone.

import (
	"encoding/json"
	"testing"
)

// The house of jellyfinFixture with two more episodes of the third season,
// so a list reaches three items.
func seasonFixture() *fakeJellyfin {
	fake := jellyfinFixture()
	for _, episode := range []struct {
		id     string
		number int
	}{{id: "item-series-3-3", number: 3}, {id: "item-series-3-4", number: 4}} {
		fake.items = append(fake.items, jellyfinItem{ID: episode.id, Type: "Episode",
			SeriesID: "item-series", ParentIndexNumber: 3, IndexNumber: episode.number})
	}
	return fake
}

// A watched list over the third season's episodes 3 to 5, pressed a minute
// before the fake clock reads.
func jellyfinListMark(t *testing.T, people []string) []byte {
	t.Helper()
	listed := []markedEpisode{}
	for episode := 3; episode <= 5; episode++ {
		listed = append(listed, markedEpisode{Season: 3, Episode: episode, Position: 2760, Duration: 2760})
	}
	payload, err := json.Marshal(titleMark{Mark: markWatched, Player: "living-room", People: people,
		Aliases: map[string]string{"tvdb": "3101"}, Episodes: listed, At: markPressed})
	if err != nil {
		t.Fatalf("building the list: %v", err)
	}
	return payload
}

// The items the fake server took a write for, in order.
func writtenItems(fake *fakeJellyfin) []string {
	items := []string{}
	for _, write := range fake.writes {
		items = append(items, write.user+"/"+write.item)
	}
	return items
}

// Each episode of the list is one write for each person at the screen,
// played at the end of the episode, and the list leaves one sent record.
func TestAListMarkWritesEveryEpisodeForEveryPerson(t *testing.T) {
	fake := seasonFixture()
	role, messages, _ := standJellyfinRole(t, fake)

	role.onMessage(markTopic("mark-living-room-1"), jellyfinListMark(t, []string{"person-a", "person-c"}))
	markPass(t, role)

	want := []string{
		"user-a/item-series-3-3", "user-c/item-series-3-3",
		"user-a/item-series-3-4", "user-c/item-series-3-4",
		"user-a/item-series-3-5", "user-c/item-series-3-5",
	}
	if got := writtenItems(fake); len(got) != len(want) || got[0] != want[0] || got[5] != want[5] {
		t.Errorf("writes = %v, want %v", got, want)
	}
	for _, write := range fake.writes {
		if !write.data.Played || write.data.PlaybackPositionTicks != jellyfinTicks(2760) {
			t.Errorf("write = %+v, want played at the end of the episode", write)
		}
	}
	message, record := messages.only(t)
	if message.topic != sentTopic("mark-living-room-1") || record.At != markPressed {
		t.Errorf("message = %+v, want the one record of the list", message)
	}
}

// A watched mark leaves an item Jellyfin already holds as played with no
// resume position, because the write would change only the date. An item
// played with a resume position, a person rewatching it in Jellyfin, takes
// the write. A cleared mark writes over a played item.
func TestAWatchedMarkSkipsAnItemJellyfinHoldsAsPlayed(t *testing.T) {
	for _, test := range []struct {
		name   string
		mark   string
		held   jellyfinUserData
		writes int
	}{
		{name: "played", mark: markWatched, held: jellyfinUserData{Played: true}, writes: 0},
		{name: "played with a resume position", mark: markWatched,
			held: jellyfinUserData{Played: true, PlaybackPositionTicks: 9_000_000_000}, writes: 1},
		{name: "not played", mark: markWatched, held: jellyfinUserData{}, writes: 1},
		{name: "played, and the mark clears it", mark: markCleared, held: jellyfinUserData{Played: true}, writes: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := jellyfinFixture()
			fake.userData = map[string]jellyfinUserData{"user-a/item-film": test.held}
			role, messages, _ := standJellyfinRole(t, fake)

			role.onMessage(markTopic("mark-living-room-1"), jellyfinMark(t, test.mark, []string{"person-a"}, 0))
			markPass(t, role)

			if len(fake.writes) != test.writes {
				t.Errorf("writes = %+v, want %d", fake.writes, test.writes)
			}
			if message, _ := messages.only(t); message.topic != sentTopic("mark-living-room-1") {
				t.Errorf("message = %+v, want the record of the mark", message)
			}
		})
	}
}

// A person who watched the earlier episodes in Jellyfin costs no write: the
// list writes only the episodes Jellyfin does not hold as played.
func TestAListWritesOnlyTheEpisodesJellyfinHasNotPlayed(t *testing.T) {
	fake := seasonFixture()
	fake.userData = map[string]jellyfinUserData{
		"user-a/item-series-3-3": {Played: true},
		"user-a/item-series-3-4": {Played: true},
	}
	role, _, _ := standJellyfinRole(t, fake)

	role.onMessage(markTopic("mark-living-room-1"), jellyfinListMark(t, []string{"person-a"}))
	markPass(t, role)

	if got := writtenItems(fake); len(got) != 1 || got[0] != "user-a/item-series-3-5" {
		t.Errorf("writes = %v, want the one episode Jellyfin has not played", got)
	}
}

// A write that failed on one episode leaves the whole list for the next
// pass, with no record. On that pass the episodes that landed read back
// the mark's own date and are left alone, and only the one that failed is
// written.
func TestAListThatFailedPartwayWritesOnlyTheRestOnTheNextPass(t *testing.T) {
	fake := seasonFixture()
	role, messages, _ := standJellyfinRole(t, fake)
	role.onMessage(markTopic("mark-living-room-1"), jellyfinListMark(t, []string{"person-a"}))

	fake.refuseItem = "item-series-3-4"
	markPass(t, role)
	if len(messages.held) != 0 {
		t.Fatalf("messages = %+v, want no record while an episode failed", messages.held)
	}
	fake.refuseItem = ""
	fake.writes = nil
	markPass(t, role)

	if got := writtenItems(fake); len(got) != 1 || got[0] != "user-a/item-series-3-4" {
		t.Errorf("writes = %v, want the episode that failed alone", got)
	}
	if message, _ := messages.only(t); message.topic != sentTopic("mark-living-room-1") {
		t.Errorf("message = %+v, want the record once every episode landed", message)
	}
}

// An episode Jellyfin does not hold is done with no write, and the rest of
// the list is written.
func TestAListSkipsAnEpisodeJellyfinDoesNotHold(t *testing.T) {
	fake := jellyfinFixture()
	role, messages, _ := standJellyfinRole(t, fake)

	role.onMessage(markTopic("mark-living-room-1"), jellyfinListMark(t, []string{"person-a"}))
	markPass(t, role)

	if got := writtenItems(fake); len(got) != 1 || got[0] != "user-a/item-series-3-5" {
		t.Errorf("writes = %v, want the one episode Jellyfin holds", got)
	}
	if message, _ := messages.only(t); message.topic != sentTopic("mark-living-room-1") {
		t.Errorf("message = %+v, want the record of the list", message)
	}
}

// A list is one press, and leaves one line for each person, with the
// counts, and not a line for each episode.
func TestAListLeavesOneLineForEachPerson(t *testing.T) {
	fake := seasonFixture()
	fake.userData = map[string]jellyfinUserData{"user-a/item-series-3-3": {Played: true}}
	role, _, logged := standJellyfinRole(t, fake)
	logged.Reset()

	role.onMessage(markTopic("mark-living-room-1"), jellyfinListMark(t, []string{"person-a"}))
	markPass(t, role)

	want := "library.liken.sh: wrote the mark mark-living-room-1 on 3 episodes of tvdb:3101 to jellyfin " +
		"for person person-a: 2 written, 1 already played, 0 played since the press, 0 not held\n"
	if got := logged.String(); got != want {
		t.Errorf("log = %q, want %q", got, want)
	}
}
