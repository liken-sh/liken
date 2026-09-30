package main

// What these tests prove: a mark that names a list of episodes, which
// Pick up here publishes, writes one row for each episode, all recorded at
// the press, each at the position of its own mark: watched for the earlier
// episodes, cleared for the later ones. The watched rows' names sort in
// series order and the cleared rows' names sort before them, so the thread
// rule stands on the last watched episode. A list delivered again writes
// only the rows the store does not hold, and leaves the rest alone.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A list mark as the browser publishes it for Pick up here, pressed a
// minute ago, over these episodes, each 2760 seconds long.
func listMarkPayload(t *testing.T, episodes ...[2]int) []byte {
	t.Helper()
	listed := make([]markedEpisode, len(episodes))
	for at, numbers := range episodes {
		listed[at] = markedEpisode{Season: numbers[0], Episode: numbers[1], Position: 2760, Duration: 2760}
	}
	payload, err := json.Marshal(titleMark{Mark: markWatched, Player: "den",
		People: []string{"person-a", "person-b"}, Aliases: map[string]string{"tvdb": "8001"},
		Episodes: listed, At: time.Now().Add(-time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// The field names are the contract the media browser writes and the
// jellyfin role reads. A list mark states no numbers of its own.
func TestAListMarkReadsItsEpisodes(t *testing.T) {
	payload := `{"mark":"watched","player":"den","people":["person-a"],` +
		`"aliases":{"tvdb":"8001"},"episodes":[{"season":1,"episode":2,` +
		`"position":2760,"duration":2760}],"at":1759140000}`

	mark := titleMark{}
	if err := json.Unmarshal([]byte(payload), &mark); err != nil {
		t.Fatal(err)
	}

	want := []markedEpisode{{Season: 1, Episode: 2, Position: 2760, Duration: 2760}}
	if !slices.Equal(mark.Episodes, want) || mark.Season != 0 || mark.Episode != 0 {
		t.Errorf("mark = %+v, want the one episode in the list", mark)
	}
}

// An entry states its own mark, and an entry that states none takes the
// message's.
func TestAListEntryReadsItsOwnMark(t *testing.T) {
	payload := `{"mark":"watched","player":"den","people":["person-a"],` +
		`"aliases":{"tvdb":"8001"},"episodes":[` +
		`{"mark":"watched","season":1,"episode":2,"position":2760,"duration":2760},` +
		`{"mark":"cleared","season":1,"episode":4,"position":0,"duration":2760},` +
		`{"season":1,"episode":5,"position":2760,"duration":2760}],"at":1759140000}`

	mark := titleMark{}
	if err := json.Unmarshal([]byte(payload), &mark); err != nil {
		t.Fatal(err)
	}

	got := []string{}
	for _, work := range mark.works("mark-den-1") {
		got = append(got, work.mark)
	}
	if want := []string{markWatched, markCleared, markWatched}; !slices.Equal(got, want) {
		t.Errorf("marks = %v, want %v", got, want)
	}
}

// A single mark is one row under the mark's own name, and a list is one row
// for each episode, each named after the mark and its numbers.
func TestAMarkNamesOneRowForEachWork(t *testing.T) {
	cases := []struct {
		name string
		mark titleMark
		want []markedWork
	}{
		{
			name: "a film",
			mark: titleMark{Mark: markWatched, Position: 6000, Duration: 6000},
			want: []markedWork{{row: "mark-den-1", mark: markWatched, position: 6000, duration: 6000}},
		},
		{
			name: "one episode",
			mark: titleMark{Mark: markWatched, Season: 2, Episode: 5, Position: 2760, Duration: 2760},
			want: []markedWork{{row: "mark-den-1", mark: markWatched, season: 2, episode: 5, position: 2760, duration: 2760}},
		},
		{
			name: "a list",
			mark: titleMark{Mark: markWatched, Episodes: []markedEpisode{
				{Season: 1, Episode: 9, Position: 2760, Duration: 2760},
				{Season: 1, Episode: 10, Position: 2700, Duration: 2700},
				{Mark: markCleared, Season: 2, Episode: 3, Position: 0, Duration: 2760},
			}},
			want: []markedWork{
				{row: "mark-den-1-s0001e0009", mark: markWatched, season: 1, episode: 9, position: 2760, duration: 2760},
				{row: "mark-den-1-s0001e0010", mark: markWatched, season: 1, episode: 10, position: 2700, duration: 2700},
				{row: "mark-den-1-cleared-s0002e0003", mark: markCleared, season: 2, episode: 3, position: 0, duration: 2760},
			},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			if got := each.mark.works("mark-den-1"); !slices.Equal(got, each.want) {
				t.Errorf("works = %+v, want %+v", got, each.want)
			}
		})
	}
}

// Every row of one press has the same recorded time, and the thread rule
// breaks that tie on the row's name. The names sort in series order, so the
// thread stands on the last episode of the list.
func TestTheRowsOfAListSortInSeriesOrder(t *testing.T) {
	mark := titleMark{Episodes: []markedEpisode{
		{Season: 1, Episode: 2}, {Season: 1, Episode: 9}, {Season: 1, Episode: 10},
		{Season: 2, Episode: 1}, {Season: 10, Episode: 1}, {Season: 10, Episode: 100},
	}}

	names := []string{}
	for _, work := range mark.works("mark-den-1") {
		names = append(names, work.row)
	}

	if !slices.IsSorted(names) {
		t.Errorf("names = %v, want them sorted in series order", names)
	}
}

// Pick up here clears the episodes after the one it marks up to, at the
// same press. A cleared row stands at position 0, which the thread rule
// reads as not started, so a cleared row that sorted last would take the
// thread and the series would leave Continue watching. Every cleared name
// sorts before every watched name, so the thread stands on the last watched
// episode and offers the picked one after it.
func TestTheClearedRowsOfAListSortBeforeItsWatchedRows(t *testing.T) {
	mark := titleMark{Mark: markWatched, Episodes: []markedEpisode{
		{Season: 1, Episode: 1}, {Season: 9, Episode: 99},
		{Mark: markCleared, Season: 10, Episode: 1}, {Mark: markCleared, Season: 10, Episode: 100},
	}}

	names := []string{}
	for _, work := range mark.works("mark-den-1") {
		names = append(names, work.row)
	}
	slices.Sort(names)

	want := []string{
		"mark-den-1-cleared-s0010e0001", "mark-den-1-cleared-s0010e0100",
		"mark-den-1-s0001e0001", "mark-den-1-s0009e0099",
	}
	if !slices.Equal(names, want) {
		t.Errorf("names in order = %v, want %v", names, want)
	}
}

// A cleared entry writes its episode at position 0, recorded at the press
// beside the watched rows.
func TestAListMarkClearsTheEpisodesItNamesCleared(t *testing.T) {
	role, db := recordingProgress(t)
	mark := titleMark{}
	_ = json.Unmarshal(listMarkPayload(t, [2]int{1, 1}), &mark)
	mark.Episodes = append(mark.Episodes, markedEpisode{Mark: markCleared, Season: 1, Episode: 3, Duration: 2760})
	payload, _ := json.Marshal(mark)

	role.onMessage(playMarkTopic(defaultTopicBase, "house", "mark-den-1"), payload)

	row := heldPlay(t, db, "mark-den-1-cleared-s0001e0003")
	if row.Position != 0 || row.Duration != 2760 || row.Season != 1 || row.Episode != 3 {
		t.Errorf("row = %+v, want the episode at position 0", row)
	}
	if row.Recorded != mark.At || row.Ended != mark.At {
		t.Errorf("row = %+v, want an ended row recorded at the press", row)
	}
	if watched := heldPlay(t, db, "mark-den-1-s0001e0001"); watched.Position != 2760 {
		t.Errorf("watched row = %+v, want the episode at its end", watched)
	}
}

// An entry that names a mark that is neither of the two makes the message
// no mark, and the role clears it from the broker as it clears any other.
func TestAListEntryWithAnUnknownMarkIsCleared(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		role, broker, db := servingProgress(t)
		waitForTopic(t, broker, role.availabilityTopic)
		topic := playMarkTopic(defaultTopicBase, "house", "mark-den-1")
		mark := titleMark{}
		_ = json.Unmarshal(listMarkPayload(t, [2]int{1, 1}), &mark)
		mark.Episodes = append(mark.Episodes, markedEpisode{Mark: "forgotten", Season: 1, Episode: 3})
		payload, _ := json.Marshal(mark)

		role.onMessage(topic, payload)

		published := waitForTopic(t, broker, topic)
		if len(published.payload) != 0 || !published.retained {
			t.Errorf("message = %q retained %v, want a retained empty payload", published.payload, published.retained)
		}
		if heldPlay(t, db, "mark-den-1-s0001e0001").Play != "" {
			t.Error("the role recorded a list that holds no mark")
		}
	})
}

// A list writes one ended row for each episode, with the audience, the
// series' aliases, and the episode's numbers, recorded at the press.
func TestAListMarkWritesOneRowForEachEpisode(t *testing.T) {
	role, db := recordingProgress(t)
	payload := listMarkPayload(t, [2]int{1, 3}, [2]int{2, 1})
	mark := titleMark{}
	_ = json.Unmarshal(payload, &mark)

	role.onMessage(playMarkTopic(defaultTopicBase, "house", "mark-den-1"), payload)

	for _, each := range []struct {
		row             string
		season, episode int
	}{
		{row: "mark-den-1-s0001e0003", season: 1, episode: 3},
		{row: "mark-den-1-s0002e0001", season: 2, episode: 1},
	} {
		row := heldPlay(t, db, each.row)
		if row.Season != each.season || row.Episode != each.episode || row.Position != 2760 {
			t.Errorf("row %s = %+v, want the episode at its end", each.row, row)
		}
		if row.Recorded != mark.At || row.Ended != mark.At || row.Phase != playPhaseFinished {
			t.Errorf("row %s = %+v, want an ended row recorded at the press", each.row, row)
		}
		if people := heldPeople(t, db, each.row); len(people) != 2 {
			t.Errorf("people of %s = %v, want the two at the screen", each.row, people)
		}
		if aliases := heldAliases(t, db, each.row); aliases["tvdb"] != "8001" {
			t.Errorf("aliases of %s = %v, want the series' tvdb id", each.row, aliases)
		}
	}
	if heldPlay(t, db, "mark-den-1").Play != "" {
		t.Error("the role wrote a row for the list itself")
	}
}

// A list delivered again writes the rows the store does not hold and
// leaves the rest alone. Here the first delivery wrote only the first
// episode, a person was forgotten since, and the second delivery writes
// the second episode and does not write the person back into the first.
func TestAListDeliveredAgainWritesOnlyTheMissingRows(t *testing.T) {
	role, db := recordingProgress(t)
	topic := playMarkTopic(defaultTopicBase, "house", "mark-den-1")
	whole := listMarkPayload(t, [2]int{1, 1}, [2]int{1, 2})
	mark := titleMark{}
	_ = json.Unmarshal(whole, &mark)
	first := mark
	first.Episodes = mark.Episodes[:1]
	partial, _ := json.Marshal(first)
	role.onMessage(topic, partial)
	role.onMessage(personForgetTopic(defaultTopicBase, "person-b"), []byte(`{"at":"2026-09-29T21:00:00Z"}`))

	role.onMessage(topic, whole)

	if people := heldPeople(t, db, "mark-den-1-s0001e0001"); len(people) != 1 {
		t.Errorf("people of the first row = %v, want person-a alone", people)
	}
	if people := heldPeople(t, db, "mark-den-1-s0001e0002"); len(people) != 2 {
		t.Errorf("people of the second row = %v, want the two the list names", people)
	}
}

// A list is one press, and leaves one line that names the mark and how
// many episodes it covers.
func TestAListMarkLeavesOneLine(t *testing.T) {
	role, _ := recordingProgress(t)
	var logged strings.Builder
	role.log = &logged

	role.onMessage(playMarkTopic(defaultTopicBase, "house", "mark-den-1"),
		listMarkPayload(t, [2]int{1, 1}, [2]int{1, 2}, [2]int{2, 1}))

	line := logged.String()
	for _, want := range []string{"mark-den-1", "3 episodes", "3 watched and 0 cleared", "s1e1 to s2e1"} {
		if !strings.Contains(line, want) {
			t.Errorf("log = %q, want %q in it", line, want)
		}
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("log = %q, want one line", line)
	}
}

// The list is one retained message, so the role clears it once, as it
// clears a single mark.
func TestAnExpiredListIsRecordedAndClearedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		role, broker, db := servingProgress(t)
		waitForTopic(t, broker, role.availabilityTopic)
		topic := playMarkTopic(defaultTopicBase, "house", "mark-den-1")
		mark := titleMark{}
		_ = json.Unmarshal(listMarkPayload(t, [2]int{1, 1}, [2]int{1, 2}), &mark)
		mark.At = time.Now().Add(-markRetention - time.Hour).Unix()
		payload, _ := json.Marshal(mark)

		role.onMessage(topic, payload)

		published := waitForTopic(t, broker, topic)
		if len(published.payload) != 0 || !published.retained {
			t.Errorf("message = %q retained %v, want a retained empty payload", published.payload, published.retained)
		}
		if heldPlay(t, db, "mark-den-1-s0001e0002").Play == "" {
			t.Error("the role cleared a list it did not record")
		}
	})
}
