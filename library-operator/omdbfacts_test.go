package main

// What these tests read: what the OMDb answerer makes of the one answer OMDb
// gives, the fields it holds no value for, and the day's limit that ends the
// provider's work in the middle of a run.

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// One title as OMDb answers it, with the plot, the certification, and the
// score of each of the three sites.
const omdbHarbour = `{"Title":"Winter Harbour","Year":"2011","Rated":"PG-13",
	"Released":"06 May 2011","Runtime":"101 min","Genre":"Drama, Thriller",
	"Plot":"A keeper watches the ice.","Actors":"Nora Vance, Ada Ferris",
	"Ratings":[{"Source":"Internet Movie Database","Value":"7.9/10"},
	{"Source":"Rotten Tomatoes","Value":"91%"},{"Source":"Metacritic","Value":"76/100"}],
	"Metascore":"76","imdbRating":"7.9","imdbVotes":"12,345","imdbID":"tt4242424",
	"Type":"movie","Response":"True"}`

// The ids of a title the identity fact has named at both databases.
func harbourIDs() providerIDs {
	return providerIDs{"tmdb": "4242", "imdb": "tt4242424"}
}

func TestTheOMDbAnswererReadsWhatTheProviderStates(t *testing.T) {
	client, _ := newFakeOMDb(t, func(url.Values) (int, string) {
		return http.StatusOK, omdbHarbour
	})
	answerer := newOMDbAnswerer(client)

	cases := []struct {
		fact  string
		check func(*testing.T, factAnswer)
	}{
		{
			fact: factOverview,
			check: func(t *testing.T, answer factAnswer) {
				if answer.Plot != "A keeper watches the ice." || answer.Tagline != "" {
					t.Errorf("answer = %+v, want the plot and no tagline", answer)
				}
				if !slices.Equal(answer.Genres, []string{"Drama", "Thriller"}) || len(answer.Studios) != 0 {
					t.Errorf("answer = %+v, want the genres and no studio", answer)
				}
				if answer.Premiered != "2011-05-06" || answer.RuntimeMinutes != 101 {
					t.Errorf("answer = %+v, want the release date and the runtime", answer)
				}
			},
		},
		{
			fact: factCertification,
			check: func(t *testing.T, answer factAnswer) {
				if answer.Certification != "PG-13" {
					t.Errorf("certification = %q, want the value OMDb rated it", answer.Certification)
				}
			},
		},
		{
			fact: factRatingIMDb,
			check: func(t *testing.T, answer factAnswer) {
				if answer.Rating.Value != 7.9 || answer.Rating.Votes != 12345 {
					t.Errorf("rating = %+v, want the score and the votes", answer.Rating)
				}
			},
		},
		{
			fact: factRatingRottenTomatoes,
			check: func(t *testing.T, answer factAnswer) {
				if answer.Rating.Value != 91 || answer.Rating.Votes != 0 {
					t.Errorf("rating = %+v, want the tomatometer and no votes", answer.Rating)
				}
			},
		},
		{
			fact: factRatingMetacritic,
			check: func(t *testing.T, answer factAnswer) {
				if answer.Rating.Value != 76 {
					t.Errorf("rating = %+v, want the metascore", answer.Rating)
				}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			answer, held, err := answerer.answer(t.Context(), test.fact,
				titleRef{kind: libraryKindMovies, ids: harbourIDs()})

			if err != nil {
				t.Fatal(err)
			}
			if !held {
				t.Fatalf("the provider answered nothing for %s", test.fact)
			}
			test.check(t, answer)
		})
	}
}

// The scores also read from the Ratings list alone, which is where OMDb states
// the Metacritic score beside the Metascore.
func TestTheOMDbScoresReadFromTheRatingsList(t *testing.T) {
	client, _ := newFakeOMDb(t, func(url.Values) (int, string) {
		return http.StatusOK, `{"Response":"True","imdbRating":"N/A","Metascore":"N/A",
			"Ratings":[{"Source":"Internet Movie Database","Value":"7.9/10"},
			{"Source":"Metacritic","Value":"76/100"}]}`
	})
	answerer := newOMDbAnswerer(client)

	for fact, want := range map[string]float64{factRatingIMDb: 7.9, factRatingMetacritic: 76} {
		t.Run(fact, func(t *testing.T) {
			answer, held, err := answerer.answer(t.Context(), fact,
				titleRef{kind: libraryKindMovies, ids: harbourIDs()})

			if err != nil || !held {
				t.Fatalf("answered %v with %v, want the score", held, err)
			}
			if answer.Rating.Value != want {
				t.Errorf("rating = %+v, want %v", answer.Rating, want)
			}
		})
	}
}

