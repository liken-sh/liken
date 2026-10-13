package catalog

// The reader rebuilds the totals from the whole file, so a target's
// archived counts survive a restart of the operator, a lost report, and
// a wiped status.

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// frameLine is one catalog line of a frame, in the wire form.
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
				t.Fatal(err)
			}
			if got := read.Totals(); !reflect.DeepEqual(got, c.want) {
				t.Errorf("totals %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestReadFindsAFrameByItsIdentity(t *testing.T) {
	read, err := Read(strings.NewReader(frameLine("a", "L", 300) + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	line, found := read.Frame("a")
	_, other := read.Frame("b")
	if !found || line.Path != "a.fits" || other {
		t.Errorf("Frame(a) = %+v, %v; Frame(b) found %v", line, found, other)
	}
}

func TestReadFileOfACatalogThatDoesNotExistIsEmpty(t *testing.T) {
	read, err := ReadFile(filepath.Join(t.TempDir(), "catalog", "m42.jsonl"))
	if err != nil || len(read.Totals()) != 0 {
		t.Errorf("ReadFile = %+v, %v", read.Totals(), err)
	}
}

func TestReadFileFailsOnADirectory(t *testing.T) {
	if _, err := ReadFile(t.TempDir()); err == nil {
		t.Error("ReadFile of a directory answered no error")
	}
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
