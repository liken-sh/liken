package main

import (
	"strings"
	"testing"
)

// The page follows the document's order, so the reader reads the
// paths in the order the API's own program declares them. This is
// the test of that: a decode into a map would sort these.
func TestParseKeepsTheDocumentOrder(t *testing.T) {
	parsed, err := parse([]byte(`{"zebra":1,"apple":2,"mango":3}`))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	parsed.each(func(name string, _ *node) {
		names = append(names, name)
	})
	if got := strings.Join(names, ","); got != "zebra,apple,mango" {
		t.Errorf("got %q, want %q", got, "zebra,apple,mango")
	}
}

func TestParseReadsEveryScalar(t *testing.T) {
	parsed, err := parse([]byte(`{"text":"a","number":3.25,"large":12345678901234567890,` +
		`"yes":true,"no":false,"nothing":null,"list":["a","b"]}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"text":    "a",
		"number":  "3.25",
		"large":   "12345678901234567890",
		"yes":     "true",
		"no":      "false",
		"nothing": "null",
	} {
		if got := parsed.member(name).value(); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := len(parsed.member("list").elements()); got != 2 {
		t.Errorf("the list holds %d values, want 2", got)
	}
}

// Every lookup chains, so a missing key and a value of the wrong
// shape both answer with nothing instead of failing.
func TestLookupsChainThroughWhatIsNotThere(t *testing.T) {
	parsed, err := parse([]byte(`{"a":{"b":"c"},"list":[1],"text":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		got  string
	}{
		{"a missing key", parsed.member("nothing").member("deeper").value()},
		{"a key of a scalar", parsed.member("text").member("b").value()},
		{"a key of an array", parsed.member("list").member("b").value()},
		{"the text of an object", parsed.member("a").value()},
	} {
		if tc.got != "" {
			t.Errorf("%s answered %q, want nothing", tc.name, tc.got)
		}
	}
	if got := len(parsed.member("a").elements()); got != 0 {
		t.Errorf("an object holds %d elements, want 0", got)
	}
}

func TestParseRefuses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		document string
	}{
		{"an empty file", ""},
		{"an unclosed object", `{"a":`},
		{"an unclosed array", `{"a":[1`},
		{"text that is not JSON", "openapi: 3.1.1"},
		{"a second document", `{}{}`},
		{"a trailing comma", `{"a":1,}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parse([]byte(tc.document)); err == nil {
				t.Error("the document must be refused")
			}
		})
	}
}
