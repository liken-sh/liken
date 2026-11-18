package main

// Content negotiation on the Accept header, as RFC 9110 section 12.5.1
// defines it. When offers tie on q, or one wildcard matches several of
// them, the earliest offer wins. So a client that sends no Accept, or
// only */*, gets the first of screenForms, image/png, and not a stream.

import (
	"strconv"
	"strings"
)

// chooseType returns the offer that the Accept header ranks highest,
// and false when it admits none. Offers that tie keep the order given.
func chooseType(accept string, offers []string) (string, bool) {
	// RFC 9110 treats a missing Accept field as accepting any type.
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
		// q=0 means the client refuses the type. Only a strictly higher
		// q replaces the chosen offer, so ties go to the earlier offer.
		if !matched || quality <= 0 || quality <= best {
			continue
		}
		chosen, best = offer, quality
	}
	return chosen, chosen != ""
}

// acceptsType reports whether Accept admits mediaType. selectType
// calls it for a route with a file extension, which serves one type.
func acceptsType(accept, mediaType string) bool {
	_, ok := chooseType(accept, []string{mediaType})
	return ok
}

// mediaRange is one entry of an Accept header. kind and subtype are
// lowercase, and quality is the value of the q parameter.
type mediaRange struct {
	kind    string
	subtype string
	quality float64
}

// parseAccept returns the valid entries of an Accept header. It skips
// a malformed entry, so one bad entry does not discard the others.
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

// rangeQuality returns the q parameter, or 1 when there is none or it
// is not a number from 0 to 1. Other parameters are ignored because a
// range matches on type and subtype only.
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

// offerQuality returns the q of the most specific range that matches
// the offer, and false when no range matches. Section 12.5.1 says the
// most specific range decides, so image/png;q=0 beats */*.
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

// matchPrecision ranks how a range matches a type: 2 for an exact
// match, 1 for kind/*, 0 for */*, and -1 for no match.
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
