package main

// What these tests prove. A mark from the media browser reaches Jellyfin
// once per person, on the pass after it arrives: watched as played at the
// end, cleared as unplayed at the start. A mark delivered again, or one the
// role already sent before a restart, sends nothing. A mark older than what
// Jellyfin holds, or older than its retention, sends nothing. A write that
// failed is sent again on the next pass.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// The press every mark here was made at: a minute before the fake clock
// reads.
var markPressed = newJellyfinClock().now().Add(-time.Minute).Unix()

// One mark as the browser publishes it.
func jellyfinMark(t *testing.T, kind string, people []string, position int) []byte {
	t.Helper()
	payload, err := json.Marshal(titleMark{
		Mark: kind, Player: "living-room", People: people,
		Aliases: map[string]string{"tmdb": "1101"}, Position: position, Duration: 8160, At: markPressed})
	if err != nil {
		t.Fatalf("building the mark: %v", err)
	}
	return payload
}

// The topics one mark and its sent record reach the role on.
func markTopic(name string) string { return playMarkTopic(defaultTopicBase, "house", name) }
func sentTopic(name string) string { return playMarkSentTopic(defaultTopicBase, "house", name) }

// The one pass of the write loop that sends what arrived.
func markPass(t *testing.T, role *jellyfin) {
	t.Helper()
	role.marks.push(t.Context())
}

// A watched mark sends played at the end of the film, and a cleared one
// sends unplayed at the start, both dated at the press.
func TestAMarkSendsItsPlayedStateToJellyfin(t *testing.T) {
	pressed := time.Unix(markPressed, 0).UTC().Format(time.RFC3339)
	for _, test := range []struct {
		name     string
		mark     string
		position int
		want     jellyfinUserData
	}{
		{name: "watched", mark: markWatched, position: 8160, want: jellyfinUserData{
			PlaybackPositionTicks: 81_600_000_000, Played: true, LastPlayedDate: pressed}},
		{name: "cleared", mark: markCleared, position: 0, want: jellyfinUserData{
			Played: false, LastPlayedDate: pressed}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := jellyfinFixture()
			role, _, _ := standJellyfinRole(t, fake)

			role.onMessage(markTopic("mark-living-room-1"), jellyfinMark(t, test.mark, []string{"person-a"}, test.position))
			markPass(t, role)

			want := fakeJellyfinWrite{item: "item-film", user: "user-a", data: test.want, stated: true}
			if len(fake.writes) != 1 || fake.writes[0] != want {
				t.Errorf("writes = %+v, want %+v", fake.writes, want)
			}
		})
	}
}

// A mark waits for the pass, so a sent record the broker delivers just
// after it on a new subscription still stops it.
func TestAMarkWaitsForThePass(t *testing.T) {
	fake := jellyfinFixture()
	role, _, _ := standJellyfinRole(t, fake)

	role.onMessage(markTopic("mark-living-room-1"), jellyfinMark(t, markWatched, []string{"person-a"}, 8160))

	if len(fake.writes) != 0 {
		t.Errorf("writes = %+v, want none before the pass", fake.writes)
	}
}

// Every person at the screen gets their own write.
func TestAMarkReachesEveryPersonAtTheScreen(t *testing.T) {
	fake := jellyfinFixture()
	role, _, _ := standJellyfinRole(t, fake)

	role.onMessage(markTopic("mark-living-room-1"),
		jellyfinMark(t, markWatched, []string{"person-a", "person-c"}, 8160))
	markPass(t, role)

	if len(fake.writes) != 2 || fake.writes[0].user != "user-a" || fake.writes[1].user != "user-c" {
		t.Errorf("writes = %+v, want one for each person", fake.writes)
	}
}

// A sent mark leaves a retained record of it, with the time of the press,
// so a restarted role reads that it sent the mark.
func TestASentMarkLeavesARecord(t *testing.T) {
	role, messages, _ := standJellyfinRole(t, jellyfinFixture())

	role.onMessage(markTopic("mark-living-room-1"), jellyfinMark(t, markWatched, []string{"person-a"}, 8160))
	markPass(t, role)

	message, record := messages.only(t)
	if message.topic != sentTopic("mark-living-room-1") || !message.retained {
		t.Errorf("message = %+v, want the retained record of the mark", message)
	}
	if record.At != markPressed {
		t.Errorf("record = %+v, want the time of the press", record)
	}
}

