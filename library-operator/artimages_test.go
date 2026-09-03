package main

// What these tests read: which endpoint each art fact asks, which image the
// choice takes out of a list, the size the fetch asks for, and how a download
// answers a provider that refuses.

import (
	"net/http"
	"testing"
	"time"
)

// The image bytes every art test writes, which are a PNG header and nothing
// more, because no code in this operator decodes an image.
const testImage = "\x89PNG\r\n\x1a\nquiet-harbor"

// The configuration the fake provider answers, with the image host pointed at
// the fake itself, so a download in a test reaches no other server.
func fakeConfiguration(base string) string {
	return `{"images":{"secure_base_url":"` + base + `/t/p/",` +
		`"poster_sizes":["w342","w780","original"],` +
		`"backdrop_sizes":["w300","w1280","original"],` +
		`"logo_sizes":["w300","w500","original"],` +
		`"still_sizes":["w185","w300","original"]}}`
}

// The client every art test runs against: the fake provider, with the
// configuration and the image bytes already in it.
func newArtTMDb(t *testing.T, answers map[string]string) (*tmdbClient, *fakeTMDb) {
	t.Helper()
	client, fake := newFakeTMDb(t, answers)
	fake.answers[tmdbKey(tmdbConfigurationPath, "", "")] = fakeConfiguration(client.base)
	return client, fake
}

// Each fact reads its own endpoint: a movie, a series, a season of that
// series, or one episode of that season.
func TestEachArtFactReadsItsOwnEndpoint(t *testing.T) {
	cases := []struct {
		name string
		kind string
		fact string
		gap  artGap
		want string
	}{
		{name: "a movie", kind: libraryKindMovies, fact: factPoster,
			gap: artGap{tmdb: "603"}, want: "/3/movie/603/images"},
		{name: "a series", kind: libraryKindSeries, fact: factBackdrop,
			gap: artGap{tmdb: "1396"}, want: "/3/tv/1396/images"},
		{name: "a season", kind: libraryKindSeries, fact: factSeasonPoster,
			gap: artGap{tmdb: "1396", season: 2}, want: "/3/tv/1396/season/2/images"},
		{name: "an episode", kind: libraryKindSeries, fact: factEpisodeThumb,
			gap: artGap{tmdb: "1396", season: 2, episode: 5}, want: "/3/tv/1396/season/2/episode/5/images"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := tmdbImagesPath(test.kind, test.fact, test.gap); got != test.want {
				t.Errorf("path = %q, want %q", got, test.want)
			}
		})
	}
}

// The fetch asks for the size this project picked where TMDb serves it, and
// for the original where it does not.
func TestTheFetchAsksForTheSizeTMDbServes(t *testing.T) {
	client, _ := newArtTMDb(t, map[string]string{})
	configuration, err := client.configuration(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		fact string
		want string
	}{
		{fact: factPoster, want: "w780"},
		{fact: factBackdrop, want: "w1280"},
		{fact: factLogo, want: "w500"},
		{fact: factEpisodeThumb, want: "w300"},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			if got := configuration.sizeFor(artTypes[test.fact]); got != test.want {
				t.Errorf("size = %q, want %q", got, test.want)
			}
		})
	}

	narrow := tmdbConfiguration{}
	narrow.Images.PosterSizes = []string{"w92"}
	if got := narrow.sizeFor(artTypes[factPoster]); got != tmdbOriginalSize {
		t.Errorf("size = %q, want the original where the size is not served", got)
	}
	want := client.base + "/t/p/w780/quiet.jpg"
	if got := configuration.imageURL("w780", "/quiet.jpg"); got != want {
		t.Errorf("address = %q, want %q", got, want)
	}
}

// A configuration that names no image host is an error, because every address
// hangs off it.
func TestAConfigurationWithNoImageHostIsAnError(t *testing.T) {
	client, fake := newArtTMDb(t, map[string]string{})
	fake.answers[tmdbKey(tmdbConfigurationPath, "", "")] = `{"images":{}}`

	if _, err := client.configuration(t.Context()); err == nil {
		t.Error("the read reported no error, want one")
	}
}

// A download waits the cooldown a 429 names and asks again, and a refusal
// that stands is an error.
func TestTheImageDownloadFollowsTheCooldownRule(t *testing.T) {
	client, fake := newArtTMDb(t, map[string]string{
		tmdbKey("/t/p/w780/quiet.jpg", "", ""): testImage,
	})
	fake.tooMany = 1
	fake.retryAfter = "2"

	data, err := client.fetchFile(t.Context(), client.base+"/t/p/w780/quiet.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != testImage {
		t.Errorf("read %d bytes, want the image", len(data))
	}
	if len(fake.cooldowns) != 1 || fake.cooldowns[0] != 2*time.Second {
		t.Errorf("cooldowns = %v, want the one the header named", fake.cooldowns)
	}
}

func TestADownloadTheProviderRefusesIsAnError(t *testing.T) {
	client, fake := newArtTMDb(t, map[string]string{})
	fake.statuses[tmdbKey("/t/p/w780/quiet.jpg", "", "")] = http.StatusInternalServerError

	if _, err := client.fetchFile(t.Context(), client.base+"/t/p/w780/quiet.jpg"); err == nil {
		t.Error("the download reported no error, want one")
	}
}

func TestADownloadOfNothingIsAnError(t *testing.T) {
	client, fake := newArtTMDb(t, map[string]string{})
	fake.answers[tmdbKey("/t/p/w780/quiet.jpg", "", "")] = ""

	if _, err := client.fetchFile(t.Context(), client.base+"/t/p/w780/quiet.jpg"); err == nil {
		t.Error("the download reported no error, want one")
	}
}

// The answer's own list is what each fact reads, and a name the answer does
// not hold reads as the stills.
func TestTheImagesAnswerHoldsEveryList(t *testing.T) {
	answer := tmdbImageAnswer{
		Posters:   []tmdbImage{{FilePath: "/p.jpg"}},
		Backdrops: []tmdbImage{{FilePath: "/b.jpg"}},
		Logos:     []tmdbImage{{FilePath: "/l.png"}},
		Stills:    []tmdbImage{{FilePath: "/s.jpg"}},
	}
	cases := []struct {
		list string
		want string
	}{
		{list: tmdbPosters, want: "/p.jpg"},
		{list: tmdbBackdrops, want: "/b.jpg"},
		{list: tmdbLogos, want: "/l.png"},
		{list: tmdbStills, want: "/s.jpg"},
	}
	for _, test := range cases {
		t.Run(test.list, func(t *testing.T) {
			if got := answer.list(test.list); len(got) != 1 || got[0].FilePath != test.want {
				t.Errorf("list = %+v, want %q", got, test.want)
			}
		})
	}
}

// A provider that refuses the settings, and an address no request can be
// built for, are both errors the fact records.
func TestTheImageCallsAnswerWhatTheProviderRefuses(t *testing.T) {
	client, fake := newArtTMDb(t, map[string]string{})
	fake.statuses[tmdbKey(tmdbConfigurationPath, "", "")] = http.StatusUnauthorized

	if _, err := client.configuration(t.Context()); err == nil {
		t.Error("the read reported no error, want one")
	}
	if _, err := client.fetchFile(t.Context(), "://not-an-address"); err == nil {
		t.Error("the download reported no error, want one")
	}
}
