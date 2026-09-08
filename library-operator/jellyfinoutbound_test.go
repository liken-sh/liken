package main

// What these tests prove: the join of a Play's two messages, the ten
// second throttle, the rule that says a person finished a work, and
// what the role does with a work or a person Jellyfin does not hold.

import (
	"encoding/json"
	"strings"
	"testing"
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

	out.audience("play-1", jellyfinAudience(t, []string{"chris"}, map[string]string{"tmdb": "603"}, 0, 0))
	out.status("play-1", []byte(`{"item":0,"position":"1:10:10","duration":"2:16:00"}`))
	out.tick(t.Context())

	if len(fake.writes) != 1 {
		t.Fatalf("writes = %+v, want one", fake.writes)
	}
	want := fakeJellyfinWrite{item: "item-matrix", user: "user-chris", data: jellyfinUserData{
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

	out.audience("play-1", jellyfinAudience(t, []string{"chris"}, map[string]string{"tmdb": "2316"}, 3, 5))
	out.status("play-1", []byte(`{"item":4,"position":"0:10:00","duration":"0:22:00"}`))
	out.tick(t.Context())

	if len(fake.writes) != 1 || fake.writes[0].item != "item-office-3-5" {
		t.Errorf("writes = %+v, want one write of the episode", fake.writes)
	}
}

// Every person who watched gets their own write, because a user-data
// write names one user.
func TestEveryPersonOfAPlayGetsAWrite(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)

	out.audience("play-1", jellyfinAudience(t, []string{"chris", "kelly"}, map[string]string{"tmdb": "603"}, 0, 0))
	out.status("play-1", []byte(`{"item":0,"position":"0:10:00","duration":"2:16:00"}`))
	out.tick(t.Context())

	if len(fake.writes) != 2 || fake.writes[0].user != "user-chris" || fake.writes[1].user != "user-kelly" {
		t.Errorf("writes = %+v, want one for each person", fake.writes)
	}
}

// A position that did not move writes nothing, so a paused film is one
// message on the bus and no request to Jellyfin.
func TestATickWritesOnlyThePositionsThatMoved(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)
	out.audience("play-1", jellyfinAudience(t, []string{"chris"}, map[string]string{"tmdb": "603"}, 0, 0))
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

// A Play's final writes at once, because the position a person resumes
// from is the one the Play ended on.
func TestAFinalWritesAtOnce(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)
	out.audience("play-1", jellyfinAudience(t, []string{"chris"}, map[string]string{"tmdb": "603"}, 0, 0))

	out.final(t.Context(), "play-1", []byte(`{"phase":"Finished","item":0,"position":"2:16:00","duration":"2:16:00"}`))

	if len(fake.writes) != 1 || !fake.writes[0].data.Played {
		t.Errorf("writes = %+v, want one write that says the film was watched", fake.writes)
	}
}

// A person finished a work when the Play ended inside the last two
// minutes of it, or when a Play that reached the end says it finished.
// A duration of zero says the report never carried one, and no such
// Play is watched.
func TestWhatCountsAsWatched(t *testing.T) {
	for _, test := range []struct {
		name   string
		play   jellyfinPlay
		ended  bool
		played bool
	}{
		{name: "a film that ran to the end", ended: true, played: true,
			play: jellyfinPlay{position: 8160, duration: 8160, phase: playPhaseFinished}},
		{name: "a film stopped inside the last two minutes", ended: true, played: true,
			play: jellyfinPlay{position: 8100, duration: 8160}},
		{name: "a film stopped an hour in", ended: true, played: false,
			play: jellyfinPlay{position: 3600, duration: 8160}},
		{name: "a film still running", ended: false, played: false,
			play: jellyfinPlay{position: 8160, duration: 8160, phase: playPhaseFinished}},
		{name: "a play that carried no duration", ended: true, played: false,
			play: jellyfinPlay{position: 0, duration: 0, phase: playPhaseFinished}},
		{name: "a play that failed short of the end", ended: true, played: false,
			play: jellyfinPlay{position: 10, duration: 8160, phase: playPhaseFailed}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := jellyfinPlayed(test.play, test.ended); got != test.played {
				t.Errorf("played = %v, want %v", got, test.played)
			}
		})
	}
}

// A Play nobody claimed writes nothing, because a user-data write names
// a user.
func TestAPlayWithNoPeopleWritesNothing(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)

	out.audience("play-1", jellyfinAudience(t, nil, map[string]string{"tmdb": "603"}, 0, 0))
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

	out.audience("play-1", jellyfinAudience(t, []string{"chris"}, map[string]string{"tmdb": "603"}, 0, 0))
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
		{name: "a film jellyfin does not hold", people: []string{"chris"},
			aliases: map[string]string{"tmdb": "1"}, line: "jellyfin holds no item for play-1"},
		{name: "a person jellyfin does not hold", people: []string{"nobody"},
			aliases: map[string]string{"tmdb": "603"}, line: "jellyfin holds no user named nobody"},
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
			out.audience("play-1", jellyfinAudience(t, []string{"chris"}, map[string]string{"tmdb": "603"}, 0, 0))
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
	out.audience("play-1", jellyfinAudience(t, []string{"chris"}, map[string]string{"tmdb": "603"}, 0, 0))
	out.status("play-1", []byte(`{"item":0,"position":"0:10:00","duration":"2:16:00"}`))
	fake.status = 500

	out.tick(t.Context())

	if !strings.Contains(logged.String(), "could not write the progress of chris") {
		t.Errorf("log = %q, want the write it could not make", logged.String())
	}
}

// The role remembers what it wrote, so the webhook that carries that
// same position back is dropped.
func TestAWriteIsRememberedForTheEchoDrop(t *testing.T) {
	fake := jellyfinFixture()
	out, _ := standJellyfinOutbound(t, fake)

	out.audience("play-1", jellyfinAudience(t, []string{"chris"}, map[string]string{"tmdb": "603"}, 0, 0))
	out.status("play-1", []byte(`{"item":0,"position":"1:10:10","duration":"2:16:00"}`))
	out.tick(t.Context())

	if !out.echoes.echoed("user-chris", "item-matrix", 4210) {
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
		{name: "the position that was written", user: "user-chris", item: "item-matrix", position: 4210, echo: true},
		{name: "one second later", user: "user-chris", item: "item-matrix", position: 4211, echo: true},
		{name: "one second earlier", user: "user-chris", item: "item-matrix", position: 4209, echo: true},
		{name: "two seconds later", user: "user-chris", item: "item-matrix", position: 4212, echo: false},
		{name: "another item", user: "user-chris", item: "item-arrival", position: 4210, echo: false},
		{name: "another user", user: "user-kelly", item: "item-matrix", position: 4210, echo: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			echoes := newJellyfinEchoes()
			echoes.remember("user-chris", "item-matrix", 4210)

			if got := echoes.echoed(test.user, test.item, test.position); got != test.echo {
				t.Errorf("echoed = %v, want %v", got, test.echo)
			}
		})
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
		{name: "the status", line: "the status of play-1 reads as no report",
			read: func(out *jellyfinOutbound) { out.status("play-1", []byte("{")) }},
		{name: "the audience", line: "the audience of play-1 reads as no audience",
			read: func(out *jellyfinOutbound) { out.audience("play-1", []byte("{")) }},
		{name: "the final", line: "the final status of play-1 reads as no status",
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

	if !strings.Contains(logged.String(), `the position "soon" of play-1 reads as no time`) {
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
