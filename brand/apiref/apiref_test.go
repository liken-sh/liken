package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/liken-sh/brand/linkcheck"
)

// The golden test is the contract: one small document that exercises
// every shape the walker supports (an operation with no parameters, a
// path parameter an operation overrides, several media types under
// one status, an enum, a reference chain, a request body, response
// headers, a link relation, a nested object, a map, a type that may
// be null, and a pipe that must be escaped), and the exact page it
// must produce.
func TestGenerateMatchesGolden(t *testing.T) {
	got, err := Generate(read(t, "testdata/sample-openapi.json"), "testdata/sample-openapi.json", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(read(t, "testdata/sample.md")) {
		t.Errorf("generated page does not match testdata/sample.md:\n%s", string(got))
	}
}

// A preamble is hand-written prose the document cannot hold, such as
// a link to the narrative page beside this one. It lands verbatim
// between the generated-from comment and the document's own summary.
func TestGenerateInsertsThePreamble(t *testing.T) {
	got, err := Generate(read(t, "testdata/sample-openapi.json"), "testdata/sample-openapi.json",
		Options{Preamble: read(t, "testdata/sample-preamble.md")})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(read(t, "testdata/sample-with-preamble.md")) {
		t.Errorf("generated page does not match testdata/sample-with-preamble.md:\n%s", string(got))
	}
}

// A postamble is the other half of the preamble: hand-written
// sections that belong under the generated tables, such as where to
// fetch the document itself.
func TestGenerateAppendsThePostamble(t *testing.T) {
	got, err := Generate(read(t, "testdata/sample-openapi.json"), "testdata/sample-openapi.json",
		Options{Postamble: read(t, "testdata/sample-postamble.md")})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(read(t, "testdata/sample-with-postamble.md")) {
		t.Errorf("generated page does not match testdata/sample-with-postamble.md:\n%s", string(got))
	}
}

// The front matter title and weight place the page among the pages a
// repository already has, so both are settable. The title falls back
// to the name the document gives itself.
func TestGenerateTitlesAndWeightsThePage(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{"defaults", Options{}, "title: widget-api\nweight: 10\n"},
		{"title", Options{Title: "Routes"}, "title: Routes\nweight: 10\n"},
		{"weight", Options{Weight: 55}, "title: widget-api\nweight: 55\n"},
		{"both", Options{Title: "Routes", Weight: 55}, "title: Routes\nweight: 55\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Generate(read(t, "testdata/sample-openapi.json"), "testdata/sample-openapi.json", tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), tc.want) {
				t.Errorf("front matter is missing %q:\n%s", tc.want, string(got))
			}
		})
	}
}

// The real document from one of the operator manuals is the test for
// the whole page: it holds every route, every header, and the problem
// schema the manuals link, and it is the file the Makefile rules
// feed this program.
func TestGenerateWritesASectionForEveryOperation(t *testing.T) {
	page := generateDisplayPage(t)
	for _, heading := range []string{
		"## `GET` `/v1/display` {data-method=GET}",
		"## `HEAD` `/v1/display` {data-method=HEAD}",
		"## `OPTIONS` `/v1/display` {data-method=OPTIONS}",
		"## `GET` `/v1/display/openapi.json` {data-method=GET}",
		"## `GET` `/v1/display/displays/{name}` {data-method=GET}",
		"## `GET` `/v1/display/displays/{name}/screen` {data-method=GET}",
		"## `GET` `/v1/display/displays/{name}/screen.png` {data-method=GET}",
		"## `GET` `/v1/display/displays/{name}/screen.jpg` {data-method=GET}",
		"## `GET` `/v1/display/displays/{name}/screen.mp4` {data-method=GET}",
		"## `GET` `/v1/display/displays/{name}/screen.mjpeg` {data-method=GET}",
		"## Schemas",
		"### Problem",
		"## Security",
	} {
		if !strings.Contains(page, heading+"\n") {
			t.Errorf("the page has no %q heading", heading)
		}
	}
}

