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

// open opens a fixture from testdata. The fixtures were made with the
// astrometry programs from a frame of the CCD simulator: catalog.fits is
// SourceExtractor's catalog with the worker's six columns, axy.fits is
// the .axy file that solve-field --just-augment made from it, and
// wcs.fits is the solution that astrometry-engine wrote.
func open(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("opening testdata/%s: %v", name, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// headerOf builds a header from card text, with the END card and the
// padding.
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
		t.Fatalf("reading the header of wcs.fits: %v", err)
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
		t.Fatalf("reading the header: %v", err)
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
		t.Errorf("the comments are %q, want [some words]", got)
	}
}

func TestAKeywordThatIsMissingOrMalformedIsAnError(t *testing.T) {
	h, err := ReadHeader(bytes.NewReader(headerOf("WORD    = 'text'", "HALF    = 1.5")))
	if err != nil {
		t.Fatalf("reading the header: %v", err)
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
			t.Errorf("%s: the error is %v, want an error with errors.Is(err, ErrMissing) = %v", c.name, c.err, c.missing)
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
			t.Errorf("%s: ReadHeader returned no error, want one", c.name)
		}
	}
}

// 40 cards make a header of two blocks.
func TestReadHeaderStopsAtTheEndOfItsBlock(t *testing.T) {
	var cards []string
	for range 40 {
		cards = append(cards, "HISTORY a line")
	}
	file := append(headerOf(cards...), []byte("DATA")...)
	r := bytes.NewReader(file)
	if _, err := ReadHeader(r); err != nil {
		t.Fatalf("reading the header: %v", err)
	}
	rest, _ := io.ReadAll(r)
	if string(rest) != "DATA" {
		t.Errorf("after the header: %q, want DATA", rest)
	}
}
