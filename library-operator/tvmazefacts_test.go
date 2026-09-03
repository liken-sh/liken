package main

// What these tests read: what the TVmaze answerer makes of the show TVmaze
// answers for a series, the summary it strips to paragraphs, and the titles it
// holds nothing for.

import (
	"net/http"
	"slices"
	"testing"
)

// One show as TVmaze answers it, with the summary in HTML and the network that
// broadcast it.
const tvmazeHarbour = `{"id":4242,"name":"Winter Harbour","premiered":"2009-09-09",
	"genres":["Drama","Mystery"],"runtime":47,"averageRuntime":45,
	"summary":"<p>A keeper watches the ice.</p><p>A town of nine &amp; one road.</p>",
	"rating":{"average":8.6},"network":{"id":2,"name":"Harbour Broadcasting"},
	"externals":{"imdb":"tt4242424","thetvdb":70533}}`

// The cast of that show, in the order TVmaze holds it.
const tvmazeHarbourCast = `[{"person":{"id":8,"name":"Nora Vance",
	"image":{"medium":"https://static.example.test/m.jpg","original":"https://static.example.test/o.jpg"}},
	"character":{"id":11,"name":"Captain"}},
	{"person":{"id":9,"name":"Ada Ferris","image":{}},"character":{"id":12,"name":"Keeper"}}]`

