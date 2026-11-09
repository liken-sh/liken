package main

// What these tests read: the creators of a series, from TMDb's created_by and
// TVmaze's crew to the creator elements of tvshow.nfo and back into the
// series body.

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

func TestTheTMDbOverviewOfASeriesNamesItsCreators(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, _ := newFakeTMDb(t, map[string]string{
			tmdbKey("/3/tv/99", "", ""): `{"overview":"A town of nine.",
				"created_by":[{"id":31,"name":"Dale Morrow"},{"id":32,"name":" "},{"id":33,"name":"Ines Calloway"}]}`,
		})

		answer, held, err := tmdbAnswerer{client: client}.answer(t.Context(), factOverview,
			titleRef{kind: libraryKindSeries, ids: providerIDs{"tmdb": "99"}})

		if err != nil || !held {
			t.Fatalf("answered %v with %v, want the overview", held, err)
		}
		if !slices.Equal(answer.Creators, []string{"Dale Morrow", "Ines Calloway"}) {
			t.Errorf("creators = %v, want the names TMDb states, in its order", answer.Creators)
		}
	})
}

// TVmaze's crew holds producers and developers beside the creators, and only
// a credit of the type Creator names one.
func TestTheTVmazeOverviewNamesTheCrewOfTheTypeCreator(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, fake := newFakeTVmaze(t, http.StatusOK, map[string]string{
			tvmazeLookupPath: tvmazeHarbour,
			tvmazeShowsPath + "4242/crew": `[
				{"type":"Creator","person":{"id":1,"name":"Dale Morrow"}},
				{"type":"Executive Producer","person":{"id":2,"name":"Pell Arden"}},
				{"type":"Developer","person":{"id":3,"name":"Ros Tamsin"}},
				{"type":"Creator","person":{"id":4,"name":"Ines Calloway"}}]`,
		})

		answer, held, err := newTVmazeAnswerer(client).answer(t.Context(), factOverview,
			titleRef{kind: libraryKindSeries, ids: providerIDs{"imdb": "tt4242424"}})

		if err != nil || !held {
			t.Fatalf("answered %v with %v, want the overview", held, err)
		}
		if !slices.Equal(answer.Creators, []string{"Dale Morrow", "Ines Calloway"}) {
			t.Errorf("creators = %v, want the two of the type Creator", answer.Creators)
		}
		if paths := requestPaths(fake); !slices.Contains(paths, tvmazeShowsPath+"4242/crew") {
			t.Errorf("the answerer asked for %v, want the crew", paths)
		}
	})
}

// A crew call that fails fails the overview, so the attempt records the
// provider's error and the fact asks again after the error window, where an
// overview with no creators would stand for the full window.
func TestATVmazeCrewCallThatFailsFailsTheOverview(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, _ := newFakeTVmaze(t, http.StatusOK, map[string]string{tvmazeLookupPath: tvmazeHarbour})

		_, held, err := newTVmazeAnswerer(client).answer(t.Context(), factOverview,
			titleRef{kind: libraryKindSeries, ids: providerIDs{"imdb": "tt4242424"}})

		if err == nil || held {
			t.Errorf("answered %v with %v, want the failure", held, err)
		}
	})
}

func requestPaths(fake *fakeTVmaze) []string {
	paths := []string{}
	for _, request := range fake.requests {
		paths = append(paths, request.URL.Path)
	}
	return paths
}

// The creators are a set, so two providers make their union in source order,
// and a name both state is written once.
func TestTheCreatorsOfTwoProvidersAreTheirUnion(t *testing.T) {
	tmdb := providerAnswer{block: providerBlockTMDb, answer: factAnswer{Creators: []string{"Dale Morrow"}}}
	tvmaze := providerAnswer{block: providerBlockTVmaze, answer: factAnswer{
		Creators: []string{"Dale Morrow", "Ines Calloway"},
	}}

	merged, names := mergeAnswers(factOverview, []providerAnswer{tmdb, tvmaze})

	if !slices.Equal(merged.Creators, []string{"Dale Morrow", "Ines Calloway"}) {
		t.Errorf("creators = %v, want the union in source order", merged.Creators)
	}
	if !slices.Equal([]string(names), []string{providerBlockTMDb, providerBlockTVmaze}) {
		t.Errorf("providers = %v, want both", names)
	}
}

// An answer that holds only the creators is still an overview to write.
func TestCreatorsAloneAnswerTheOverview(t *testing.T) {
	if !answersFact(factOverview, factAnswer{Creators: []string{"Dale Morrow"}}) {
		t.Error("an answer with creators alone answered nothing")
	}
}

// The overview writes one creator element per name into tvshow.nfo, and the
// scanner reads them back as the creators of the series body.
func TestTheOverviewWritesTheCreatorsTheScannerReads(t *testing.T) {
	document := []byte("<tvshow>\n  <title>Winter Harbour</title>\n  <creator>An Old Name</creator>\n</tvshow>\n")
	answer := factAnswer{Plot: "A town of nine.", Creators: []string{"Dale Morrow", "Ines & Co"}}

	edited, err := editElementGroup(document, nfoGroup(factOverview), nfoElements(factOverview, answer))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(edited), "<creator>Ines &amp; Co</creator>") {
		t.Errorf("the .nfo file reads\n%s\nwant the name escaped", edited)
	}
	meta, err := parseSeriesNFO(edited)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(meta.Body.Creators, []string{"Dale Morrow", "Ines & Co"}) {
		t.Errorf("creators = %v, want the two the overview wrote and not the old one", meta.Body.Creators)
	}
}
