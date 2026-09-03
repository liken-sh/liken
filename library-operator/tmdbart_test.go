package main

// What these tests read: the settings TMDb states once for the whole
// container, the facts it holds no list for, and the image it states no path
// for.

import (
	"testing"
)

// The settings are read once, so the second art fact of a container makes one
// call and not two.
func TestTheTMDbArtAnswererReadsItsSettingsOnce(t *testing.T) {
	client, fake := newArtTMDb(t, map[string]string{
		tmdbKey("/3/movie/603/images", "", ""): imagesAnswer(tmdbPosters, "/quiet.jpg", artLanguage),
	})
	answerer := newTMDbArtAnswerer(client)
	title := titleRef{kind: libraryKindMovies}
	gap := artGap{tmdb: "603"}

	for range 2 {
		candidates, err := answerer.candidates(t.Context(), factPoster, gap, title)
		if err != nil {
			t.Fatal(err)
		}
		if len(candidates) != 1 || candidates[0].URL != client.base+"/t/p/w780/quiet.jpg" {
			t.Fatalf("candidates = %+v, want the one image at its size", candidates)
		}
	}

	if fake.served[tmdbKey(tmdbConfigurationPath, "", "")] != 1 {
		t.Errorf("the answerer read the settings %d times, want one",
			fake.served[tmdbKey(tmdbConfigurationPath, "", "")])
	}
}

// A fact TMDb holds no list for, and an image it states no path for, are both
// no image and no call.
func TestWhatTheTMDbArtAnswererHoldsNoImageFor(t *testing.T) {
	client, fake := newArtTMDb(t, map[string]string{
		tmdbKey("/3/movie/603/images", "", ""): `{"posters":[{"file_path":"","vote_average":9}]}`,
	})
	answerer := newTMDbArtAnswerer(client)
	title := titleRef{kind: libraryKindMovies}
	gap := artGap{tmdb: "603"}

	if candidates, err := answerer.candidates(t.Context(), factClearart, gap, title); err != nil ||
		len(candidates) != 0 {
		t.Errorf("the answerer held %+v and %v, want no image", candidates, err)
	}
	if len(fake.requestPath) != 0 {
		t.Errorf("the answerer made %v, want no call", fake.requestPath)
	}

	if candidates, err := answerer.candidates(t.Context(), factPoster, gap, title); err != nil ||
		len(candidates) != 0 {
		t.Errorf("the answerer held %+v and %v, want no image", candidates, err)
	}
}
