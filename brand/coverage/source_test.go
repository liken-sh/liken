package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture tree under testdata is a two-file Go module and one
// Rust file, with a profile and a Cobertura report that describe
// them. Every number below is countable by hand from those files.
const tree = "testdata/tree"

func TestReadGoProfileCountsStatements(t *testing.T) {
	source, err := Read("testdata/go.out", tree)
	if err != nil {
		t.Fatal(err)
	}
	if source.Label != "Go (example.com/widget)" {
		t.Errorf("label is %q, want the module path", source.Label)
	}
	if source.Unit != "statements" {
		t.Errorf("unit is %q, want statements", source.Unit)
	}
	if source.Covered != 3 || source.Total != 5 {
		t.Errorf("totals are %d/%d, want 3/5", source.Covered, source.Total)
	}
}

func TestReadGoProfileCountsEachFile(t *testing.T) {
	source, err := Read("testdata/go.out", tree)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		covered int
		total   int
	}{
		{"count.go", 1, 1},
		{"widget.go", 2, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := fileNamed(t, source, tc.name)
			if file.Covered != tc.covered || file.Total != tc.total {
				t.Errorf("%s is %d/%d, want %d/%d", tc.name, file.Covered, file.Total, tc.covered, tc.total)
			}
		})
	}
}

// A profile row names a block, not a line, and two blocks meet on
// one line: the line that opens a branch belongs to the code before
// it and to the branch itself. Such a line counts as covered, because
// the test did reach it. Only the lines inside a block that never
// ran are uncovered.
func TestReadGoProfileMarksLines(t *testing.T) {
	source, err := Read("testdata/go.out", tree)
	if err != nil {
		t.Fatal(err)
	}
	file := fileNamed(t, source, "widget.go")
	for _, tc := range []struct {
		line int
		want Mark
	}{
		{3, Unmarked},
		{4, Covered},
		{5, Covered},
		{6, Uncovered},
		{7, Uncovered},
		{8, Uncovered},
		{9, Covered},
		{10, Unmarked},
	} {
		if got := file.Marks[tc.line]; got != tc.want {
			t.Errorf("line %d is %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestReadGoProfileTakesTheSourceText(t *testing.T) {
	source, err := Read("testdata/go.out", tree)
	if err != nil {
		t.Fatal(err)
	}
	file := fileNamed(t, source, "widget.go")
	if !strings.Contains(file.Text, "func Add(a, b int) int {") {
		t.Errorf("widget.go's text is not the file on disk:\n%s", file.Text)
	}
}

func TestReadCoberturaCountsLines(t *testing.T) {
	source, err := Read("testdata/cobertura.xml", tree)
	if err != nil {
		t.Fatal(err)
	}
	if source.Label != "cobertura.xml" {
		t.Errorf("label is %q, want the input's name", source.Label)
	}
	if source.Unit != "lines" {
		t.Errorf("unit is %q, want lines", source.Unit)
	}
	if source.Covered != 3 || source.Total != 4 {
		t.Errorf("totals are %d/%d, want 3/4", source.Covered, source.Total)
	}
	file := fileNamed(t, source, "spin/src/spin.rs")
	if file.Covered != 3 || file.Total != 4 {
		t.Errorf("spin.rs is %d/%d, want 3/4", file.Covered, file.Total)
	}
	if !strings.Contains(file.Text, "pub fn spin(n: u32) -> u32 {") {
		t.Errorf("spin.rs's text is not the file on disk:\n%s", file.Text)
	}
}

func TestReadCoberturaMarksLines(t *testing.T) {
	source, err := Read("testdata/cobertura.xml", tree)
	if err != nil {
		t.Fatal(err)
	}
	file := fileNamed(t, source, "spin/src/spin.rs")
	for _, tc := range []struct {
		line int
		want Mark
	}{
		{1, Covered},
		{2, Covered},
		{3, Uncovered},
		{4, Unmarked},
		{5, Covered},
	} {
		if got := file.Marks[tc.line]; got != tc.want {
			t.Errorf("line %d is %v, want %v", tc.line, got, tc.want)
		}
	}
}

// The XML may come from another machine, where the tree stood at
// another path. The root wins whenever it holds the file, so a report
// built here reads the sources here.
func TestReadCoberturaFallsBackToTheSourceElement(t *testing.T) {
	absolute, err := filepath.Abs(tree)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	source, err := Read(coberturaFrom(t, absolute), elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	file := fileNamed(t, source, "spin/src/spin.rs")
	if !strings.Contains(file.Text, "pub fn spin") {
		t.Errorf("the source element did not find spin.rs:\n%s", file.Text)
	}
}

// A file that neither the root nor the source element holds still
// gets its row, because its numbers are true even where its text is
// missing.
func TestReadKeepsAFileWithNoTextOnDisk(t *testing.T) {
	source, err := Read("testdata/cobertura.xml", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := fileNamed(t, source, "spin/src/spin.rs")
	if file.Text != "" {
		t.Errorf("spin.rs has text from nowhere:\n%s", file.Text)
	}
	if file.Covered != 3 || file.Total != 4 {
		t.Errorf("spin.rs is %d/%d, want 3/4", file.Covered, file.Total)
	}
}

func TestReadRefusesAnInputOfNeitherKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("coverage was fine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path, tree); err == nil {
		t.Error("a file that is neither a profile nor a Cobertura report must be refused")
	}
}

// An input of the right kind can still be damaged, by a run that
// stopped halfway or by a merge of two files. The report refuses it
// rather than counting a part of it.
func TestReadRefusesADamagedInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"a profile row that is not a block", "mode: set\nexample.com/widget/widget.go:4.24\n"},
		{"a report that is not whole", `<coverage lines-covered="1" lines-valid="2">`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Read(path, tree); err == nil {
				t.Error("a damaged input must be refused")
			}
		})
	}
}

func TestReadRefusesAMissingInput(t *testing.T) {
	if _, err := Read(filepath.Join(t.TempDir(), "gone.out"), tree); err == nil {
		t.Error("a missing input must be refused")
	}
}

func TestReadRefusesATreeWithNoModuleFile(t *testing.T) {
	if _, err := Read("testdata/go.out", t.TempDir()); err == nil {
		t.Error("a Go profile against a tree with no go.mod must be refused")
	}
}

// fileNamed finds one file's row, so each test above reads as the
// number it checks.
func fileNamed(t *testing.T, source *Source, name string) File {
	t.Helper()
	for _, file := range source.Files {
		if file.Name == name {
			return file
		}
	}
	t.Fatalf("%s holds no file named %s", source.Label, name)
	return File{}
}

// coberturaFrom writes a report whose <source> is the given
// directory, so a test can put the tree where only that element
// names it.
func coberturaFrom(t *testing.T, source string) string {
	t.Helper()
	report, err := os.ReadFile("testdata/cobertura.xml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cobertura.xml")
	moved := strings.Replace(string(report), "/nowhere/tree", source, 1)
	if err := os.WriteFile(path, []byte(moved), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
