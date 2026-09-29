package main

// What these tests prove: the topic a mark travels on, the row the
// progress role writes from one mark, that a mark delivered again never
// changes a row that already stands, and that the role clears each mark
// it recorded once the mark is older than the retention.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTheMarkTopicsCarryThePlayLayout(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "one mark",
			got:  playMarkTopic(defaultTopicBase, "house", "mark-den-1759140000"),
			want: "liken/library/plays/house/mark-den-1759140000/mark",
		},
		{
			name: "one namespace's marks",
			got:  playMarkFilter(defaultTopicBase, "house"),
			want: "liken/library/plays/house/+/mark",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			if each.got != each.want {
				t.Errorf("topic = %q, want %q", each.got, each.want)
			}
		})
	}
}

// The field names are the contract the media browser writes and the
// jellyfin role reads, so a rename here breaks both.
func TestAMarkReadsItsFields(t *testing.T) {
	payload := `{"mark":"watched","player":"den","people":["person-a"],` +
		`"aliases":{"tvdb":"8001"},"season":2,"episode":5,` +
		`"position":2760,"duration":2760,"at":1759140000}`

	mark := titleMark{}
	if err := json.Unmarshal([]byte(payload), &mark); err != nil {
		t.Fatal(err)
	}

	want := titleMark{Mark: markWatched, Player: "den", People: []string{"person-a"},
		Aliases: map[string]string{"tvdb": "8001"}, Season: 2, Episode: 5,
		Position: 2760, Duration: 2760, At: 1759140000}
	got, _ := json.Marshal(mark)
	held, _ := json.Marshal(want)
	if string(got) != string(held) {
		t.Errorf("mark = %s, want %s", got, held)
	}
}

// markPayload is one mark as the browser publishes it, pressed this many
// seconds before now.
func markPayload(t *testing.T, kind string, position int, age time.Duration) []byte {
	t.Helper()
	payload, err := json.Marshal(titleMark{Mark: kind, Player: "den",
		People: []string{"person-a", "person-b"}, Aliases: map[string]string{"tmdb": "2101"},
		Position: position, Duration: 6000, At: time.Now().Add(-age).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// A mark writes one whole row: the screen it was pressed on, the position
// it names, the people at the screen, and the work's aliases. The row is
// ended, because no Play runs behind it, and its recorded time is the
// press.
func TestAMarkWritesOneRowWithTheAudienceAndTheAliases(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		position int
		phase    string
	}{
		{name: "watched", kind: markWatched, position: 6000, phase: playPhaseFinished},
		{name: "cleared", kind: markCleared, position: 0, phase: playPhaseFinished},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			role, db := recordingProgress(t)
			payload := markPayload(t, each.kind, each.position, time.Minute)

			role.onMessage(playMarkTopic(defaultTopicBase, "house", "mark-den-1"), payload)

			row := heldPlay(t, db, "mark-den-1")
			mark := titleMark{}
			_ = json.Unmarshal(payload, &mark)
			if row.Player != "den" || row.Position != each.position || row.Duration != 6000 {
				t.Errorf("row = %+v, want the screen and the positions the mark names", row)
			}
			if row.Recorded != mark.At || row.Ended != mark.At || row.Phase != each.phase {
				t.Errorf("row = %+v, want an ended row recorded at the press", row)
			}
			if people := heldPeople(t, db, "mark-den-1"); len(people) != 2 {
				t.Errorf("people = %v, want the two at the screen", people)
			}
			if aliases := heldAliases(t, db, "mark-den-1"); aliases["tmdb"] != "2101" {
				t.Errorf("aliases = %v, want the tmdb id", aliases)
			}
		})
	}
}

// A mark delivered again, as the broker does on every reconnect while the
// mark is retained, changes nothing in a row that already stands. Here a
// person was forgotten after the mark was recorded, and the second
// delivery does not write them back.
func TestAMarkDeliveredAgainLeavesTheRowAlone(t *testing.T) {
	role, db := recordingProgress(t)
	topic := playMarkTopic(defaultTopicBase, "house", "mark-den-1")
	payload := markPayload(t, markWatched, 6000, time.Minute)
	role.onMessage(topic, payload)
	role.onMessage(personForgetTopic(defaultTopicBase, "person-b"), []byte(`{"at":"2026-09-29T21:00:00Z"}`))

	role.onMessage(topic, payload)

	if people := heldPeople(t, db, "mark-den-1"); len(people) != 1 || people["person-a"] != 1 {
		t.Errorf("people = %v, want person-a alone", people)
	}
}