func TestTheTVmazeAnswererReadsWhatTheProviderStates(t *testing.T) {
	client, _ := newFakeTVmaze(t, http.StatusOK, map[string]string{
		tvmazeLookupPath:              tvmazeHarbour,
		tvmazeShowsPath + "4242/cast": tvmazeHarbourCast,
	})
	answerer := newTVmazeAnswerer(client)

	cases := []struct {
		fact  string
		check func(*testing.T, factAnswer)
	}{
		{
			fact: factOverview,
			check: func(t *testing.T, answer factAnswer) {
				if answer.Plot != "A keeper watches the ice.\n\nA town of nine & one road." {
					t.Errorf("plot = %q, want the summary as paragraphs", answer.Plot)
				}
				if !slices.Equal(answer.Genres, []string{"Drama", "Mystery"}) ||
					!slices.Equal(answer.Studios, []string{"Harbour Broadcasting"}) {
					t.Errorf("answer = %+v, want the genres and the network as the studio", answer)
				}
				if answer.Premiered != "2009-09-09" || answer.RuntimeMinutes != 47 {
					t.Errorf("answer = %+v, want the premiere and the runtime", answer)
				}
			},
		},
		{
			fact: factCredits,
			check: func(t *testing.T, answer factAnswer) {
				if len(answer.Cast) != 2 || answer.Cast[0].Name != "Nora Vance" || answer.Cast[1].Order != 1 {
					t.Fatalf("cast = %+v, want the cast in the order TVmaze holds it", answer.Cast)
				}
				if answer.Cast[0].Role != "Captain" || answer.Cast[0].Thumb != "https://static.example.test/o.jpg" {
					t.Errorf("cast = %+v, want the character and the picture", answer.Cast[0])
				}
				if answer.Cast[1].Thumb != "" {
					t.Errorf("cast = %+v, want no picture for a person TVmaze holds none of", answer.Cast[1])
				}
				// TVmaze states no crew, so the directors and the writers of a
				// series come from the other providers alone.
				if len(answer.Directors) > 0 || len(answer.Writers) > 0 {
					t.Errorf("crew = %+v and %+v, want none", answer.Directors, answer.Writers)
				}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			answer, held, err := answerer.answer(t.Context(), test.fact,
				titleRef{kind: libraryKindSeries, ids: providerIDs{"imdb": "tt4242424"}})

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

// The facts of one title cost one lookup. the first fact looks the show up and
// the others read the show the container holds, so the credits fact asks only
// for the cast.
func TestTheFactsOfOneTitleCostOneTVmazeLookup(t *testing.T) {
	client, fake := newFakeTVmaze(t, http.StatusOK, map[string]string{
		tvmazeLookupPath:              tvmazeHarbour,
		tvmazeShowsPath + "4242/cast": tvmazeHarbourCast,
	})
	answerer := newTVmazeAnswerer(client)

	for _, fact := range nfoFacts {
		if _, _, err := answerer.answer(t.Context(), fact,
			titleRef{kind: libraryKindSeries, ids: providerIDs{"imdb": "tt4242424"}}); err != nil {
			t.Fatalf("the %s fact answered %v", fact, err)
		}
	}

	paths := []string{}
	for _, request := range fake.requests {
		paths = append(paths, request.URL.Path)
	}
	if !slices.Equal(paths, []string{tvmazeLookupPath, tvmazeShowsPath + "4242/cast"}) {
		t.Errorf("the answerer asked for %v, want the one lookup and the cast", paths)
	}
}

// TVmaze answers on the IMDb id first, and on the TheTVDB id where the title
// has no IMDb id of its own.
func TestTheTVmazeAnswererAsksWithTheIDItHas(t *testing.T) {
	cases := []struct {
		name   string
		ids    providerIDs
		scheme string
		id     string
	}{
		{
			name: "a title with both ids", scheme: tvmazeSchemeIMDb, id: "tt4242424",
			ids: providerIDs{"imdb": "tt4242424", "tvdb": "70533"},
		},
		{
			name: "a title with the TheTVDB id alone", scheme: tvmazeSchemeTheTVDB, id: "70533",
			ids: providerIDs{"tvdb": "70533"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, fake := newFakeTVmaze(t, http.StatusOK,
				map[string]string{tvmazeLookupPath: tvmazeHarbour})

			_, held, err := newTVmazeAnswerer(client).answer(t.Context(), factOverview,
				titleRef{kind: libraryKindSeries, ids: test.ids})

			if err != nil || !held {
				t.Fatalf("answered %v with %v, want the overview", held, err)
			}
			if len(fake.requests) != 1 {
				t.Fatalf("the answerer made %d requests, want one", len(fake.requests))
			}
			if got := fake.requests[0].URL.Query().Get(test.scheme); got != test.id {
				t.Errorf("the lookup asked %v, want %s of %s", fake.requests[0].URL.Query(), test.scheme, test.id)
			}
		})
	}
}

// A movie library's title, a title with no id TVmaze takes, and a fact TVmaze
// does not serve are all no answer and no request.
func TestTheTVmazeAnswererHoldsNothingForATitleItCannotAsk(t *testing.T) {
	cases := []struct {
		name string
		kind string
		fact string
		ids  providerIDs
	}{
		{name: "a movie library", kind: libraryKindMovies, fact: factOverview,
			ids: providerIDs{"imdb": "tt4242424"}},
		{name: "a title with no id TVmaze takes", kind: libraryKindSeries, fact: factOverview,
			ids: providerIDs{"tmdb": "4242"}},
		{name: "a fact TVmaze does not serve", kind: libraryKindSeries, fact: factCertification,
			ids: providerIDs{"imdb": "tt4242424"}},
		{name: "a rating of a site TVmaze has no score for", kind: libraryKindSeries,
			fact: factRatingIMDb, ids: providerIDs{"imdb": "tt4242424"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, fake := newFakeTVmaze(t, http.StatusOK,
				map[string]string{tvmazeLookupPath: tvmazeHarbour})

			_, held, err := newTVmazeAnswerer(client).answer(t.Context(), test.fact,
				titleRef{kind: test.kind, ids: test.ids})

			if err != nil || held {
				t.Fatalf("answered %v with %v, want nothing and no error", held, err)
			}
			if len(fake.requests) != 0 {
				t.Errorf("the answerer made %d requests, want none", len(fake.requests))
			}
		})
	}
}

// An id TVmaze does not hold is a miss and not an error, and a show with none
// of the overview in it is a miss too.
func TestATVmazeShowThatAnswersNothing(t *testing.T) {
	cases := []struct {
		name   string
		status int
		show   string
	}{
		{name: "an id TVmaze does not hold", status: http.StatusNotFound},
		{name: "a show with none of the overview", status: http.StatusOK, show: `{"id":4242}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, _ := newFakeTVmaze(t, test.status, map[string]string{tvmazeLookupPath: test.show})

			_, held, err := newTVmazeAnswerer(client).answer(t.Context(), factOverview,
				titleRef{kind: libraryKindSeries, ids: providerIDs{"imdb": "tt4242424"}})

			if err != nil || held {
				t.Fatalf("answered %v with %v, want nothing and no error", held, err)
			}
		})
	}
}

// A show on a streaming service names a web channel where a broadcaster would
// be, and the studio is the one it names.
func TestAShowOnAStreamingServiceNamesItsWebChannel(t *testing.T) {
	client, _ := newFakeTVmaze(t, http.StatusOK, map[string]string{
		tvmazeLookupPath: `{"id":4242,"summary":"<p>A keeper watches the ice.</p>",
			"network":null,"webChannel":{"id":9,"name":"Harbour Stream"}}`,
	})

	answer, held, err := newTVmazeAnswerer(client).answer(t.Context(), factOverview,
		titleRef{kind: libraryKindSeries, ids: providerIDs{"imdb": "tt4242424"}})

	if err != nil || !held {
		t.Fatalf("answered %v with %v, want the overview", held, err)
	}
	if !slices.Equal(answer.Studios, []string{"Harbour Stream"}) {
		t.Errorf("studios = %v, want the web channel", answer.Studios)
	}
}

// The runtime of an episode falls back to the length the episodes average,
// which is what TVmaze holds for a show with no fixed slot.
func TestAShowWithNoSlotTakesTheAverageRuntime(t *testing.T) {
	client, _ := newFakeTVmaze(t, http.StatusOK, map[string]string{
		tvmazeLookupPath: `{"id":4242,"runtime":0,"averageRuntime":45}`,
	})

	answer, held, err := newTVmazeAnswerer(client).answer(t.Context(), factOverview,
		titleRef{kind: libraryKindSeries, ids: providerIDs{"imdb": "tt4242424"}})

	if err != nil || !held {
		t.Fatalf("answered %v with %v, want the overview", held, err)
	}
	if answer.RuntimeMinutes != 45 {
		t.Errorf("runtime = %d, want the average", answer.RuntimeMinutes)
	}
}

// The summary reads as the paragraphs a person wrote, with the markup gone and
// the entities as their characters.
func TestTheSummaryReadsAsParagraphs(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		want    string
	}{
		{name: "two paragraphs", summary: "<p>One.</p><p>Two.</p>", want: "One.\n\nTwo."},
		{name: "a line break", summary: "<p>One.<br>Two.</p>", want: "One.\n\nTwo."},
		{name: "an entity", summary: "<p>One &amp; two.</p>", want: "One & two."},
		{name: "a tag inside a paragraph", summary: "<p>One <b>and</b> two.</p>", want: "One and two."},
		{name: "no summary at all", summary: ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := tvmazePlot(test.summary); got != test.want {
				t.Errorf("plot = %q, want %q", got, test.want)
			}
		})
	}
}

// A call that fails is an error the attempt records, and the credits call
// fails on its own.
func TestATVmazeAnswerThatFails(t *testing.T) {
	cases := []struct {
		name    string
		answers map[string]string
		status  int
	}{
		{name: "a lookup that fails", status: http.StatusInternalServerError},
		{
			name: "a cast call that fails", status: http.StatusOK,
			answers: map[string]string{tvmazeLookupPath: tvmazeHarbour},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, _ := newFakeTVmaze(t, test.status, test.answers)

			_, held, err := newTVmazeAnswerer(client).answer(t.Context(), factCredits,
				titleRef{kind: libraryKindSeries, ids: providerIDs{"imdb": "tt4242424"}})

			if err == nil || held {
				t.Errorf("answered %v with %v, want the failure", held, err)
			}
		})
	}
}
