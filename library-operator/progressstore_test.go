package main

// What these tests prove: the rows the progress store writes and reads,
// against a real SQLite database loaded with the shipped progress
// schema, so a statement that names a column the schema does not have
// fails here.

import (
	"database/sql"
	"net/http/httptest"
	"testing"
	"time"
)

// The time every test here records at, so a written row carries a value
// a test can name.
var testRecordedAt = time.Unix(1_700_000_000, 0).UTC()

// newSQLiteProgressStore serves the progress schema over the two
// endpoints a Corrosion agent answers, and hands back the store the
// progress role writes through.
func newSQLiteProgressStore(t *testing.T) (*progressStore, *sql.DB) {
	t.Helper()
	db := newSQLiteProgress(t)
	server := httptest.NewServer(&sqliteAgent{db: db})
	t.Cleanup(server.Close)
	return newProgressStore(server.URL, server.Client()), db
}

// The first position report creates the row, stamps the time it
// started, and marks the Play running.
func TestTheFirstPositionCreatesTheRow(t *testing.T) {
	store, db := newSQLiteProgressStore(t)

	if err := store.recordPosition(t.Context(), "play-1", 2, 61, 3600, testRecordedAt); err != nil {
		t.Fatal(err)
	}

	row := heldPlay(t, db, "play-1")
	if row.Item != 2 || row.Position != 61 || row.Duration != 3600 {
		t.Errorf("row = %+v, want the reported item, position, and duration", row)
	}
	if row.Phase != playPhaseRunning {
		t.Errorf("phase = %q, want %q", row.Phase, playPhaseRunning)
	}
	if row.Started != testRecordedAt.Unix() || row.Recorded != testRecordedAt.Unix() {
		t.Errorf("row = %+v, want the time of the first write", row)
	}
	if row.Ended != 0 {
		t.Errorf("ended = %d, want 0 while the Play runs", row.Ended)
	}
}

// A later report moves the position and the recorded time and leaves
// the time the Play started where it was.
func TestALaterPositionKeepsTheStart(t *testing.T) {
	store, db := newSQLiteProgressStore(t)
	if err := store.recordPosition(t.Context(), "play-1", 0, 10, 3600, testRecordedAt); err != nil {
		t.Fatal(err)
	}

	later := testRecordedAt.Add(time.Minute)
	if err := store.recordPosition(t.Context(), "play-1", 0, 70, 3600, later); err != nil {
		t.Fatal(err)
	}

	row := heldPlay(t, db, "play-1")
	if row.Started != testRecordedAt.Unix() {
		t.Errorf("started = %d, want the first write %d", row.Started, testRecordedAt.Unix())
	}
	if row.Position != 70 || row.Recorded != later.Unix() {
		t.Errorf("row = %+v, want the later position and time", row)
	}
}

// The audience names the Player, the Library, the Watch, and the
// episode, and it writes one row per person and one per alias.
func TestTheAudienceWritesItsPeopleAndAliases(t *testing.T) {
	store, db := newSQLiteProgressStore(t)
	audience := playAudience{
		Player: "living-room", Library: "series", Watch: "the-office-with-the-girls",
		People:  []string{"chris", "thora"},
		Aliases: map[string]string{"tmdb": "2316", "imdb": "tt0386676"},
		Season:  3, Episode: 5,
	}

	if err := store.recordAudience(t.Context(), "play-1", audience, testRecordedAt); err != nil {
		t.Fatal(err)
	}

	row := heldPlay(t, db, "play-1")
	if row.Player != "living-room" || row.Library != "series" || row.Watch != "the-office-with-the-girls" {
		t.Errorf("row = %+v, want the Player, the Library, and the Watch", row)
	}
	if row.Season != 3 || row.Episode != 5 {
		t.Errorf("row = %+v, want the season and the episode", row)
	}
	if row.Phase != "" {
		t.Errorf("phase = %q, want no phase from an audience alone", row.Phase)
	}
	if people := heldPeople(t, db, "play-1"); len(people) != 2 || people["chris"] != 1 || people["thora"] != 1 {
		t.Errorf("people = %v, want chris and thora", people)
	}
	if aliases := heldAliases(t, db, "play-1"); aliases["tmdb"] != "2316" || aliases["imdb"] != "tt0386676" {
		t.Errorf("aliases = %v, want the two provider ids", aliases)
	}
}

