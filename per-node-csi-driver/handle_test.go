package main

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTheDriverAcceptsADNSSubdomainAndRefusesEverythingElse(t *testing.T) {
	for _, c := range []struct {
		name     string
		handle   string
		accepted bool
	}{
		{name: "one word", handle: "example-store", accepted: true},
		{name: "dotted segments", handle: "example.store.one", accepted: true},
		{name: "digits alone", handle: "12345", accepted: true},
		{name: "the longest name", handle: strings.Repeat("a", 253), accepted: true},
		{name: "no name at all", handle: ""},
		{name: "one character over the limit", handle: strings.Repeat("a", 254)},
		{name: "a separator", handle: "example/store"},
		{name: "an upper-case letter", handle: "Example-store"},
		{name: "a leading dash", handle: "-example"},
		{name: "a trailing dot", handle: "example."},
		{name: "this directory", handle: "."},
		{name: "the parent directory", handle: ".."},
		{name: "a relative segment", handle: "example/../store"},
		{name: "an empty segment", handle: "example..store"},
		{name: "a segment that starts with a dash", handle: "example.-store"},
		{name: "a segment that ends with a dash", handle: "example-.store"},
		{name: "a space", handle: "example store"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := checkHandle(c.handle)
			if c.accepted && err != nil {
				t.Fatalf("checkHandle(%q): %v, want it accepted", c.handle, err)
			}
			if !c.accepted && status.Code(err) != codes.InvalidArgument {
				t.Fatalf("checkHandle(%q) answered %v, want InvalidArgument", c.handle, err)
			}
		})
	}
}
