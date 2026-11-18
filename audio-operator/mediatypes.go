package main

// A route with a file extension serves one format. A route without one
// serves the format with the highest q-value in Accept (RFC 9110
// section 12.5.1). Between formats with equal q, the one first in the
// route's Serves wins, and a request with no Accept gets the first.
//
// A route with an extension still checks Accept, and returns 406 when
// Accept excludes its format, instead of a 200 that the client said it
// would not take. That is also why an extension route's response has
// Vary: Accept.

import (
	"strconv"
	"strings"
)

// mediaRange drops every parameter but q, because the formats differ
// by type and subtype alone, and the only codec in audio/ogg here is
// Opus.
type mediaRange struct {
	Type    string
	Subtype string
	Quality float64
}

// parseAccept skips a malformed range instead of rejecting the header,
// so the other ranges still apply.
func parseAccept(header string) []mediaRange {
	var ranges []mediaRange
	for _, entry := range strings.Split(header, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, parameters, _ := strings.Cut(entry, ";")
		kind, subtype, found := strings.Cut(strings.TrimSpace(name), "/")
		if !found {
			continue
		}
		accepted := mediaRange{
			Type:    strings.ToLower(strings.TrimSpace(kind)),
			Subtype: strings.ToLower(strings.TrimSpace(subtype)),
			Quality: 1,
		}
		for _, parameter := range strings.Split(parameters, ";") {
			key, value, found := strings.Cut(parameter, "=")
			if !found || strings.TrimSpace(key) != "q" {
				continue
			}
			quality, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				continue
			}
			accepted.Quality = quality
			break
		}
		ranges = append(ranges, accepted)
	}
	return ranges
}

// quality returns false when no range matches form. The most specific
// matching range wins (RFC 9110 section 12.5.1): audio/wav beats
// audio/*, which beats */*. So "audio/wav;q=0, */*" excludes WAV and
// admits the other formats.
func quality(ranges []mediaRange, form representation) (float64, bool) {
	best, precision, matched := 0.0, -1, false
	for _, accepted := range ranges {
		rank := -1
		switch {
		case accepted.Type == "*" && accepted.Subtype == "*":
			rank = 0
		case accepted.Subtype == "*" && matchesType(accepted.Type, form):
			rank = 1
		case matchesExactly(accepted, form):
			rank = 2
		}
		if rank <= precision {
			continue
		}
		best, precision, matched = accepted.Quality, rank, true
	}
	return best, matched
}

// matchesType reports whether kind, from a range such as audio/*, is
// the type of any spelling in form.Accepted.
func matchesType(kind string, form representation) bool {
	for _, spelling := range form.Accepted {
		if before, _, _ := strings.Cut(spelling, "/"); before == kind {
			return true
		}
	}
	return false
}

// matchesExactly matches any spelling in form.Accepted, so
// audio/vnd.wave selects WAV and audio/x-flac selects FLAC.
func matchesExactly(accepted mediaRange, form representation) bool {
	name := accepted.Type + "/" + accepted.Subtype
	for _, spelling := range form.Accepted {
		if spelling == name {
			return true
		}
	}
	return false
}

// negotiate returns false when Accept excludes every representation of
// the route.
func negotiate(route apiRoute, accept string) (representation, bool) {
	if strings.TrimSpace(accept) == "" {
		if len(route.Serves) == 0 {
			return representation{}, false
		}
		return route.Serves[0], true
	}
	ranges := parseAccept(accept)
	chosen, best, found := representation{}, 0.0, false
	for _, form := range route.Serves {
		weight, matched := quality(ranges, form)
		if !matched || weight <= 0 {
			continue
		}
		// Serves is in the server's order of preference, so a later form
		// wins only with a higher q.
		if found && weight <= best {
			continue
		}
		chosen, best, found = form, weight, true
	}
	return chosen, found
}
