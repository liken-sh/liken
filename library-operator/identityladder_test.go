package main

import (
	"testing"
	"time"
)

func TestTheLadderWritesAnIdOnEveryRungItCanClimb(t *testing.T) {
	cases := []struct {
		name       string
		answers    map[string]string
		search     identitySearch
		wantID     int
		wantReason string
	}{
		{
			name: "the title and the year name one result",
			answers: map[string]string{
				tmdbKey("/3/search/movie", "The Long Survey", "1982"): `{"results":[` + tmdbResultJSON(1101, "The Long Survey", "1982-06-01") + `]}`,
			},
			search:     identitySearch{kind: libraryKindMovies, title: "The Long Survey", year: 1982},
			wantID:     1101,
			wantReason: reasonFrom(testTitle, testYear),
		},
		{
			name: "the provider spells the title another way and the original matches",
			answers: map[string]string{
				tmdbKey("/3/search/movie", "Celine", "2001"): `{"results":[{"id":1102,"title":"C` + "é" + `line","original_title":"La Longue Route de C` + "é" + `line Morel","release_date":"2001-04-01"}]}`,
			},
			search:     identitySearch{kind: libraryKindMovies, title: "Celine", year: 2001},
			wantID:     1102,
			wantReason: reasonFrom(testTitle, testYear),
		},
		{
			name: "a roman numeral and an article read the same on both sides",
			answers: map[string]string{
				tmdbKey("/3/search/movie", "The Harbormaster Part II", "1974"): `{"results":[` + tmdbResultJSON(1103, "Harbormaster: Part 2", "1974-12-24") + `]}`,
			},
			search:     identitySearch{kind: libraryKindMovies, title: "The Harbormaster Part II", year: 1974},
			wantID:     1103,
			wantReason: reasonFrom(testTitle, testYear),
		},
		{
			name: "the year is one off, so the search runs on either side",
			answers: map[string]string{
				tmdbKey("/3/search/movie", "Lowland", "1985"): `{"results":[]}`,
				tmdbKey("/3/search/movie", "Lowland", "1984"): `{"results":[` + tmdbResultJSON(1104, "Lowland", "1984-12-22") + `]}`,
			},
			search:     identitySearch{kind: libraryKindMovies, title: "Lowland", year: 1985},
			wantID:     1104,
			wantReason: reasonFrom(testTitle, testNearYear),
		},
		{
			name: "two results on the year, and the runtime parts them",
			answers: map[string]string{
				tmdbKey("/3/search/movie", "The Long Survey", "1982"): `{"results":[` +
					tmdbResultJSON(1101, "The Long Survey", "1982-06-01") + `,` +
					tmdbResultJSON(9999, "The Long Survey", "1982-01-05") + `]}`,
				tmdbKey("/3/movie/1101", "", ""): `{"runtime":109}`,
				tmdbKey("/3/movie/9999", "", ""): `{"runtime":42}`,
			},
			search:     identitySearch{kind: libraryKindMovies, title: "The Long Survey", year: 1982, duration: 108 * time.Minute},
			wantID:     1101,
			wantReason: reasonFrom(testTitle, testYear, testRuntime),
		},
		{
			name: "the name carries no year, and one title matches",
			answers: map[string]string{
				tmdbKey("/3/search/movie", "Veltrano", ""): `{"results":[` + tmdbResultJSON(1105, "Veltrano", "1982-09-05") + `]}`,
			},
			search:     identitySearch{kind: libraryKindMovies, title: "Veltrano"},
			wantID:     1105,
			wantReason: reasonFrom(testTitle),
		},
		{
			name: "a series matches on its first air date",
			answers: map[string]string{
				tmdbKey("/3/search/tv", "Pine Hollow", "1990"): `{"results":[{"id":2103,"name":"Pine Hollow","original_name":"Pine Hollow","first_air_date":"1990-04-16"}]}`,
			},
			search:     identitySearch{kind: libraryKindSeries, title: "Pine Hollow", year: 1990},
			wantID:     2103,
			wantReason: reasonFrom(testTitle, testYear),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, _ := newFakeTMDb(t, test.answers)

			answer, err := climbIdentityLadder(t.Context(), client, test.search)
			if err != nil {
				t.Fatal(err)
			}
			if answer.id != test.wantID {
				t.Errorf("id = %d, want %d", answer.id, test.wantID)
			}
			if answer.reason != test.wantReason {
				t.Errorf("reason = %q, want %q", answer.reason, test.wantReason)
			}
			if len(answer.candidates) != 0 {
				t.Errorf("candidates = %+v, want none on a sure answer", answer.candidates)
			}
		})
	}
}

