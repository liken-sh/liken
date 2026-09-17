package main

// This file owns content negotiation: the forms each aspect serves,
// the Accept field, and the choice between them. An extension in the
// path names one fixed form, and the only question is whether the
// Accept field admits it. An extensionless route negotiates on Accept
// with q-values, RFC 9110 section 12.5.1, and no Accept means the
// aspect's default. The screen and audio forms are what the sibling
// APIs serve, so a redirect carries the extension the negotiation
// chose and the sibling negotiates nothing again.

import (
	"strconv"
	"strings"
)

// mediaForm is one fixed representation of an aspect. It has both a
// media type and an extension because a client reaches it two ways:
// by naming the type in Accept on the extensionless route, or by
// naming the extension in the path. Each way must land on the same
// form, and a redirect's Location carries the extension.
type mediaForm struct {
	// Type is the media type the form is served as, and the one a
	// Content-Type and a Link type parameter carry.
	Type string
	// Extension is the path extension that names this form, mp4 for
	// video/mp4.
	Extension string
	// Aliases are the other media types that match this form. A
	// client that names the deprecated video/x-matroska or one of the
	// three unregistered WAVE names asks for the same bytes, so the
	// alias matches, and the answer is served under Type.
	Aliases []string
}

// The forms each aspect serves, in order of the server's preference.
// The first form is the aspect's default, the one an absent Accept
// gets, and the order breaks a tie between forms the client rates at
// equal quality. The screen and audio lists are exactly what the
// display API and the audio API serve, because a screen or audio
// request redirects there and the extension in the Location must be
// one the sibling answers. The composed list is this API's own.
var (
	screenForms = []mediaForm{
		{Type: "image/png", Extension: "png"},
		{Type: "image/jpeg", Extension: "jpg"},
		{Type: "video/mp4", Extension: "mp4"},
		{Type: "multipart/x-mixed-replace", Extension: "mjpeg"},
	}
	audioForms = []mediaForm{
		{Type: "audio/wav", Extension: "wav", Aliases: []string{"audio/wave", "audio/x-wav", "audio/vnd.wave"}},
		{Type: "audio/flac", Extension: "flac", Aliases: []string{"audio/x-flac"}},
		{Type: "audio/ogg", Extension: "opus"},
	}
	composedForms = []mediaForm{
		{Type: "video/mp4", Extension: "mp4"},
		{Type: "video/matroska", Extension: "mkv", Aliases: []string{"video/x-matroska"}},
	}
)

// negotiate picks the form an extensionless route serves from the
// Accept field. An absent or empty field means the aspect's default,
// the first form. Otherwise the highest-quality form the field admits
// wins. A false answer means nothing offered is acceptable, which the
// route answers as 406.
func negotiate(accept string, forms []mediaForm) (mediaForm, bool) {
	if len(forms) == 0 {
		return mediaForm{}, false
	}
	if strings.TrimSpace(accept) == "" {
		return forms[0], true
	}
	ranges := parseAccept(accept)
	chosen, found := mediaForm{}, false
	bestQuality, bestSpecificity := 0.0, 0
	for _, form := range forms {
		quality, specificity, ok := bestRange(ranges, form)
		// A form at q=0 is one the client refused, not one it rates
		// low: RFC 9110 section 12.4.2 says q=0 means "not
		// acceptable". Serving it when nothing else matches would
		// hand the client bytes it said it cannot use.
		if !ok || quality <= 0 {
			continue
		}
		// Quality decides first. At equal quality, the form matched
		// by the more specific range wins, so `video/*, video/matroska`
		// picks matroska. At equal specificity the earlier form wins,
		// which is the server's own preference.
		if found && (quality < bestQuality || (quality == bestQuality && specificity <= bestSpecificity)) {
			continue
		}
		chosen, found, bestQuality, bestSpecificity = form, true, quality, specificity
	}
	return chosen, found
}

// acceptable answers the one question an extension route asks: does
// the Accept field admit the form the path named? An absent field
// admits everything. A field that names other types, or this form at
// q=0, does not, and the route answers 406.
func acceptable(accept string, form mediaForm) bool {
	if strings.TrimSpace(accept) == "" {
		return true
	}
	quality, _, ok := bestRange(parseAccept(accept), form)
	return ok && quality > 0
}

