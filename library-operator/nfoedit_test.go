package main

// what these tests read: the group edit writes the elements one fact owns, and
// leaves every other byte of the sidecar as it was.

import (
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// jellyfinSidecar is a full sidecar of the shape Jellyfin writes, the one the
// diff test proves the edit keeps.
const jellyfinSidecar = `<?xml version="1.0" encoding="utf-8"?>
<movie>
  <plot>An old plot.</plot>
  <outline>An outline no fact owns.</outline>
  <lockdata>false</lockdata>
  <dateadded>2024-01-02 03:04:05</dateadded>
  <title>Winter Harbour</title>
  <originaltitle>Winter Harbour</originaltitle>
  <director>Ada Ferris</director>
  <writer>Ada Ferris</writer>
  <credits>Ada Ferris</credits>
  <trailer>plugin://plugin.video.youtube/?action=play_video&amp;videoid=abc</trailer>
  <ratings>
    <rating name="imdb" max="10" default="true">
      <value>7.1</value>
      <votes>2000</votes>
    </rating>
  </ratings>
  <year>2011</year>
  <mpaa>PG</mpaa>
  <imdbid>tt1234567</imdbid>
  <tmdbid>4242</tmdbid>
  <premiered>2011-05-06</premiered>
  <releasedate>2011-05-06</releasedate>
  <runtime>101</runtime>
  <tagline>An old tagline.</tagline>
  <genre>Drama</genre>
  <studio>Harbour Pictures</studio>
  <uniqueid type="tmdb" default="true">4242</uniqueid>
  <actor>
    <name>Nora Vance</name>
    <role>Captain</role>
    <order>0</order>
  </actor>
  <art>
    <poster>/media/Winter Harbour (2011)/poster.jpg</poster>
  </art>
  <fileinfo>
    <streamdetails>
      <video>
        <codec>h264</codec>
      </video>
    </streamdetails>
  </fileinfo>
</movie>
`

// withoutGroup is the document with every element the group owns taken out,
// each with the whitespace that led it. Two documents that agree here differ
// only inside the group.
func withoutGroup(t *testing.T, document []byte, group elementGroup) string {
	t.Helper()
	places, err := groupSpans(document, group)
	if err != nil {
		t.Fatal(err)
	}
	out := document
	for at := len(places.spans) - 1; at >= 0; at-- {
		span := places.spans[at]
		lead := len(trailingWhitespace(document[:span.start]))
		out = splice(out, span.start-lead, span.end, nil)
	}
	return string(out)
}

func TestAGroupEditKeepsEveryByteOutsideTheGroup(t *testing.T) {
	answer := factAnswer{
		Plot: "A new plot.", Tagline: "A new tagline.",
		Genres: []string{"Drama", "Thriller"}, Studios: []string{"Harbour Pictures"},
		Premiered: "2011-05-06", RuntimeMinutes: 101,
		Certification: "PG-13",
		Rating:        &titleRating{Value: 8.4, Votes: 1234},
		Cast:          []creditedActor{{Name: "Nora Vance", Role: "Captain", Thumb: "https://example.test/a.jpg"}},
	}
	for _, fact := range nfoFacts {
		t.Run(fact, func(t *testing.T) {
			group := nfoGroup(fact)
			document := []byte(jellyfinSidecar)

			edited, err := editElementGroup(document, group, nfoElements(fact, answer))
			if err != nil {
				t.Fatal(err)
			}

			if before, after := withoutGroup(t, document, group), withoutGroup(t, edited, group); before != after {
				t.Errorf("the edit changed bytes outside the group:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if _, err := parseMovieNFO(edited); err != nil {
				t.Errorf("the edited sidecar does not parse: %v", err)
			}
		})
	}
}

func TestAGroupEditWritesEachFactsOwnElements(t *testing.T) {
	answer := factAnswer{
		Plot: "A new plot.", Tagline: "A new tagline.",
		Genres: []string{"Drama", "Thriller"}, Studios: []string{"Harbour Pictures"},
		Premiered: "2011-05-06", RuntimeMinutes: 101,
		Certification: "PG-13",
		Rating:        &titleRating{Value: 8.4, Votes: 1234},
		Cast:          []creditedActor{{Name: "Nora Vance", Role: "Captain", Thumb: "https://example.test/a.jpg"}},
	}
	cases := []struct {
		fact string
		want []string
		gone []string
	}{
		{
			fact: factOverview,
			want: []string{
				"<plot>A new plot.</plot>", "<tagline>A new tagline.</tagline>",
				"<genre>Drama</genre>", "<genre>Thriller</genre>",
				"<studio>Harbour Pictures</studio>", "<premiered>2011-05-06</premiered>",
				"<runtime>101</runtime>",
			},
			gone: []string{"An old plot.", "An old tagline."},
		},
		{fact: factCertification, want: []string{"<mpaa>PG-13</mpaa>"}, gone: []string{"<mpaa>PG</mpaa>"}},
		{
			fact: factRatingTMDb,
			want: []string{
				`<rating name="themoviedb" max="10" default="true">`,
				"<value>8.4</value>", "<votes>1234</votes>",
				`<rating name="imdb" max="10" default="true">`,
			},
		},
		{
			fact: factCredits,
			want: []string{
				"<name>Nora Vance</name>", "<role>Captain</role>", "<order>0</order>",
				"<thumb>https://example.test/a.jpg</thumb>",
			},
		},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			edited, err := editElementGroup([]byte(jellyfinSidecar), nfoGroup(test.fact),
				nfoElements(test.fact, answer))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range test.want {
				if !strings.Contains(string(edited), want) {
					t.Errorf("the sidecar holds no %s:\n%s", want, edited)
				}
			}
			for _, gone := range test.gone {
				if strings.Contains(string(edited), gone) {
					t.Errorf("the sidecar still holds %s:\n%s", gone, edited)
				}
			}
		})
	}
}