// A second audience replaces the sets, so a person who left the room
// and an alias the operator no longer states are gone.
func TestASecondAudienceReplacesTheSets(t *testing.T) {
	store, db := newSQLiteProgressStore(t)
	first := playAudience{People: []string{"chris", "thora"}, Aliases: map[string]string{"tmdb": "2316", "imdb": "tt0386676"}}
	if err := store.recordAudience(t.Context(), "play-1", first, testRecordedAt); err != nil {
		t.Fatal(err)
	}

	second := playAudience{People: []string{"chris"}, Aliases: map[string]string{"tmdb": "2316"}}
	if err := store.recordAudience(t.Context(), "play-1", second, testRecordedAt); err != nil {
		t.Fatal(err)
	}

	people := heldPeople(t, db, "play-1")
	if len(people) != 1 || people["chris"] != 1 {
		t.Errorf("people = %v, want chris alone", people)
	}
	aliases := heldAliases(t, db, "play-1")
	if len(aliases) != 1 || aliases["tmdb"] != "2316" {
		t.Errorf("aliases = %v, want the tmdb id alone", aliases)
	}
}

// An audience for a Play with no row creates one, so an audience that
// arrives before the first position still holds the people.
func TestAnAudienceCreatesTheRowItNames(t *testing.T) {
	store, db := newSQLiteProgressStore(t)

	if err := store.recordAudience(t.Context(), "play-1",
		playAudience{Player: "bedroom"}, testRecordedAt); err != nil {
		t.Fatal(err)
	}

	row := heldPlay(t, db, "play-1")
	if row.Play != "play-1" || row.Player != "bedroom" || row.Started != testRecordedAt.Unix() {
		t.Errorf("row = %+v, want the row an audience created", row)
	}
}

// The final marks the row ended, so the operator releases the Play's
// finalizer knowing the last position is in the store.
func TestTheFinalMarksTheRowEnded(t *testing.T) {
	store, db := newSQLiteProgressStore(t)
	if err := store.recordPosition(t.Context(), "play-1", 0, 10, 3600, testRecordedAt); err != nil {
		t.Fatal(err)
	}

	ended := testRecordedAt.Add(time.Hour)
	if err := store.recordFinal(t.Context(), "play-1",
		playFinal{Phase: "Finished", Item: 1}, 3599, 3600, ended); err != nil {
		t.Fatal(err)
	}

	row := heldPlay(t, db, "play-1")
	if row.Phase != "Finished" || row.Item != 1 || row.Position != 3599 || row.Duration != 3600 {
		t.Errorf("row = %+v, want the final status", row)
	}
	if row.Ended != ended.Unix() || row.Recorded != ended.Unix() {
		t.Errorf("row = %+v, want the time the Play ended", row)
	}
}

// The time the outside plays here are recorded at, which is the time the
// event carries and not the time of the write.
const testOutsideAt int64 = 1_757_300_000

// One outside play writes the whole row: the player, the episode, the people,
// the aliases, the position, and the time of the event as the recorded time.
func TestAnOutsidePlayWritesTheWholeRow(t *testing.T) {
	store, db := newSQLiteProgressStore(t)
	outside := outsidePlay{
		Player:  "jellyfin",
		People:  []string{"chris"},
		Aliases: map[string]string{"tmdb": "603", "imdb": "tt0133093"},
		Season:  2, Episode: 4,
		Position: 4210, Duration: 8160, At: testOutsideAt,
	}

	if err := store.recordOutside(t.Context(), "jellyfin-7-19", outside); err != nil {
		t.Fatal(err)
	}

	row := heldPlay(t, db, "jellyfin-7-19")
	if row.Player != "jellyfin" || row.Library != "" || row.Watch != "" {
		t.Errorf("row = %+v, want the outside player and no Library and no Watch", row)
	}
	if row.Season != 2 || row.Episode != 4 || row.Position != 4210 || row.Duration != 8160 {
		t.Errorf("row = %+v, want the episode, the position, and the duration", row)
	}
	if row.Phase != playPhaseRunning || row.Ended != 0 {
		t.Errorf("row = %+v, want a play still running", row)
	}
	if row.Recorded != testOutsideAt || row.Started != testOutsideAt {
		t.Errorf("row = %+v, want the time of the event", row)
	}
	if people := heldPeople(t, db, "jellyfin-7-19"); len(people) != 1 || people["chris"] != 1 {
		t.Errorf("people = %v, want chris", people)
	}
	if aliases := heldAliases(t, db, "jellyfin-7-19"); aliases["tmdb"] != "603" || aliases["imdb"] != "tt0133093" {
		t.Errorf("aliases = %v, want the two provider ids", aliases)
	}
}

