package main

// what these tests read: what the TMDb answerer makes of the answers TMDb
// gives, for a movie and for a series.

import (
	"net/http"
	"slices"
	"testing"
)

const (
	tmdbMovieDetails = `{"overview":"A keeper watches the ice.","tagline":"The ice remembers.",
		"genres":[{"name":"Drama"},{"name":"Thriller"}],
		"production_companies":[{"name":"Harbour Pictures"}],
		"release_date":"2011-05-06","runtime":101,"vote_average":8.4,"vote_count":1234}`
	tmdbSeriesDetails = `{"overview":"A town of nine.","tagline":"Nine ways home.",
		"genres":[{"name":"Mystery"}],"networks":[{"name":"Harbour Broadcasting"}],
		"first_air_date":"2009-09-09","episode_run_time":[47],"vote_average":7.7,"vote_count":88}`
)

func TestTheTMDbAnswererReadsWhatTheProviderStates(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/movie/4242", "", ""):               tmdbMovieDetails,
		tmdbKey("/3/movie/4242/release_dates", "", ""): `{"results":[{"iso_3166_1":"GB","release_dates":[{"certification":"15"}]},{"iso_3166_1":"US","release_dates":[{"certification":""},{"certification":"PG-13"}]}]}`,
		tmdbKey("/3/movie/4242/credits", "", ""):       `{"cast":[{"name":"Ada Ferris","character":"Keeper","order":1,"profile_path":"/b.jpg"},{"name":"Nora Vance","character":"Captain","order":0,"profile_path":"/a.jpg"}]}`,
		tmdbKey("/3/tv/99", "", ""):                    tmdbSeriesDetails,
		tmdbKey("/3/tv/99/content_ratings", "", ""):    `{"results":[{"iso_3166_1":"US","rating":"TV-14"}]}`,
		tmdbKey("/3/tv/99/aggregate_credits", "", ""):  `{"cast":[{"name":"Nora Vance","order":0,"roles":[{"character":"Captain"}]}]}`,
	})
	answerer := tmdbAnswerer{client: client}

	cases := []struct {
		name  string
		kind  string
		id    string
		fact  string
		check func(*testing.T, factAnswer)
	}{
		{
			name: "a movie's overview", kind: libraryKindMovies, id: "4242", fact: factOverview,
			check: func(t *testing.T, answer factAnswer) {
				if answer.Plot != "A keeper watches the ice." || answer.Tagline != "The ice remembers." {
					t.Errorf("answer = %+v, want the plot and the tagline", answer)
				}
				if !slices.Equal(answer.Genres, []string{"Drama", "Thriller"}) ||
					!slices.Equal(answer.Studios, []string{"Harbour Pictures"}) {
					t.Errorf("answer = %+v, want the genres and the studio", answer)
				}
				if answer.Premiered != "2011-05-06" || answer.RuntimeMinutes != 101 {
					t.Errorf("answer = %+v, want the release date and the runtime", answer)
				}
			},
		},
		{
			name: "a series' overview", kind: libraryKindSeries, id: "99", fact: factOverview,
			check: func(t *testing.T, answer factAnswer) {
				if answer.Premiered != "2009-09-09" || answer.RuntimeMinutes != 47 {
					t.Errorf("answer = %+v, want the first air date and the episode runtime", answer)
				}
				if !slices.Equal(answer.Studios, []string{"Harbour Broadcasting"}) {
					t.Errorf("answer = %+v, want the network as the studio", answer)
				}
			},
		},
		{
			name: "a movie's certification", kind: libraryKindMovies, id: "4242", fact: factCertification,
			check: func(t *testing.T, answer factAnswer) {
				if answer.Certification != "PG-13" {
					t.Errorf("certification = %q, want the country's own", answer.Certification)
				}
			},
		},
		{
			name: "a series' certification", kind: libraryKindSeries, id: "99", fact: factCertification,
			check: func(t *testing.T, answer factAnswer) {
				if answer.Certification != "TV-14" {
					t.Errorf("certification = %q, want the content rating", answer.Certification)
				}
			},
		},
		{
			name: "a movie's rating", kind: libraryKindMovies, id: "4242", fact: factRatingTMDb,
			check: func(t *testing.T, answer factAnswer) {
				if answer.Rating == nil || answer.Rating.Value != 8.4 || answer.Rating.Votes != 1234 {
					t.Errorf("rating = %+v, want the score and the votes", answer.Rating)
				}
			},
		},
		{
			name: "a movie's credits", kind: libraryKindMovies, id: "4242", fact: factCredits,
			check: func(t *testing.T, answer factAnswer) {
				if len(answer.Cast) != 2 || answer.Cast[0].Name != "Nora Vance" {
					t.Fatalf("cast = %+v, want the billed order", answer.Cast)
				}
				if answer.Cast[0].Thumb != tmdbImageBase+tmdbProfileSize+"/a.jpg" {
					t.Errorf("thumb = %q, want the provider's picture", answer.Cast[0].Thumb)
				}
			},
		},
		{
			name: "a series' credits", kind: libraryKindSeries, id: "99", fact: factCredits,
			check: func(t *testing.T, answer factAnswer) {
				if len(answer.Cast) != 1 || answer.Cast[0].Role != "Captain" {
					t.Errorf("cast = %+v, want the role out of the roles the seasons hold", answer.Cast)
				}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			answer, held, err := answerer.answer(t.Context(), test.fact,
				titleRef{kind: test.kind, ids: providerIDs{"tmdb": test.id}})

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

// A title with no TMDb id is no answer and not an error, because every call
// this answerer makes keys on that id.
func TestTheTMDbAnswererHoldsNothingForATitleWithNoTMDbID(t *testing.T) {
	client, fake := newFakeTMDb(t, nil)

	_, held, err := tmdbAnswerer{client: client}.answer(t.Context(), factOverview,
		titleRef{kind: libraryKindMovies, ids: providerIDs{"imdb": "tt0084787"}})

	if err != nil || held {
		t.Fatalf("answered %v with %v, want nothing and no error", held, err)
	}
	if len(fake.requestPath) != 0 {
		t.Errorf("the answerer made %v, want no request at all", fake.requestPath)
	}
}

// A title nobody has voted on has no rating to write.
func TestATitleWithNoVotesHasNoRating(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/movie/4242", "", ""): `{"overview":"A keeper watches the ice.","vote_average":0}`,
	})

	_, held, err := tmdbAnswerer{client: client}.answer(t.Context(), factRatingTMDb,
		titleRef{kind: libraryKindMovies, ids: providerIDs{"tmdb": "4242"}})

	if err != nil || held {
		t.Fatalf("answered %v with %v, want no rating and no error", held, err)
	}
}

// A provider that refuses one call fails that fact alone, so the container
// records an error and asks again on the next run.
func TestTheTMDbAnswererCarriesTheProvidersRefusal(t *testing.T) {
	client, fake := newFakeTMDb(t, nil)
	for _, path := range []string{
		"/3/movie/4242", "/3/movie/4242/release_dates", "/3/movie/4242/credits",
	} {
		fake.statuses[tmdbKey(path, "", "")] = http.StatusUnauthorized
	}
	answerer := tmdbAnswerer{client: client}

	for _, fact := range nfoFacts {
		if !answerer.serves(fact) {
			continue
		}
		t.Run(fact, func(t *testing.T) {
			_, held, err := answerer.answer(t.Context(), fact,
				titleRef{kind: libraryKindMovies, ids: providerIDs{"tmdb": "4242"}})

			if err == nil || held {
				t.Errorf("answered %v with %v, want the refusal", held, err)
			}
		})
	}
}

// A fact this answerer does not serve is no answer and no request.
func TestTheTMDbAnswererServesTheFactsTheTableHolds(t *testing.T) {
	client, _ := newFakeTMDb(t, nil)
	answerer := tmdbAnswerer{client: client}

	if answerer.serves("rating.imdb") {
		t.Error("the answerer serves a fact its provider has no row for")
	}
	if _, held, err := answerer.answer(t.Context(), "rating.imdb",
		titleRef{kind: libraryKindMovies, ids: providerIDs{"tmdb": "4242"}}); held || err != nil {
		t.Errorf("answered %v with %v, want nothing", held, err)
	}
}

// A person with no picture at the provider carries none into the sidecar.
func TestAPersonWithNoPictureCarriesNoThumb(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/movie/4242/credits", "", ""): `{"cast":[{"name":"Nora Vance","character":"Captain","profile_path":""}]}`,
	})

	answer, held, err := tmdbAnswerer{client: client}.answer(t.Context(), factCredits,
		titleRef{kind: libraryKindMovies, ids: providerIDs{"tmdb": "4242"}})

	if err != nil || !held {
		t.Fatalf("answered %v with %v, want the cast", held, err)
	}
	if answer.Cast[0].Thumb != "" {
		t.Errorf("thumb = %q, want none", answer.Cast[0].Thumb)
	}
}

