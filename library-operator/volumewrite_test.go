package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAWriteLandsAndLeavesNoTemporary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "movie.nfo")

	if err := newVolumeWriter("movies-enrich").write(target, []byte("<movie></movie>")); err != nil {
		t.Fatal(err)
	}

	if got := readFileString(t, target); got != "<movie></movie>" {
		t.Errorf("wrote %q, want the document", got)
	}
	if left := namesIn(t, dir); len(left) != 1 || left[0] != "movie.nfo" {
		t.Errorf("the directory holds %v, want the target alone", left)
	}
}

func TestAWriteReplacesAFileThatIsAlreadyThere(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "probe.yaml")
	writeFile(t, target, "attempts: []\n")

	if err := newVolumeWriter("movies-enrich").write(target, []byte("attempts:\n- path: a\n")); err != nil {
		t.Fatal(err)
	}

	if got := readFileString(t, target); got != "attempts:\n- path: a\n" {
		t.Errorf("wrote %q, want the second document", got)
	}
}

func TestAWriteIntoADirectoryCreatesTheDirectory(t *testing.T) {
	dir := t.TempDir()

	if err := newVolumeWriter("movies-enrich").writeInto(filepath.Join(dir, likenDirectory), "probe.yaml", []byte("x")); err != nil {
		t.Fatal(err)
	}

	if got := readFileString(t, filepath.Join(dir, likenDirectory, "probe.yaml")); got != "x" {
		t.Errorf("wrote %q, want the document", got)
	}
}

func TestAWriteFailsWhereTheDirectoryDoesNotExist(t *testing.T) {
	target := filepath.Join(t.TempDir(), "missing", "movie.nfo")

	if err := newVolumeWriter("movies-enrich").write(target, []byte("<movie/>")); err == nil {
		t.Error("the write reported no error, want one")
	}
}

func TestOnlyANameWithTheTemporaryMarkIsRemoved(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		removed bool
	}{
		{name: "a temporary this package made", file: "movie.nfo" + likenTempMark + "movies-enrich", removed: true},
		{name: "the sidecar itself", file: "movie.nfo", removed: false},
		{name: "a video file", file: "The Thing (1982).mkv", removed: false},
		{name: "a name that only looks like a temporary", file: "liken-tmp-movies-enrich", removed: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, test.file)
			writeFile(t, path, "held")

			err := newVolumeWriter("movies-enrich").removeTemporary(path)

			if (err == nil) != test.removed {
				t.Fatalf("remove err = %v, want removed = %v", err, test.removed)
			}
			if _, statErr := os.Stat(path); (statErr == nil) == test.removed {
				t.Errorf("the file exists = %v, want %v", statErr == nil, !test.removed)
			}
		})
	}
}

// The document under the edit tests. It carries elements no reader in this
// repository knows, because those are the bytes an edit must not move.
const nfoWithUnknownElements = `<?xml version="1.0" encoding="utf-8"?>
<movie>
  <title>The Thing</title>
  <lockdata>false</lockdata>
  <criticrating>84</criticrating>
  <uniqueid type="imdb">tt0084787</uniqueid>
  <art>
    <poster>/volume/poster.jpg</poster>
  </art>
</movie>
`

func TestAnEditLeavesEveryOtherByteAsItWas(t *testing.T) {
	inserted := `<uniqueid type="tmdb" default="true">1091</uniqueid>`

	edited, err := editElement([]byte(nfoWithUnknownElements), xmlElement{name: "uniqueid", attribute: "type", value: "tmdb"}, []byte(inserted))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(edited), inserted) {
		t.Fatalf("the edit did not insert the element:\n%s", edited)
	}
	rest := strings.Replace(string(edited), "  "+inserted+"\n", "", 1)
	if rest != nfoWithUnknownElements {
		t.Errorf("the edit moved other bytes:\n%s", rest)
	}
}

