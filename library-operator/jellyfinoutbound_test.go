package main

// What these tests prove: the join of a Play's two messages, the ten
// second throttle, what the role does with a work or a person Jellyfin
// does not have, and the played state a write states only past the line.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// One outbound half over a fake Jellyfin, with the index already
// primed and a log the test reads.
func standJellyfinOutbound(t *testing.T, fake *fakeJellyfin) (*jellyfinOutbound, *strings.Builder) {
	t.Helper()
	clock := newJellyfinClock()
	logged := &strings.Builder{}
	api := standJellyfinServer(t, fake)
	index := newJellyfinIndex(api, clock.now, logged)
	index.prime(t.Context())
	return newJellyfinOutbound(api, index, clock.now, logged), logged
}

// The audience of one Play, as the operator publishes it.
func jellyfinAudience(t *testing.T, people []string, aliases map[string]string, season, episode int) []byte {
	t.Helper()
	payload, err := json.Marshal(playAudience{
		Player: "living-room", People: people, Aliases: aliases, Season: season, Episode: episode})
	if err != nil {
		t.Fatalf("building the audience: %v", err)
	}
	return payload
}

// The status and the audience of one Play join into one write, with the
// position in ticks and the time of the write beside it.
func TestAStatusAndAnAudienceBecomeOneWrite(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)

	out.audience("play-1", jellyfinAudience(t, []string{"person-a"}, map[string]string{"tmdb": "1101"}, 0, 0))
	out.status("play-1", []byte(`{"item":0,"position":"1:10:10","duration":"2:16:00"}`))
	out.tick(t.Context())

	if len(fake.writes) != 1 {
		t.Fatalf("writes = %+v, want one", fake.writes)
	}
	want := fakeJellyfinWrite{item: "item-film", user: "user-a", data: jellyfinUserData{
		PlaybackPositionTicks: 42_100_000_000, LastPlayedDate: "2026-09-08T20:04:05Z"}}
	if fake.writes[0] != want {
		t.Errorf("write = %+v, want %+v", fake.writes[0], want)
	}
}

// An episode is written against the episode's item, which the season
// and the episode numbers name under the series' ids.
func TestAnEpisodePlayWritesTheEpisodesItem(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)

	out.audience("play-1", jellyfinAudience(t, []string{"person-a"}, map[string]string{"tmdb": "2101"}, 3, 5))
	out.status("play-1", []byte(`{"item":4,"position":"0:10:00","duration":"0:22:00"}`))
	out.tick(t.Context())

	if len(fake.writes) != 1 || fake.writes[0].item != "item-series-3-5" {
		t.Errorf("writes = %+v, want one write of the episode", fake.writes)
	}
}

// Every person who watched gets their own write, because a user-data
// write names one user.
func TestEveryPersonOfAPlayGetsAWrite(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)

	out.audience("play-1", jellyfinAudience(t, []string{"person-a", "person-c"}, map[string]string{"tmdb": "1101"}, 0, 0))
	out.status("play-1", []byte(`{"item":0,"position":"0:10:00","duration":"2:16:00"}`))
	out.tick(t.Context())

	if len(fake.writes) != 2 || fake.writes[0].user != "user-a" || fake.writes[1].user != "user-c" {
		t.Errorf("writes = %+v, want one for each person", fake.writes)
	}
}

// A position that did not move writes nothing, so a paused film is one
// message on the bus and no request to Jellyfin.
func TestATickWritesOnlyThePositionsThatMoved(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)
	out.audience("play-1", jellyfinAudience(t, []string{"person-a"}, map[string]string{"tmdb": "1101"}, 0, 0))
	out.status("play-1", []byte(`{"item":0,"position":"0:10:00","duration":"2:16:00"}`))

	out.tick(t.Context())
	out.tick(t.Context())
	out.status("play-1", []byte(`{"item":0,"position":"0:10:10","duration":"2:16:00"}`))
	out.tick(t.Context())

	if len(fake.writes) != 2 {
		t.Fatalf("writes = %+v, want one for each position", fake.writes)
	}
	if fake.writes[1].data.PlaybackPositionTicks != 6_100_000_000 {
		t.Errorf("position = %d ticks, want the second one", fake.writes[1].data.PlaybackPositionTicks)
	}
}