// The other databases' ids answer as the map the sidecar and the ledger carry,
// with an id the provider left empty dropped.
func TestTheExternalIDsDropWhatTheProviderLeftEmpty(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   int
	}{
		{name: "both ids", answer: `{"imdb_id":"tt1","tvdb_id":9}`, want: 2},
		{name: "an empty imdb id", answer: `{"imdb_id":"","tvdb_id":0}`, want: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, _ := newFakeTMDb(t, map[string]string{
				tmdbKey("/3/movie/4242/external_ids", "", ""): test.answer,
			})

			ids, err := client.externalIDs(t.Context(), libraryKindMovies, 4242)
			if err != nil {
				t.Fatal(err)
			}
			if len(ids.providerIDs()) != test.want {
				t.Errorf("ids = %v, want %d", ids.providerIDs(), test.want)
			}
		})
	}
}

// A title the provider states no certification for in this country is no
// answer, and another country's is never read in its place.
func TestACertificationOfAnotherCountryIsNoAnswer(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/movie/4242/release_dates", "", ""): `{"results":[{"iso_3166_1":"GB","release_dates":[{"certification":"15"}]}]}`,
	})

	_, held, err := tmdbAnswerer{client: client}.answer(t.Context(), factCertification,
		titleRef{kind: libraryKindMovies, ids: providerIDs{"tmdb": "4242"}})

	if err != nil || held {
		t.Fatalf("answered %v with %v, want no certification", held, err)
	}
}