// A mark the broker delivers again, on a reconnect or a second pass, is
// the same mark, and sends nothing more.
func TestAMarkDeliveredAgainSendsNothingMore(t *testing.T) {
	fake := jellyfinFixture()
	role, _, _ := standJellyfinRole(t, fake)
	mark := jellyfinMark(t, markWatched, []string{"person-a"}, 8160)

	role.onMessage(markTopic("mark-living-room-1"), mark)
	markPass(t, role)
	role.onMessage(sentTopic("mark-living-room-1"), sentRecord(t, markPressed))
	role.onMessage(markTopic("mark-living-room-1"), mark)
	markPass(t, role)

	if len(fake.writes) != 1 {
		t.Errorf("writes = %+v, want one", fake.writes)
	}
}

// One sent record, as the role publishes it.
func sentRecord(t *testing.T, at int64) []byte {
	t.Helper()
	payload, err := json.Marshal(jellyfinMarkSent{At: at})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// A role that restarts reads the mark and its sent record back from the
// broker, and sends nothing, so a toggle a person made in Jellyfin since
// the mark stands. Jellyfin clears the last played date on an unplayed
// toggle, so the date alone cannot stop the mark.
func TestARestartedRoleDoesNotSendAMarkItSent(t *testing.T) {
	fake := jellyfinFixture()
	fake.userData = map[string]jellyfinUserData{"user-a/item-film": {Played: false}}
	role, _, _ := standJellyfinRole(t, fake)

	role.onMessage(markTopic("mark-living-room-1"), jellyfinMark(t, markWatched, []string{"person-a"}, 8160))
	role.onMessage(sentTopic("mark-living-room-1"), sentRecord(t, markPressed))
	markPass(t, role)

	if len(fake.writes) != 0 {
		t.Errorf("writes = %+v, want none for a mark the role already sent", fake.writes)
	}
}

// A person who played the title in Jellyfin after the press has the newer
// state, and the mark leaves it alone. A date equal to the press is the
// mark's own write, which a second send would only repeat.
func TestAMarkDoesNotOverwriteANewerPlayInJellyfin(t *testing.T) {
	for _, test := range []struct {
		name   string
		date   string
		writes int
	}{
		{name: "played after the press", writes: 0,
			date: time.Unix(markPressed+60, 0).UTC().Format(time.RFC3339Nano)},
		{name: "the mark's own date", writes: 0,
			date: time.Unix(markPressed, 0).UTC().Format(time.RFC3339)},
		{name: "played before the press", writes: 1,
			date: time.Unix(markPressed-60, 0).UTC().Format("2006-01-02T15:04:05.0000000Z")},
		{name: "never played", writes: 1, date: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := jellyfinFixture()
			fake.userData = map[string]jellyfinUserData{"user-a/item-film": {LastPlayedDate: test.date}}
			role, _, _ := standJellyfinRole(t, fake)

			role.onMessage(markTopic("mark-living-room-1"), jellyfinMark(t, markCleared, []string{"person-a"}, 0))
			markPass(t, role)

			if len(fake.writes) != test.writes {
				t.Errorf("writes = %+v, want %d", fake.writes, test.writes)
			}
		})
	}
}

// A mark older than its retention sends nothing. The progress role clears
// such a mark at once, and a person has had a day to change the title
// since.
func TestAMarkPastItsRetentionSendsNothing(t *testing.T) {
	fake := jellyfinFixture()
	role, _, _ := standJellyfinRole(t, fake)
	payload, _ := json.Marshal(titleMark{Mark: markWatched, People: []string{"person-a"},
		Aliases: map[string]string{"tmdb": "1101"}, Position: 8160, Duration: 8160,
		At: newJellyfinClock().now().Add(-markRetention).Unix()})

	role.onMessage(markTopic("mark-living-room-1"), payload)
	markPass(t, role)

	if len(fake.writes) != 0 {
		t.Errorf("writes = %+v, want none", fake.writes)
	}
}

// A write Jellyfin refused is sent again on the next pass, and the record
// waits until it lands.
func TestAFailedMarkIsSentAgainOnTheNextPass(t *testing.T) {
	fake := jellyfinFixture()
	role, messages, _ := standJellyfinRole(t, fake)
	role.onMessage(markTopic("mark-living-room-1"), jellyfinMark(t, markWatched, []string{"person-a"}, 8160))

	fake.status = http.StatusServiceUnavailable
	markPass(t, role)
	fake.status = 0
	markPass(t, role)

	if len(fake.writes) != 1 {
		t.Errorf("writes = %+v, want the one that landed", fake.writes)
	}
	if len(messages.held) != 1 {
		t.Errorf("messages = %+v, want one record, after the write landed", messages.held)
	}
}

