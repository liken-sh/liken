package main

// What these tests prove. A mark holds one episode's position, and
// Jellyfin holds one position for a file of several episodes. So a mark on
// an episode of a double is written at that episode's seconds in the file:
// watched on the first episode as the second episode's start, watched on
// the last as played at the end of the file, and cleared as unplayed at the
// cleared episode's start. A list that names several episodes of one item
// makes one write for that item, and the echo record holds the file
// position the role wrote.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// A mark on one episode of the double in jellyfinDoubleFixture: season 1,
// episodes 1 and 2, a file of 2,640 seconds.
func doubleEpisodeMark(t *testing.T, kind string, episode, position, duration int) []byte {
	t.Helper()
	payload, err := json.Marshal(titleMark{
		Mark: kind, Player: "living-room", People: []string{"person-a"},
		Aliases: map[string]string{"tmdb": "2101"}, Season: 1, Episode: episode,
		Position: position, Duration: duration, At: markPressed})
	if err != nil {
		t.Fatalf("building the mark: %v", err)
	}
	return payload
}

// The double with no run time, so the role takes the mark's duration of
// one episode times the episodes the item holds.
func doubleWithNoRunTime() *fakeJellyfin {
	fake := jellyfinDoubleFixture()
	for index := range fake.items {
		if fake.items[index].ID == "item-double" {
			fake.items[index].RunTimeTicks = 0
		}
	}
	return fake
}

// One write of the double, as the fake server records it: the position in
// seconds of the file, and the played state where the write states one.
func doubleWrite(seconds int, played *bool) fakeJellyfinWrite {
	data := jellyfinUserData{PlaybackPositionTicks: jellyfinTicks(seconds),
		LastPlayedDate: time.Unix(markPressed, 0).UTC().Format(time.RFC3339)}
	if played != nil {
		data.Played = *played
	}
	return fakeJellyfinWrite{item: "item-double", user: "user-a", data: data, stated: played != nil}
}

func TestAMarkOnAnEpisodeOfADoubleIsWrittenAtItsSecondsInTheFile(t *testing.T) {
	played, unplayed := true, false
	for _, test := range []struct {
		name     string
		fake     func() *fakeJellyfin
		held     jellyfinUserData
		mark     string
		episode  int
		position int
		duration int
		want     []fakeJellyfinWrite
	}{
		{name: "watched on the first episode resumes at the second",
			fake: jellyfinDoubleFixture, mark: markWatched, episode: 1, position: 1320, duration: 1320,
			want: []fakeJellyfinWrite{doubleWrite(1320, nil)}},
		{name: "watched on the first episode leaves a played item played",
			fake: jellyfinDoubleFixture, held: jellyfinUserData{Played: true}, mark: markWatched, episode: 1,
			position: 1320, duration: 1320, want: []fakeJellyfinWrite{doubleWrite(1320, nil)}},
		{name: "watched on the last episode is played at the end of the file",
			fake: jellyfinDoubleFixture, mark: markWatched, episode: 2, position: 1320, duration: 1320,
			want: []fakeJellyfinWrite{doubleWrite(2640, &played)}},
		{name: "watched on the last episode skips an item played with no position",
			fake: jellyfinDoubleFixture, held: jellyfinUserData{Played: true}, mark: markWatched, episode: 2,
			position: 1320, duration: 1320, want: nil},
		{name: "cleared on the first episode is unplayed at the start",
			fake: jellyfinDoubleFixture, mark: markCleared, episode: 1, duration: 1320,
			want: []fakeJellyfinWrite{doubleWrite(0, &unplayed)}},
		{name: "cleared on the second episode is unplayed at its start",
			fake: jellyfinDoubleFixture, mark: markCleared, episode: 2, duration: 1320,
			want: []fakeJellyfinWrite{doubleWrite(1320, &unplayed)}},
		{name: "with no run time, the file is the mark's duration times the episodes",
			fake: doubleWithNoRunTime, mark: markWatched, episode: 1, position: 1300, duration: 1300,
			want: []fakeJellyfinWrite{doubleWrite(1300, nil)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := test.fake()
			fake.userData = map[string]jellyfinUserData{"user-a/item-double": test.held}
			role, _, _ := standJellyfinRole(t, fake)

			role.onMessage(markTopic("mark-living-room-1"),
				doubleEpisodeMark(t, test.mark, test.episode, test.position, test.duration))
			markPass(t, role)

			if !reflect.DeepEqual(fake.writes, test.want) {
				t.Errorf("writes = %+v, want %+v", fake.writes, test.want)
			}
		})
	}
}

// A Pick up here list over the double, with each episode's own mark.
func doubleListMark(t *testing.T, first, second string) []byte {
	t.Helper()
	payload, err := json.Marshal(titleMark{Mark: markWatched, Player: "living-room", People: []string{"person-a"},
		Aliases: map[string]string{"tmdb": "2101"}, At: markPressed, Episodes: []markedEpisode{
			{Mark: first, Season: 1, Episode: 1, Position: 1320, Duration: 1320},
			{Mark: second, Season: 1, Episode: 2, Position: 1320, Duration: 1320},
		}})
	if err != nil {
		t.Fatalf("building the list: %v", err)
	}
	return payload
}

// A list that names both episodes of the double makes one write for it.
// The furthest watched episode sets the position, and with no watched
// episode, the first cleared episode does.
func TestAListOverADoubleMakesOneWriteForIt(t *testing.T) {
	played, unplayed := true, false
	for _, test := range []struct {
		name          string
		first, second string
		want          fakeJellyfinWrite
	}{
		{name: "pick up at the second episode", first: markWatched, second: markCleared,
			want: doubleWrite(1320, nil)},
		{name: "pick up at the first episode", first: markCleared, second: markCleared,
			want: doubleWrite(0, &unplayed)},
		{name: "both episodes watched", first: markWatched, second: markWatched,
			want: doubleWrite(2640, &played)},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := jellyfinDoubleFixture()
			role, _, _ := standJellyfinRole(t, fake)

			role.onMessage(markTopic("mark-living-room-1"), doubleListMark(t, test.first, test.second))
			markPass(t, role)

			if want := []fakeJellyfinWrite{test.want}; !reflect.DeepEqual(fake.writes, want) {
				t.Errorf("writes = %+v, want %+v", fake.writes, want)
			}
		})
	}
}

// The webhook compares the position of each post with the role's own last
// write, in the file's seconds, so the echo record holds the file position
// and not the mark's position in its episode.
func TestTheEchoOfAMarkOnADoubleHoldsTheFilePosition(t *testing.T) {
	role, _, _ := standJellyfinRole(t, jellyfinDoubleFixture())

	role.onMessage(markTopic("mark-living-room-1"), doubleEpisodeMark(t, markCleared, 2, 0, 1320))
	markPass(t, role)

	if !role.out.echoes.echoed("user-a", "item-double", 1320) {
		t.Error("the echo record does not hold the file position 1320")
	}
	if role.out.echoes.echoed("user-a", "item-double", 0) {
		t.Error("the echo record holds the mark's own position 0")
	}
}
