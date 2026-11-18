package schema

// make crosscheck downloads the page from GitHub, so no test writes the
// report again. These tests check that each line of the committed
// report names a property that a source still defines, and that the
// cross-check finds each kind of disagreement on a page built in the
// test.

import (
	"bufio"
	"bytes"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/observatory-operator/indi/standard"
)

func TestTheReportListsPropertiesThatExist(t *testing.T) {
	sets, _, _, err := loadKinds("..")
	if err != nil {
		t.Fatalf("loading the kinds: %v", err)
	}
	raw, err := os.ReadFile(ReportFile)
	if err != nil {
		t.Fatalf("reading %s: %v", ReportFile, err)
	}
	if !regexp.MustCompile(`(?m)^# indilib/docs commit: [0-9a-f]{40}$`).Match(raw) {
		t.Errorf("%s names no commit of indilib/docs", ReportFile)
	}
	defined := func(name string) bool {
		for _, set := range sets {
			if _, ok := set.Property(name); ok {
				return true
			}
		}
		return false
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || strings.Contains(line, "no source defines it") {
			continue
		}
		name, _, _ := strings.Cut(line, ":")
		name, _, _ = strings.Cut(name, ".")
		if !defined(name) {
			t.Errorf("%s names %s, which no source defines; run make crosscheck", ReportFile, name)
		}
	}
}

// page builds a standard properties page with one section of rows.
func page(section string, rows ...string) []byte {
	return []byte("## " + section + "\n\n| Name | Type | Values | Description |\n| --- | --- | --- | --- |\n" + strings.Join(rows, "\n") + "\n")
}

func TestTheCrossCheckFindsEachDisagreement(t *testing.T) {
	speed := standard.Property{Name: "FOCUS_SPEED", Type: "Number", Members: []standard.Member{{Name: "FOCUS_SPEED_VALUE"}}}
	sets := map[string]standard.Interface{"focuser": {Name: "focuser", Properties: []standard.Property{speed}}}
	focusers := []kind{{curated: curated{Kind: "Focuser", Interfaces: []string{"focuser"}}, Plural: "focusers",
		Fields: []property{{Property: speed, entry: entry{Field: "speed", Unit: "steps"}}}}}
	cases := []struct {
		name string
		page []byte
		want string
	}{
		{"another type", page("Focuser Properties", "| FOCUS_SPEED | Switch | | Speed |"),
			`FOCUS_SPEED: the standard properties page gives the type "Switch", and the capture of focuser gives "Number"`},
		{"a member that the capture lacks", page("Focuser Properties", "| FOCUS_SPEED | Number | FOCUS_SPEED_VALUE | Speed |", "| | | FOCUS_SPEED_MAX | Most |"),
			"FOCUS_SPEED: the standard properties page names the member FOCUS_SPEED_MAX, and the capture of focuser does not define it"},
		{"a member that the page lacks", page("Focuser Properties", "| FOCUS_SPEED | Number | FOCUS_SPEED_OTHER | Speed |"),
			"FOCUS_SPEED: the capture of focuser defines the member FOCUS_SPEED_VALUE, and the standard properties page does not name it"},
		{"a property that no source defines", page("Focuser Properties", "| FOCUS_GONE | Number | | Gone |"),
			"FOCUS_GONE: the standard properties page lists it for focuser, and no source defines it"},
		{"another unit", page("Focuser Properties", "| FOCUS_SPEED | Number | FOCUS_SPEED_VALUE | Speed in RPM |"),
			"FOCUS_SPEED: the standard properties page gives the unit rpm, and focusers-fields.yaml gives steps"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			report, err := crossCheck(c.page, sets, focusers)
			if err != nil {
				t.Fatalf("cross-checking the page: %v", err)
			}
			if !slices.Contains(report, c.want) {
				t.Errorf("report = %q, want it to hold %q", report, c.want)
			}
		})
	}
}

func TestTheCrossCheckReadsTheCameraRowForTheGuideHead(t *testing.T) {
	exposure := standard.Property{Name: "GUIDER_EXPOSURE", Type: "Number", Members: []standard.Member{{Name: "GUIDER_EXPOSURE_VALUE"}}}
	sets := map[string]standard.Interface{"ccd": {Name: "ccd", Properties: []standard.Property{exposure}}}
	report, err := crossCheck(page("CCD Properties", "| CCD_EXPOSURE | Number | CCD_EXPOSURE_VALUE | Expose for this many hours |"), sets,
		[]kind{{curated: curated{Interfaces: []string{"ccd"}}, Plural: "cameras", Fields: []property{{Property: exposure, entry: entry{Unit: "s"}}}}})
	if err != nil {
		t.Fatalf("cross-checking the page: %v", err)
	}
	want := "GUIDER_EXPOSURE: the standard properties page gives the unit h, and cameras-fields.yaml gives s"
	if !slices.Contains(report, want) {
		t.Errorf("report = %q, want it to hold %q", report, want)
	}
}

// The page's Markdown formatter writes DIGITAL_INPUT_* as
// DIGITAL*INPUT*\*, and continues a description that does not fit its
// cell in the next row. readDocs reverses both.
func TestTheDocsReaderUndoesTheMarkdown(t *testing.T) {
	docs, err := readDocs(page("Input Interface Properties",
		"| DIGITAL*INPUT*\\* | Switch | | Digital input |", "| | | | state |", "| | | ON | On |", "| | | | when high |"))
	if err != nil {
		t.Fatalf("reading the page: %v", err)
	}
	got := docs[0]
	if got.Name != "DIGITAL_INPUT_*" || got.Description != "Digital input state" || got.Members[0].Description != "On when high" {
		t.Errorf("the first section reads as %+v, want DIGITAL_INPUT_* described \"Digital input state\" with a member described \"On when high\"", got)
	}
}

func TestTheDocsReaderRefusesASectionWithNoInterface(t *testing.T) {
	if _, err := readDocs([]byte("## Telescope Toys\n")); err == nil {
		t.Error("readDocs returned no error for a section that names no interface, want one")
	}
}

// CrossCheck reads the committed sources, so the report has the lines
// that Load finds there, as well as the commit header and the lines
// from the page built here.
func TestTheReportHoldsTheCommitAndEverySource(t *testing.T) {
	commit := strings.Repeat("ab", 20)
	built := bytes.Join([][]byte{
		page("General Properties", "| ACTIVE_DEVICES | Text | ACTIVE_TELESCOPE | The mount |", "| FITS_HEADER | Text | KEYWORD_NAME | Name |"),
		page("CCD Properties", "| CCD_INFO | Number | | Sensor |", "| | | CCD_PIXEL_SIZE | Pixel size in microns |", "| | | CCD_MAX_X | Width in mm |"),
	}, nil)
	report, err := CrossCheck("..", built, commit)
	if err != nil {
		t.Fatalf("cross-checking the page: %v", err)
	}
	for _, want := range []string{
		"# indilib/docs commit: " + commit,
		`FOCUS_TEMPERATURE: conventions.yaml gives the permission "ro", and the focus simulator gives "rw"`,
		"CCD_INFO.CCD_MAX_X: the standard properties page gives the unit mm, and cameras-fields.yaml gives px",
	} {
		if !bytes.Contains(report, []byte(want)) {
			t.Errorf("the report holds no line %q", want)
		}
	}
}