// A running play past the watched rule is written as watched. No final is
// needed, because the position alone says the person watched the work.
func TestARunningPlayPastTheWatchedLineIsWrittenWatched(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)
	out.audience("play-1", jellyfinAudience(t, []string{"person-a"}, map[string]string{"tmdb": "1101"}, 0, 0))

	out.status("play-1", []byte(`{"item":0,"position":"2:10:59","duration":"2:16:00"}`))
	out.tick(t.Context())
	out.status("play-1", []byte(`{"item":0,"position":"2:11:00","duration":"2:16:00"}`))
	out.tick(t.Context())

	if len(fake.writes) != 2 {
		t.Fatalf("writes = %+v, want one for each position", fake.writes)
	}
	if fake.writes[0].stated || !fake.writes[1].data.Played {
		t.Errorf("writes = %+v, want no played state and then played", fake.writes)
	}
}

// A credits mark in the second half of the work moves the watched line to
// the start of the credits, so a long film is written as watched with more
// than five minutes left.
func TestACreditsMarkMovesTheLineAPlayIsWrittenWatchedAt(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)
	payload, err := json.Marshal(playAudience{Player: "living-room", People: []string{"person-a"},
		Aliases: map[string]string{"tmdb": "1101"}, Credits: []creditsSpan{credit(7680, 8160)}})
	if err != nil {
		t.Fatal(err)
	}
	out.audience("play-1", payload)

	out.status("play-1", []byte(`{"item":0,"position":"2:07:59","duration":"2:16:00"}`))
	out.tick(t.Context())
	out.status("play-1", []byte(`{"item":0,"position":"2:08:00","duration":"2:16:00"}`))
	out.tick(t.Context())

	if len(fake.writes) != 2 {
		t.Fatalf("writes = %+v, want one for each position", fake.writes)
	}
	if fake.writes[0].stated || !fake.writes[1].data.Played {
		t.Errorf("writes = %+v, want no played state and then played at the start of the credits",
			fake.writes)
	}
}

// A replay below the line keeps the mark Jellyfin holds. Each write of the
// replay moves the position and states no played state, so a person who
// rewatches ten minutes of a finished film leaves it played in Jellyfin.
func TestAReplayBelowTheLineKeepsJellyfinsMark(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)
	out.audience("play-1", jellyfinAudience(t, []string{"person-a"}, map[string]string{"tmdb": "1101"}, 0, 0))

	out.final(t.Context(), "play-1", []byte(`{"phase":"Finished","item":0,"position":"2:16:00","duration":"2:16:00"}`))
	out.status("play-1", []byte(`{"item":0,"position":"0:10:00","duration":"2:16:00"}`))
	out.tick(t.Context())

	if len(fake.writes) != 2 || fake.writes[1].stated {
		t.Errorf("writes = %+v, want the replay's write to name no played state", fake.writes)
	}
	if held := fake.userData["user-a/item-film"]; !held.Played || held.PlaybackPositionTicks != 6_000_000_000 {
		t.Errorf("jellyfin holds %+v, want the film played at the replay's position", held)
	}
}

// A Play's final writes at once, because the position a person resumes
// from is the one the Play ended on.
func TestAFinalWritesAtOnce(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)
	out.audience("play-1", jellyfinAudience(t, []string{"person-a"}, map[string]string{"tmdb": "1101"}, 0, 0))

	out.final(t.Context(), "play-1", []byte(`{"phase":"Finished","item":0,"position":"2:16:00","duration":"2:16:00"}`))

	if len(fake.writes) != 1 || !fake.writes[0].data.Played {
		t.Errorf("writes = %+v, want one write that says the film was watched", fake.writes)
	}
}

// A Play nobody claimed writes nothing, because a user-data write names
// a user.
func TestAPlayWithNoPeopleWritesNothing(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)

	out.audience("play-1", jellyfinAudience(t, nil, map[string]string{"tmdb": "1101"}, 0, 0))
	out.status("play-1", []byte(`{"item":0,"position":"0:10:00","duration":"2:16:00"}`))
	out.tick(t.Context())

	if len(fake.writes) != 0 {
		t.Errorf("writes = %+v, want none", fake.writes)
	}
}

