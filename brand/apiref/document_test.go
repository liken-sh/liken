package main

import (
	"strings"
	"testing"
)

// The keys are out of alphabetical order, so a reader that decodes
// into a map and sorts them fails this test.
func TestParseKeepsTheDocumentOrder(t *testing.T) {
	parsed, err := parse([]byte(`{"zebra":1,"apple":2,"mango":3}`))
	if err != nil {
		t.Fatalf("parsing the document: %v", err)
	}
	var names []string
	parsed.each(func(name string, _ *node) {
		names = append(names, name)
	})
	if got := strings.Join(names, ","); got != "zebra,apple,mango" {
		t.Errorf("the key order is %q, want %q", got, "zebra,apple,mango")
	}
}

func TestParseReadsEveryScalar(t *testing.T) {
	parsed, err := parse([]byte(`{"text":"a","number":3.25,"large":12345678901234567890,` +
		`"yes":true,"no":false,"nothing":null,"list":["a","b"]}`))
	if err != nil {
		t.Fatalf("parsing the document: %v", err)
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
		t.Errorf("the list has %d values, want 2", got)
	}
}

// A lookup on a missing key or on a node of the wrong type returns an
// empty value and does not fail, so lookups can chain.
func TestLookupsChainThroughWhatIsNotThere(t *testing.T) {
	parsed, err := parse([]byte(`{"a":{"b":"c"},"list":[1],"text":"x"}`))
	if err != nil {
		t.Fatalf("parsing the document: %v", err)
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
			t.Errorf("%s returned %q, want empty", tc.name, tc.got)
		}
	}
	if got := len(parsed.member("a").elements()); got != 0 {
		t.Errorf("an object has %d elements, want 0", got)
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
				t.Errorf("parse accepted %q, want an error", tc.document)
			}
		})
	}
}