func TestAGroupEditFillsASidecarThatHoldsNoneOfIt(t *testing.T) {
	answer := factAnswer{
		Plot: "A new plot.", Certification: "PG-13",
		Rating: &titleRating{Value: 8.4},
		Cast:   []creditedActor{{Name: "Nora Vance"}},
	}
	for _, fact := range nfoFacts {
		t.Run(fact, func(t *testing.T) {
			document := minimalNFO(nfoRootMovie, "Winter Harbour")

			edited, err := editElementGroup(document, nfoGroup(fact), nfoElements(fact, answer))
			if err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(string(edited), "<title>Winter Harbour</title>") {
				t.Errorf("the edit lost the title:\n%s", edited)
			}
			hash, err := groupHash(edited, nfoGroup(fact))
			if err != nil {
				t.Fatalf("the edited sidecar does not parse: %v", err)
			}
			if hash == "" {
				t.Errorf("the sidecar holds none of the %s group:\n%s", fact, edited)
			}
		})
	}
}

// The rating of one site arrives inside the ratings element, and the ratings
// element is created for the first one that lands in a sidecar with none.
func TestARatingLandsInsideTheRatingsElement(t *testing.T) {
	document := minimalNFO(nfoRootMovie, "Winter Harbour")
	answer := factAnswer{Rating: &titleRating{Value: 8.4, Votes: 12}}

	edited, err := editElementGroup(document, nfoGroup(factRatingTMDb), nfoElements(factRatingTMDb, answer))
	if err != nil {
		t.Fatal(err)
	}

	var read movieNFO
	if err := xml.Unmarshal(edited, &read); err != nil {
		t.Fatal(err)
	}
	rating := ratingNamed(read.Ratings.Ratings, tmdbRatingName)
	if rating == nil || rating.Value != "8.4" || rating.Votes != 12 || rating.Max != 10 {
		t.Fatalf("read %+v, want the TMDb rating under the ratings element:\n%s", read.Ratings, edited)
	}

	second, err := editElementGroup(edited, nfoGroup(factRatingTMDb),
		nfoElements(factRatingTMDb, factAnswer{Rating: &titleRating{Value: 9}}))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(second, []byte("<ratings>")) != 1 {
		t.Errorf("the second write made a second ratings element:\n%s", second)
	}
}

