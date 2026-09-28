package main

// These tests cover the presentation blocks: the operator bakes one per
// item into the pod, and the command sidecar reads them back and forwards each
// one to the display as the playlist reaches its item.

import "testing"

// The presentation blocks arrive as one baked variable. A value the
// sidecar cannot read leaves no blocks, so every item forwards the empty object
// and the display falls back to the file name.
func TestParsePresentations(t *testing.T) {
	cases := []struct {
		name   string
		value  string
		blocks []string
	}{
		{name: "two baked blocks", value: `[{"title":"One"},{"title":"Two"}]`, blocks: []string{`{"title":"One"}`, `{"title":"Two"}`}},
		{name: "an empty array", value: `[]`, blocks: []string{}},
		{name: "the variable is unset"},
		{name: "the value is not JSON", value: `{`},
		{name: "the value is not an array", value: `{"title":"One"}`},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			blocks := parsePresentations(each.value)
			mustMatch(t, len(blocks), len(each.blocks))
			for index, block := range blocks {
				mustMatch(t, string(block), each.blocks[index])
			}
		})
	}
}