// A Play whose audience arrived and whose position has not writes
// nothing, because a write of the start would move a person's resume
// point in Jellyfin back to the beginning.
func TestAPlayThatReportedNoPositionWritesNothing(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)

	out.audience("play-1", jellyfinAudience(t, []string{"person-a"}, map[string]string{"tmdb": "1101"}, 0, 0))
	out.tick(t.Context())

	if len(fake.writes) != 0 {
		t.Errorf("writes = %+v, want none", fake.writes)
	}
}

// A work Jellyfin does not hold leaves a line and no write, and so does
// a person it does not hold.
func TestAWorkOrAPersonJellyfinDoesNotHoldIsSkipped(t *testing.T) {
	for _, test := range []struct {
		name    string
		people  []string
		aliases map[string]string
		line    string
	}{
		{name: "a film jellyfin does not hold", people: []string{"person-a"},
			aliases: map[string]string{"tmdb": "1"}, line: "jellyfin holds no item for the Play of tmdb:1"},
		{name: "a person jellyfin does not hold", people: []string{"nobody"},
			aliases: map[string]string{"tmdb": "1101"}, line: "jellyfin holds no user named nobody"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := jellyfinFixture()
			out, logged := standJellyfinOutbound(t, fake)

			out.audience("play-1", jellyfinAudience(t, test.people, test.aliases, 0, 0))
			out.status("play-1", []byte(`{"item":0,"position":"0:10:00","duration":"2:16:00"}`))
			out.tick(t.Context())

			if len(fake.writes) != 0 {
				t.Errorf("writes = %+v, want none", fake.writes)
			}
			if !strings.Contains(logged.String(), test.line) {
				t.Errorf("log = %q, want %q", logged.String(), test.line)
			}
		})
	}
}

// A cleared topic is the operator releasing the Play, and the join goes
// with it.
func TestAClearedTopicForgetsThePlay(t *testing.T) {
	for _, test := range []struct {
		name  string
		clear func(*jellyfinOutbound)
	}{
		{name: "the status", clear: func(out *jellyfinOutbound) { out.status("play-1", nil) }},
		{name: "the audience", clear: func(out *jellyfinOutbound) { out.audience("play-1", nil) }},
		{name: "the final", clear: func(out *jellyfinOutbound) { out.final(t.Context(), "play-1", nil) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := jellyfinFixture()
			out, _ := standJellyfinOutbound(t, fake)
			out.audience("play-1", jellyfinAudience(t, []string{"person-a"}, map[string]string{"tmdb": "1101"}, 0, 0))
			out.status("play-1", []byte(`{"item":0,"position":"0:10:00","duration":"2:16:00"}`))

			test.clear(out)
			out.tick(t.Context())

			if len(fake.writes) != 0 {
				t.Errorf("writes = %+v, want none for a Play the role forgot", fake.writes)
			}
		})
	}
}

// A write the server refused leaves a line, and the role runs on.
func TestAWriteJellyfinRefusedLeavesALine(t *testing.T) {
	fake := jellyfinFixture()
	out, logged := standJellyfinOutbound(t, fake)
	out.audience("play-1", jellyfinAudience(t, []string{"person-a"}, map[string]string{"tmdb": "1101"}, 0, 0))
	out.status("play-1", []byte(`{"item":0,"position":"0:10:00","duration":"2:16:00"}`))
	fake.status = 500

	out.tick(t.Context())

	if !strings.Contains(logged.String(), "could not write the progress of person-a") {
		t.Errorf("log = %q, want the write it could not make", logged.String())
	}
}

// The role remembers what it wrote, so the webhook that carries that
// same position back is dropped.
func TestAWriteIsRememberedForTheEchoDrop(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)

	out.audience("play-1", jellyfinAudience(t, []string{"person-a"}, map[string]string{"tmdb": "1101"}, 0, 0))
	out.status("play-1", []byte(`{"item":0,"position":"1:10:10","duration":"2:16:00"}`))
	out.tick(t.Context())

	if !out.echoes.echoed("user-a", "item-film", 4210) {
		t.Error("the role did not remember the position it wrote")
	}
}

