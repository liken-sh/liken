package main

// The query of a capture request: the W3C Media Fragments temporal
// dimension t= and this API's own bitrate= knob.
//
// The query form has standing in the specification. Media Fragments
// section 3.1 says a URI query produces a new resource requiring
// server processing, and section 7.4 says a query approach may change
// the media type, with a fragment retrieved as a JPEG through Accept
// as its example. That is why identity is in the path, the format is
// in the extension or in Accept, and the span is here.
//
// The parse order is section 5.1.1's: split the query on the
// ampersand and the equals sign first, and percent-decode each part
// second. That order is what makes t=10%2C20, t=%6ept:10, and
// t=npt%3a10 parse, and it is why this file does not use net/url's
// own query parser, which also decodes a plus sign as a space.
//
// This API departs from the specification in one place. Sections
// 6.2, 6.2.1, and 6.3.1 tell a user agent to ignore an invalid,
// unknown, or non-existent dimension. This API answers 400 instead,
// because a query produces a new resource and a client that asked for
// a span must not silently get something else. A repeated dimension
// is 400, and t=a,b with a at or past b is 400.

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// captureBeginMax bounds the discard. The three capture APIs share
// this number. A larger begin is a 400 rather than a request the API
// holds open for longer than any client would wait.
const captureBeginMax = 60 * time.Second

// bitrateMin and bitrateMax are the range opusenc takes, in kbit/s
// per channel. When the knob is absent opusenc chooses for itself,
// and its own default is 64 kbit/s per mono stream and 96 kbit/s per
// coupled pair for input at 44.1 kHz or higher.
const (
	bitrateMin = 6
	bitrateMax = 256
)

// timeSpan is the half-open interval [Begin, End) the tap delivers.
//
// Media Fragments fixes NPT's zero at the start of the source media
// (section 6.1.1). A live tap has no start, so this API defines the
// source media's zero as the instant the capture container accepts
// the request. The clock: format that would say this directly is in
// the advanced document, not in 1.0.
type timeSpan struct {
	Begin time.Duration
	End   time.Duration
	Ended bool
}

// sampleBytes is the size of one sample of one channel. Every tap is
// s16le, so it is two bytes, and 16 bits is the width every player
// reads.
const sampleBytes = 2

// bodyBytes is how many raw bytes the body carries. An absent end
// means until the client closes, which is no bound at all.
func (s timeSpan) bodyBytes(rate, channels int) int64 {
	if !s.Ended {
		return 0
	}
	return rawBytes(s.End-s.Begin, rate, channels)
}

func rawBytes(length time.Duration, rate, channels int) int64 {
	if length <= 0 {
		return 0
	}
	return int64(length.Seconds() * float64(rate*channels*sampleBytes))
}

// captureKnobs is the whole of what a request's query says.
type captureKnobs struct {
	Span    timeSpan
	Bitrate int
}

// parseKnobs reads the query a route takes, and refuses every other
// name in it.
func parseKnobs(route apiRoute, rawQuery string) (captureKnobs, error) {
	var knobs captureKnobs
	seen := map[string]bool{}
	for _, pair := range splitQuery(rawQuery) {
		if !route.takes(pair.Name) {
			return captureKnobs{}, fmt.Errorf("the route %s takes no %s", route.Template, pair.Name)
		}
		if seen[pair.Name] {
			return captureKnobs{}, fmt.Errorf("%s is given more than once", pair.Name)
		}
		seen[pair.Name] = true
		switch pair.Name {
		case "t":
			span, err := parseSpan(pair.Value)
			if err != nil {
				return captureKnobs{}, err
			}
			knobs.Span = span
		case "bitrate":
			rate, err := strconv.Atoi(pair.Value)
			if err != nil {
				return captureKnobs{}, fmt.Errorf("bitrate=%s is not a whole number of kbit/s", pair.Value)
			}
			if rate < bitrateMin || rate > bitrateMax {
				return captureKnobs{}, fmt.Errorf("bitrate=%d is outside %d to %d kbit/s",
					rate, bitrateMin, bitrateMax)
			}
			knobs.Bitrate = rate
		}
	}
	return knobs, nil
}

// queryPair is one name and value of the query, decoded.
type queryPair struct {
	Name  string
	Value string
}

