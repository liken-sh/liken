package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// frameLine returns the JSON of a frame's catalog line.
func frameLine(id, filter string, seconds float64) string {
	return `{"record":"frame","frameID":"` + id + `","target":"m42","reservation":"r","telescope":"s","camera":"c",` +
		`"path":"` + id + `.fits","sizeBytes":1,"sha256":"h","filter":"` + filter + `","exposureSeconds":` +
		strconv.FormatFloat(seconds, 'f', -1, 64) + `,"binning":1}`
}

func TestReadCountsEachFrameOnceBySettings(t *testing.T) {
	cases := []struct {
		name string
		file string
		want []Total
	}{
		{"an empty catalog", "", nil},
		{"two filters",
			frameLine("a", "L", 300) + "\n" + frameLine("b", "L", 300) + "\n" + frameLine("c", "R", 300) + "\n",
			[]Total{{Settings{Filter: "L", ExposureSeconds: 300, Binning: 1}, 2}, {Settings{Filter: "R", ExposureSeconds: 300, Binning: 1}, 1}}},
		{"a FRAMEID recorded twice counts once",
			frameLine("a", "L", 300) + "\n" + frameLine("a", "L", 300) + "\n",
			[]Total{{Settings{Filter: "L", ExposureSeconds: 300, Binning: 1}, 1}}},
		{"a record of another kind counts nowhere",
			frameLine("a", "L", 300) + "\n" + `{"record":"grade","frameID":"a","grade":"rejected"}` + "\n",
			[]Total{{Settings{Filter: "L", ExposureSeconds: 300, Binning: 1}, 1}}},
		{"a line cut short by a crash is skipped, and the next line counts",
			frameLine("a", "L", 300) + "\n" + `{"record":"frame","frameID":"b","tar` + "\n" + frameLine("c", "L", 300) + "\n",
			[]Total{{Settings{Filter: "L", ExposureSeconds: 300, Binning: 1}, 2}}},
		{"a last line with no newline counts",
			frameLine("a", "L", 0.5),
			[]Total{{Settings{Filter: "L", ExposureSeconds: 0.5, Binning: 1}, 1}}},
		{"gain and offset tell settings apart",
			`{"record":"frame","frameID":"a","exposureSeconds":1,"binning":1,"gain":100}` + "\n" +
				`{"record":"frame","frameID":"b","exposureSeconds":1,"binning":1}` + "\n",
			[]Total{{Settings{ExposureSeconds: 1, Binning: 1}, 1}, {Settings{ExposureSeconds: 1, Binning: 1, Gain: number(100)}, 1}}},
		{"an absent offset sorts first, whichever line comes first",
			`{"record":"frame","frameID":"a","exposureSeconds":1,"binning":1}` + "\n" +
				`{"record":"frame","frameID":"b","exposureSeconds":1,"binning":1,"offset":10}` + "\n" +
				`{"record":"frame","frameID":"c","exposureSeconds":1,"binning":1,"offset":5}` + "\n",
			[]Total{{Settings{ExposureSeconds: 1, Binning: 1}, 1}, {Settings{ExposureSeconds: 1, Binning: 1, Offset: number(5)}, 1},
				{Settings{ExposureSeconds: 1, Binning: 1, Offset: number(10)}, 1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			read, err := Read(strings.NewReader(c.file))
			if err != nil {
				t.Fatalf("reading the catalog: %v", err)
			}
			if got := read.Totals(); !reflect.DeepEqual(got, c.want) {
				t.Errorf("the totals are %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestReadFindsAFrameByItsIdentity(t *testing.T) {
	read, err := Read(strings.NewReader(frameLine("a", "L", 300) + "\n"))
	if err != nil {
		t.Fatalf("reading the catalog: %v", err)
	}
	line, found := read.Frame("a")
	_, other := read.Frame("b")
	if !found || line.Path != "a.fits" || other {
		t.Errorf("Frame(\"a\") = %+v, found %v, want found with path a.fits; Frame(\"b\") found %v, want not found", line, found, other)
	}
}

func TestReadFileOfACatalogThatDoesNotExistIsEmpty(t *testing.T) {
	read, err := ReadFile(filepath.Join(t.TempDir(), "catalog", "m42.jsonl"))
	if err != nil || len(read.Totals()) != 0 {
		t.Errorf("ReadFile of a missing file has totals %+v and error %v, want no totals and no error", read.Totals(), err)
	}
}

func TestReadFileFailsOnADirectory(t *testing.T) {
	if _, err := ReadFile(t.TempDir()); err == nil {
		t.Error("ReadFile of a directory returned no error, want one")
	}
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("creating the directory of %s: %v", name, err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(content)
}
