package main

import (
	"strings"
	"testing"
)

// A whole franchise.yaml, the shape the manual documents. A test that wants
// one bad field starts from a file that validates. The calendar the eras need
// and the order the file requires are named on their own, so a case below can
// take a whole block out.
const franchiseCalendarBlock = `calendar:
  unit: years
  zero: Battle of Yavin
  before: BBY
  after: ABY
`

const franchiseOrderBlock = `order:
  - movie: tmdb:1893
    title: "Star Wars: Episode I"
    released: 1999-05-19
    time: { from: -32, to: -32 }
  - series: tvdb:83268
    title: "Star Wars: The Clone Wars"
    released: 2008-10
    time: { from: -22, to: -19 }
    seasons:
      - season: 1
      - season: 3
        episodes: [S03E01, S03E03-S03E05]
        note: Lucasfilm plays S03E02 later.
  - movie: tmdb:11
    universes: [Prime, Earth-616]
`

const wholeFranchiseFile = `name: Star Wars
sources:
  - https://www.starwars.com/news/star-wars-timeline
` + franchiseCalendarBlock + `universe: Prime
eras:
  - name: Age of Rebellion
    from: -5
    to: 5
` + franchiseOrderBlock

// The parser reads every part of the file the manual documents: a calendar,
// the eras, the home universe, and the order all reach the value the scanner
// writes rows from.
func TestTheParserReadsAWholeFranchiseFile(t *testing.T) {
	file, err := parseFranchiseFile([]byte(wholeFranchiseFile))
	if err != nil {
		t.Fatal(err)
	}

	if file.Name != "Star Wars" || file.Universe != "Prime" {
		t.Errorf("name = %q and universe = %q, want the file's own", file.Name, file.Universe)
	}
	if file.Calendar.Unit != "years" || file.Calendar.Before != "BBY" {
		t.Errorf("calendar = %+v, want the years the file counts from Yavin", file.Calendar)
	}
	if len(file.Eras) != 1 || *file.Eras[0].From != -5 {
		t.Errorf("eras = %+v, want the one era the file names", file.Eras)
	}
	if len(file.Order) != 3 {
		t.Fatalf("order holds %d entries, want the three the file names", len(file.Order))
	}
	if file.Order[1].Series != "tvdb:83268" || len(file.Order[1].Seasons) != 2 {
		t.Errorf("the series entry is %+v, want its two seasons", file.Order[1])
	}
	if file.Order[0].Released != "1999-05-19" || file.Order[2].Released != "" {
		t.Errorf("the released dates are %+v, want 1999-05-19 and none", file.Order)
	}
	if len(file.Order[2].Universes) != 2 {
		t.Errorf("the last entry names %v, want the two universes", file.Order[2].Universes)
	}
}

// franchiseFileWith is the whole file with one line replaced, so each case
// below states only the rule it breaks.
func franchiseFileWith(t *testing.T, was, now string) []byte {
	t.Helper()
	if !strings.Contains(wholeFranchiseFile, was) {
		t.Fatalf("the whole file holds no %q to replace", was)
	}
	return []byte(strings.Replace(wholeFranchiseFile, was, now, 1))
}

