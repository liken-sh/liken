package main

// What these tests prove: the index the role builds from one listing,
// the keys a movie and an episode take, the series ids the webhook
// needs, and the two bounds a rebuild takes.

import (
	"strings"
	"testing"
	"time"
)

// One index over a fake Jellyfin, with a clock the test moves and a log
// the test reads.
func standJellyfinIndex(t *testing.T, fake *fakeJellyfin) (*jellyfinIndex, *jellyfinClock, *strings.Builder) {
	t.Helper()
	clock := newJellyfinClock()
	logged := &strings.Builder{}
	return newJellyfinIndex(standJellyfinServer(t, fake), clock.now, logged), clock, logged
}

// A movie is keyed by every provider id it carries, and an episode by
// the series' ids with the season and the episode beside them.
func TestTheIndexAnswersTheItemOfOneWork(t *testing.T) {
	for _, test := range []struct {
		name    string
		aliases map[string]string
		season  int
		episode int
		item    string
	}{
		{name: "a film by its tmdb id", aliases: map[string]string{"tmdb": "603"}, item: "item-matrix"},
		{name: "a film by its imdb id", aliases: map[string]string{"imdb": "tt0133093"}, item: "item-matrix"},
		{name: "a film by two ids", aliases: map[string]string{"imdb": "tt0133093", "tmdb": "603"}, item: "item-matrix"},
		{name: "the other film", aliases: map[string]string{"tmdb": "329865"}, item: "item-arrival"},
		{name: "an episode by the series' tmdb id", aliases: map[string]string{"tmdb": "2316"},
			season: 3, episode: 5, item: "item-office-3-5"},
		{name: "an episode by the series' tvdb id", aliases: map[string]string{"tvdb": "73244"},
			season: 3, episode: 5, item: "item-office-3-5"},
		{name: "a film nothing holds", aliases: map[string]string{"tmdb": "1"}, item: ""},
		{name: "an episode of a season nothing holds", aliases: map[string]string{"tmdb": "2316"},
			season: 4, episode: 5, item: ""},
		{name: "an episode by its own id", aliases: map[string]string{"tmdb": "999"},
			season: 3, episode: 5, item: ""},
		{name: "a work with no ids", aliases: nil, item: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			index, _, _ := standJellyfinIndex(t, jellyfinFixture())
			index.prime(t.Context())

			item, found := index.itemFor(t.Context(), test.aliases, test.season, test.episode)

			if item != test.item || found != (test.item != "") {
				t.Errorf("item = %q, %v, want %q", item, found, test.item)
			}
		})
	}
}

// The listing carries every series, so the ids of a series an episode
// names are already held and no second call is made.
func TestTheIndexHoldsTheSeriesTheListingCarried(t *testing.T) {
	fake := jellyfinFixture()
	index, _, _ := standJellyfinIndex(t, fake)
	index.prime(t.Context())

	aliases := index.seriesAliases(t.Context(), "item-office")

	if aliases["tmdb"] != "2316" || aliases["tvdb"] != "73244" {
		t.Errorf("aliases = %v, want the series' two ids", aliases)
	}
	if fake.seriesReads != 0 {
		t.Errorf("the index read %d series, want none", fake.seriesReads)
	}
}

// A series the index does not hold is read once and held, because a
// webhook carries an episode's own ids and the series' id alone.
func TestASeriesTheIndexDoesNotHoldIsReadOnce(t *testing.T) {
	fake := jellyfinFixture()
	index, _, _ := standJellyfinIndex(t, fake)

	first := index.seriesAliases(t.Context(), "item-office")
	second := index.seriesAliases(t.Context(), "item-office")

	if first["tmdb"] != "2316" || second["tmdb"] != "2316" {
		t.Errorf("aliases = %v and %v, want the series' ids both times", first, second)
	}
	if fake.seriesReads != 1 {
		t.Errorf("the index read %d series, want one", fake.seriesReads)
	}
}

// An event that names no series reads nothing, and a series the server
// does not hold leaves a line and no ids.
func TestASeriesTheServerCannotAnswerIsNoIdentity(t *testing.T) {
	fake := jellyfinFixture()
	index, _, logged := standJellyfinIndex(t, fake)

	if aliases := index.seriesAliases(t.Context(), ""); aliases != nil {
		t.Errorf("aliases = %v, want none for an event that names no series", aliases)
	}
	if aliases := index.seriesAliases(t.Context(), "item-nothing"); aliases != nil {
		t.Errorf("aliases = %v, want none for a series the server refused", aliases)
	}
	if !strings.Contains(logged.String(), "item-nothing") {
		t.Errorf("log = %q, want the series it could not read", logged.String())
	}
}

