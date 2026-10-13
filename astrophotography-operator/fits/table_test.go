package fits

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// astropy read the same catalog: 1,675 rows, the first with X_IMAGE
// 675.21466, FWHM_IMAGE 7.0659075, and FLAGS 3.
func TestReadTableReadsSourceExtractorsCatalog(t *testing.T) {
	table, err := ReadTable(open(t, "catalog.fits"))
	if err != nil {
		t.Fatal(err)
	}
	x, _ := table.Column("X_IMAGE")
	fwhm, _ := table.Column("FWHM_IMAGE")
	flags, _ := table.Column("FLAGS")
	cases := []struct {
		name      string
		got, want float64
	}{
		{"rows", float64(table.Rows()), 1675},
		{"X_IMAGE", x[0], 675.21466},
		{"FWHM_IMAGE", fwhm[0], 7.0659075},
		{"FLAGS", flags[0], 3},
	}
	for _, c := range cases {
		if math.Abs(c.got-c.want) > 1e-4 {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// tableOf builds a file of an empty primary HDU and one binary table
// with one row, whose bytes are given.
func tableOf(row []byte, cards ...string) []byte {
	file := headerOf("SIMPLE  = T", "BITPIX  = 8", "NAXIS   = 0")
	table := append([]string{"XTENSION= 'BINTABLE'", "BITPIX  = 8", "NAXIS   = 2", "NAXIS2  = 1"}, cards...)
	file = append(file, headerOf(table...)...)
	return append(file, row...)
}

func bigEndian(values ...any) []byte {
	var b bytes.Buffer
	for _, v := range values {
		_ = binary.Write(&b, binary.BigEndian, v)
	}
	return b.Bytes()
}

// Each numeric type reads at its offset, after the columns before it,
// including a string and bits that the reader steps over.
func TestColumnReadsEachNumericType(t *testing.T) {
	row := append([]byte("ab"), 0xff)
	row = append(row, bigEndian(int16(-2), int32(-3), int64(4), float64(5.5), uint8(6))...)
	file := tableOf(row,
		"NAXIS1  = 26", "TFIELDS = 7",
		"TTYPE1  = 'NAME'", "TFORM1  = '2A'",
		"TTYPE2  = 'BITS'", "TFORM2  = '8X'",
		"TTYPE3  = 'I'", "TFORM3  = 'I'",
		"TTYPE4  = 'J'", "TFORM4  = '1J'",
		"TTYPE5  = 'K'", "TFORM5  = '1K'",
		"TTYPE6  = 'D'", "TFORM6  = '1D'",
		"TTYPE7  = 'B'", "TFORM7  = '1B'",
	)
	table, err := ReadTable(bytes.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		column string
		want   float64
	}{
		{"I", -2}, {"J", -3}, {"K", 4}, {"D", 5.5}, {"B", 6},
	}
	for _, c := range cases {
		got, err := table.Column(c.column)
		if err != nil || got[0] != c.want {
			t.Errorf("%s = %v (%v), want %v", c.column, got, err, c.want)
		}
	}
}

func TestColumnRefusesWhatItCannotRead(t *testing.T) {
	cases := []struct {
		name   string
		cards  []string
		column string
	}{
		{"no TFIELDS", []string{"NAXIS1  = 4"}, "A"},
		{"no TFORM", []string{"NAXIS1  = 4", "TFIELDS = 1"}, "A"},
		{"a form with no type", []string{"NAXIS1  = 4", "TFIELDS = 1", "TFORM1  = '4'"}, "A"},
		{"an unknown type", []string{"NAXIS1  = 4", "TFIELDS = 1", "TFORM1  = '1P'"}, "A"},
		{"a vector", []string{"NAXIS1  = 4", "TFIELDS = 1", "TTYPE1  = 'A'", "TFORM1  = '2I'"}, "A"},
		{"a missing column", []string{"NAXIS1  = 4", "TFIELDS = 1", "TTYPE1  = 'A'", "TFORM1  = '1E'"}, "B"},
	}
	for _, c := range cases {
		table, err := ReadTable(bytes.NewReader(tableOf(make([]byte, 4), c.cards...)))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if _, err := table.Column(c.column); err == nil {
			t.Errorf("%s: no error", c.name)
		}
	}
}

func TestReadTableRefusesAFileWithNoTable(t *testing.T) {
	primary := headerOf("SIMPLE  = T", "BITPIX  = 16", "NAXIS   = 2", "NAXIS1  = 2", "NAXIS2  = 2", "PCOUNT  = 0")
	image := append(primary, make([]byte, BlockSize)...)
	cases := []struct {
		name string
		file []byte
	}{
		{"an empty file", nil},
		{"an image and nothing after", image},
		{"an image extension", append(image, headerOf("XTENSION= 'IMAGE'", "BITPIX  = 8", "NAXIS   = 0")...)},
		{"a primary HDU with no BITPIX", headerOf("SIMPLE  = T", "NAXIS   = 0")},
		{"a primary HDU with no NAXIS", headerOf("SIMPLE  = T", "BITPIX  = 8")},
		{"a primary HDU with no NAXIS1", headerOf("SIMPLE  = T", "BITPIX  = 8", "NAXIS   = 1")},
		{"data cut short", primary},
		{"a table with no NAXIS1", append(headerOf("SIMPLE  = T", "BITPIX  = 8", "NAXIS   = 0"), headerOf("XTENSION= 'BINTABLE'")...)},
		{"a table with no NAXIS2", tableOfRows("NAXIS1  = 4")},
		{"rows cut short", tableOf(nil, "NAXIS1  = 4")},
	}
	for _, c := range cases {
		if _, err := ReadTable(bytes.NewReader(c.file)); err == nil {
			t.Errorf("%s: no error", c.name)
		}
	}
}

// tableOfRows builds a table header with no NAXIS2.
func tableOfRows(cards ...string) []byte {
	file := headerOf("SIMPLE  = T", "BITPIX  = 8", "NAXIS   = 0")
	return append(file, headerOf(append([]string{"XTENSION= 'BINTABLE'"}, cards...)...)...)
}