// The progress role clears a mark once its retention has run, and the
// jellyfin role clears its record with it, so the broker holds neither.
func TestTheClearOfAMarkClearsItsRecord(t *testing.T) {
	role, messages, _ := standJellyfinRole(t, jellyfinFixture())
	role.onMessage(sentTopic("mark-living-room-1"), sentRecord(t, markPressed))

	role.onMessage(markTopic("mark-living-room-1"), nil)

	message, _ := messages.only(t)
	if message.topic != sentTopic("mark-living-room-1") || !message.retained || len(message.payload) != 0 {
		t.Errorf("message = %+v, want the retained clear of the record", message)
	}
}

// A record whose mark is past its retention is cleared when the role reads
// it, because the mark's own clear may have come while the role was down.
func TestARecordPastItsRetentionIsCleared(t *testing.T) {
	role, messages, _ := standJellyfinRole(t, jellyfinFixture())

	role.onMessage(sentTopic("mark-living-room-1"),
		sentRecord(t, newJellyfinClock().now().Add(-markRetention).Unix()))

	message, _ := messages.only(t)
	if message.topic != sentTopic("mark-living-room-1") || len(message.payload) != 0 {
		t.Errorf("message = %+v, want the clear of the record", message)
	}
}

// A mark Jellyfin cannot take writes nothing and leaves its record, so
// the next pass does not try it again: a later pass would find the same.
func TestAMarkJellyfinCannotTakeIsDone(t *testing.T) {
	for _, test := range []struct {
		name    string
		people  []string
		aliases map[string]string
	}{
		{name: "a mark that names nobody", people: nil, aliases: map[string]string{"tmdb": "1101"}},
		{name: "a work jellyfin does not hold", people: []string{"person-a"},
			aliases: map[string]string{"tmdb": "4040"}},
		{name: "a person jellyfin does not hold", people: []string{"person-z"},
			aliases: map[string]string{"tmdb": "1101"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := jellyfinFixture()
			role, messages, _ := standJellyfinRole(t, fake)
			payload, _ := json.Marshal(titleMark{Mark: markWatched, People: test.people,
				Aliases: test.aliases, Position: 8160, Duration: 8160, At: markPressed})

			role.onMessage(markTopic("mark-living-room-1"), payload)
			markPass(t, role)

			message, _ := messages.only(t)
			if len(fake.writes) != 0 || message.topic != sentTopic("mark-living-room-1") {
				t.Errorf("writes = %+v and message = %+v, want no write and the record", fake.writes, message)
			}
		})
	}
}

// A clear of a record forgets it, so the role reads the broker's state and
// not what it last wrote there.
func TestAClearedRecordIsForgotten(t *testing.T) {
	fake := jellyfinFixture()
	role, _, _ := standJellyfinRole(t, fake)
	role.onMessage(sentTopic("mark-living-room-1"), sentRecord(t, markPressed))

	role.onMessage(sentTopic("mark-living-room-1"), nil)
	role.onMessage(markTopic("mark-living-room-1"), jellyfinMark(t, markWatched, []string{"person-a"}, 8160))
	markPass(t, role)

	if len(fake.writes) != 1 {
		t.Errorf("writes = %+v, want the mark sent once its record is gone", fake.writes)
	}
}

// A payload that is no mark sends nothing. The progress role clears it.
func TestAPayloadThatIsNoMarkSendsNothing(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload []byte
	}{
		{name: "no JSON", payload: []byte("not a mark")},
		{name: "another word", payload: []byte(`{"mark":"liked","people":["person-a"],"aliases":{"tmdb":"1101"}}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := jellyfinFixture()
			role, _, _ := standJellyfinRole(t, fake)

			role.onMessage(markTopic("mark-living-room-1"), test.payload)
			markPass(t, role)

			if len(fake.writes) != 0 {
				t.Errorf("writes = %+v, want none", fake.writes)
			}
		})
	}
}

// The one message the role published, with its payload read as a record.
func (m *jellyfinMessages) only(t *testing.T) (jellyfinMessage, jellyfinMarkSent) {
	t.Helper()
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if len(m.held) != 1 {
		t.Fatalf("messages = %d, want one", len(m.held))
	}
	record := jellyfinMarkSent{}
	if len(m.held[0].payload) > 0 {
		if err := json.Unmarshal(m.held[0].payload, &record); err != nil {
			t.Fatalf("reading the record back: %v", err)
		}
	}
	return m.held[0], record
}