// A value OMDb holds none of is N/A, which is no answer at all, so the fact
// records a miss and writes no element.
func TestAnOMDbValueOfNotAvailableIsNoAnswer(t *testing.T) {
	client, _ := newFakeOMDb(t, func(url.Values) (int, string) {
		return http.StatusOK, `{"Response":"True","Rated":"N/A","Released":"N/A","Runtime":"N/A",
			"Genre":"N/A","Plot":"N/A","Metascore":"N/A","imdbRating":"N/A","imdbVotes":"N/A","Ratings":[]}`
	})
	answerer := newOMDbAnswerer(client)

	for _, fact := range providerFacts[providerBlockOMDb] {
		t.Run(fact, func(t *testing.T) {
			answer, held, err := answerer.answer(t.Context(), fact,
				titleRef{kind: libraryKindMovies, ids: harbourIDs()})

			if err != nil || held {
				t.Fatalf("answered %+v with %v, want nothing and no error", answer, err)
			}
		})
	}
}

// A title OMDb does not hold, and a title the identity fact has no IMDb id
// for, are both no answer and not an error.
func TestTheOMDbAnswererHoldsNothingForATitleItCannotAsk(t *testing.T) {
	cases := []struct {
		name     string
		ids      providerIDs
		fact     string
		answer   string
		requests int
	}{
		{
			name: "a title with no IMDb id", ids: providerIDs{"tmdb": "4242"},
			fact: factOverview, answer: omdbHarbour,
		},
		{
			name: "a title OMDb does not hold", ids: harbourIDs(), fact: factOverview,
			answer: `{"Response":"False","Error":"Incorrect IMDb ID."}`, requests: 1,
		},
		{
			name: "a fact OMDb does not serve", ids: harbourIDs(), fact: factRatingTMDb,
			answer: omdbHarbour,
		},
		{
			name: "a fact the answerer serves for no provider", ids: harbourIDs(), fact: factCredits,
			answer: omdbHarbour,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, fake := newFakeOMDb(t, func(url.Values) (int, string) {
				return http.StatusOK, test.answer
			})

			_, held, err := newOMDbAnswerer(client).answer(t.Context(), test.fact,
				titleRef{kind: libraryKindMovies, ids: test.ids})

			if err != nil || held {
				t.Fatalf("answered %v with %v, want nothing and no error", held, err)
			}
			if len(fake.requests) != test.requests {
				t.Errorf("the answerer made %d requests, want %d", len(fake.requests), test.requests)
			}
		})
	}
}

