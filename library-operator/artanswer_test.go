package main

// What these tests read: the image the choice takes out of a list, the
// answerers the source order builds, which provider writes where two of them
// hold an image, and what an ask does with a provider that refuses.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// The choice takes the highest-voted image of the library's own language,
// then the highest-voted image with no language, then any.
func TestTheChoiceFollowsTheLanguageOrder(t *testing.T) {
	cases := []struct {
		name       string
		candidates []artCandidate
		want       string
	}{
		{
			name: "the language wins over a higher vote",
			candidates: []artCandidate{
				{URL: "/de.jpg", Language: "de", Votes: 9},
				{URL: "/en.jpg", Language: "en", Votes: 4},
			},
			want: "/en.jpg",
		},
		{
			name: "the highest vote of that language",
			candidates: []artCandidate{
				{URL: "/low.jpg", Language: "en", Votes: 2},
				{URL: "/high.jpg", Language: "en", Votes: 8},
			},
			want: "/high.jpg",
		},
		{
			name: "no language comes before another language",
			candidates: []artCandidate{
				{URL: "/de.jpg", Language: "de", Votes: 9},
				{URL: "/none.jpg", Language: "", Votes: 1},
			},
			want: "/none.jpg",
		},
		{
			name:       "any language where there is nothing else",
			candidates: []artCandidate{{URL: "/de.jpg", Language: "de", Votes: 3}},
			want:       "/de.jpg",
		},
		{
			name:       "an image with no address is no image",
			candidates: []artCandidate{{URL: "", Language: "en", Votes: 9}},
			want:       "",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			candidate, held := chooseArt(test.candidates, artLanguage)
			if held != (test.want != "") {
				t.Fatalf("chose %+v, want %q", candidate, test.want)
			}
			if candidate.URL != test.want {
				t.Errorf("chose %q, want %q", candidate.URL, test.want)
			}
		})
	}
}

// The line holds one answerer per block the source order names, in that order,
// and a block whose key did not reach the container holds none.
func TestTheArtLineFollowsTheSourceOrder(t *testing.T) {
	keys := map[string]string{
		providerTokenVariable(providerBlockTMDb):   "a-tmdb-key",
		providerTokenVariable(providerBlockFanart): "a-fanart-key",
	}
	cases := []struct {
		name   string
		blocks []string
		keys   map[string]string
		want   []string
	}{
		{name: "the art of Fanart.tv before TMDb's", keys: keys,
			blocks: []string{providerBlockFanart, providerBlockTMDb},
			want:   []string{providerBlockFanart, providerBlockTMDb}},
		{name: "the art of TMDb before Fanart.tv's", keys: keys,
			blocks: []string{providerBlockTMDb, providerBlockFanart},
			want:   []string{providerBlockTMDb, providerBlockFanart}},
		{name: "a block whose key did not reach the container", keys: map[string]string{},
			blocks: []string{providerBlockTMDb, providerBlockFanart}, want: nil},
		{name: "the block that needs no key", keys: map[string]string{},
			blocks: []string{providerBlockTVmaze}, want: []string{providerBlockTVmaze}},
		{name: "a block this image has no art answerer for", keys: keys,
			blocks: []string{providerBlockOMDb}, want: nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			line := newArtLine(test.blocks, func(name string) string { return test.keys[name] })

			blocks := []string{}
			for _, one := range line.answerers {
				blocks = append(blocks, one.providerBlock())
			}
			if len(blocks) != len(test.want) {
				t.Fatalf("blocks = %v, want %v", blocks, test.want)
			}
			for index, want := range test.want {
				if blocks[index] != want {
					t.Errorf("blocks = %v, want %v", blocks, test.want)
				}
			}
		})
	}
}