// The newer event wins. A message whose time is not past the time the row
// holds writes nothing, so a post that arrives late never moves a position
// backward.
func TestTheNewerOutsidePlayWins(t *testing.T) {
	cases := []struct {
		name     string
		at       int64
		position int
		want     int
		recorded int64
	}{
		{name: "an older event", at: testOutsideAt - 60, position: 10, want: 4210, recorded: testOutsideAt},
		{name: "the same second", at: testOutsideAt, position: 10, want: 4210, recorded: testOutsideAt},
		{name: "a newer event", at: testOutsideAt + 60, position: 7000, want: 7000, recorded: testOutsideAt + 60},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store, db := newSQLiteProgressStore(t)
			first := outsidePlay{Player: "jellyfin", People: []string{"chris"},
				Position: 4210, Duration: 8160, At: testOutsideAt}
			if err := store.recordOutside(t.Context(), "jellyfin-7-19", first); err != nil {
				t.Fatal(err)
			}

			second := outsidePlay{Player: "jellyfin", People: []string{"thora"},
				Position: testCase.position, Duration: 8160, At: testCase.at}
			if err := store.recordOutside(t.Context(), "jellyfin-7-19", second); err != nil {
				t.Fatal(err)
			}

			row := heldPlay(t, db, "jellyfin-7-19")
			if row.Position != testCase.want || row.Recorded != testCase.recorded {
				t.Errorf("row = %+v, want position %d recorded %d",
					row, testCase.want, testCase.recorded)
			}
		})
	}
}

// A message the store did not take leaves the people it did not write, which
// is how a reader sees that the whole apply was skipped.
func TestAnOlderOutsidePlayLeavesThePeople(t *testing.T) {
	store, db := newSQLiteProgressStore(t)
	first := outsidePlay{Player: "jellyfin", People: []string{"chris"}, At: testOutsideAt}
	if err := store.recordOutside(t.Context(), "jellyfin-7-19", first); err != nil {
		t.Fatal(err)
	}

	older := outsidePlay{Player: "jellyfin", People: []string{"thora"}, At: testOutsideAt - 60}
	if err := store.recordOutside(t.Context(), "jellyfin-7-19", older); err != nil {
		t.Fatal(err)
	}

	people := heldPeople(t, db, "jellyfin-7-19")
	if len(people) != 1 || people["chris"] != 1 {
		t.Errorf("people = %v, want chris alone", people)
	}
}

// A stop marks the row ended and finished, which is what a screen reads as a
// work watched to the end.
func TestAnEndedOutsidePlayMarksTheRowEnded(t *testing.T) {
	store, db := newSQLiteProgressStore(t)
	running := outsidePlay{Player: "jellyfin", Position: 4210, Duration: 8160, At: testOutsideAt}
	if err := store.recordOutside(t.Context(), "jellyfin-7-19", running); err != nil {
		t.Fatal(err)
	}

	stopped := outsidePlay{Player: "jellyfin", Position: 8160, Duration: 8160,
		Ended: true, At: testOutsideAt + 3600}
	if err := store.recordOutside(t.Context(), "jellyfin-7-19", stopped); err != nil {
		t.Fatal(err)
	}

	row := heldPlay(t, db, "jellyfin-7-19")
	if row.Phase != playPhaseFinished || row.Position != 8160 {
		t.Errorf("row = %+v, want the finished phase and the last position", row)
	}
	if row.Ended != testOutsideAt+3600 || row.Recorded != testOutsideAt+3600 {
		t.Errorf("row = %+v, want the time the play stopped", row)
	}
}

// Forgetting a person takes their rows out of every Play and leaves the
// Plays and the people who remain, because a shared record belongs to
// the people who are still in it.
func TestForgettingAPersonLeavesThePlaysAndTheOthers(t *testing.T) {
	store, db := newSQLiteProgressStore(t)
	if err := store.recordAudience(t.Context(), "play-1",
		playAudience{People: []string{"chris", "thora"}}, testRecordedAt); err != nil {
		t.Fatal(err)
	}

	if err := store.forgetPerson(t.Context(), "thora"); err != nil {
		t.Fatal(err)
	}

	people := heldPeople(t, db, "play-1")
	if len(people) != 1 || people["chris"] != 1 {
		t.Errorf("people = %v, want chris alone", people)
	}
	if heldPlay(t, db, "play-1").Play != "play-1" {
		t.Error("forgetting a person took the Play's own row")
	}
}

