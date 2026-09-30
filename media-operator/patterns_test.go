package main

// These tests cover what a pattern:// URI resolves to: a file the player
// image carries, at the frame the URI names, or at the frame of the
// Player's screen when it names none. A pattern costs the pod no mount.

import (
	"strings"
	"testing"
)

func TestResolveAPatternToTheFileInTheImage(t *testing.T) {
	cases := []struct {
		uri    string
		screen frame
		want   string
	}{
		{"pattern://white/1920x1080", frame{}, "/usr/share/liken/patterns/white-1920x1080.mkv"},
		{"pattern://bars/3840x2160", frame{width: 1280, height: 720}, "/usr/share/liken/patterns/bars-3840x2160.mkv"},
		{"pattern://bars/1920x804", frame{width: 1920, height: 1080}, "/usr/share/liken/patterns/bars-1920x804.mkv"},
		{"pattern://white", frame{width: 1920, height: 1080}, "/usr/share/liken/patterns/white-1920x1080.mkv"},
	}
	for _, c := range cases {
		t.Run(c.uri, func(t *testing.T) {
			resolved, err := resolvePlay("house", mediaItems(c.uri), nil, c.screen)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Items[0] != c.want {
				t.Errorf("item = %q, want %q", resolved.Items[0], c.want)
			}
			if len(resolved.Volumes) != 0 || len(resolved.Mounts) != 0 {
				t.Errorf("volumes = %+v, mounts = %+v, want none", resolved.Volumes, resolved.Mounts)
			}
		})
	}
}

// A URI with no frame takes the largest frame of the screen's shape that
// fits on the screen, and 1920x1080 when no frame has the screen's shape
// or the screen is not known.
func TestAPatternWithNoFrameFitsTheScreen(t *testing.T) {
	cases := []struct {
		name   string
		screen frame
		want   frame
	}{
		{"a 4K screen", frame{3840, 2160}, frame{3840, 2160}},
		{"a 1080p screen", frame{1920, 1080}, frame{1920, 1080}},
		{"a 720p screen", frame{1280, 720}, frame{1280, 720}},
		{"a 1440p screen", frame{2560, 1440}, frame{1920, 1080}},
		{"a 3840x1600 screen", frame{3840, 1600}, frame{3840, 1600}},
		{"a 2560x1080 screen", frame{2560, 1080}, frame{2560, 1080}},
		{"a 3440x1440 screen", frame{3440, 1440}, frame{2560, 1080}},
		{"a 16:9 screen smaller than every frame", frame{1024, 576}, frame{1280, 720}},
		{"a 4:3 screen", frame{1024, 768}, frame{1920, 1080}},
		{"an unknown screen", frame{}, frame{1920, 1080}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fitFrame(c.screen); got != c.want {
				t.Errorf("fitFrame(%v) = %v, want %v", c.screen, got, c.want)
			}
		})
	}
}

// A pattern or a frame the image does not carry fails the Play, and the
// message lists what the image carries.
func TestResolveRefusesAPatternTheImageDoesNotCarry(t *testing.T) {
	cases := []struct {
		uri  string
		want string
	}{
		{"pattern://purple", "white, bars"},
		{"pattern://white/1024x768", "1280x720, 1920x1080"},
		{"pattern://white/1920x1080/extra", "pattern://<pattern>/<frame>"},
		{"pattern:///1920x1080", "names no pattern"},
	}
	for _, c := range cases {
		t.Run(c.uri, func(t *testing.T) {
			_, err := resolvePlay("house", mediaItems(c.uri), nil, frame{})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want one that says %q", err, c.want)
			}
		})
	}
}

// A pattern is something to play, not a picture to draw, so a logo or
// cover art that names one fails the Play.
func TestResolveRefusesAPatternAsArt(t *testing.T) {
	items := []PlayItem{{
		URI:          "pattern://white",
		Presentation: &Presentation{Logo: "pattern://bars"},
	}}
	_, err := resolvePlay("house", items, nil, frame{})
	if err == nil || !strings.Contains(err.Error(), "plays as an item") {
		t.Errorf("err = %v, want a refusal of a pattern as art", err)
	}
}

// The frame of a screen comes from the compositor's mode on its Display,
// because the compositor's canvas is what mpv draws on.
func TestTheScreenFrameIsTheCompositorsMode(t *testing.T) {
	cases := []struct {
		name string
		mode string
		want frame
	}{
		{"a mode with a rate", "1920x1080@60", frame{1920, 1080}},
		{"a mode with no rate", "3840x1600", frame{3840, 1600}},
		{"no mode", "", frame{}},
		{"a mode that does not parse", "wide", frame{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			display := &Display{Status: DisplayStatus{Mode: DisplayMode{Weston: c.mode}}}
			if got := screenFrame(display); got != c.want {
				t.Errorf("screenFrame(%q) = %v, want %v", c.mode, got, c.want)
			}
		})
	}
}