func TestAnEditReplacesTheElementItNames(t *testing.T) {
	cases := []struct {
		name     string
		document string
		element  xmlElement
		want     string
	}{
		{
			name:     "an element the document already holds",
			document: "<movie>\n  <fileinfo>old</fileinfo>\n</movie>\n",
			element:  xmlElement{name: "fileinfo"},
			want:     "<movie>\n  <NEW/>\n</movie>\n",
		},
		{
			name:     "a self-closing element",
			document: "<movie>\n  <fileinfo/>\n</movie>\n",
			element:  xmlElement{name: "fileinfo"},
			want:     "<movie>\n  <NEW/>\n</movie>\n",
		},
		{
			name:     "an element the document does not hold",
			document: "<movie>\n  <title>X</title>\n</movie>\n",
			element:  xmlElement{name: "fileinfo"},
			want:     "<movie>\n  <title>X</title>\n  <NEW/>\n</movie>\n",
		},
		{
			name:     "a document with no whitespace at all",
			document: "<movie><title>X</title></movie>",
			element:  xmlElement{name: "fileinfo"},
			want:     "<movie><title>X</title><NEW/></movie>",
		},
		{
			name:     "the uniqueid of one provider beside another's",
			document: "<movie>\n  <uniqueid type=\"imdb\">tt1</uniqueid>\n  <uniqueid type=\"tmdb\">1</uniqueid>\n</movie>\n",
			element:  xmlElement{name: "uniqueid", attribute: "type", value: "tmdb"},
			want:     "<movie>\n  <uniqueid type=\"imdb\">tt1</uniqueid>\n  <NEW/>\n</movie>\n",
		},
		{
			name:     "a uniqueid no provider in the document carries",
			document: "<movie>\n  <uniqueid type=\"imdb\">tt1</uniqueid>\n</movie>\n",
			element:  xmlElement{name: "uniqueid", attribute: "type", value: "tmdb"},
			want:     "<movie>\n  <uniqueid type=\"imdb\">tt1</uniqueid>\n  <NEW/>\n</movie>\n",
		},
		{
			name:     "an element nested deeper than the root's children",
			document: "<movie>\n  <art>\n    <fileinfo>deep</fileinfo>\n  </art>\n</movie>\n",
			element:  xmlElement{name: "fileinfo"},
			want:     "<movie>\n  <art>\n    <fileinfo>deep</fileinfo>\n  </art>\n  <NEW/>\n</movie>\n",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			edited, err := editElement([]byte(test.document), test.element, []byte("<NEW/>"))
			if err != nil {
				t.Fatal(err)
			}
			if string(edited) != test.want {
				t.Errorf("edited to\n%q\nwant\n%q", edited, test.want)
			}
		})
	}
}

func TestAnEditRefusesADocumentItCannotRead(t *testing.T) {
	cases := []struct {
		name     string
		document string
	}{
		{name: "a document that is not XML", document: "this is not xml <<<"},
		{name: "a document with no root element", document: "<?xml version=\"1.0\"?>\n"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := editElement([]byte(test.document), xmlElement{name: "fileinfo"}, []byte("<NEW/>")); err == nil {
				t.Error("the edit reported no error, want one")
			}
		})
	}
}

// The calls that would let an enricher lose a file on the volume, which only
// the write door may make.
var forbiddenVolumeCalls = []string{"os.Remove(", "os.RemoveAll(", "os.Truncate(", "os.Rename("}

func TestOnlyTheWritePackageRemovesRenamesOrTruncates(t *testing.T) {
	if found := forbiddenCallsInPackage(t); len(found) > 0 {
		t.Errorf("found %v, want every one of them in volumewrite.go alone", found)
	}
}

// forbiddenCallsInPackage reads every file the rule covers and names each
// forbidden call it carries, so the test itself stays one assertion.
func forbiddenCallsInPackage(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "volumewrite.go" {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range forbiddenVolumeCalls {
			if strings.Contains(string(source), call) {
				found = append(found, name+": "+call)
			}
		}
	}
	return found
}

// The two read helpers the enrichment tests share. They fail the test instead
// of handing back an error every case would have to answer.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func namesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// The block the probe marshals already carries the indentation of its own
// first line, so the edit indents it once and never twice.
func TestAnInsertedBlockTakesTheSiblingIndentationOnce(t *testing.T) {
	document := "<episodedetails>\n  <title>Breakage</title>\n</episodedetails>\n"
	replacement := "  <fileinfo>\n    <streamdetails></streamdetails>\n  </fileinfo>"

	edited, err := editElement([]byte(document), xmlElement{name: "fileinfo"}, []byte(replacement))
	if err != nil {
		t.Fatal(err)
	}

	want := "<episodedetails>\n  <title>Breakage</title>\n" +
		"  <fileinfo>\n    <streamdetails></streamdetails>\n  </fileinfo>\n</episodedetails>\n"
	if string(edited) != want {
		t.Errorf("edited to\n%q\nwant\n%q", edited, want)
	}
}

func TestAnEditKeepsTheByteOrderMarkTheDocumentOpensWith(t *testing.T) {
	document := byteOrderMark + "<movie>\n  <title>Solaris</title>\n</movie>\n"

	edited, err := editElement([]byte(document), xmlElement{name: "fileinfo"}, []byte("<NEW/>"))
	if err != nil {
		t.Fatal(err)
	}

	want := byteOrderMark + "<movie>\n  <title>Solaris</title>\n  <NEW/>\n</movie>\n"
	if string(edited) != want {
		t.Errorf("edited to\n%q\nwant\n%q", edited, want)
	}
}