// The Watch reads the latest Play recorded against it, which is what
// the operator writes into Watch.status.
func TestTheWatchReadsItsLatestPlay(t *testing.T) {
	store, _ := newSQLiteProgressStore(t)
	watch := playAudience{Watch: "the-office-with-the-girls"}
	for _, seeded := range []struct {
		play string
		at   time.Time
		item int
	}{
		{"play-1", testRecordedAt, 1},
		{"play-2", testRecordedAt.Add(time.Hour), 2},
	} {
		if err := store.recordAudience(t.Context(), seeded.play, watch, seeded.at); err != nil {
			t.Fatal(err)
		}
		if err := store.recordPosition(t.Context(), seeded.play, seeded.item, 30, 1800, seeded.at); err != nil {
			t.Fatal(err)
		}
	}

	row, held, err := store.latestForWatch(t.Context(), "the-office-with-the-girls")
	if err != nil {
		t.Fatal(err)
	}

	if !held || row.Play != "play-2" || row.Item != 2 {
		t.Errorf("row = %+v held %v, want the latest Play of the Watch", row, held)
	}
}

// A Watch nothing was recorded against reads no row, so the role
// publishes nothing for it.
func TestAWatchWithNoPlayReadsNoRow(t *testing.T) {
	store, _ := newSQLiteProgressStore(t)

	_, held, err := store.latestForWatch(t.Context(), "nobody")
	if err != nil {
		t.Fatal(err)
	}

	if held {
		t.Error("a Watch with no Play read a row")
	}
}

// The Watch one Play belongs to, which is how a write finds the Watch
// it has to republish.
func TestTheStoreReadsTheWatchOfAPlay(t *testing.T) {
	store, _ := newSQLiteProgressStore(t)
	if err := store.recordAudience(t.Context(), "play-1",
		playAudience{Watch: "the-office-with-the-girls"}, testRecordedAt); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		play string
		want string
	}{
		{name: "a Play in a Watch", play: "play-1", want: "the-office-with-the-girls"},
		{name: "a Play the store never saw", play: "play-9", want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			watch, err := store.watchOf(t.Context(), testCase.play)
			if err != nil {
				t.Fatal(err)
			}
			if watch != testCase.want {
				t.Errorf("watchOf(%q) = %q, want %q", testCase.play, watch, testCase.want)
			}
		})
	}
}

// A store whose agent refuses the write answers the failure, so the
// role logs it and drops the message.
func TestTheStoreAnswersTheFailureItsAgentGives(t *testing.T) {
	db := newSQLiteProgress(t)
	agent := &sqliteAgent{db: db, transactionsLeft: 1}
	server := httptest.NewServer(agent)
	t.Cleanup(server.Close)
	store := newProgressStore(server.URL, server.Client())

	err := store.recordPosition(t.Context(), "play-1", 0, 10, 3600, testRecordedAt)

	if err == nil {
		t.Fatal("the store hid a refused write")
	}
}

// heldPlay reads one plays row straight out of the database, so a test
// reads what the agent holds and not what the store answered.
func heldPlay(t *testing.T, db *sql.DB, play string) playRow {
	t.Helper()
	row := playRow{}
	err := db.QueryRow(`SELECT play, player, library, watch, started, ended, item, position,`+
		` duration, phase, season, episode, recorded FROM plays WHERE play = ?`, play).Scan(
		&row.Play, &row.Player, &row.Library, &row.Watch, &row.Started, &row.Ended,
		&row.Item, &row.Position, &row.Duration, &row.Phase, &row.Season, &row.Episode, &row.Recorded)
	if err == sql.ErrNoRows {
		return playRow{}
	}
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func heldPeople(t *testing.T, db *sql.DB, play string) map[string]int {
	t.Helper()
	rows, err := db.Query(`SELECT person FROM play_people WHERE play = ?`, play)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	held := map[string]int{}
	for rows.Next() {
		var person string
		if err := rows.Scan(&person); err != nil {
			t.Fatal(err)
		}
		held[person]++
	}
	return held
}

func heldAliases(t *testing.T, db *sql.DB, play string) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT provider, id FROM play_aliases WHERE play = ?`, play)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	held := map[string]string{}
	for rows.Next() {
		var provider, id string
		if err := rows.Scan(&provider, &id); err != nil {
			t.Fatal(err)
		}
		held[provider] = id
	}
	return held
}
