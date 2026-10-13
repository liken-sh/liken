package catalog

// The writer appends one line for each frame, and a crash during an
// append never costs the next line.

import (
	"os"
	"path/filepath"
	"testing"
)

// A crash during an append can leave a last line with no newline. The
// next append starts a new line, so the line it writes reads whole.
func TestAppendStartsANewLineAfterACutLine(t *testing.T) {
	cases := []struct{ name, before string }{
		{"no catalog yet", ""},
		{"a whole line", frameLine("a", "L", 300) + "\n"},
		{"a cut line", `{"record":"frame","frameID":"a","tar`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "astro", "catalog", "m42.jsonl")
			if c.before != "" {
				writeFile(t, file, c.before)
			}
			if err := Append(file, Line{Record: RecordFrame, FrameID: "b", Settings: Settings{ExposureSeconds: 1, Binning: 1}}); err != nil {
				t.Fatal(err)
			}
			read, err := ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if _, found := read.Frame("b"); !found {
				t.Errorf("the appended line does not read back from\n%s", readFile(t, file))
			}
		})
	}
}

func TestAppendFailsWhenTheDirectoryCannotBeMade(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "astro")
	writeFile(t, blocker, "a file where the directory belongs")
	if err := Append(filepath.Join(blocker, "catalog", "m42.jsonl"), Line{}); err == nil {
		t.Error("Append answered no error")
	}
}

// A catalog that the Job cannot open is an error, never an empty
// catalog, because an empty one would record every frame again.
func TestACatalogThatCannotBeOpenedIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads and writes through every permission")
	}
	file := filepath.Join(t.TempDir(), "m42.jsonl")
	writeFile(t, file, frameLine("a", "L", 300)+"\n")
	if err := os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(file); err == nil {
		t.Error("ReadFile answered no error")
	}
	if err := Append(file, Line{}); err == nil {
		t.Error("Append answered no error")
	}
}

func TestSyncDirOfADirectoryThatIsGoneIsAnError(t *testing.T) {
	if err := SyncDir(filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Error("SyncDir answered no error")
	}
}
