package promote

// This file renders a frame's name from the Target's archive.format.
// The runner renders the name before each exposure, because the INDI
// driver needs it before it writes the file. The promotion Job never
// renames a frame, so a frame has the same name in the outbox and in
// the archive.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Values are the values of a format's tokens for one frame.
type Values struct {
	Target, Object, Filter string
	Exposure               time.Duration
	// Binning, Gain, and Offset are the values the sequence sets. A nil
	// value means the sequence leaves the camera's own setting, and
	// renders as "default".
	Binning, Gain, Offset                 *int32
	Camera, Telescope, Train, Reservation string
	// Requested is the time at which the runner requested the exposure.
	Requested time.Time
	// Longitude is the observatory's longitude in degrees east, for
	// {night}.
	Longitude float64
	// Number is the frame's number in its sequence.
	Number  int
	FrameID string
}

// FrameIDToken is the token that renders the frame's FRAMEID. A format
// that contains it gives every frame a unique name, so Name adds no
// suffix.
const FrameIDToken = "{frameid}"

// token matches one token, such as {filter}, in a format.
var token = regexp.MustCompile(`\{[a-z]+\}`)

// Name renders a format into a frame's name: a path relative to the
// archive's path, with no extension.
//
// When the format does not contain {frameid}, Name appends "_" and the
// last 12 hexadecimal digits of the FRAMEID. The INDI driver overwrites
// a file of the same name with no error, and {n} alone does not make
// names unique, because a restart can count one frame twice. The last
// 48 bits of a UUIDv7 are random, so with 1,000 frames the chance that
// two share a suffix is about 2 in a billion.
func Name(format string, v Values) string {
	rendered := token.ReplaceAllStringFunc(format, func(t string) string { return safe(v.value(t)) })
	if !strings.Contains(format, FrameIDToken) {
		rendered += "_" + suffix(v.FrameID)
	}
	segments := strings.Split(rendered, "/")
	for i, segment := range segments {
		segments[i] = safeSegment(segment)
	}
	return strings.Join(segments, "/")
}

// value returns the text that replaces one token. It returns an unknown
// token unchanged, because the CRD already refuses a format with any
// token that is not listed here.
func (v Values) value(t string) string {
	switch t {
	case "{target}":
		return v.Target
	case "{object}":
		return v.Object
	case "{filter}":
		return orNone(v.Filter)
	case "{exposure}":
		return strconv.FormatFloat(v.Exposure.Seconds(), 'f', -1, 64) + "s"
	case "{binning}":
		if v.Binning == nil {
			return "default"
		}
		return fmt.Sprintf("%dx%d", *v.Binning, *v.Binning)
	case "{gain}":
		return orDefault(v.Gain)
	case "{offset}":
		return orDefault(v.Offset)
	case "{camera}":
		return v.Camera
	case "{telescope}":
		return v.Telescope
	case "{train}":
		return orNone(v.Train)
	case "{reservation}":
		return v.Reservation
	case "{night}":
		return Night(v.Requested, v.Longitude)
	case "{datetime}":
		return v.Requested.UTC().Format("20060102T150405Z")
	case "{n}":
		return fmt.Sprintf("%04d", v.Number)
	case FrameIDToken:
		return v.FrameID
	}
	return t
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func orDefault(v *int32) string {
	if v == nil {
		return "default"
	}
	return strconv.Itoa(int(*v))
}

// Night returns the date of the night that t belongs to: the date, in
// local mean solar time at the longitude, of the time 12 hours before
// t. Every exposure from one local noon to the next gets the same date.
func Night(t time.Time, longitude float64) string {
	offset := time.Duration(longitude / 15 * float64(time.Hour))
	return t.UTC().Add(offset - 12*time.Hour).Format(time.DateOnly)
}

// unsafe matches each character that a token's value may not contain.
var unsafe = regexp.MustCompile(`[^A-Za-z0-9._+-]`)

// safe makes a token's value usable in a path. It replaces each
// character that unsafe matches with "_". It also lowercases the
// strings XXX and ISO8601, because the INDI driver replaces them in an
// upload prefix.
func safe(value string) string {
	value = unsafe.ReplaceAllString(value, "_")
	return strings.NewReplacer("XXX", "xxx", "ISO8601", "iso8601").Replace(value)
}

// safeSegment replaces the leading dots of a path segment with one "_".
// Without it, a value could make "..", which leaves the archive, or a
// hidden name like the promotion Job's .part files. It also turns an
// empty segment, which an empty value makes, into "_".
func safeSegment(segment string) string {
	if segment == "" {
		return "_"
	}
	if strings.HasPrefix(segment, ".") {
		return "_" + strings.TrimLeft(segment, ".")
	}
	return segment
}

// suffix returns the last 12 hexadecimal digits of a FRAMEID.
func suffix(frameID string) string {
	digits := strings.ReplaceAll(frameID, "-", "")
	return digits[max(0, len(digits)-12):]
}