func TestTheLadderLeavesCandidatesWhereNoRungParts(t *testing.T) {
	cases := []struct {
		name        string
		answers     map[string]string
		search      identitySearch
		wantCount   int
		wantReceipt map[string]string
	}{
		{
			name: "two results the runtime cannot part",
			answers: map[string]string{
				tmdbKey("/3/search/movie", "Deep Orbit", "1977"): `{"results":[` +
					tmdbResultJSON(1111, "Deep Orbit", "1977-05-01") + `,` +
					tmdbResultJSON(1112, "Deep Orbit", "1977-06-05") + `]}`,
				tmdbKey("/3/movie/1111", "", ""): `{"runtime":121}`,
				tmdbKey("/3/movie/1112", "", ""): `{"runtime":122}`,
			},
			search:      identitySearch{kind: libraryKindMovies, title: "Deep Orbit", year: 1977, duration: 121 * time.Minute},
			wantCount:   2,
			wantReceipt: map[string]string{"title": "match", "year": "match", "runtime": "match"},
		},
		{
			name: "two results and no runtime to part them",
			answers: map[string]string{
				tmdbKey("/3/search/movie", "Deep Orbit", "1977"): `{"results":[` +
					tmdbResultJSON(1111, "Deep Orbit", "1977-05-01") + `,` +
					tmdbResultJSON(1112, "Deep Orbit", "1977-06-05") + `]}`,
			},
			search:      identitySearch{kind: libraryKindMovies, title: "Deep Orbit", year: 1977},
			wantCount:   2,
			wantReceipt: map[string]string{"title": "match", "year": "match"},
		},
		{
			name: "the title matches on a neighbouring year alone",
			answers: map[string]string{
				tmdbKey("/3/search/movie", "Deep Orbit", "1976"): `{"results":[` + tmdbResultJSON(1111, "Deep Orbit", "1976-05-01") + `]}`,
				tmdbKey("/3/search/movie", "Deep Orbit", "1978"): `{"results":[` + tmdbResultJSON(1112, "Deep Orbit", "1978-05-01") + `]}`,
			},
			search:      identitySearch{kind: libraryKindMovies, title: "Deep Orbit", year: 1977},
			wantCount:   2,
			wantReceipt: map[string]string{"title": "match", "year": "no match"},
		},
		{
			name:      "no result at all",
			answers:   nil,
			search:    identitySearch{kind: libraryKindMovies, title: "A Film Nobody Filmed", year: 2030},
			wantCount: 0,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, _ := newFakeTMDb(t, test.answers)

			answer, err := climbIdentityLadder(t.Context(), client, test.search)
			if err != nil {
				t.Fatal(err)
			}
			if answer.id != 0 {
				t.Errorf("id = %d, want none", answer.id)
			}
			if len(answer.candidates) != test.wantCount {
				t.Fatalf("candidates = %+v, want %d", answer.candidates, test.wantCount)
			}
			if test.wantCount == 0 {
				return
			}
			if got := answer.candidates[0].Receipt; !sameReceipt(got, test.wantReceipt) {
				t.Errorf("receipt = %v, want %v", got, test.wantReceipt)
			}
		})
	}
}