// A miss rebuilds the index, and at most once a minute, so a work
// Jellyfin does not hold costs one listing a minute.
func TestAMissRebuildsTheIndexAtMostOnceAMinute(t *testing.T) {
	fake := jellyfinFixture()
	index, clock, _ := standJellyfinIndex(t, fake)
	index.prime(t.Context())
	fake.items = append(fake.items,
		jellyfinItem{ID: "item-dune", Type: "Movie", ProviderIds: map[string]string{"Tmdb": "438631"}})
	dune := map[string]string{"tmdb": "438631"}

	if _, found := index.itemFor(t.Context(), dune, 0, 0); found {
		t.Error("the index answered a film it had not read")
	}
	if fake.listings != 1 {
		t.Fatalf("the index read %d listings, want the one it was primed with", fake.listings)
	}

	clock.advance(jellyfinIndexFloor)
	item, found := index.itemFor(t.Context(), dune, 0, 0)

	if item != "item-dune" || !found {
		t.Errorf("item = %q, %v, want item-dune", item, found)
	}
	if fake.listings != 2 {
		t.Errorf("the index read %d listings, want two", fake.listings)
	}
}

// An index older than an hour is rebuilt before the lookup, so a film
// added today is found without a miss.
func TestAnIndexOlderThanAnHourIsRebuilt(t *testing.T) {
	fake := jellyfinFixture()
	index, clock, _ := standJellyfinIndex(t, fake)
	index.prime(t.Context())
	clock.advance(jellyfinIndexLifetime)

	if _, found := index.itemFor(t.Context(), map[string]string{"tmdb": "603"}, 0, 0); !found {
		t.Error("the index lost the film it held")
	}
	if fake.listings != 2 {
		t.Errorf("the index read %d listings, want two", fake.listings)
	}
}

// A Person name reaches a user id without case, because Jellyfin holds
// the name a person typed and the Person name is the one the cluster
// holds.
func TestTheUsersMapReadsAPersonNameWithoutCase(t *testing.T) {
	for _, test := range []struct {
		name   string
		person string
		user   string
	}{
		{name: "the name as jellyfin holds it", person: "Chris", user: "user-chris"},
		{name: "the name in lower case", person: "chris", user: "user-chris"},
		{name: "the name in upper case", person: "KELLY", user: "user-kelly"},
		{name: "a person jellyfin does not hold", person: "nobody", user: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			index, _, _ := standJellyfinIndex(t, jellyfinFixture())
			index.prime(t.Context())

			user, known := index.userFor(t.Context(), test.person)

			if user != test.user || known != (test.user != "") {
				t.Errorf("user = %q, %v, want %q", user, known, test.user)
			}
		})
	}
}

// A person the map does not hold reads the users again, under the same
// floor the listing takes, because a person added today is a miss
// today.
func TestAPersonTheMapDoesNotHoldReadsTheUsersAgain(t *testing.T) {
	fake := jellyfinFixture()
	index, clock, _ := standJellyfinIndex(t, fake)
	index.prime(t.Context())
	fake.users = append(fake.users, jellyfinUser{Name: "sam", ID: "user-sam"})

	if _, known := index.userFor(t.Context(), "sam"); known {
		t.Error("the index answered a user it had not read")
	}
	if fake.userReads != 1 {
		t.Fatalf("the index read the users %d times, want the one it was primed with", fake.userReads)
	}

	clock.advance(jellyfinIndexFloor)
	user, known := index.userFor(t.Context(), "sam")

	if user != "user-sam" || !known {
		t.Errorf("user = %q, %v, want user-sam", user, known)
	}
	if fake.userReads != 2 {
		t.Errorf("the index read the users %d times, want two", fake.userReads)
	}
}

// A Jellyfin that refuses every call leaves an empty index and two
// lines in the pod log, and the role runs on.
func TestAJellyfinThatRefusesLeavesTheIndexEmpty(t *testing.T) {
	fake := jellyfinFixture()
	fake.status = 500
	index, _, logged := standJellyfinIndex(t, fake)

	index.prime(t.Context())

	if _, found := index.itemFor(t.Context(), map[string]string{"tmdb": "603"}, 0, 0); found {
		t.Error("the index answered from a server that refused it")
	}
	if _, known := index.userFor(t.Context(), "chris"); known {
		t.Error("the index answered a user from a server that refused it")
	}
	if !strings.Contains(logged.String(), "could not read the items") ||
		!strings.Contains(logged.String(), "could not read the users") {
		t.Errorf("log = %q, want a line for each read it could not make", logged.String())
	}
}

// An index with no log writes nothing and fails at nothing, which is
// the shape a caller that wants no lines builds.
func TestAnIndexWithNoLogWritesNothing(t *testing.T) {
	fake := jellyfinFixture()
	fake.status = 500
	index := newJellyfinIndex(standJellyfinServer(t, fake), newJellyfinClock().now, nil)

	index.refreshItems(t.Context(), time.Duration(0))
}
