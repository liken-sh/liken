package main

// This file chooses the representation of a capture response from the
// request's Accept header. A route with an extension in the path
// serves that one form, and the Accept header can only refuse it. A
// route without an extension picks a form by the q-values of the
// Accept header (RFC 9110 section 12.5.1), and a request with no
// Accept header gets the aspect's first form. The screen and audio
// aspects redirect to the display and audio APIs with the chosen
// extension in the Location, so those APIs do not negotiate again.

import (
	"strconv"
	"strings"
)

// mediaForm is one format a capture can be served in. A client
// selects it either by media type in the Accept header or by
// extension in the path, and both must select the same form.
type mediaForm struct {
	// Type is the media type sent in Content-Type and in the type
	// parameter of a Link.
	Type string
	// Extension is the path extension that selects this form.
	Extension string
	// Aliases are other media types that an Accept header can use for
	// this form, such as the deprecated video/x-matroska. A request
	// that names an alias is served under Type.
	Aliases []string
}

// The forms of each aspect, in the server's order of preference. The
// first form is the default for a request with no Accept header, and
// the order breaks a tie between forms that the client rates at the
// same quality. The screen and audio lists must match the forms that
// the display API and the audio API serve, because the redirect's
// Location sends one of these extensions to those APIs. This API
// serves the composed forms itself.
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

// negotiate picks the form for a route with no extension. It returns
// the first form when the Accept header is empty, and otherwise the
// form with the highest quality in the header. It returns false when
// the header admits no form, and the route then responds 406.
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
		// q=0 means "not acceptable" (RFC 9110 section 12.4.2), so
		// skip the form even when no other form matches. Serving it
		// would send a representation the client refused.
		if !ok || quality <= 0 {
			continue
		}
		// The higher quality wins. At equal quality, the form that
		// matched a more specific range wins, so `video/*,
		// video/matroska` picks matroska. At equal specificity the
		// earlier form in the list wins.
		if found && (quality < bestQuality || (quality == bestQuality && specificity <= bestSpecificity)) {
			continue
		}
		chosen, found, bestQuality, bestSpecificity = form, true, quality, specificity
	}
	return chosen, found
}

// acceptable reports whether the Accept header admits the form that
// the path's extension selected. An empty header admits every form.
// A header that lists only other types, or lists this form at q=0,
// does not, and the route then responds 406.
func acceptable(accept string, form mediaForm) bool {
	if strings.TrimSpace(accept) == "" {
		return true
	}
	quality, _, ok := bestRange(parseAccept(accept), form)
	return ok && quality > 0
}

// formByExtension returns the form that an extension selects. The
// Accept header does not change the result: mp4 is always video/mp4.
func formByExtension(extension string, forms []mediaForm) (mediaForm, bool) {
	for _, form := range forms {
		if form.Extension == extension {
			return form, true
		}
	}
	return mediaForm{}, false
}

// mediaRange is one entry of an Accept header: a type, a subtype, and
// a quality. Either part may be `*`. An entry with no q parameter has
// quality 1.
type mediaRange struct {
	kind    string
	subtype string
	quality float64
}

// parseAccept splits an Accept header into its ranges. It reads only
// the q parameter and ignores the others, such as codecs, because
// each form has one fixed encoding. A q value that does not parse
// leaves the quality at 1, so a malformed value admits the form
// instead of refusing it.
func parseAccept(accept string) []mediaRange {
	ranges := []mediaRange{}
	for _, entry := range splitOutsideQuotes(accept, ',') {
		fields := splitOutsideQuotes(entry, ';')
		kind, subtype, named := strings.Cut(strings.TrimSpace(fields[0]), "/")
		// An entry with no slash is not a media range, so skip it.
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

// bestRange returns the quality of a form from the most specific range
// that matches it (RFC 9110 section 12.5.1), so `video/*;q=0.5,
// video/mp4` gives video/mp4 quality 1. Among ranges of equal
// specificity, the higher quality is used. ok is false when no range
// matches.
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

// matchRange reports whether a range matches the form's type or one
// of its aliases, and returns the specificity of the match: 0 for
// `*/*`, 1 for a type wildcard such as `video/*`, and 2 for an exact
// type.
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

// splitOutsideQuotes splits a header value on a separator byte that is
// not inside a quoted string. A codecs parameter can contain a comma,
// as in `video/mp4;codecs="avc1.64001f,Opus"`, and splitting on every
// comma would cut that entry in two.
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