func sameReceipt(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

func TestACandidatesReceiptStatesHowFarTheRuntimeSits(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "Deep Orbit", "1977"): `{"results":[` +
			tmdbResultJSON(1111, "Deep Orbit", "1977-05-01") + `,` +
			tmdbResultJSON(1112, "Deep Orbit", "1977-06-05") + `]}`,
		tmdbKey("/3/movie/1111", "", ""): `{"runtime":100}`,
		tmdbKey("/3/movie/1112", "", ""): `{"runtime":140}`,
	})

	answer, err := climbIdentityLadder(t.Context(), client, identitySearch{
		kind: libraryKindMovies, title: "Deep Orbit", year: 1977, duration: 121 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(answer.candidates) != 2 {
		t.Fatalf("candidates = %+v, want both", answer.candidates)
	}
	if got := answer.candidates[0].Receipt["runtime"]; got != "21 minutes off" {
		t.Errorf("runtime receipt = %q, want the distance", got)
	}
	if got := answer.candidates[0].ID["tmdb"]; got != "1111" {
		t.Errorf("candidate id = %q, want the provider's", got)
	}
}

func TestACandidateWithNoYearInTheNameSaysSo(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "Quiet Moon", ""): `{"results":[` +
			tmdbResultJSON(1121, "Quiet Moon", "1972-03-24") + `,` +
			tmdbResultJSON(1122, "Quiet Moon", "2002-11-03") + `]}`,
	})

	answer, err := climbIdentityLadder(t.Context(), client, identitySearch{kind: libraryKindMovies, title: "Quiet Moon"})
	if err != nil {
		t.Fatal(err)
	}

	if len(answer.candidates) != 2 {
		t.Fatalf("candidates = %+v, want both", answer.candidates)
	}
	if got := answer.candidates[0].Receipt["year"]; got != "the name carries no year" {
		t.Errorf("year receipt = %q, want the name's own silence", got)
	}
}

func TestALadderFailsWhereTheProviderFails(t *testing.T) {
	cases := []struct {
		name    string
		answers map[string]string
		refuse  string
	}{
		{name: "the first search", refuse: tmdbKey("/3/search/movie", "Deep Orbit", "1977")},
		{
			name:   "a search on a neighbouring year",
			refuse: tmdbKey("/3/search/movie", "Deep Orbit", "1976"),
		},
		{
			name: "a runtime read",
			answers: map[string]string{
				tmdbKey("/3/search/movie", "Deep Orbit", "1977"): `{"results":[` +
					tmdbResultJSON(1111, "Deep Orbit", "1977-05-01") + `,` +
					tmdbResultJSON(1112, "Deep Orbit", "1977-06-05") + `]}`,
			},
			refuse: tmdbKey("/3/movie/1111", "", ""),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, fake := newFakeTMDb(t, test.answers)
			fake.statuses[test.refuse] = 500

			_, err := climbIdentityLadder(t.Context(), client, identitySearch{
				kind: libraryKindMovies, title: "Deep Orbit", year: 1977, duration: 121 * time.Minute,
			})
			if err == nil {
				t.Error("the ladder reported no error, want the provider's")
			}
		})
	}
}

func TestATitleNormalizesTheSameOnBothSides(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  string
	}{
		{name: "case and punctuation", title: "The Harbormaster: Part II", want: "harbormaster part 2"},
		{name: "an accent", title: "Céline", want: "celine"},
		{name: "a leading article", title: "A Patient Man", want: "patient man"},
		{name: "an article that is the whole title", title: "The", want: "the"},
		{name: "a roman numeral inside the title", title: "Kestrel IV", want: "kestrel 4"},
		{name: "a title that is already plain", title: "Orbit", want: "orbit"},
		{name: "an empty title", title: "", want: ""},
		{name: "a trailing country qualifier", title: "Ruckus (US)", want: "ruckus"},
		{name: "a trailing year qualifier", title: "The Depot (2011)", want: "depot"},
		{name: "a title that is one parenthesized group", title: "(2011)", want: "2011"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeTitle(test.title); got != test.want {
				t.Errorf("normalizeTitle(%q) = %q, want %q", test.title, got, test.want)
			}
		})
	}
}

