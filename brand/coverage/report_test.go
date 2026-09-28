package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The report the tests read: the fixture module and the fixture Rust
// file, in the order the command line would give them.
func fixtureReport(t *testing.T) string {
	t.Helper()
	var sources []*Source
	for _, input := range []string{"testdata/go.out", "testdata/cobertura.xml"} {
		source, err := Read(input, tree)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, source)
	}
	page, err := Render("widget", sources)
	if err != nil {
		t.Fatal(err)
	}
	return string(page)
}

func TestRenderNamesTheReport(t *testing.T) {
	page := fixtureReport(t)
	for _, want := range []string{"<title>widget</title>", "<h1>widget</h1>"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
}

// The summary says what each input is, in which unit it counts, and
// what it totals. Go counts statements and Cobertura counts lines, so
// the unit belongs beside every number.
func TestRenderSummarizesEveryInput(t *testing.T) {
	page := fixtureReport(t)
	for _, want := range []string{
		"Go (example.com/widget)",
		"cobertura.xml",
		"statements",
		"lines",
		"60.0%",
		"75.0%",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the summary is missing %q", want)
		}
	}
}

func TestRenderCountsEveryFile(t *testing.T) {
	page := fixtureReport(t)
	for _, want := range []string{"widget.go", "count.go", "spin/src/spin.rs", "50.0%", "100.0%"} {
		if !strings.Contains(page, want) {
			t.Errorf("the file tables are missing %q", want)
		}
	}
}

// Every file name in a table is a link to that file's own source
// block further down the one page, so a reader lands on the code
// instead of scrolling for it.
func TestRenderLinksEveryNameToItsSource(t *testing.T) {
	page := fixtureReport(t)
	ids := map[string]bool{}
	for _, match := range regexp.MustCompile(`id="([^"]+)"`).FindAllStringSubmatch(page, -1) {
		ids[match[1]] = true
	}
	links := regexp.MustCompile(`href="#([^"]+)"`).FindAllStringSubmatch(page, -1)
	if len(links) < 3 {
		t.Fatalf("the page has %d links, want one for each of the three files", len(links))
	}
	for _, match := range links {
		if !ids[match[1]] {
			t.Errorf("the link to #%s lands on nothing", match[1])
		}
	}
}

// The source block colors the code the tests reached and the code
// they missed. Everything else, such as a comment, stays plain.
func TestRenderColorsTheLines(t *testing.T) {
	widget := blockOf(t, fixtureReport(t), "f0-widget-go")
	for _, tc := range []struct {
		line int
		want string
	}{
		{3, "line"},
		{4, "line covered"},
		{5, "line covered"},
		{6, "line uncovered"},
		{9, "line covered"},
	} {
		if got := classOfLine(t, widget, tc.line); got != tc.want {
			t.Errorf("line %d has class %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestRenderNumbersTheLines(t *testing.T) {
	widget := blockOf(t, fixtureReport(t), "f0-widget-go")
	if !strings.Contains(widget, "func Add(a, b int) int {") {
		t.Errorf("the block does not hold widget.go's text:\n%s", widget)
	}
	if classOfLine(t, widget, 10) != "line" {
		t.Errorf("the block ends before widget.go does:\n%s", widget)
	}
}

// A file with nothing missing is still on the page, because a
// reader looking for a file must find it. It opens closed, so the
// files that need reading are the ones on show.
func TestRenderCollapsesAFullyCoveredFile(t *testing.T) {
	page := fixtureReport(t)
	if !strings.Contains(page, `<details class="file" id="f0-count-go">`) {
		t.Error("the fully covered file is not collapsed")
	}
	if !strings.Contains(page, `<details class="file" id="f0-widget-go" open>`) {
		t.Error("the file with uncovered lines is not open")
	}
}

// The report is one file. A site publishes it and a release ships
// it, so it must draw itself with nothing else beside it.
func TestRenderIsSelfContained(t *testing.T) {
	page := fixtureReport(t)
	for _, forbidden := range []string{"<script", "<link", "<img", "url(http"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("the page holds %q, so it asks for a second file", forbidden)
		}
	}
	if !strings.Contains(page, "--link:") {
		t.Error("the page does not inline the shared stylesheet")
	}
}

func TestRenderEscapesTheSource(t *testing.T) {
	page := fixtureReport(t)
	if strings.Contains(page, `println("negative")`) {
		t.Error("the source text lands in the page unescaped")
	}
}

// blockOf cuts one file's source block out of the page, so a test
// reads the lines of the file it names and not of another.
func blockOf(t *testing.T, page, id string) string {
	t.Helper()
	start := strings.Index(page, `id="`+id+`"`)
	if start < 0 {
		t.Fatalf("the page has no block with id %s", id)
	}
	end := strings.Index(page[start:], "</details>")
	if end < 0 {
		t.Fatalf("the block with id %s never ends", id)
	}
	return page[start : start+end]
}

var lineSpan = regexp.MustCompile(`<span class="([^"]+)"><span class="number">\s*(\d+)</span>`)

// classOfLine reports how one numbered line of a source block is
// colored.
func classOfLine(t *testing.T, block string, number int) string {
	t.Helper()
	for _, match := range lineSpan.FindAllStringSubmatch(block, -1) {
		if match[2] == strconv.Itoa(number) {
			return match[1]
		}
	}
	t.Fatalf("the block has no line %d:\n%s", number, block)
	return ""
}
