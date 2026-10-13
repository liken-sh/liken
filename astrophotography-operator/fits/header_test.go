package fits

import (
	"bytes"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

// The fixtures in testdata come from the astrometry image and a frame
// of the CCD simulator: catalog.fits is SourceExtractor's catalog of
// the worker's six columns, axy.fits is the .axy that solve-field
// --just-augment made of it, and wcs.fits is the solution that
// astrometry-engine wrote.
func open(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// headerOf builds a header block from cards written as they appear in
// a file, each padded to 80 characters, then END.
func headerOf(cards ...string) []byte {
	var b bytes.Buffer
	for _, c := range append(cards, "END") {
		b.WriteString(c + strings.Repeat(" ", cardSize-len(c)))
	}
	b.Write(bytes.Repeat([]byte(" "), int(padded(int64(b.Len()))-int64(b.Len()))))
	return b.Bytes()
}

func TestReadHeaderReadsTheValuesOfARealSolution(t *testing.T) {
	h, err := ReadHeader(open(t, "wcs.fits"))
	if err != nil {
		t.Fatal(err)
	}
	ra, _ := h.Float("CRVAL1")
	width, _ := h.Int("IMAGEW")
	ctype, _ := h.String("CTYPE1")
	cases := []struct {
		name      string
		got, want any
	}{
		{"CRVAL1", ra, 29.6069701529},
		{"IMAGEW", width, int64(1280)},
		{"CTYPE1", ctype, "RA---TAN-SIP"},
		{"index id comment", slices.Contains(h.Comments(), "index id: 4207"), true},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestValueReadsEachFormOfCard(t *testing.T) {
	h, err := ReadHeader(bytes.NewReader(headerOf(
		"QUOTED  = 'it''s here'         / a quote in a string",
		"PADDED  = 'Red     '           / trailing spaces do not count",
		"NUMBER  =         4.000D+02 / a D exponent",
		"COMMENT   some words",
		"",
		"NOVALUE   text with no equals sign",
	)))
	if err != nil {
		t.Fatal(err)
	}
	number, _ := h.Float("NUMBER")
	cases := []struct {
		key, want string
	}{
		{"QUOTED", "it's here"},
		{"PADDED", "Red"},
		{"NOVALUE", "text with no equals sign"},
	}
	for _, c := range cases {
		if got, _ := h.Get(c.key); got != c.want {
			t.Errorf("%s = %q, want %q", c.key, got, c.want)
		}
	}
	if number != 400 {
		t.Errorf("NUMBER = %v, want 400", number)
	}
	if got := h.Comments(); !slices.Equal(got, []string{"some words"}) {
		t.Errorf("comments = %q", got)
	}
}

func TestAKeywordThatIsMissingOrMalformedIsAnError(t *testing.T) {
	h, err := ReadHeader(bytes.NewReader(headerOf("WORD    = 'text'", "HALF    = 1.5")))
	if err != nil {
		t.Fatal(err)
	}
	_, missingInt := h.Int("NAXIS")
	_, missingFloat := h.Float("CRVAL1")
	_, notInt := h.Int("HALF")
	_, notFloat := h.Float("WORD")
	cases := []struct {
		name    string
		err     error
		missing bool
	}{
		{"missing integer", missingInt, true},
		{"missing number", missingFloat, true},
		{"not an integer", notInt, false},
		{"not a number", notFloat, false},
	}
	for _, c := range cases {
		if c.err == nil || errors.Is(c.err, ErrMissing) != c.missing {
			t.Errorf("%s: %v", c.name, c.err)
		}
	}
}

func TestReadHeaderRefusesWhatIsNotAHeader(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"short", []byte("SIMPLE  =                    T")},
		{"no END", bytes.Repeat([]byte(" "), BlockSize*maxHeaderBlocks)},
	}
	for _, c := range cases {
		if _, err := ReadHeader(bytes.NewReader(c.data)); err == nil {
			t.Errorf("%s: no error", c.name)
		}
	}
}

// A header spans as many blocks as its cards need, and the reader stops
// after the block that holds END, at the start of the data.
func TestReadHeaderStopsAtTheEndOfItsBlock(t *testing.T) {
	var cards []string
	for range 40 {
		cards = append(cards, "HISTORY a line")
	}
	file := append(headerOf(cards...), []byte("DATA")...)
	r := bytes.NewReader(file)
	if _, err := ReadHeader(r); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(r)
	if string(rest) != "DATA" {
		t.Errorf("after the header: %q, want DATA", rest)
	}
}
