package promote

// The name of a frame, from the target's archive.format (plan 03). The
// runner renders it before each exposure, because the driver takes the
// name before it writes the file, and the frame keeps the name from the
// outbox to the archive. So the promotion Job never renames a frame,
// and a person finds each frame under the name they chose.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Values are what the tokens of a format name.
type Values struct {
	Target, Object, Filter string
	Exposure               time.Duration
	// Binning, Gain, and Offset are the sequence's values. An absent
	// value leaves the camera's own, and renders as "default".
	Binning, Gain, Offset                 *int32
	Camera, Telescope, Train, Reservation string
	// Requested is when the runner requested the exposure.
	Requested time.Time
	// Longitude is the observatory's, in degrees east, for {night}.
	Longitude float64
	// Number is the frame's number in its sequence.
	Number  int
	FrameID string
}

// FrameIDToken is the token of the frame's identity. A format that
// holds it names each frame uniquely, so the name takes no suffix.
const FrameIDToken = "{frameid}"

// token matches one token of a format.
var token = regexp.MustCompile(`\{[a-z]+\}`)

// Name renders a format into a frame's name: a path relative to the
// archive's path, with no extension.
//
// Unless the format holds {frameid}, the name ends with "_" and the
// last 12 hexadecimal digits of the FRAMEID. Those digits are random,
// so two frames of one target meet with a chance near 2 in a billion
// at 1,000 frames. The driver replaces a frame of the same name with no
// message, and {n} alone does not prevent that, because a restart can
// count one frame twice.
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

// value answers what a token names. The CRD refuses a format with any
// other token.
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

// Night answers the date of the night of a time: the local mean solar
// date at a longitude, in degrees east, 12 hours before the time. So
// every exposure from noon to noon at the observatory has one date.
func Night(t time.Time, longitude float64) string {
	offset := time.Duration(longitude / 15 * float64(time.Hour))
	return t.UTC().Add(offset - 12*time.Hour).Format(time.DateOnly)
}

// unsafe matches each character that a token's value cannot hold.
var unsafe = regexp.MustCompile(`[^A-Za-z0-9._+-]`)

// safe makes a token's value safe in a path: each other character
// becomes "_", and the driver's patterns XXX and ISO8601, which it
// replaces in a prefix, become lower case.
func safe(value string) string {
	value = unsafe.ReplaceAllString(value, "_")
	return strings.NewReplacer("XXX", "xxx", "ISO8601", "iso8601").Replace(value)
}

// safeSegment keeps a part of the path inside the archive and visible:
// a part that starts with "." becomes one that starts with "_", so a
// value cannot make "..", and no frame hides beside the .part files of
// the promotion Job. An empty part, from an empty value, becomes "_".
func safeSegment(segment string) string {
	if segment == "" {
		return "_"
	}
	if strings.HasPrefix(segment, ".") {
		return "_" + strings.TrimLeft(segment, ".")
	}
	return segment
}

// suffix answers the last 12 hexadecimal digits of a FRAMEID.
func suffix(frameID string) string {
	digits := strings.ReplaceAll(frameID, "-", "")
	return digits[max(0, len(digits)-12):]
}
