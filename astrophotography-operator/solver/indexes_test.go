package solver

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/fits"
)

// The scales of the lab's series in radians, as the headers of the
// 2MASS index files on the laptop give them.
const (
	arcmin22 = "0.00639954059064"
	arcmin30 = "0.00872664625997"
	arcmin42 = "0.0122173047640"
)

// indexHeader builds the first block of an index file: the header that
// the solver reads. The quads that follow it are not read.
func indexHeader(cards ...string) []byte {
	var b bytes.Buffer
	for _, c := range append([]string{"SIMPLE  = T", "BITPIX  = 8", "NAXIS   = 0"}, append(cards, "END")...) {
		b.WriteString(c + strings.Repeat(" ", 80-len(c)))
	}
	b.Write(bytes.Repeat([]byte(" "), fits.BlockSize-b.Len()))
	return b.Bytes()
}

func indexOfSeries(id int, low, high string) []byte {
	return indexHeader(fmt.Sprintf("INDEXID = %d", id), "SCALE_U = "+high, "SCALE_L = "+low)
}

// writeFiles writes each file into a new directory.
func writeFiles(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The lab's claim holds 12 files of 4207 and one of 4208, by Debian's
// names. The engine reads each file's series from its header, so the
// names do not matter, and a file that is not named index-*.fits is
// not an index file.
func TestTheSolverListsEachSeriesOfItsClaim(t *testing.T) {
	files := map[string][]byte{
		"index-2mass-08.fits": indexOfSeries(4208, arcmin30, arcmin42),
		"README":              []byte("not an index"),
	}
	for tile := range 12 {
		files[fmt.Sprintf("index-2mass-07-%02d.fits", tile)] = indexOfSeries(4207, arcmin22, arcmin30)
	}
	found, err := ScanIndexes(writeFiles(t, files))
	if err != nil {
		t.Fatal(err)
	}
	want := Indexes{
		{ID: 4207, Files: 12, ScaleArcmin: astrophotography.ScaleRange{Low: 22, High: 30}},
		{ID: 4208, Files: 1, ScaleArcmin: astrophotography.ScaleRange{Low: 30, High: 42}},
	}
	if got := Series(found); !slices.Equal(got, want) {
		t.Errorf("series %+v, want %+v", got, want)
	}
}

// The engine opens each file by the path its configuration gives, and
// stops a field at the configuration's CPU limit.
func TestTheEngineConfigNamesEachFileAndTheLimit(t *testing.T) {
	files := []IndexFile{{Path: "/indexes/index-2mass-07-00.fits"}, {Path: "/indexes/index-2mass-08.fits"}}
	want := "index /indexes/index-2mass-07-00.fits\nindex /indexes/index-2mass-08.fits\ncpulimit 60\n"
	if got := EngineConfig(files, 60); got != want {
		t.Errorf("config\n%s\nwant\n%s", got, want)
	}
}

// A file that the engine could not read fails the start, with the
// file's name, so the pod's log says which file to replace.
func TestAnIndexFileThatDoesNotReadFailsTheStart(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"not FITS", []byte("truncated")},
		{"no INDEXID", indexHeader("SCALE_U = " + arcmin30)},
		{"no SCALE_L", indexHeader("INDEXID = 4207", "SCALE_U = "+arcmin30)},
		{"no SCALE_U", indexHeader("INDEXID = 4207", "SCALE_L = "+arcmin22)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ScanIndexes(writeFiles(t, map[string][]byte{"index-bad.fits": c.data}))
			if err == nil || !strings.Contains(err.Error(), "index-bad.fits") {
				t.Errorf("answered %v, want an error that names the file", err)
			}
		})
	}
}

// A link whose file is gone, such as one into a claim that a person
// emptied, does not open.
func TestAnIndexFileThatDoesNotOpenFailsTheStart(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, "index-gone.fits")); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanIndexes(dir); err == nil {
		t.Error("no error")
	}
}

func TestAPatternThatDoesNotParseIsAnError(t *testing.T) {
	if _, err := ScanIndexes("["); err == nil {
		t.Error("no error")
	}
}
