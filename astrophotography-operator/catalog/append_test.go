package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

// After a crash leaves a last line with no newline, the next append
// still writes a line that reads back.
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
				t.Fatalf("appending the line: %v", err)
			}
			read, err := ReadFile(file)
			if err != nil {
				t.Fatalf("reading the catalog: %v", err)
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
		t.Error("Append returned no error, want one because the directory cannot be made")
	}
}

// A catalog that the Job cannot open is an error and not an empty
// catalog, because the Job would then record every frame again.
func TestACatalogThatCannotBeOpenedIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads and writes through every permission")
	}
	file := filepath.Join(t.TempDir(), "m42.jsonl")
	writeFile(t, file, frameLine("a", "L", 300)+"\n")
	if err := os.Chmod(file, 0); err != nil {
		t.Fatalf("removing the permissions of the catalog: %v", err)
	}
	if _, err := ReadFile(file); err == nil {
		t.Error("ReadFile returned no error, want one for a file with no permissions")
	}
	if err := Append(file, Line{}); err == nil {
		t.Error("Append returned no error, want one for a file with no permissions")
	}
}

func TestSyncDirOfADirectoryThatIsGoneIsAnError(t *testing.T) {
	if err := SyncDir(filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Error("SyncDir returned no error, want one for a directory that does not exist")
	}
}
