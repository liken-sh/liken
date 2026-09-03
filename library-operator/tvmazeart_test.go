package main

// What these tests read: the file each TVmaze image type lands under, the id
// the lookup reads on and the one it falls back to, the lookup a second fact
// makes no call for, and the movie TVmaze holds nothing about.

import (
	"net/http"
	"path/filepath"
	"testing"
)

// The images of one show, with the main poster beside another of the same
// type, and every address on the fake itself.
func tvmazeImageAnswer(base string) string {
	return `[{"id":1,"type":"poster","main":false,
		"resolutions":{"original":{"url":"` + base + `/image/other-poster.jpg"}}},
		{"id":2,"type":"poster","main":true,
		"resolutions":{"original":{"url":"` + base + `/image/poster.jpg"}}},
		{"id":3,"type":"background","main":true,
		"resolutions":{"original":{"url":"` + base + `/image/background.jpg"}}},
		{"id":4,"type":"banner","main":true,
		"resolutions":{"original":{"url":"` + base + `/image/banner.jpg"}}},
		{"id":5,"type":"typography","main":true,
		"resolutions":{"original":{"url":"` + base + `/image/typography.jpg"}}}]`
}

// The three types TVmaze holds land under the names Kodi reads, and the main
// image of a type is the one that lands.
func TestEachTVmazeArtTypeLandsUnderItsName(t *testing.T) {
	folder := "Quiet Harbor (2008)"
	cases := []struct {
		fact string
		file string
		want string
	}{
		{fact: factPoster, file: "poster.jpg", want: "/image/poster.jpg"},
		{fact: factBackdrop, file: "fanart.jpg", want: "/image/background.jpg"},
		{fact: factBanner, file: "banner.jpg", want: "/image/banner.jpg"},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			writeFile(t, filepath.Join(root, folder, "Season 01", "Quiet Harbor - S01E05.mkv"), "video")
			writeSeriesSidecar(t, root, folder, "81189")
			seedArtSeries(t, catalog, folder, []int{1})
			work, _ := testEnricher(t, libraryKindSeries, root, catalog)
			answers := map[string]string{tvmazeLookupPath: tvmazeShowAnswer}
			client, _ := newFakeTVmaze(t, http.StatusOK, answers)
			answers[tvmazeShowsPath+"1371/images"] = tvmazeImageAnswer(client.base)
			answers[test.want] = test.want

			line := artLineOf(newTVmazeArtAnswerer(client))
			if err := work.artGap(t.Context(), test.fact, line); err != nil {
				t.Fatal(err)
			}

			if got := readFileString(t, filepath.Join(root, folder, test.file)); got != test.want {
				t.Errorf("%s holds %q, want the image at %q", test.file, got, test.want)
			}
		})
	}
}

// The show one title is costs one lookup for the whole container, so a second
// art fact of the same title makes none, and the answer is the nfo answerer's
// own lookup.
func TestTheTVmazeArtAnswererHoldsOneLookupPerTitle(t *testing.T) {
	answers := map[string]string{tvmazeLookupPath: tvmazeShowAnswer}
	client, fake := newFakeTVmaze(t, http.StatusOK, answers)
	answers[tvmazeShowsPath+"1371/images"] = tvmazeImageAnswer(client.base)
	answerer := newTVmazeArtAnswerer(client)
	title := titleRef{kind: libraryKindSeries, ids: providerIDs{"imdb": "tt0098936"}}

	for _, fact := range []string{factPoster, factBanner} {
		candidates, err := answerer.candidates(t.Context(), fact, artGap{}, title)
		if err != nil {
			t.Fatal(err)
		}
		if len(candidates) == 0 {
			t.Fatalf("the answerer held no %s, want the image", fact)
		}
	}

	lookups := 0
	for _, request := range fake.requests {
		if request.URL.Path == tvmazeLookupPath {
			lookups++
		}
	}
	if lookups != 1 {
		t.Errorf("the answerer made %d lookups, want the one", lookups)
	}
}

// TVmaze holds series alone and three types of image, so a movie and a fact
// outside the three are no answer and no call.
func TestWhatTVmazeHoldsNoArtFor(t *testing.T) {
	cases := []struct {
		name  string
		fact  string
		title titleRef
	}{
		{name: "a movie", fact: factPoster,
			title: titleRef{kind: libraryKindMovies, ids: providerIDs{"tvdb": "81189"}}},
		{name: "a type TVmaze does not hold", fact: factClearart,
			title: titleRef{kind: libraryKindSeries, ids: providerIDs{"tvdb": "81189"}}},
		{name: "a series TVmaze does not hold", fact: factPoster,
			title: titleRef{kind: libraryKindSeries}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, fake := newFakeTVmaze(t, http.StatusOK,
				map[string]string{tvmazeLookupPath: tvmazeShowAnswer})
			answerer := newTVmazeArtAnswerer(client)

			candidates, err := answerer.candidates(t.Context(), test.fact, artGap{}, test.title)

			if err != nil || len(candidates) != 0 {
				t.Errorf("the answerer held %+v and %v, want no image", candidates, err)
			}
			if len(fake.requests) != 0 {
				t.Errorf("the answerer made %d calls, want none", len(fake.requests))
			}
		})
	}
}

// A lookup and an images call the provider refuses are errors the fact
// records, and the answerer serves the facts the provider table names.
func TestWhatTheTVmazeArtAnswererDoesWithARefusal(t *testing.T) {
	client, _ := newFakeTVmaze(t, http.StatusInternalServerError, map[string]string{})
	answerer := newTVmazeArtAnswerer(client)
	title := titleRef{kind: libraryKindSeries, ids: providerIDs{"tvdb": "81189"}}

	if _, err := answerer.candidates(t.Context(), factPoster, artGap{}, title); err == nil {
		t.Error("the answerer reported no error, want one")
	}
	if !answerer.serves(factPoster) || answerer.serves(factDiscart) {
		t.Error("the answerer serves the wrong facts")
	}

	held, _ := newFakeTVmaze(t, http.StatusOK, map[string]string{
		tvmazeLookupPath: tvmazeShowAnswer,
	})
	refuses := newTVmazeArtAnswerer(held)
	if _, err := refuses.candidates(t.Context(), factPoster, artGap{}, title); err == nil {
		t.Error("the answerer read no error from an images call it could not read")
	}
}

// An image TVmaze states no address for is no image.
func TestATVmazeImageWithNoAddressIsNoImage(t *testing.T) {
	images := []tvmazeArtwork{{ID: 1, Type: tvmazeArtworkPoster, Main: true}}

	if got := tvmazeCandidates(images); len(got) != 0 {
		t.Errorf("candidates = %+v, want none", got)
	}
}

// An id TVmaze answers no show for is no image and not an error.
func TestAnIDTVmazeAnswersNoShowFor(t *testing.T) {
	client, _ := newFakeTVmaze(t, http.StatusOK, map[string]string{tvmazeLookupPath: `{}`})
	answerer := newTVmazeArtAnswerer(client)
	title := titleRef{kind: libraryKindSeries, ids: providerIDs{"tvdb": "81189"}}

	candidates, err := answerer.candidates(t.Context(), factPoster, artGap{}, title)

	if err != nil || len(candidates) != 0 {
		t.Errorf("the answerer held %+v and %v, want no image", candidates, err)
	}
}
