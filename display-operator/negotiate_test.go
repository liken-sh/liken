package main

import "testing"

// These rows are the manual's negotiation table, and chooseType and
// acceptsType each answer every one of them.
var negotiationRows = []struct {
	name   string
	accept string
	want   string
	ok     bool
}{
	{"no accept field", "", "image/png", true},
	{"one type", "image/jpeg", "image/jpeg", true},
	{"a type wildcard", "image/*", "image/png", true},
	{"a lower q on the default", "video/mp4, image/png;q=0.5", "video/mp4", true},
	{"the default excluded", "image/png;q=0, */*", "image/jpeg", true},
	{"nothing the route serves", "audio/wav", "", false},
	{"a full wildcard", "*/*", "image/png", true},
	{"the stream", "multipart/*", "multipart/x-mixed-replace", true},
	{"equal q keeps the route's order", "video/mp4;q=0.7, image/jpeg;q=0.7", "image/jpeg", true},
	{"an exact range beats a type wildcard", "image/*;q=0.2, image/jpeg;q=0.9", "image/jpeg", true},
	{"case and space are ignored", "  IMAGE/JPEG ;Q=1 ", "image/jpeg", true},
	{"a parameter other than q matches on type alone", "image/jpeg;profile=x", "image/jpeg", true},
	{"an accept-param after q is ignored", "image/jpeg;q=1;profile=x", "image/jpeg", true},
	{"a malformed q reads as absent", "image/jpeg;q=high, image/png;q=0.4", "image/jpeg", true},
	{"a malformed entry is skipped", "image, image/jpeg", "image/jpeg", true},
	{"every type excluded", "*/*;q=0", "", false},
}

func TestNegotiationChoosesTheTypeToServe(t *testing.T) {
	for _, test := range negotiationRows {
		t.Run(test.name, func(t *testing.T) {
			got, ok := chooseType(test.accept, screenMediaTypes())
			if got != test.want || ok != test.ok {
				t.Fatalf("chooseType(%q) = %q, %v, want %q, %v",
					test.accept, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestAcceptsTypeAdmitsOneRepresentation(t *testing.T) {
	tests := []struct {
		name      string
		accept    string
		mediaType string
		want      bool
	}{
		{"no accept field", "", "image/png", true},
		{"another type", "image/jpeg", "image/png", false},
		{"its own type", "image/jpeg", "image/jpeg", true},
		{"a type wildcard", "image/*", "image/png", true},
		{"a lower q still admits", "video/mp4, image/png;q=0.5", "image/png", true},
		{"q=0 excludes", "image/png;q=0, */*", "image/png", false},
		{"q=0 under a wildcard admits a sibling", "image/png;q=0, */*", "image/jpeg", true},
		{"nothing the route serves", "audio/wav", "image/png", false},
		{"the extension route's own type", "image/*", "image/png", true},
		{"a type the extension route cannot serve", "video/mp4", "image/png", false},
		{"the stream", "multipart/x-mixed-replace", "multipart/x-mixed-replace", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := acceptsType(test.accept, test.mediaType)
			if got != test.want {
				t.Fatalf("acceptsType(%q, %q) = %v, want %v",
					test.accept, test.mediaType, got, test.want)
			}
		})
	}
}

func TestNegotiationOverOneOffer(t *testing.T) {
	tests := []struct {
		name   string
		accept string
		want   string
		ok     bool
	}{
		{"the offer itself", "video/mp4", "video/mp4", true},
		{"no accept field", "", "video/mp4", true},
		{"a type wildcard", "video/*", "video/mp4", true},
		{"another type", "image/png", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := chooseType(test.accept, []string{"video/mp4"})
			if got != test.want || ok != test.ok {
				t.Fatalf("chooseType(%q, [video/mp4]) = %q, %v, want %q, %v",
					test.accept, got, ok, test.want, test.ok)
			}
		})
	}
}
