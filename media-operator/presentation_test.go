package main

import "testing"

// parsePresentations reads the JSON array of presentation blocks, one
// per playlist item, that the operator writes into the pod's
// environment. A missing value or one that is not a JSON array gives no
// blocks.
func TestParsePresentations(t *testing.T) {
	cases := []struct {
		name   string
		value  string
		blocks []string
	}{
		{name: "two blocks", value: `[{"title":"One"},{"title":"Two"}]`, blocks: []string{`{"title":"One"}`, `{"title":"Two"}`}},
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