// formByExtension finds the one form an extension names. The extension
// is not negotiated: mp4 is video/mp4 whatever the Accept field says.
func formByExtension(extension string, forms []mediaForm) (mediaForm, bool) {
	for _, form := range forms {
		if form.Extension == extension {
			return form, true
		}
	}
	return mediaForm{}, false
}

// mediaRange is one entry of an Accept field: a type, a subtype, and
// the quality the client gives the pair. Either part may be `*`. A
// range with no q parameter is at full quality, 1.
type mediaRange struct {
	kind    string
	subtype string
	quality float64
}

// parseAccept reads an Accept field into its ranges. Only the q
// parameter matters for matching. Every other parameter, such as
// codecs, is ignored, because this API serves one representation per
// form and a parameter that names a codec neither adds a form nor
// removes one. A q value that does not parse is taken as full quality,
// so a malformed parameter widens the match rather than refusing a
// client.
func parseAccept(accept string) []mediaRange {
	ranges := []mediaRange{}
	for _, entry := range splitOutsideQuotes(accept, ',') {
		fields := splitOutsideQuotes(entry, ';')
		kind, subtype, named := strings.Cut(strings.TrimSpace(fields[0]), "/")
		// An entry with no slash is not a media range, and matching
		// it against a form has no defined answer, so it is dropped.
		if !named {
			continue
		}
		parsed := mediaRange{
			kind:    strings.ToLower(strings.TrimSpace(kind)),
			subtype: strings.ToLower(strings.TrimSpace(subtype)),
			quality: 1,
		}
		for _, field := range fields[1:] {
			key, value, _ := strings.Cut(field, "=")
			if !strings.EqualFold(strings.TrimSpace(key), "q") {
				continue
			}
			if quality, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
				parsed.quality = quality
			}
		}
		ranges = append(ranges, parsed)
	}
	return ranges
}

// bestRange finds the range that decides a form's quality when several
// match. The most specific matching range wins, RFC 9110 section
// 12.5.1, so `video/*;q=0.5, video/mp4` gives video/mp4 full quality.
// The specificity is how many parts of the range are exact: 0 for
// `*/*`, 1 for `video/*`, and 2 for a full type.
func bestRange(ranges []mediaRange, form mediaForm) (quality float64, specificity int, ok bool) {
	for _, candidate := range ranges {
		candidateSpecificity, matched := matchRange(candidate, form)
		if !matched {
			continue
		}
		if ok && (candidateSpecificity < specificity || (candidateSpecificity == specificity && candidate.quality <= quality)) {
			continue
		}
		quality, specificity, ok = candidate.quality, candidateSpecificity, true
	}
	return quality, specificity, ok
}

// matchRange tells whether a range matches a form, by the form's type
// or one of its aliases, and scores the match. The catch-all `*/*`
// scores 0, a type wildcard such as `video/*` scores 1, and an exact
// type scores 2.
func matchRange(candidate mediaRange, form mediaForm) (int, bool) {
	if candidate.kind == "*" && candidate.subtype == "*" {
		return 0, true
	}
	for _, name := range append([]string{form.Type}, form.Aliases...) {
		kind, subtype, _ := strings.Cut(name, "/")
		if candidate.kind != kind {
			continue
		}
		if candidate.subtype == "*" {
			return 1, true
		}
		if candidate.subtype == subtype {
			return 2, true
		}
	}
	return 0, false
}

// splitOutsideQuotes splits a field on a separator that is not inside
// a quoted string. A codecs parameter carries a comma inside its
// quotes, `video/mp4;codecs="avc1.64001f,Opus"`, and a plain split on
// the comma would cut that one entry into two.
func splitOutsideQuotes(field string, separator byte) []string {
	parts := []string{}
	quoted, start := false, 0
	for index := 0; index < len(field); index++ {
		switch {
		case field[index] == '"':
			quoted = !quoted
		case field[index] == separator && !quoted:
			parts = append(parts, field[start:index])
			start = index + 1
		}
	}
	return append(parts, field[start:])
}