// The hash the ledger keeps changes when the group changes and holds when the
// rest of the document changes, which is what the fight check reads.
func TestTheGroupHashFollowsTheGroupAlone(t *testing.T) {
	group := nfoGroup(factOverview)
	document := []byte(jellyfinSidecar)
	first, err := groupHash(document, group)
	if err != nil {
		t.Fatal(err)
	}

	elsewhere := bytes.Replace(document, []byte("<year>2011</year>"), []byte("<year>2012</year>"), 1)
	same, err := groupHash(elsewhere, group)
	if err != nil {
		t.Fatal(err)
	}
	if same != first {
		t.Errorf("the hash moved with an element the fact does not own")
	}

	inside := bytes.Replace(document, []byte("An old plot."), []byte("A hand-written plot."), 1)
	changed, err := groupHash(inside, group)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Errorf("the hash held while the group changed")
	}
}

// The one remove in the enricher stays the temporary's, whatever a group edit
// writes.
func TestAGroupEditLeavesNoTemporaryBehind(t *testing.T) {
	dir := t.TempDir()
	sidecar := filepath.Join(dir, movieSidecarName)
	writeFile(t, sidecar, jellyfinSidecar)
	edited, err := editElementGroup([]byte(jellyfinSidecar), nfoGroup(factCertification),
		nfoElements(factCertification, factAnswer{Certification: "PG-13"}))
	if err != nil {
		t.Fatal(err)
	}

	if err := newVolumeWriter("movies-enrich").write(sidecar, edited); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != movieSidecarName {
		t.Errorf("the directory holds %v, want the sidecar alone", entries)
	}
}

// A sidecar the parser stops on fails the group edit, and the bytes stay as
// they were.
func TestAGroupEditFailsOnADocumentThatIsNotXML(t *testing.T) {
	for _, fact := range nfoFacts {
		t.Run(fact, func(t *testing.T) {
			_, err := editElementGroup([]byte("this is not xml <<<"), nfoGroup(fact),
				[][]byte{[]byte("<plot>A new plot.</plot>")})

			if err == nil {
				t.Error("the edit reported no error, want one")
			}
		})
	}
}

// A document with no root element has nowhere to write a group.
func TestAGroupEditFailsOnADocumentWithNoRootElement(t *testing.T) {
	_, err := editElementGroup([]byte(`<?xml version="1.0"?>`), nfoGroup(factOverview),
		[][]byte{[]byte("<plot>A new plot.</plot>")})

	if err == nil {
		t.Error("the edit reported no error, want one")
	}
}

// A parent element with no children of its own takes the group on its own line.
func TestAGroupLandsUnderAnEmptyParent(t *testing.T) {
	document := []byte("<movie>\n  <title>Winter Harbour</title>\n  <ratings></ratings>\n</movie>\n")

	edited, err := editElementGroup(document, nfoGroup(factRatingTMDb),
		nfoElements(factRatingTMDb, factAnswer{Rating: &titleRating{Value: 8.4}}))
	if err != nil {
		t.Fatal(err)
	}

	var read movieNFO
	if err := xml.Unmarshal(edited, &read); err != nil {
		t.Fatalf("the edited sidecar does not parse: %v\n%s", err, edited)
	}
	if rating := ratingNamed(read.Ratings.Ratings, tmdbRatingName); rating == nil || rating.Value != "8.4" {
		t.Errorf("read %+v, want the rating under the parent it found:\n%s", read.Ratings, edited)
	}
}