// The crew of the two kinds. A movie states one job per credit and a series
// states every job a person held over the seasons, and both answer the
// directors and the writers a player reads.
func TestTheTMDbAnswererReadsTheCrewAPlayerReads(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/movie/4242/credits", "", ""): `{"cast":[{"name":"Nora Vance","order":0}],"crew":[
			{"id":11,"name":"Iris Kell","job":"Director","department":"Directing"},
			{"id":12,"name":"Otto Rhee","job":"Assistant Director","department":"Directing"},
			{"id":11,"name":"Iris Kell","job":"Screenplay","department":"Writing"},
			{"id":13,"name":"Petra Lund","job":"Story","department":"Writing"},
			{"id":11,"name":"Iris Kell","job":"Story","department":"Writing"},
			{"name":"Rune Aas","job":"Novel","department":"Writing"},
			{"name":"Rune Aas","job":"Screenplay","department":"Writing"},
			{"id":15,"name":"","job":"Director","department":"Directing"},
			{"id":14,"name":"Bo Vance","job":"Director of Photography","department":"Camera"}]}`,
		tmdbKey("/3/tv/99/aggregate_credits", "", ""): `{"cast":[],"crew":[
			{"id":21,"name":"Mira Solberg","department":"Directing",
				"jobs":[{"job":"Producer"},{"job":"Director"}]},
			{"id":21,"name":"Mira Solberg","department":"Writing","jobs":[{"job":"Writer"}]},
			{"id":22,"name":"Halvard Ness","department":"Production",
				"jobs":[{"job":"Executive Producer"}]}]}`,
	})
	answerer := tmdbAnswerer{client: client}

	cases := []struct {
		name      string
		kind      string
		id        string
		directors []string
		writers   []string
	}{
		{
			name: "a movie", kind: libraryKindMovies, id: "4242",
			directors: []string{"Iris Kell"},
			writers:   []string{"Iris Kell", "Petra Lund", "Rune Aas"},
		},
		{
			name: "a series", kind: libraryKindSeries, id: "99",
			directors: []string{"Mira Solberg"},
			writers:   []string{"Mira Solberg"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			answer, held, err := answerer.answer(t.Context(), factCredits,
				titleRef{kind: test.kind, ids: providerIDs{"tmdb": test.id}})

			if err != nil || !held {
				t.Fatalf("answered %v with %v, want the credits", held, err)
			}
			if got := peopleNames(answer.Directors); !slices.Equal(got, test.directors) {
				t.Errorf("directors = %v, want %v", got, test.directors)
			}
			if got := peopleNames(answer.Writers); !slices.Equal(got, test.writers) {
				t.Errorf("writers = %v, want %v", got, test.writers)
			}
		})
	}
}

// The names of one crew list, which is what a player reads off the elements.
func peopleNames(people []creditedPerson) []string {
	names := make([]string, 0, len(people))
	for _, person := range people {
		names = append(names, person.Name)
	}
	return names
}

// A crew credit carries the person's id, which is what names the directory in
// .contributors/ and tells two people of one name apart.
func TestACrewCreditCarriesTheProvidersID(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/movie/4242/credits", "", ""): `{"cast":[],"crew":[
			{"id":11,"name":"Iris Kell","job":"Director","department":"Directing"}]}`,
	})

	answer, held, err := tmdbAnswerer{client: client}.answer(t.Context(), factCredits,
		titleRef{kind: libraryKindMovies, ids: providerIDs{"tmdb": "4242"}})

	if err != nil || !held {
		t.Fatalf("answered %v with %v, want the crew of a title with no cast", held, err)
	}
	if len(answer.Directors) != 1 || answer.Directors[0].IDs["tmdb"] != "11" {
		t.Errorf("directors = %+v, want the id TMDb gave for the person", answer.Directors)
	}
}