func TestTheTitleTestDropsTheResultsThatDoNotMatch(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "The Long Survey", "1982"): `{"results":[` +
			tmdbResultJSON(1, "Another Film Entirely", "1982-06-01") + `,` +
			tmdbResultJSON(2, "The Long Survey", "1951-04-03") + `,` +
			tmdbResultJSON(1101, "The Long Survey", "1982-06-01") + `]}`,
	})

	answer, err := climbIdentityLadder(t.Context(), client, identitySearch{
		kind: libraryKindMovies, title: "The Long Survey", year: 1982,
	})
	if err != nil {
		t.Fatal(err)
	}

	if answer.id != 1101 {
		t.Errorf("id = %d, want the one result that matches on both the title and the year", answer.id)
	}
}

func TestAResultOnBothNeighbouringYearsIsOneCandidate(t *testing.T) {
	both := `{"results":[` + tmdbResultJSON(1104, "Lowland", "1985-02-26") + `]}`
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "Lowland", "1985"): `{"results":[]}`,
		tmdbKey("/3/search/movie", "Lowland", "1984"): `{"results":[` + tmdbResultJSON(1104, "Lowland", "1984-02-26") + `]}`,
		tmdbKey("/3/search/movie", "Lowland", "1986"): both,
	})

	answer, err := climbIdentityLadder(t.Context(), client, identitySearch{
		kind: libraryKindMovies, title: "Lowland", year: 1985,
	})
	if err != nil {
		t.Fatal(err)
	}

	if answer.id != 1104 || answer.reason != reasonFrom(testTitle, testNearYear) {
		t.Errorf("answer = %+v, want the one result both searches named", answer)
	}
}

// The country a namer writes after a series title, as in Ruckus (US), is a
// test of its own. TMDb states the origin in origin_country, and the two shows
// of one name carry different ones.
func TestACountryQualifierPartsTwoSeriesOfOneName(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/tv", "Ruckus", ""): `{"results":[` +
			`{"id":2101,"name":"Ruckus","original_name":"Ruckus",` +
			`"first_air_date":"2004-01-21","origin_country":["GB"]},` +
			`{"id":2102,"name":"Ruckus","original_name":"Ruckus",` +
			`"first_air_date":"2011-01-17","origin_country":["US"]}]}`,
	})

	answer, err := climbIdentityLadder(t.Context(), client,
		identitySearch{kind: libraryKindSeries, title: "Ruckus (US)"})

	if err != nil {
		t.Fatal(err)
	}
	if answer.id != 2102 {
		t.Errorf("id = %d, want the show made for the country the name states", answer.id)
	}
	if answer.reason != reasonFrom(testTitle, testCountry) {
		t.Errorf("reason = %q, want the country among the tests", answer.reason)
	}
}

// A year in parentheses after the title is the year, where the name states it
// nowhere else.
func TestAYearQualifierIsTheYearTheSearchNarrowsOn(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "Night Visit", "2011"): `{"results":[` +
			tmdbResultJSON(1131, "Night Visit", "2011-08-23") + `]}`,
	})

	answer, err := climbIdentityLadder(t.Context(), client,
		identitySearch{kind: libraryKindMovies, title: "Night Visit (2011)"})

	if err != nil {
		t.Fatal(err)
	}
	if answer.id != 1131 {
		t.Errorf("id = %d, want the film of the year the qualifier states", answer.id)
	}
	if answer.reason != reasonFrom(testTitle, testYear) {
		t.Errorf("reason = %q, want the year among the tests", answer.reason)
	}
}

// A qualifier that names neither a year nor a country is no test of its own.
// The title still reaches the provider without it.
func TestAQualifierThatNamesNeitherAYearNorACountryIsNoTest(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/tv", "The Listening Post", ""): `{"results":[{"id":2104,"name":"The Listening Post",` +
			`"original_name":"The Listening Post","first_air_date":"2015-04-07","origin_country":["FR"]}]}`,
	})

	answer, err := climbIdentityLadder(t.Context(), client,
		identitySearch{kind: libraryKindSeries, title: "The Listening Post (4K)"})

	if err != nil {
		t.Fatal(err)
	}
	if answer.id != 2104 {
		t.Errorf("id = %d, want the one result the title matched", answer.id)
	}
	if answer.reason != reasonFrom(testTitle) {
		t.Errorf("reason = %q, want the title alone", answer.reason)
	}
}
