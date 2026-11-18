package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// fixtureReport renders the report for the fixture Go profile and the
// fixture Cobertura report, in that order.
func fixtureReport(t *testing.T) string {
	t.Helper()
	var sources []*Source
	for _, input := range []string{"testdata/go.out", "testdata/cobertura.xml"} {
		source, err := Read(input, tree)
		if err != nil {
			t.Fatalf("reading %s: %v", input, err)
		}
		sources = append(sources, source)
	}
	page, err := Render("widget", sources)
	if err != nil {
		t.Fatalf("rendering the report: %v", err)
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

// The summary shows each input's label, unit, and totals. Go counts
// statements and Cobertura counts lines, so the unit appears beside
// every number.
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

// Every file name in a table links to that file's source block on the
// same page.
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
			t.Errorf("the page links to #%s, which is the id of no element on it", match[1])
		}
	}
}

// A line that the coverage data does not mention, such as a comment,
// has only the line class.
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
		t.Errorf("the block does not contain widget.go's text:\n%s", widget)
	}
	if classOfLine(t, widget, 10) != "line" {
		t.Errorf("the block ends before widget.go does:\n%s", widget)
	}
}

// A fully covered file stays on the page, in a details element that
// starts closed.
func TestRenderCollapsesAFullyCoveredFile(t *testing.T) {
	page := fixtureReport(t)
	if !strings.Contains(page, `<details class="file" id="f0-count-go">`) {
		t.Error("the fully covered file is not collapsed")
	}
	if !strings.Contains(page, `<details class="file" id="f0-widget-go" open>`) {
		t.Error("the file with uncovered lines is not open")
	}
}

// The report must render with no other file, so it has no script,
// linked stylesheet, image, or external URL.
func TestRenderIsSelfContained(t *testing.T) {
	page := fixtureReport(t)
	for _, forbidden := range []string{"<script", "<link", "<img", "url(http"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("the page contains %q, so the report needs a second file", forbidden)
		}
	}
	if !strings.Contains(page, "--link:") {
		t.Error("the page does not inline the shared stylesheet")
	}
}

func TestRenderEscapesTheSource(t *testing.T) {
	page := fixtureReport(t)
	if strings.Contains(page, `println("negative")`) {
		t.Error("the page contains the source text unescaped")
	}
}

// blockOf returns the part of the page that holds the source block
// with the given id.
func blockOf(t *testing.T, page, id string) string {
	t.Helper()
	start := strings.Index(page, `id="`+id+`"`)
	if start < 0 {
		t.Fatalf("the page has no block with the id %s", id)
	}
	end := strings.Index(page[start:], "</details>")
	if end < 0 {
		t.Fatalf("the block with id %s never ends", id)
	}
	return page[start : start+end]
}

var lineSpan = regexp.MustCompile(`<span class="([^"]+)"><span class="number">\s*(\d+)</span>`)

// classOfLine returns the CSS class of the numbered line in a source
// block.
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