// Every table cell that names a schema links to a section of this
// same page, and the manual's link check resolves those links with
// linkcheck.Anchor. This test holds the two together: each link
// target the page writes must be an id the page renders.
func TestEveryLinkOnThePageLandsOnThisPage(t *testing.T) {
	page := generateDisplayPage(t)
	ids := map[string]bool{}
	for _, line := range strings.Split(page, "\n") {
		if heading := strings.TrimLeft(line, "#"); strings.HasPrefix(line, "#") && heading != line {
			ids[linkcheck.Anchor(strings.TrimSpace(heading))] = true
		}
	}
	for _, match := range regexp.MustCompile(`\bid="([^"]+)"`).FindAllStringSubmatch(page, -1) {
		ids[match[1]] = true
	}
	for _, match := range regexp.MustCompile(`\]\(#([^)]+)\)`).FindAllStringSubmatch(page, -1) {
		if !ids[match[1]] {
			t.Errorf("the page links #%s, which no heading and no row on it renders", match[1])
		}
	}
}

func TestGenerateRefuses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		document string
	}{
		{"an empty file", ""},
		{"text that is not JSON", "openapi: 3.1.1\n"},
		{"two documents in one file", `{"openapi":"3.1.1","paths":{"/a":{}}}{"openapi":"3.1.1"}`},
		{"a document with no openapi member", `{"paths":{"/a":{}}}`},
		{"a document with no paths", `{"openapi":"3.1.1","info":{"title":"a"}}`},
		{"a document whose paths are empty", `{"openapi":"3.1.1","paths":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Generate([]byte(tc.document), "x.json", Options{}); err == nil {
				t.Error("the document must be refused")
			}
		})
	}
}

// The Makefile rules pass the flags before the positional arguments,
// and the preamble is the third positional argument, the way crdref
// takes it.
func TestRunTakesTheFlagsBeforeThePositionalArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			"positional only",
			[]string{"testdata/sample-openapi.json", "", "testdata/sample-preamble.md"},
			"title: widget-api\nweight: 10\n",
		},
		{
			"title and weight",
			[]string{"-title", "Routes", "-weight", "55", "testdata/sample-openapi.json", ""},
			"title: Routes\nweight: 55\n",
		},
		{
			"postamble",
			[]string{"-postamble", "testdata/sample-postamble.md", "testdata/sample-openapi.json", ""},
			"## The document itself",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "page.md")
			args := append([]string(nil), tc.args...)
			for i, a := range args {
				if a == "" {
					args[i] = out
				}
			}
			if err := run(args); err != nil {
				t.Fatal(err)
			}
			page, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(page), tc.want) {
				t.Errorf("page is missing %q:\n%s", tc.want, string(page))
			}
		})
	}
}

func TestRunRefusesTheWrongArgumentCount(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"testdata/sample-openapi.json"},
		{"a", "b", "c", "d"},
	} {
		if err := run(args); err == nil {
			t.Errorf("run(%q) must be refused", args)
		}
	}
}

func TestRunRefusesAFileItCannotRead(t *testing.T) {
	for _, args := range [][]string{
		{"testdata/no-such-document.json", "out.md"},
		{"testdata/sample-openapi.json", "out.md", "testdata/no-such-preamble.md"},
		{"-postamble", "testdata/no-such-postamble.md", "testdata/sample-openapi.json", "out.md"},
	} {
		if err := run(args); err == nil {
			t.Errorf("run(%q) must be refused", args)
		}
	}
}

func TestDisplayPath(t *testing.T) {
	for path, want := range map[string]string{
		"../openapi.json":                  "openapi.json",
		"content/docs/reference/open.json": "content/docs/reference/open.json",
	} {
		if got := displayPath(path); got != want {
			t.Errorf("displayPath(%q) = %q, want %q", path, got, want)
		}
	}
}

// generateDisplayPage renders the page for display-api, whose
// document is the copy that manual publishes.
func generateDisplayPage(t *testing.T) string {
	t.Helper()
	page, err := Generate(read(t, "testdata/display-openapi.json"), "testdata/display-openapi.json", Options{})
	if err != nil {
		t.Fatal(err)
	}
	return string(page)
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
