package fits

import (
	"bytes"
	"os"
	"testing"
)

// The files that the worker and the engine wrote hold every HDU their
// headers announce.
func TestCompleteAcceptsRealFiles(t *testing.T) {
	for _, name := range []string{"axy.fits", "catalog.fits", "wcs.fits"} {
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := Complete(bytes.NewReader(data), int64(len(data))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// indexFile builds a primary HDU and one table of 3,000 bytes, padded
// to two blocks, the way an index file holds its quads.
func indexFile() []byte {
	file := headerOf("SIMPLE  = T", "BITPIX  = 8", "NAXIS   = 0")
	file = append(file, headerOf("XTENSION= 'BINTABLE'", "BITPIX  = 8", "NAXIS   = 2", "NAXIS1  = 3", "NAXIS2  = 1000")...)
	return append(file, make([]byte, 2*BlockSize)...)
}

func TestCompleteFollowsTheDataOfEachHDU(t *testing.T) {
	whole := indexFile()
	cases := []struct {
		name     string
		data     []byte
		complete bool
	}{
		{"the whole file", whole, true},
		{"a last HDU with no padding", whole[:len(whole)-2*BlockSize+3000], true},
		{"a file that ends inside the data", whole[:len(whole)-BlockSize], false},
		{"a file that ends inside a header", whole[:BlockSize+100], false},
		{"a header with no BITPIX", headerOf("SIMPLE  = T", "NAXIS   = 0"), false},
		{"an empty file", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Complete(bytes.NewReader(c.data), int64(len(c.data)))
			if (err == nil) != c.complete {
				t.Errorf("answered %v, want complete %v", err, c.complete)
			}
		})
	}
}