// An empty payload is the clear, and a payload that is no mark names
// nothing to record. Neither writes a row.
func TestAMarkThatSaysNothingWritesNothing(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{name: "the clear", payload: nil},
		{name: "no JSON", payload: []byte(`{"mark":`)},
		{name: "another kind", payload: []byte(`{"mark":"liked","player":"den","at":1759140000}`)},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			role, db := recordingProgress(t)

			role.onMessage(playMarkTopic(defaultTopicBase, "house", "mark-den-1"), each.payload)

			if heldPlay(t, db, "mark-den-1").Play != "" {
				t.Error("the role recorded a row from a message that is no mark")
			}
		})
	}
}

// A mark on another namespace's tree belongs to that namespace's store.
func TestAMarkOfAnotherNamespaceWritesNothing(t *testing.T) {
	role, db := recordingProgress(t)

	role.onMessage(playMarkTopic(defaultTopicBase, "loft", "mark-den-1"),
		markPayload(t, markWatched, 6000, time.Minute))

	if heldPlay(t, db, "mark-den-1").Play != "" {
		t.Error("the role recorded a mark of another namespace")
	}
}

// The role keeps a recorded mark on the broker until the retention has
// run from the press, so the jellyfin role reads it after a restart of its
// own, and then clears it.
func TestTheWaitBeforeTheClearRunsFromThePress(t *testing.T) {
	now := time.Unix(1_759_140_000, 0)
	cases := []struct {
		name string
		at   time.Time
		want time.Duration
	}{
		{name: "pressed now", at: now, want: markRetention},
		{name: "pressed an hour ago", at: now.Add(-time.Hour), want: markRetention - time.Hour},
		{name: "pressed at the retention", at: now.Add(-markRetention), want: 0},
		{name: "pressed before it", at: now.Add(-2 * markRetention), want: 0},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			if got := markClearIn(each.at.Unix(), now); got != each.want {
				t.Errorf("wait = %v, want %v", got, each.want)
			}
		})
	}
}

// A mark older than the retention is recorded and then cleared at once, so
// a progress role that was down for longer than the retention still
// records what was pressed while it was down.
func TestAnExpiredMarkIsRecordedAndCleared(t *testing.T) {
	role, broker, db := servingProgress(t)
	waitForTopic(t, broker, role.availabilityTopic)
	topic := playMarkTopic(defaultTopicBase, "house", "mark-den-1")

	role.onMessage(topic, markPayload(t, markCleared, 0, markRetention+time.Hour))

	published := waitForTopic(t, broker, topic)
	if len(published.payload) != 0 || !published.retained {
		t.Errorf("message = %q retained %v, want a retained empty payload", published.payload, published.retained)
	}
	if heldPlay(t, db, "mark-den-1").Play == "" {
		t.Error("the role cleared a mark it did not record")
	}
}

// A fresh mark is cleared when its retention runs out, which the test
// shortens to milliseconds.
func TestAFreshMarkIsClearedWhenItsRetentionRunsOut(t *testing.T) {
	retentionWas := markRetention
	t.Cleanup(func() { markRetention = retentionWas })
	markRetention = 50 * time.Millisecond
	role, broker, _ := servingProgress(t)
	waitForTopic(t, broker, role.availabilityTopic)
	topic := playMarkTopic(defaultTopicBase, "house", "mark-den-1")

	role.onMessage(topic, markPayload(t, markWatched, 6000, 0))

	published := waitForTopic(t, broker, topic)
	if len(published.payload) != 0 || !published.retained {
		t.Errorf("message = %q retained %v, want a retained empty payload", published.payload, published.retained)
	}
}

// Each mark the role records is one line in the pod log that names the
// row, the kind, and the position, and names no title.
func TestAMarkLeavesOneLine(t *testing.T) {
	role, _ := recordingProgress(t)
	var logged strings.Builder
	role.log = &logged

	role.onMessage(playMarkTopic(defaultTopicBase, "house", "mark-den-1"),
		markPayload(t, markWatched, 6000, time.Minute))

	line := logged.String()
	for _, want := range []string{"mark-den-1", "watched", "6000 of 6000"} {
		if !strings.Contains(line, want) {
			t.Errorf("log = %q, want %q in it", line, want)
		}
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("log = %q, want one line", line)
	}
}