// The scanner enforces the schema the repository publishes. A file that breaks
// one rule is rejected whole, and the error names the rule.
func TestTheParserRefusesAFileTheSchemaRefuses(t *testing.T) {
	cases := []struct {
		name string
		was  string
		now  string
		says string
	}{
		{"no name", "name: Star Wars\n", "", "name"},
		{"an empty order", franchiseOrderBlock, "order: []\n", "order"},
		{"an unknown field", "universe: Prime", "galaxy: Prime", "galaxy"},
		{"eras with no calendar", franchiseCalendarBlock, "", "calendar"},
		{"an era with no span", "    from: -5\n", "", "from"},
		{"a calendar with no unit", "  unit: years\n", "", "unit"},
		{"a calendar unit the schema does not name", "unit: years", "unit: parsecs", "parsecs"},
		{"an entry that is neither", "  - movie: tmdb:11\n    universes: [Prime, Earth-616]\n", "  - title: nothing\n", "movie"},
		{"an entry that is both", "  - movie: tmdb:11\n", "  - movie: tmdb:11\n    series: tvdb:9\n", "series"},
		{"a provider id in no scheme", "movie: tmdb:1893", "movie: 1893", "1893"},
		{"a movie with seasons", "  - movie: tmdb:11\n", "  - movie: tmdb:11\n    seasons: [{season: 1}]\n", "seasons"},
		{"a season with no number", "      - season: 1\n", "      - note: nothing\n", "season"},
		{"an episode code in no form", "S03E01", "3x01", "3x01"},
		{"a range that runs backwards", "S03E03-S03E05", "S03E05-S03E03", "S03E05-S03E03"},
		{"a range across two seasons", "S03E03-S03E05", "S03E03-S04E05", "S03E03-S04E05"},
		{"a time with one end", "time: { from: -32, to: -32 }", "time: { from: -32 }", "to"},
		{"a universe with no name", "universes: [Prime, Earth-616]", `universes: ["", Earth-616]`, "universes"},
		{"an art key the schema does not name", "universe: Prime",
			"universe: Prime\nart: {postre: 'https://art.example/p.jpg'}", "postre"},
		{"an art link that is not https", "universe: Prime",
			"universe: Prime\nart: {poster: 'http://art.example/p.jpg'}", "http://art.example/p.jpg"},
		{"the release year the schema replaced", "released: 1999-05-19", "release_year: 1999", "release_year"},
		{"a released date of two digits", "released: 1999-05-19", "released: '99'", "99"},
		{"a released month past twelve", "released: 1999-05-19", "released: 1999-13-19", "1999-13-19"},
		{"a released day past thirty-one", "released: 1999-05-19", "released: 1999-05-32", "1999-05-32"},
		{"a released day of zero", "released: 1999-05-19", "released: 1999-05-00", "1999-05-00"},
		{"a released month of one digit", "released: 1999-05-19", "released: 1999-5-19", "1999-5-19"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := parseFranchiseFile(franchiseFileWith(t, testCase.was, testCase.now))
			if err == nil {
				t.Fatalf("the parser read the file, want it refused for %s", testCase.name)
			}
			if !strings.Contains(err.Error(), testCase.says) {
				t.Errorf("the error is %q, want it to name %q", err, testCase.says)
			}
		})
	}
}

// A range expands to every episode between its two codes, and a code that
// names one episode is that one episode.
func TestTheParserExpandsAnEpisodeRange(t *testing.T) {
	cases := []struct {
		code string
		want []franchiseEpisode
	}{
		{"S03E01", []franchiseEpisode{{3, 1}}},
		{"S03E03-S03E05", []franchiseEpisode{{3, 3}, {3, 4}, {3, 5}}},
		{"S00E02-S00E02", []franchiseEpisode{{0, 2}}},
		{"S101E010", []franchiseEpisode{{101, 10}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.code, func(t *testing.T) {
			held, err := expandEpisodeCode(testCase.code)
			if err != nil {
				t.Fatal(err)
			}
			if len(held) != len(testCase.want) {
				t.Fatalf("%s expands to %v, want %v", testCase.code, held, testCase.want)
			}
			for i, episode := range held {
				if episode != testCase.want[i] {
					t.Errorf("%s expands to %v, want %v", testCase.code, held, testCase.want)
				}
			}
		})
	}
}

// The year the wall labels a row with is the first four characters of the
// released date, and a file that names no date leaves it at 0.
func TestTheEntryDerivesItsYearFromTheReleasedDate(t *testing.T) {
	cases := []struct {
		released string
		want     int
	}{
		{"1999", 1999},
		{"1999-05", 1999},
		{"1999-05-19", 1999},
		{"", 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.released, func(t *testing.T) {
			entry := franchiseEntry{Movie: "tmdb:1893", Released: testCase.released}

			if year := entry.releaseYear(); year != testCase.want {
				t.Errorf("releaseYear() = %d, want %d", year, testCase.want)
			}
		})
	}
}

// Every precision the schema states is read whole.
func TestTheParserReadsEveryPrecisionOfAReleasedDate(t *testing.T) {
	for _, released := range []string{"1888", "2026-12", "1999-05-19", "2000-02-29"} {
		t.Run(released, func(t *testing.T) {
			file, err := parseFranchiseFile(franchiseFileWith(t,
				"released: 1999-05-19", "released: "+released))
			if err != nil {
				t.Fatal(err)
			}
			if file.Order[0].Released != released {
				t.Errorf("released = %q, want %q", file.Order[0].Released, released)
			}
		})
	}
}
