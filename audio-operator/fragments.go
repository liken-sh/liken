package main

// The query of a capture request: the W3C Media Fragments temporal
// dimension t= and this API's own bitrate= parameter.
//
// Media Fragments section 3.1 says a URI query produces a new resource
// that needs server processing, and section 7.4 says a query can change
// the media type. So the path identifies the sink or source, the
// extension or the Accept header selects the format, and the query
// selects the span.
//
// The parser follows the order of section 5.1.1: split the query on &
// and =, then percent-decode each part. That is how t=10%2C20,
// t=%6ept:10, and t=npt%3a10 parse. It does not use net/url's query
// parser, because that also decodes a plus sign as a space.
//
// Sections 6.2, 6.2.1, and 6.3.1 tell a user agent to ignore an
// invalid, unknown, or non-existent dimension. This API refuses them
// with a 400 instead, because a query produces a new resource, and a
// client that asked for a span must not get something else. A repeated
// dimension is a 400, and so is t=a,b when a is at or past b.

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// captureBeginMax limits the begin offset, because the API discards the
// audio before begin, so the client waits that long for its first byte.
// The display capture API uses the same limit.
const captureBeginMax = 60 * time.Second

// bitrateMin and bitrateMax are the range that opusenc accepts, in
// kbit/s per channel. Without the bitrate parameter, opusenc uses its
// own default: 64 kbit/s per mono stream and 96 kbit/s per coupled
// pair for input at 44.1 kHz or higher.
const (
	bitrateMin = 6
	bitrateMax = 256
)

// timeSpan is the half-open interval [Begin, End) of the tap. Ended is
// false when the request names no end.
//
// Media Fragments puts NPT's zero at the start of the source media
// (section 6.1.1). A live tap has no start, so this API puts zero at
// the moment the capture container accepts the request. The clock:
// format would state that directly, but it is in the advanced Media
// Fragments document and not in 1.0.
type timeSpan struct {
	Begin time.Duration
	End   time.Duration
	Ended bool
}

// sampleBytes is the size of one sample of one channel, because every
// tap is s16le.
const sampleBytes = 2

// bodyBytes returns 0 when the span has no end, because the body then
// runs until the client closes the connection.
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

type captureQuery struct {
	Span    timeSpan
	Bitrate int
}

// parseCaptureQuery refuses a parameter that the route does not accept, a
// repeated parameter, and an invalid value.
func parseCaptureQuery(route apiRoute, rawQuery string) (captureQuery, error) {
	var query captureQuery
	seen := map[string]bool{}
	for _, pair := range splitQuery(rawQuery) {
		if !route.accepts(pair.Name) {
			return captureQuery{}, fmt.Errorf("the route %s accepts no %s", route.Template, pair.Name)
		}
		if seen[pair.Name] {
			return captureQuery{}, fmt.Errorf("%s is given more than once", pair.Name)
		}
		seen[pair.Name] = true
		switch pair.Name {
		case "t":
			span, err := parseSpan(pair.Value)
			if err != nil {
				return captureQuery{}, err
			}
			query.Span = span
		case "bitrate":
			rate, err := strconv.Atoi(pair.Value)
			if err != nil {
				return captureQuery{}, fmt.Errorf("bitrate=%s is not a whole number of kbit/s", pair.Value)
			}
			if rate < bitrateMin || rate > bitrateMax {
				return captureQuery{}, fmt.Errorf("bitrate=%d is outside %d to %d kbit/s",
					rate, bitrateMin, bitrateMax)
			}
			query.Bitrate = rate
		}
	}
	return query, nil
}

type queryPair struct {
	Name  string
	Value string
}

// splitQuery keeps a part that does not percent-decode as received, so
// the caller refuses it as invalid and does not treat it as absent.
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

// parseSpan parses the value of t= with the npttimedef grammar of
// section 4.2.1: begin and end, begin alone, or a comma and end alone.
func parseSpan(value string) (timeSpan, error) {
	// The npt: prefix is optional (section 4.2.1).
	if after, found := strings.CutPrefix(value, "npt:"); found {
		value = after
	} else if index := strings.IndexByte(value, ':'); index >= 0 {
		// smpte: and clock: are valid Media Fragments formats that this
		// API refuses, because a live tap's zero is the moment the
		// request is accepted.
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

// parseNPT parses one time, which section 4.2.1 defines as npt-sec,
// npt-mmss, or npt-hhmmss:
//
//	npt-sec    = 1*DIGIT [ "." *DIGIT ]
//	npt-mmss   = 2DIGIT ":" 2DIGIT [ "." *DIGIT ]
//	npt-hhmmss = 1*DIGIT ":" 2DIGIT ":" 2DIGIT [ "." *DIGIT ]
//
// The grammar is checked before strconv.ParseFloat, which also accepts
// nan, inf, infinity, an exponent, and a leading sign. Every comparison
// with NaN is false, so a NaN begin would pass the range checks, and
// t=nan,inf would run the tap without bound.
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

// parseWhole parses one integer field. width is the required number of
// digits, or 0 for any number. most is the largest allowed value, or
// negative for no limit.
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

// parseSeconds parses the last field, which is the only one that can
// have a fraction. After a minutes field, it must be two digits and
// less than 60.
func parseSeconds(text string, afterMinutes bool) (float64, bool) {
	whole, fraction, dotted := strings.Cut(text, ".")
	if !digits(whole) || (dotted && !digits(fraction)) {
		return 0, false
	}
	if afterMinutes && len(whole) != 2 {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, false
	}
	if afterMinutes && value >= 60 {
		return 0, false
	}
	return value, true
}

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

// expandAspectTemplate expands the RFC 6570 template that the discovery
// document publishes. A test sends the result to the router, so it
// fails when the published template and the router disagree.
func expandAspectTemplate(template, name, extension string, values map[string]string) string {
	path, rest, _ := strings.Cut(template, "{.ext}")
	expanded := strings.ReplaceAll(path, "{name}", name)
	if extension != "" {
		expanded += "." + extension
	}
	names := strings.Split(strings.Trim(rest, "{?}"), ",")
	var query []string
	for _, parameter := range names {
		value, given := values[parameter]
		if !given {
			continue
		}
		query = append(query, parameter+"="+url.QueryEscape(value))
	}
	if len(query) == 0 {
		return expanded
	}
	return expanded + "?" + strings.Join(query, "&")
}
