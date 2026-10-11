package capture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteTranscriptsWritesOneFileForEachPhase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "focus")
	replies := []Reply{{"baseline", []byte("<a/>")}, {"move", []byte("<b/>")}}

	if err := WriteTranscripts(dir, replies); err != nil {
		t.Fatal(err)
	}

	for _, r := range replies {
		data, err := os.ReadFile(filepath.Join(dir, r.Phase+".xml"))
		if err != nil || string(data) != string(r.Data) {
			t.Errorf("%s.xml = %q, %v, want %q", r.Phase, data, err, r.Data)
		}
	}
}

func bare(name string) Bare { return Bare{Name: name, Driver: "indi_bare_" + name} }

func defs(names ...string) []Reply {
	var data []byte
	for _, name := range names {
		data = append(data, vector(name, "1", "A")...)
	}
	return []Reply{{"baseline", data}}
}

func readFile(t *testing.T, dir, name string) Capture {
	t.Helper()
	file, err := os.Open(filepath.Join(dir, name+".xml"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	c, err := Read(file)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A capture of an interface leaves out what the capture of
// DefaultDevice in the same directory holds.
func TestWriteCaptureLeavesOutTheDefaultDevice(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteCapture(dir, bare(DefaultDevice), "2.2.5", "Bare Device", defs("CONNECTION", "DEBUG")); err != nil {
		t.Fatal(err)
	}

	kept, err := WriteCapture(dir, bare("dustcap"), "2.2.5", "Bare Dust Cap", defs("CONNECTION", "CAP_PARK", "DEBUG"))
	if err != nil {
		t.Fatal(err)
	}

	c := readFile(t, dir, "dustcap")
	if kept != 1 || !c.Defines("CAP_PARK") || c.Defines("CONNECTION") || c.Defines("DEBUG") {
		t.Errorf("kept %d, and the capture is %+v; want CAP_PARK alone", kept, c)
	}
	want := Header{Driver: "indi_bare_dustcap", Device: "Bare Dust Cap", INDI: "2.2.5"}
	if c.Header != want {
		t.Errorf("header = %+v, want %+v", c.Header, want)
	}
	if device := readFile(t, dir, DefaultDevice); !device.Defines("CONNECTION") || !device.Defines("DEBUG") {
		t.Errorf("the capture of DefaultDevice is %+v, want CONNECTION and DEBUG", device)
	}
}

func TestWriteCaptureFails(t *testing.T) {
	cases := []struct {
		name    string
		bare    Bare
		replies []Reply
	}{
		{"no capture of DefaultDevice", bare("dustcap"), defs("CAP_PARK")},
		{"a reply that ends inside a tag", bare(DefaultDevice), []Reply{{"baseline", []byte(`<defSwitchVector name="A`)}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := WriteCapture(t.TempDir(), c.bare, "2.2.5", "Bare", c.replies); err == nil {
				t.Error("WriteCapture succeeded")
			}
		})
	}
}