// The facts of one title cost one call. the first fact makes the call and the
// other three read the answer the container holds, and that a title OMDb does
// not hold is held the same way.
func TestTheFactsOfOneTitleCostOneOMDbCall(t *testing.T) {
	cases := []struct {
		name   string
		answer string
	}{
		{name: "a title OMDb holds", answer: omdbHarbour},
		{name: "a title OMDb does not hold", answer: `{"Response":"False","Error":"Incorrect IMDb ID."}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, fake := newFakeOMDb(t, func(url.Values) (int, string) {
				return http.StatusOK, test.answer
			})
			answerer := newOMDbAnswerer(client)

			for _, fact := range providerFacts[providerBlockOMDb] {
				if _, _, err := answerer.answer(t.Context(), fact,
					titleRef{kind: libraryKindMovies, ids: harbourIDs()}); err != nil {
					t.Fatalf("the %s fact answered %v", fact, err)
				}
			}

			if len(fake.requests) != 1 {
				t.Errorf("the answerer made %d requests, want the one the title costs", len(fake.requests))
			}
		})
	}
}

// A provider that refuses one call fails that title alone, and the container
// records an error and asks again on the next run.
func TestTheOMDbAnswererCarriesTheProvidersRefusal(t *testing.T) {
	client, _ := newFakeOMDb(t, func(url.Values) (int, string) {
		return http.StatusInternalServerError, ""
	})

	_, held, err := newOMDbAnswerer(client).answer(t.Context(), factOverview,
		titleRef{kind: libraryKindMovies, ids: harbourIDs()})

	if err == nil || held {
		t.Errorf("answered %v with %v, want the refusal", held, err)
	}
}

// A key with no calls left answers the daily limit, which the answer line
// reads as the end of that provider's work for the run.
func TestAKeyWithNoCallsLeftAnswersTheDailyLimit(t *testing.T) {
	calls := 0
	client, _ := newFakeOMDb(t, func(url.Values) (int, string) {
		calls++
		if calls == 1 {
			return http.StatusOK, omdbHarbour
		}
		return http.StatusUnauthorized, `{"Response":"False","Error":"Request limit reached!"}`
	})
	answerer := newOMDbAnswerer(client)
	reached := titleRef{kind: libraryKindMovies, ids: harbourIDs()}
	next := titleRef{kind: libraryKindMovies, ids: providerIDs{"imdb": "tt4242425"}}
	after := titleRef{kind: libraryKindMovies, ids: providerIDs{"imdb": "tt4242426"}}

	if _, held, err := answerer.answer(t.Context(), factOverview, reached); err != nil || !held {
		t.Fatalf("the first title answered %v with %v, want the overview", held, err)
	}
	_, _, spent := answerer.answer(t.Context(), factOverview, next)
	_, _, left := answerer.answer(t.Context(), factOverview, after)
	_, cached, held := answerer.answer(t.Context(), factCertification, reached)

	for at, err := range []error{spent, left} {
		if !errors.Is(err, errDailyLimit) {
			t.Errorf("the title after the limit %d answered %v, want the daily limit", at, err)
		}
	}
	if held != nil || !cached {
		t.Errorf("the title the provider answered reads %v with %v, want the answer held", cached, held)
	}
	if calls != 2 {
		t.Errorf("the answerer made %d calls, want the one that worked and the one that spent the day", calls)
	}
}

// One title with an IMDb id in its sidecar, which is what OMDb keys on.
func seedOMDbGap(t *testing.T, catalog *Catalog, root, folder, id string) {
	t.Helper()
	writeFile(t, filepath.Join(root, folder, movieSidecarName), `<?xml version="1.0" encoding="utf-8"?>
<movie>
  <title>Winter Harbour</title>
  <uniqueid type="imdb">`+id+`</uniqueid>
</movie>
`)
	seed := &walkResult{movies: []movieRow{{
		Id: "movie:imdb:" + id, Library: "house/movies", Kind: libraryKindMovies,
		Path: folder, Title: "Winter Harbour", Released: "2011",
	}}}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

// The day's limit ends OMDb's work in the middle of the run: the titles it
// reached hold their answers, the titles it did not keep their gaps, and the
// container logs the count it left.
func TestTheDailyLimitLeavesTheRestOfTheTitlesTheirGaps(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	for at := range 3 {
		seedOMDbGap(t, catalog, root, fmt.Sprintf("Winter Harbour %d (2011)", at),
			fmt.Sprintf("tt424242%d", at))
	}
	calls := 0
	client, _ := newFakeOMDb(t, func(url.Values) (int, string) {
		if calls++; calls == 1 {
			return http.StatusOK, omdbHarbour
		}
		return http.StatusUnauthorized, `{"Response":"False","Error":"Request limit reached!"}`
	})
	work, log := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.nfoGap(t.Context(), factCertification, lineOf(newOMDbAnswerer(client))); err != nil {
		t.Fatal(err)
	}

	if calls != 2 {
		t.Errorf("the container made %d calls, want the one that worked and the one that did not", calls)
	}
	if !strings.Contains(log.String(), "left the certification of 2 titles") {
		t.Errorf("log = %q, want the count it left", log.String())
	}
	answered := 0
	for at := range 3 {
		ledger, err := readLikenLedger(filepath.Join(root, fmt.Sprintf("Winter Harbour %d (2011)", at)),
			factCertification)
		if err != nil {
			t.Fatal(err)
		}
		answered += len(ledger.Attempts)
	}
	if answered != 1 {
		t.Errorf("%d titles recorded an attempt, want the one the provider reached", answered)
	}
}
