package main

// A pattern:// URI plays a test pattern: a white screen, color bars, or
// a speaker walk. A pattern is known content, so checking the on-screen
// display does not depend on finding a bright, flat scene in a film. The
// files are committed in patterns/, and the player image copies them in,
// so playing a pattern needs no volume mount and no network.
//
// The URI names the pattern and, optionally, the frame:
// pattern://white, or pattern://bars/1920x1080. Without a frame, the
// pattern uses the frame of the Player's screen, so it fills the screen
// with no scaling. With a frame, it uses that frame on any screen, which
// is how a scope frame letterboxes on a 16:9 screen on purpose.

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

const patternScheme = "pattern"

// patternDir is the directory of the pattern files in the player image.
const patternDir = "/usr/share/liken/patterns"

// patternForm is the URI syntax that each error for a malformed pattern
// URI repeats.
const patternForm = "a pattern URI is pattern://<pattern>/<frame>, and the frame may be left out"

// frame is the size of a pattern's picture, or of a screen, in pixels.
// The zero frame means the size is unknown.
type frame struct {
	width  int
	height int
}

func (f frame) String() string {
	return strconv.Itoa(f.width) + "x" + strconv.Itoa(f.height)
}

// pattern is one test pattern: its name in the URI, the frames the image
// has it at, and the fallback frame. The fallback is used when the
// screen size is unknown or no frame has the screen's shape, and mpv
// scales it to the screen.
type pattern struct {
	name     string
	frames   []frame
	fallback frame
}

// screenFrames are the frames of common screens and of a scope film:
// 16:9 at 720p, 1080p, and 4K, two ultrawide screens, and 1920x804,
// which letterboxes on a 16:9 screen.
var screenFrames = []frame{
	{1280, 720},
	{1920, 1080},
	{3840, 2160},
	{2560, 1080},
	{3840, 1600},
	{1920, 804},
}

// The patterns in the image, in the order the error for an unknown
// pattern lists them. White is the worst case for the dark backgrounds
// drawn behind the on-screen display, and the SMPTE HD bars show a color
// or range error at a glance. The speaker walk plays pink noise from
// each speaker of a 7.1 layout in turn and shows the speaker's name on
// the screen. It is meant to be heard, so it has one small frame. The
// Makefile's patterns target must encode the same patterns at the same
// frames.
var patterns = []pattern{
	{name: "white", frames: screenFrames, fallback: frame{1920, 1080}},
	{name: "bars", frames: screenFrames, fallback: frame{1920, 1080}},
	{name: "speakers", frames: []frame{{1280, 720}}, fallback: frame{1280, 720}},
}

// shapeTolerance is how far apart two width-to-height ratios may be, as
// a fraction of the smaller, and still count as the same shape.
// 2560x1080, 3440x1440, and 3840x1600 are all sold as 21:9 and differ
// by under 2 percent.
const shapeTolerance = 0.02

// patternRef is one parsed pattern URI. A zero frame means the URI had
// no frame.
type patternRef struct {
	pattern pattern
	frame   frame
}

// path returns the file the pod plays: the URI's frame, or the frame
// that fits screen when the URI has none.
func (p patternRef) path(screen frame) string {
	chosen := p.frame
	if chosen == (frame{}) {
		chosen = p.pattern.fit(screen)
	}
	return patternDir + "/" + p.pattern.name + "-" + chosen.String() + ".mkv"
}

// parsePattern reads the pattern and the frame from one pattern URI. It
// fails for a pattern or a frame that the image does not have, and the
// error lists the ones it has.
func parsePattern(parsed *url.URL, raw string) (*patternRef, error) {
	if parsed.Host == "" {
		return nil, fmt.Errorf("the URI %q names no pattern: %s", raw, patternForm)
	}
	index := slices.IndexFunc(patterns, func(known pattern) bool { return known.name == parsed.Host })
	if index < 0 {
		names := make([]string, len(patterns))
		for index, known := range patterns {
			names[index] = known.name
		}
		return nil, fmt.Errorf("the URI %q names the pattern %s, and the image has the patterns %s",
			raw, parsed.Host, strings.Join(names, ", "))
	}
	chosen := patterns[index]
	segments := splitPath(parsed.Path)
	switch len(segments) {
	case 0:
		return &patternRef{pattern: chosen}, nil
	case 1:
		for _, known := range chosen.frames {
			if known.String() == segments[0] {
				return &patternRef{pattern: chosen, frame: known}, nil
			}
		}
		names := make([]string, len(chosen.frames))
		for index, known := range chosen.frames {
			names[index] = known.String()
		}
		return nil, fmt.Errorf("the URI %q names the frame %s, and the pattern %s has the frames %s",
			raw, segments[0], chosen.name, strings.Join(names, ", "))
	default:
		return nil, fmt.Errorf("the URI %q has more than a pattern and a frame: %s", raw, patternForm)
	}
}

// fit chooses the frame for a screen: the largest frame of the screen's
// shape that fits within it, so mpv does not scale it or scales it up as
// little as possible. A screen smaller than every frame of its shape
// gets the smallest of them. A screen with no known size, or with no
// frame of its shape, gets the fallback.
func (p pattern) fit(screen frame) frame {
	if screen.width <= 0 || screen.height <= 0 {
		return p.fallback
	}
	ratio := float64(screen.width) / float64(screen.height)
	var fits, smallest frame
	for _, candidate := range p.frames {
		shape := float64(candidate.width) / float64(candidate.height)
		if shape/ratio > 1+shapeTolerance || ratio/shape > 1+shapeTolerance {
			continue
		}
		if candidate.width <= screen.width && candidate.height <= screen.height &&
			candidate.width*candidate.height > fits.width*fits.height {
			fits = candidate
		}
		if smallest == (frame{}) || candidate.width*candidate.height < smallest.width*smallest.height {
			smallest = candidate
		}
	}
	switch {
	case fits != frame{}:
		return fits
	case smallest != frame{}:
		return smallest
	}
	return p.fallback
}

// screenFrame returns the size of the compositor's canvas on a Display,
// read from a mode such as 1920x1080@60, because mpv draws on the
// canvas. It returns the zero frame when the mode is absent or does not
// parse.
func screenFrame(display *Display) frame {
	if display == nil {
		return frame{}
	}
	size, _, _ := strings.Cut(display.Status.Mode.Weston, "@")
	width, height, found := strings.Cut(size, "x")
	if !found {
		return frame{}
	}
	w, errW := strconv.Atoi(width)
	h, errH := strconv.Atoi(height)
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return frame{}
	}
	return frame{width: w, height: h}
}

// patternScreen returns the frame of the screen a Player shows on, for
// a pattern URI with no frame. It returns the zero frame when the pass
// cannot read the Player's screen or Display, and the pattern then
// plays at its fallback frame.
func (o *operator) patternScreen(player *Player) frame {
	lookup := o.screenLookup()
	found, ok := lookup.screenFor(player)
	if !ok {
		return frame{}
	}
	display, err := lookup.displayFor(found.monitor)
	if err != nil {
		return frame{}
	}
	return screenFrame(display)
}
