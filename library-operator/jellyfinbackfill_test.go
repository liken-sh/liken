package main

// what these tests prove: what one item Jellyfin holds becomes on the bus,
// which items the run skips, that an item in both reads is published once,
// and what one user's failed read costs the run.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// a Jellyfin that answers the three calls the backfill makes from fixture
// values: the users, one user's items under each of the two filters, and one
// series. It records every query, so a test proves the paths. The refusals
// are what a test drives a failed read with: every call, or the item reads of
// one user, or that user's played read alone.
type fakeBackfillJellyfin struct {
	mutex     sync.Mutex
	users     []jellyfinUser
	resumable map[string][]jellyfinItem
	played    map[string][]jellyfinItem
	series    map[string]jellyfinItem
	refuse    string
	refuseOne bool
	refuseAll bool
	queries   []string
}

func (f *fakeBackfillJellyfin) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.queries = append(f.queries, request.URL.RequestURI())
	switch {
	case request.URL.Path == "/Users":
		if f.refuseAll {
			http.Error(w, "the server refused", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(f.users)
	case request.URL.Path == "/Items":
		user := request.URL.Query().Get("userId")
		played := request.URL.Query().Get("isPlayed") == "true"
		if user == f.refuse && (played || !f.refuseOne) {
			http.Error(w, "the server refused", http.StatusInternalServerError)
			return
		}
		held := f.resumable[user]
		if played {
			held = f.played[user]
		}
		_ = json.NewEncoder(w).Encode(jellyfinItemList{Items: held})
	default:
		item, held := f.series[strings.TrimPrefix(request.URL.Path, "/Items/")]
		if !held {
			http.Error(w, "no such item", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(item)
	}
}

// the one series every episode of these tests belongs to.
func backfillSeries() map[string]jellyfinItem {
	return map[string]jellyfinItem{
		"item-office": {ID: "item-office", Type: "Series",
			ProviderIds: map[string]string{"Tmdb": "2316", "Tvdb": "73244"}},
	}
}

// one backfill over a fake Jellyfin, with a recording publish function and a
// log the test reads.
func standBackfill(t *testing.T, fake *fakeBackfillJellyfin) (
	*jellyfinBackfill, *jellyfinMessages, *strings.Builder) {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	logged := &strings.Builder{}
	messages := &jellyfinMessages{}
	backfill := newJellyfinBackfillOn("house", defaultTopicBase,
		newJellyfinAPI(server.URL, "the-key", server.Client()), messages.publish, logged)
	return backfill, messages, logged
}

// every message the run published, read back as the payloads the progress
// role receives.
func (m *jellyfinMessages) outsidePlays(t *testing.T) map[string]outsidePlay {
	t.Helper()
	m.mutex.Lock()
	defer m.mutex.Unlock()
	plays := map[string]outsidePlay{}
	for _, message := range m.held {
		play := outsidePlay{}
		if err := json.Unmarshal(message.payload, &play); err != nil {
			t.Fatalf("reading the message back: %v", err)
		}
		plays[message.topic] = play
	}
	return plays
}

// one item of Jellyfin becomes one outside play, with the position and the
// duration in seconds, the identity the catalog gives the work, and the last
// played date as the recorded time.
func TestOneJellyfinItemBecomesAnOutsidePlay(t *testing.T) {
	played := time.Date(2026, 9, 7, 10, 54, 56, 0, time.UTC).Unix()
	for _, test := range []struct {
		name string
		item jellyfinItem
		play string
		want outsidePlay
	}{
		{
			name: "a film with a resume point",
			item: jellyfinItem{ID: "item-matrix", Type: "Movie",
				ProviderIds:  map[string]string{"Tmdb": "603", "Imdb": "tt0133093"},
				RunTimeTicks: 81600000000,
				UserData: jellyfinUserData{PlaybackPositionTicks: 42100000000,
					LastPlayedDate: "2026-09-07T10:54:56.1234567Z"}},
			play: "jellyfin-user-chris-item-matrix",
			want: outsidePlay{Player: "jellyfin", People: []string{"Chris"},
				Aliases:  map[string]string{"tmdb": "603", "imdb": "tt0133093"},
				Position: 4210, Duration: 8160, At: played},
		},
		{
			name: "an episode the person finished, under the series' ids",
			item: jellyfinItem{ID: "item-office-3-5", Type: "Episode", SeriesID: "item-office",
				ParentIndexNumber: 3, IndexNumber: 5,
				ProviderIds:  map[string]string{"Tmdb": "999"},
				RunTimeTicks: 13200000000,
				UserData: jellyfinUserData{PlaybackPositionTicks: 13200000000, Played: true,
					LastPlayedDate: "2026-09-07T10:54:56Z"}},
			play: "jellyfin-user-chris-item-office-3-5",
			want: outsidePlay{Player: "jellyfin", People: []string{"Chris"},
				Aliases: map[string]string{"tmdb": "2316", "tvdb": "73244"},
				Season:  3, Episode: 5, Position: 1320, Duration: 1320, Ended: true, At: played},
		},
		{
			name: "an item a person marked played by hand, with no date",
			item: jellyfinItem{ID: "item-arrival", Type: "Movie",
				ProviderIds: map[string]string{"Tmdb": "329865"},
				UserData:    jellyfinUserData{Played: true}},
			play: "jellyfin-user-chris-item-arrival",
			want: outsidePlay{Player: "jellyfin", People: []string{"Chris"},
				Aliases: map[string]string{"tmdb": "329865"}, Ended: true, At: 1},
		},
		{
			name: "an item whose date is of another shape",
			item: jellyfinItem{ID: "item-arrival", Type: "Movie",
				ProviderIds: map[string]string{"Tmdb": "329865"},
				UserData:    jellyfinUserData{LastPlayedDate: "yesterday"}},
			play: "jellyfin-user-chris-item-arrival",
			want: outsidePlay{Player: "jellyfin", People: []string{"Chris"},
				Aliases: map[string]string{"tmdb": "329865"}, At: 1},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backfill, messages, _ := standBackfill(t, &fakeBackfillJellyfin{
				users:     []jellyfinUser{{Name: "Chris", ID: "user-chris"}},
				resumable: map[string][]jellyfinItem{"user-chris": {test.item}},
				series:    backfillSeries(),
			})

			counts, err := backfill.run(t.Context())

			if err != nil {
				t.Fatalf("running the backfill: %v", err)
			}
			if counts != (jellyfinBackfillCounts{users: 1, published: 1}) {
				t.Errorf("counts = %+v, want one user and one play", counts)
			}
			topic := playOutsideTopic(defaultTopicBase, "house", test.play)
			got, held := messages.outsidePlays(t)[topic]
			if !held {
				t.Fatalf("nothing was published on %q", topic)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("play = %+v, want %+v", got, test.want)
			}
		})
	}
}

// the two reads name the user and the fields the mapping needs, and the
// message is not retained, because a rerun repeats the position.
func TestTheBackfillReadsBothFiltersAndRetainsNothing(t *testing.T) {
	fake := &fakeBackfillJellyfin{
		users: []jellyfinUser{{Name: "Chris", ID: "user-chris"}},
		resumable: map[string][]jellyfinItem{"user-chris": {{ID: "item-matrix", Type: "Movie",
			ProviderIds: map[string]string{"Tmdb": "603"}}}},
	}
	backfill, messages, _ := standBackfill(t, fake)

	if _, err := backfill.run(t.Context()); err != nil {
		t.Fatalf("running the backfill: %v", err)
	}

	want := []string{
		"/Users",
		"/Items?userId=user-chris&recursive=true&includeItemTypes=Movie,Episode&fields=ProviderIds&filters=IsResumable",
		"/Items?userId=user-chris&recursive=true&includeItemTypes=Movie,Episode&fields=ProviderIds&isPlayed=true",
	}
	if !reflect.DeepEqual(fake.queries, want) {
		t.Errorf("queries = %q, want %q", fake.queries, want)
	}
	if messages.held[0].retained {
		t.Error("the outside play was retained")
	}
}

// an item that comes back in both reads is published once, and the played
// read is the one that stands, because it says the person finished.
func TestAnItemInBothReadsIsPublishedOnce(t *testing.T) {
	backfill, messages, _ := standBackfill(t, &fakeBackfillJellyfin{
		users: []jellyfinUser{{Name: "Chris", ID: "user-chris"}},
		resumable: map[string][]jellyfinItem{"user-chris": {{ID: "item-matrix", Type: "Movie",
			ProviderIds: map[string]string{"Tmdb": "603"},
			UserData:    jellyfinUserData{PlaybackPositionTicks: 42100000000}}}},
		played: map[string][]jellyfinItem{"user-chris": {{ID: "item-matrix", Type: "Movie",
			ProviderIds: map[string]string{"Tmdb": "603"},
			UserData:    jellyfinUserData{PlaybackPositionTicks: 81600000000, Played: true}}}},
	})

	counts, err := backfill.run(t.Context())

	if err != nil {
		t.Fatalf("running the backfill: %v", err)
	}
	if counts.published != 1 {
		t.Fatalf("published = %d, want one", counts.published)
	}
	play := messages.outsidePlays(t)[playOutsideTopic(defaultTopicBase, "house",
		"jellyfin-user-chris-item-matrix")]
	if play.Position != 8160 || !play.Ended {
		t.Errorf("play = %+v, want the played read's position and its ended mark", play)
	}
}

// an item that names no work is skipped and counted, because the store has no
// identity to write it under.
func TestAnItemWithNoProviderIdsIsSkipped(t *testing.T) {
	for _, test := range []struct {
		name string
		item jellyfinItem
	}{
		{name: "a film with no provider ids", item: jellyfinItem{ID: "item-home", Type: "Movie"}},
		{name: "an episode that names no series",
			item: jellyfinItem{ID: "item-home", Type: "Episode", ProviderIds: map[string]string{"Tmdb": "999"}}},
		{name: "an episode of a series the server does not hold",
			item: jellyfinItem{ID: "item-home", Type: "Episode", SeriesID: "item-none"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			backfill, messages, logged := standBackfill(t, &fakeBackfillJellyfin{
				users:     []jellyfinUser{{Name: "Chris", ID: "user-chris"}},
				resumable: map[string][]jellyfinItem{"user-chris": {test.item}},
				series:    backfillSeries(),
			})

			counts, err := backfill.run(t.Context())

			if err != nil {
				t.Fatalf("running the backfill: %v", err)
			}
			if counts.skipped != 1 || counts.published != 0 {
				t.Errorf("counts = %+v, want one item skipped and nothing published", counts)
			}
			if len(messages.held) != 0 {
				t.Errorf("messages = %d, want none", len(messages.held))
			}
			if !strings.Contains(logged.String(), "names no provider ids") {
				t.Errorf("log = %q, want the item it skipped", logged.String())
			}
		})
	}
}
