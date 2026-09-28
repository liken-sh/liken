package main

// Choosing the representation a request gets.
//
// An extension names one fixed representation. With no extension,
// Accept chooses by RFC 9110 section 12.5.1 with q-values, and the
// server's own preference order breaks a tie, which is the order
// audioRepresentations is written in. A request with no Accept gets
// the first of that order.
//
// An extension route still reads Accept. An Accept that excludes the
// route's one representation is a 406 rather than a 200 of something
// the client refused, which is also why Vary: Accept goes on an
// extension route's answer.

import (
	"strconv"
	"strings"
)

// mediaRange is one entry of an Accept header: a type, a subtype, and
// the quality the client gave it. Every other parameter is read and
// dropped: this API's three representations differ by type and
// subtype alone, and the codecs parameter on audio/ogg names the one
// codec the Ogg route ever carries.
type mediaRange struct {
	Type    string
	Subtype string
	Quality float64
}

// parseAccept reads an Accept header into its ranges, in the order the
// client wrote them. A range this parser cannot read is dropped rather
// than failing the header, because a client that sends one malformed
// range still stated preferences in the rest.
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

// quality is the weight one Accept header gives one representation.
// The most specific range wins, which is section 12.5.1's rule: an
// exact type and subtype beats a subtype wildcard, which beats the
// full wildcard. That is what makes "audio/wav;q=0, */*" exclude WAV
// and admit the other two.
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

// matchesType says whether a subtype wildcard covers this form, by the
// type half of any spelling the form accepts.
func matchesType(kind string, form representation) bool {
	for _, spelling := range form.Accepted {
		if before, _, _ := strings.Cut(spelling, "/"); before == kind {
			return true
		}
	}
	return false
}

// matchesExactly says whether a range names this form. Every spelling
// the form accepts names it, so audio/vnd.wave and audio/x-flac reach
// the same representation as the name this API sends.
func matchesExactly(accepted mediaRange, form representation) bool {
	name := accepted.Type + "/" + accepted.Subtype
	for _, spelling := range form.Accepted {
		if spelling == name {
			return true
		}
	}
	return false
}

// negotiate chooses the representation a route serves this request, or
// reports that the route serves none the client will take.
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
		// The server's order breaks a tie, so a later form has to beat
		// the weight of an earlier one, not equal it.
		if found && weight <= best {
			continue
		}
		chosen, best, found = form, weight, true
	}
	return chosen, found
}
