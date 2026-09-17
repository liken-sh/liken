package main

// This file holds proactive content negotiation, RFC 9110 section
// 12.5.1. The route's own order decides every tie and every
// wildcard, so a client that states no preference gets image/png,
// the cheapest form to take, and never a stream it did not ask for.
// An extension route offers one type, so on it Accept decides
// between 200 and 406 and never between types.

import (
	"strconv"
	"strings"
)

// chooseType picks the media type to serve from the offers, which
// are in the route's own order.
func chooseType(accept string, offers []string) (string, bool) {
	// A request with no Accept field takes any type the route serves
	// (RFC 9110 section 12.5.1), so it gets the first offer.
	if strings.TrimSpace(accept) == "" {
		if len(offers) == 0 {
			return "", false
		}
		return offers[0], true
	}
	ranges := parseAccept(accept)
	chosen := ""
	best := 0.0
	for _, offer := range offers {
		quality, matched := offerQuality(ranges, offer)
		// q=0 is a refusal. Only a strictly higher q replaces an
		// earlier offer, so offers that tie keep the route's order.
		if !matched || quality <= 0 || quality <= best {
			continue
		}
		chosen, best = offer, quality
	}
	return chosen, chosen != ""
}

// acceptsType reports whether Accept admits one fixed
// representation, the question an extension route asks.
func acceptsType(accept, mediaType string) bool {
	_, ok := chooseType(accept, []string{mediaType})
	return ok
}

// One media range from Accept: the type and subtype, which match
// case-insensitively, and the weight its q parameter states.
type mediaRange struct {
	kind    string
	subtype string
	quality float64
}

// A malformed entry in the list is skipped, and the entries beside
// it still count, rather than one bad entry failing the whole field.
func parseAccept(accept string) []mediaRange {
	ranges := make([]mediaRange, 0, strings.Count(accept, ",")+1)
	for _, entry := range strings.Split(accept, ",") {
		parsed, ok := parseMediaRange(entry)
		if !ok {
			continue
		}
		ranges = append(ranges, parsed)
	}
	return ranges
}

func parseMediaRange(entry string) (mediaRange, bool) {
	fields := strings.Split(entry, ";")
	kind, subtype, split := strings.Cut(strings.ToLower(strings.TrimSpace(fields[0])), "/")
	if !split || kind == "" || subtype == "" {
		return mediaRange{}, false
	}
	return mediaRange{kind: kind, subtype: subtype, quality: rangeQuality(fields[1:])}, true
}

// The parameters before q belong to the media type, and a range is
// matched on its type and subtype only, so they are skipped. The
// parameters after q are accept extensions, also skipped. A
// malformed q is read as absent, which is 1.
func rangeQuality(parameters []string) float64 {
	for _, parameter := range parameters {
		name, value, _ := strings.Cut(parameter, "=")
		if !strings.EqualFold(strings.TrimSpace(name), "q") {
			continue
		}
		quality, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || quality < 0 || quality > 1 {
			return 1
		}
		return quality
	}
	return 1
}

// The most specific range that matches an offer is the one whose q
// the offer carries, per section 12.5.1.
func offerQuality(ranges []mediaRange, offer string) (float64, bool) {
	kind, subtype, _ := strings.Cut(strings.ToLower(offer), "/")
	precision := -1
	quality := 0.0
	for _, candidate := range ranges {
		candidatePrecision := matchPrecision(candidate, kind, subtype)
		if candidatePrecision <= precision {
			continue
		}
		precision, quality = candidatePrecision, candidate.quality
	}
	return quality, precision >= 0
}

// The three ways a range matches a type, from the most specific to
// the least, the order section 12.5.1 ranks them in: the exact type,
// the type with any subtype, and any type at all.
func matchPrecision(candidate mediaRange, kind, subtype string) int {
	switch {
	case candidate.kind == kind && candidate.subtype == subtype:
		return 2
	case candidate.kind == kind && candidate.subtype == "*":
		return 1
	case candidate.kind == "*" && candidate.subtype == "*":
		return 0
	}
	return -1
}