// splitQuery is section 5.1.1's order: split on the ampersand and the
// equals sign, then percent-decode each part. A part that does not
// decode is kept as it arrived, which leaves the refusal to the
// grammar below rather than hiding it as a missing name.
func splitQuery(rawQuery string) []queryPair {
	var pairs []queryPair
	for _, part := range strings.Split(rawQuery, "&") {
		if part == "" {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		pairs = append(pairs, queryPair{Name: decode(name), Value: decode(value)})
	}
	return pairs
}

func decode(text string) string {
	decoded, err := url.PathUnescape(text)
	if err != nil {
		return text
	}
	return decoded
}

// parseSpan reads the temporal dimension. The grammar is section
// 4.2.1's npttimedef: begin and end, begin alone, or a comma and end
// alone, each an npt-sec, an npt-mmss, or an npt-hhmmss.
func parseSpan(value string) (timeSpan, error) {
	// Section 4.2.1 makes the npt: prefix optional, and section 6.1.1
	// is the case where it arrives percent-encoded.
	if after, found := strings.CutPrefix(value, "npt:"); found {
		value = after
	} else if index := strings.IndexByte(value, ':'); index >= 0 {
		// A prefix this API does not serve. smpte: and clock: are in
		// the specification; neither has a meaning for a live tap
		// whose zero is the accept instant.
		if prefix := value[:index]; prefix == "smpte" || prefix == "clock" ||
			strings.HasPrefix(prefix, "smpte-") {
			return timeSpan{}, fmt.Errorf("t=%s names a time format this API does not serve", value)
		}
	}

	begin, end, hasEnd := strings.Cut(value, ",")
	span := timeSpan{}
	if begin != "" {
		length, err := parseNPT(begin)
		if err != nil {
			return timeSpan{}, err
		}
		span.Begin = length
	}
	if hasEnd {
		if end == "" {
			return timeSpan{}, fmt.Errorf("t=%s names no end after the comma", value)
		}
		length, err := parseNPT(end)
		if err != nil {
			return timeSpan{}, err
		}
		span.End = length
		span.Ended = true
	}
	if begin == "" && !hasEnd {
		return timeSpan{}, fmt.Errorf("t= names no time")
	}
	if span.Ended && span.Begin >= span.End {
		return timeSpan{}, fmt.Errorf("t=%s begins at or after it ends", value)
	}
	if span.Begin > captureBeginMax {
		return timeSpan{}, fmt.Errorf("t=%s begins past the %s this API discards",
			value, captureBeginMax)
	}
	return span, nil
}

// parseNPT reads one npttime, which section 4.2.1 writes as
// npt-sec, npt-mmss, or npt-hhmmss:
//
//	npt-sec    = 1*DIGIT [ "." *DIGIT ]
//	npt-mmss   = 2DIGIT ":" 2DIGIT [ "." *DIGIT ]
//	npt-hhmmss = 1*DIGIT ":" 2DIGIT ":" 2DIGIT [ "." *DIGIT ]
//
// The grammar is checked before the number is read. strconv.ParseFloat
// takes nan, inf, infinity, an exponent, and a leading sign, and the
// grammar above produces none of them. A NaN begin passes every
// comparison a number would fail, so a t=nan,inf that reached the tap
// would run unbounded instead of answering 400.
//
// The hours field is 1*DIGIT where the minutes and the seconds are
// always two digits, so an end of any number of hours parses.
func parseNPT(text string) (time.Duration, error) {
	refuse := func() (time.Duration, error) {
		return 0, fmt.Errorf("%q is not a time in NPT", text)
	}
	parts := strings.Split(text, ":")
	switch len(parts) {
	case 1:
		seconds, ok := parseSeconds(parts[0], false)
		if !ok {
			return refuse()
		}
		return asDuration(seconds), nil
	case 2:
		minutes, ok := parseWhole(parts[0], 2, 59)
		if !ok {
			return refuse()
		}
		seconds, ok := parseSeconds(parts[1], true)
		if !ok {
			return refuse()
		}
		return asDuration(float64(minutes)*60 + seconds), nil
	case 3:
		hours, ok := parseWhole(parts[0], 0, -1)
		if !ok {
			return refuse()
		}
		minutes, ok := parseWhole(parts[1], 2, 59)
		if !ok {
			return refuse()
		}
		seconds, ok := parseSeconds(parts[2], true)
		if !ok {
			return refuse()
		}
		return asDuration(float64(hours)*3600 + float64(minutes)*60 + seconds), nil
	default:
		return refuse()
	}
}

func asDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

// parseWhole reads one whole field. width is how many digits the field
// must have, or zero for any number of them, and most is the largest
// value it may take, or a negative number for no bound.
func parseWhole(text string, width, most int) (int, bool) {
	if !digits(text) || (width > 0 && len(text) != width) {
		return 0, false
	}
	value, err := strconv.Atoi(text)
	if err != nil || (most >= 0 && value > most) {
		return 0, false
	}
	return value, true
}

// parseSeconds reads the last field, which is the only one that may
// carry a fraction. Two digits and a value under sixty are required
// wherever minutes precede it.
func parseSeconds(text string, inMinutes bool) (float64, bool) {
	whole, fraction, dotted := strings.Cut(text, ".")
	if !digits(whole) || (dotted && !digits(fraction)) {
		return 0, false
	}
	if inMinutes && len(whole) != 2 {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, false
	}
	if inMinutes && value >= 60 {
		return 0, false
	}
	return value, true
}

// digits says whether text is one or more decimal digits and nothing
// else: no sign, no exponent, and no name of a number that is not one.
func digits(text string) bool {
	if text == "" {
		return false
	}
	for _, character := range text {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// expandAspectTemplate is RFC 6570 expansion of the template the
// discovery document publishes. The round-trip test calls what it
// builds, so the published template and the router never drift.
//
// Form-style query expansion (section 3.2.8) percent-encodes the
// comma in t=5,7, and Media Fragments section 5.1.1's parse order is
// what accepts the result.
func expandAspectTemplate(template, name, extension string, knobs map[string]string) string {
	path, rest, _ := strings.Cut(template, "{.ext}")
	expanded := strings.ReplaceAll(path, "{name}", name)
	if extension != "" {
		expanded += "." + extension
	}
	names := strings.Split(strings.Trim(rest, "{?}"), ",")
	var query []string
	for _, knob := range names {
		value, given := knobs[knob]
		if !given {
			continue
		}
		query = append(query, knob+"="+url.QueryEscape(value))
	}
	if len(query) == 0 {
		return expanded
	}
	return expanded + "?" + strings.Join(query, "&")
}