// A position within one second of the last write is that write coming
// back. Anything else is a person watching in Jellyfin.
func TestWhatCountsAsAnEcho(t *testing.T) {
	for _, test := range []struct {
		name     string
		user     string
		item     string
		position int
		echo     bool
	}{
		{name: "the position that was written", user: "user-a", item: "item-film", position: 4210, echo: true},
		{name: "one second later", user: "user-a", item: "item-film", position: 4211, echo: true},
		{name: "one second earlier", user: "user-a", item: "item-film", position: 4209, echo: true},
		{name: "two seconds later", user: "user-a", item: "item-film", position: 4212, echo: false},
		{name: "another item", user: "user-a", item: "item-other-film", position: 4210, echo: false},
		{name: "another user", user: "user-c", item: "item-film", position: 4210, echo: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			echoes := newJellyfinEchoes(newJellyfinClock().now)
			echoes.remember("user-a", "item-film", 4210)

			if got := echoes.echoed(test.user, test.item, test.position); got != test.echo {
				t.Errorf("echoed = %v, want %v", got, test.echo)
			}
		})
	}
}

// The written positions are bounded. The next write drops every write
// older than jellyfinEchoLife, so a pod that ran for weeks holds only its
// recent writes.
func TestAWrittenPositionIsDroppedOnceItAges(t *testing.T) {
	clock := newJellyfinClock()
	echoes := newJellyfinEchoes(clock.now)
	echoes.remember("user-a", "item-film", 4210)

	clock.advance(jellyfinEchoLife + time.Second)
	echoes.remember("user-c", "item-other-film", 10)

	if echoes.echoed("user-a", "item-film", 4210) {
		t.Error("a write past the echo's life still dropped a post")
	}
	if len(echoes.written) != 1 {
		t.Errorf("held %d writes, want the one still inside the life", len(echoes.written))
	}
}

// A message of a shape the role cannot read leaves a line and changes
// nothing, because one bad message must not stop the rest.
func TestAMessageOfAnotherShapeLeavesALine(t *testing.T) {
	for _, test := range []struct {
		name string
		read func(*jellyfinOutbound)
		line string
	}{
		{name: "the status", line: "the status of a Play with no audience yet reads as no report",
			read: func(out *jellyfinOutbound) { out.status("play-1", []byte("{")) }},
		{name: "the audience", line: "the audience of a Play with no audience yet reads as no audience",
			read: func(out *jellyfinOutbound) { out.audience("play-1", []byte("{")) }},
		{name: "the final", line: "the final status of a Play with no audience yet reads as no status",
			read: func(out *jellyfinOutbound) { out.final(t.Context(), "play-1", []byte("{")) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, logged := standJellyfinOutbound(t, jellyfinFixture())

			test.read(out)

			if !strings.Contains(logged.String(), test.line) {
				t.Errorf("log = %q, want %q", logged.String(), test.line)
			}
		})
	}
}

// A position of another shape records zero and leaves a line, the rule
// the progress role follows.
func TestAPositionOfAnotherShapeReadsAsTheStart(t *testing.T) {
	out, logged := standJellyfinOutbound(t, jellyfinFixture())

	out.status("play-1", []byte(`{"item":0,"position":"soon","duration":"2:16:00"}`))

	if !strings.Contains(logged.String(), `the position "soon" of a Play with no audience yet reads as no time`) {
		t.Errorf("log = %q, want the position it could not read", logged.String())
	}
}

// An outbound half with no log writes nothing and fails at nothing.
func TestAnOutboundWithNoLogWritesNothing(t *testing.T) {
	api := standJellyfinServer(t, jellyfinFixture())
	clock := newJellyfinClock()
	out := newJellyfinOutbound(api, newJellyfinIndex(api, clock.now, nil), clock.now, nil)

	out.status("play-1", []byte("{"))
}

// A tick over a Play nothing reported writes nothing.
func TestATickOverNoPlaysWritesNothing(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)

	out.tick(t.Context())

	if len(fake.writes) != 0 {
		t.Errorf("writes = %+v, want none", fake.writes)
	}
}