// The four rating facts write four elements into one ratings block, each fact
// owning its own element and leaving the others.
func TestTheRatingOfEachSiteSitsBesideTheOthers(t *testing.T) {
	scores := map[string]float64{
		factRatingTMDb: 8.4, factRatingIMDb: 7.9,
		factRatingRottenTomatoes: 91, factRatingMetacritic: 76,
	}
	document := minimalNFO(nfoRootMovie, "Winter Harbour")
	for fact, score := range scores {
		edited, err := editElementGroup(document, nfoGroup(fact),
			nfoElements(fact, factAnswer{Rating: &titleRating{Value: score}}))
		if err != nil {
			t.Fatal(err)
		}
		document = edited
	}

	var read movieNFO
	if err := xml.Unmarshal(document, &read); err != nil {
		t.Fatalf("the edited sidecar does not parse: %v\n%s", err, document)
	}
	marked := []string{}
	for fact, want := range scores {
		site := ratingSites[fact]
		rating := ratingNamed(read.Ratings.Ratings, site.name)
		if rating == nil || rating.Max != float64(site.max) {
			t.Fatalf("the %s rating reads %+v, want %v out of %d:\n%s", fact, rating, want, site.max, document)
		}
		if value, _ := rating.score(); value != want {
			t.Fatalf("the %s rating reads %+v, want %v out of %d:\n%s", fact, rating, want, site.max, document)
		}
		marked = append(marked, markedRatings(*rating, site)...)
	}
	if !slices.Equal(marked, []string{tmdbRatingName}) {
		t.Errorf("the ratings marked are %v, want the one a reader takes first:\n%s", marked, document)
	}
}

// The name of a rating that carries the mark a reader takes first, and the
// names of the three that do not.
func markedRatings(rating nfoRating, site ratingSite) []string {
	if !rating.Default {
		return nil
	}
	return []string{site.name}
}

// The hash of a document the parser stops on is an error, so the fight check
// never reads a torn document as another writer's work.
func TestTheGroupHashFailsOnADocumentThatIsNotXML(t *testing.T) {
	if _, err := groupHash([]byte("this is not xml <<<"), nfoGroup(factOverview)); err == nil {
		t.Error("the hash reported no error, want one")
	}
}

// A sidecar with no child element at all takes the group at the indentation of
// a first child.
func TestAGroupLandsInADocumentWithNoChildren(t *testing.T) {
	edited, err := editElementGroup([]byte("<movie></movie>\n"), nfoGroup(factOverview),
		nfoElements(factOverview, factAnswer{Plot: "A new plot."}))
	if err != nil {
		t.Fatal(err)
	}

	meta, err := parseMovieNFO(edited)
	if err != nil {
		t.Fatalf("the edited sidecar does not parse: %v\n%s", err, edited)
	}
	if meta.Body.Plot != "A new plot." {
		t.Errorf("plot = %q, want the one the fact wrote:\n%s", meta.Body.Plot, edited)
	}
}

// A fact this wave does not run owns no element, so nothing writes for it.
func TestAFactOfALaterWaveOwnsNoElement(t *testing.T) {
	if group := nfoGroup(factContributorBiography); len(group.owned) != 0 {
		t.Errorf("group = %+v, want no element", group)
	}
	if elements := nfoElements(factContributorBiography, factAnswer{Plot: "A new plot."}); elements != nil {
		t.Errorf("elements = %s, want none", elements)
	}
	if answersFact(factContributorBiography, factAnswer{Plot: "A new plot."}) {
		t.Error("a fact of a later wave reads an answer, want none")
	}
}

// A sidecar Jellyfin wrote may hold a bare ampersand in a URL, and a strict
// reader stops at it. The edit finds its group past that entity, and every
// byte of the document outside the group stays, the entity included.
func TestAGroupEditReadsPastABareAmpersand(t *testing.T) {
	document := []byte("<tvshow>\n  <title>Harbor Lights</title>\n" +
		"  <thumb>https://images.example/poster.jpg?size=large&id=42</thumb>\n" +
		"  <plot>Old plot.</plot>\n</tvshow>\n")
	edited, err := editElementGroup(document, nfoGroup(factOverview),
		[][]byte{[]byte("<plot>New plot.</plot>")})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(edited, []byte("&id=42</thumb>")) {
		t.Errorf("the entity did not survive the edit:\n%s", edited)
	}
	if !bytes.Contains(edited, []byte("<plot>New plot.</plot>")) || bytes.Contains(edited, []byte("Old plot")) {
		t.Errorf("the group was not replaced:\n%s", edited)
	}
}