// Art is a single value, so the first block of the Library's sources that
// holds an image writes the file, and the ledger names it. The same two
// providers in the other order write the other image.
func TestTheFirstSourceThatHoldsAnImageWritesIt(t *testing.T) {
	folder := "The Signal (2014)"
	cases := []struct {
		name  string
		order func(fanart, tmdb artAnswerer) *artLine
		want  string
		block string
	}{
		{
			name:  "Fanart.tv before TMDb",
			order: func(fanart, tmdb artAnswerer) *artLine { return artLineOf(fanart, tmdb) },
			want:  "/art/movieposter.jpg", block: providerBlockFanart,
		},
		{
			name:  "TMDb before Fanart.tv",
			order: func(fanart, tmdb artAnswerer) *artLine { return artLineOf(tmdb, fanart) },
			want:  testImage, block: providerBlockTMDb,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			writeFile(t, filepath.Join(root, folder, "The Signal (2014).mkv"), "video")
			seedArtMovie(t, catalog, folder)
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)
			answers := map[string]string{}
			fanart, _ := newArtFanart(t, answers)
			answers[fanartMoviePath+"603"] = fanartMovieArt(fanart.base)
			tmdb, _ := newArtTMDb(t, map[string]string{
				tmdbKey("/3/movie/603/images", "", ""): imagesAnswer(tmdbPosters, "/quiet.jpg", artLanguage),
				tmdbKey("/t/p/w780/quiet.jpg", "", ""): testImage,
			})
			line := test.order(fanartArtAnswerer{client: fanart}, newTMDbArtAnswerer(tmdb))

			if err := work.artGap(t.Context(), factPoster, line); err != nil {
				t.Fatal(err)
			}

			if got := readFileString(t, filepath.Join(root, folder, "poster.jpg")); got != test.want {
				t.Errorf("poster.jpg holds %q, want %q", got, test.want)
			}
			ledger := artLedger(t, filepath.Join(root, folder), factPoster)
			if len(ledger.Items) != 1 || !ledger.Items[0].Provider.is(test.block) {
				t.Errorf("ledger items = %+v, want %s", ledger.Items, test.block)
			}
		})
	}
}

// One answerer of a test, which holds the images it was given and the error it
// answers with.
type fakeArtAnswerer struct {
	block  string
	images []artCandidate
	err    error
	skips  bool
}

func (a fakeArtAnswerer) providerBlock() string { return a.block }
func (a fakeArtAnswerer) serves(string) bool    { return !a.skips }

func (a fakeArtAnswerer) candidates(context.Context, string, artGap, titleRef) ([]artCandidate, error) {
	return a.images, a.err
}

func (a fakeArtAnswerer) fetchFile(context.Context, string) ([]byte, error) {
	return []byte(testImage), nil
}

// A provider that refuses leaves the blocks behind it their answer, and the
// refusal is reported only where no block answered at all.
func TestAnAskCarriesOnPastAProviderThatRefuses(t *testing.T) {
	refused := errors.New("the provider refused the call")
	held := []artCandidate{{URL: "/held.jpg"}}
	cases := []struct {
		name      string
		answerers []artAnswerer
		want      string
		wantError bool
	}{
		{
			name: "a refusal before an answer",
			answerers: []artAnswerer{
				fakeArtAnswerer{block: "one", err: refused},
				fakeArtAnswerer{block: "two", images: held},
			},
			want: "two",
		},
		{
			name: "a refusal with no answer behind it",
			answerers: []artAnswerer{
				fakeArtAnswerer{block: "one", err: refused},
				fakeArtAnswerer{block: "two"},
			},
			wantError: true,
		},
		{
			name:      "no provider holds an image",
			answerers: []artAnswerer{fakeArtAnswerer{block: "one"}},
		},
		{
			name: "a provider that does not serve the fact",
			answerers: []artAnswerer{
				fakeArtAnswerer{block: "one", images: held, skips: true},
				fakeArtAnswerer{block: "two", images: held},
			},
			want: "two",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			line := artLineOf(test.answerers...)

			answerer, candidates, err := line.ask(t.Context(), factPoster, artGap{}, titleRef{})

			if (err != nil) != test.wantError {
				t.Fatalf("the ask answered %v, want an error: %t", err, test.wantError)
			}
			if test.want == "" {
				if answerer != nil {
					t.Errorf("the ask answered %s, want none", answerer.providerBlock())
				}
				return
			}
			if answerer == nil || answerer.providerBlock() != test.want || len(candidates) != 1 {
				t.Errorf("the ask answered %+v, want the images of %s", candidates, test.want)
			}
		})
	}
}
