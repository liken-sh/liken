package main

// A pattern:// URI plays a test pattern: a white screen, color bars, or
// a walk of the speakers.
// A pattern is the known ground under the display, so a check of the OSD
// does not depend on finding a bright, flat scene in a film. The files
// are committed in patterns/, the Makefile's patterns target makes them,
// and the player image carries them, so a pattern costs the pod no mount
// and no network.
//
// The URI names the pattern and, optionally, the frame:
// pattern://white, or pattern://bars/1920x1080. A URI with no frame takes
// the frame of the Player's screen, so the pattern fills it with no
// scaling. A URI that names a frame takes it on any screen, which is how a
// scope frame letterboxes on a 16:9 screen on purpose.

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// patternScheme is the scheme of a test pattern URI.
const patternScheme = "pattern"

// patternDir is where the player image carries the pattern files.
const patternDir = "/usr/share/liken/patterns"

// patternForm is the form of a pattern URI, which each refusal of a
// malformed one repeats.
const patternForm = "a pattern URI is pattern://<pattern>/<frame>, and the frame may be left out"

// frame is the size of a pattern's picture, or of a screen, in pixels.
// The zero frame is a screen whose size is not known.
type frame struct {
	width  int
	height int
}

func (f frame) String() string {
	return strconv.Itoa(f.width) + "x" + strconv.Itoa(f.height)
}

// pattern is one test pattern: its name in the URI, the frames the image
// carries it at, and the frame it plays at when the screen is not known
// or no frame has the screen's shape. mpv scales that frame to the
// screen.
type pattern struct {
	name     string
	frames   []frame
	fallback frame
}

// screenFrames are the sizes of real screens and of a scope film: 16:9
// at 720p, 1080p, and 4K, two wide screens, and 1920x804, which
// letterboxes on a 16:9 screen.
var screenFrames = []frame{
	{1280, 720},
	{1920, 1080},
	{3840, 2160},
	{2560, 1080},
	{3840, 1600},
	{1920, 804},
}

// The patterns the image carries, in the order a refusal lists them.
// White is the worst case for a dark scrim over a bright frame, and the
// SMPTE HD bars show a color or a range error at a glance. The speaker
// walk plays pink noise from each speaker of a 7.1 layout in turn and
// names the speaker on the screen; it is for the ears, so it carries one
// small frame. The Makefile encodes the same patterns at the same frames.
var patterns = []pattern{
	{name: "white", frames: screenFrames, fallback: frame{1920, 1080}},
	{name: "bars", frames: screenFrames, fallback: frame{1920, 1080}},
	{name: "speakers", frames: []frame{{1280, 720}}, fallback: frame{1280, 720}},
}

// shapeTolerance is how far apart two ratios of width to height may be
// and still count as one shape. 2560x1080, 3440x1440, and 3840x1600 are
// all sold as 21:9 and differ by under 2 percent.
const shapeTolerance = 0.02

// patternRef is one parsed pattern URI. A zero frame means the URI named
// none.
type patternRef struct {
	pattern pattern
	frame   frame
}

// path is the file the pod plays, at the URI's own frame, or at the
// frame that fits the screen when the URI names none.
func (p patternRef) path(screen frame) string {
	chosen := p.frame
	if chosen == (frame{}) {
		chosen = p.pattern.fit(screen)
	}
	return patternDir + "/" + p.pattern.name + "-" + chosen.String() + ".mkv"
}

// parsePattern reads the pattern and the frame from one pattern URI. A
// pattern, or a frame the image does not carry that pattern at, fails,
// and the message lists what it carries.
func parsePattern(parsed *url.URL, raw string) (*patternRef, error) {
	if parsed.Host == "" {
		return nil, fmt.Errorf("the URI %q names no pattern; %s", raw, patternForm)
	}
	index := slices.IndexFunc(patterns, func(known pattern) bool { return known.name == parsed.Host })
	if index < 0 {
		names := make([]string, len(patterns))
		for index, known := range patterns {
			names[index] = known.name
		}
		return nil, fmt.Errorf("the URI %q names the pattern %s; the patterns are %s",
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
		return nil, fmt.Errorf("the URI %q names the frame %s; the pattern %s has the frames %s",
			raw, segments[0], chosen.name, strings.Join(names, ", "))
	default:
		return nil, fmt.Errorf("the URI %q names more than a pattern and a frame; %s", raw, patternForm)
	}
}

// fit chooses the frame for a screen: the largest frame of the screen's
// shape that fits on it, so mpv shows it with no scaling or scales it up
// by the least. A screen smaller than every frame of its shape takes the
// smallest of them. A screen of no known shape, or no known size, takes
// the fallback.
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

// screenFrame is the size of the compositor's canvas on one Display, from
// a mode such as 1920x1080@60, because the canvas is what mpv draws on. A
// mode that is absent or does not parse gives the zero frame.
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

// patternScreen is the frame of the screen a Player shows on, for a
// pattern URI with no frame. A Player whose screen or Display the pass
// cannot read gives the zero frame, and the pattern plays at the
// fallback.
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
