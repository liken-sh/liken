package main

import (
	"reflect"
	"testing"
)

func TestADockerfileNamesWhatItBuildsFrom(t *testing.T) {
	cases := map[string]struct {
		text          string
		from, sources []string
	}{
		"an image and a stage": {
			text: "FROM golang:1.27 AS build\nFROM mpv\nCOPY --from=build /x /x\n",
			from: []string{"golang:1.27", "mpv"},
		},
		"a stage that follows is a stage": {
			text: "FROM vulkan AS base\nfrom base\n",
			from: []string{"vulkan"},
		},
		"a platform flag": {
			text: "FROM --platform=$BUILDPLATFORM debian@sha256:abc AS build\n",
			from: []string{"debian@sha256:abc"},
		},
		"copy and mount sources": {
			text:    "FROM scratch\nCOPY --chown=1:1 --from=brand fonts/ /f/\nRUN --mount=type=bind,from=vulkan,target=/v true\nCOPY --from=vulkan / /\n",
			from:    []string{"scratch"},
			sources: []string{"brand", "vulkan"},
		},
		"comments and blank lines": {
			text: "# FROM nothing\n\n   \nFROM scratch\n",
			from: []string{"scratch"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			d := parseDockerfile(c.text)
			if !reflect.DeepEqual(d.From, c.from) || !reflect.DeepEqual(d.Sources, c.sources) {
				t.Errorf("from %v, sources %v", d.From, d.Sources)
			}
		})
	}
}

func TestTheBasesAreTheImagesOfTheRepository(t *testing.T) {
	d := parseDockerfile("FROM golang AS build\nFROM mpv\nCOPY --from=brand f /\nCOPY --from=media-operator Cargo.toml /\nCOPY --from=vulkan / /v\n")
	producer := map[string]string{"mpv": "mpv", "vulkan": "vulkan", "media-operator": "media-operator"}
	image := Image{Contexts: map[string]string{"brand": "brand", "media-operator": "media-operator"}}
	if got := d.Bases(image, producer); !reflect.DeepEqual(got, []string{"mpv", "vulkan"}) {
		t.Errorf("bases %v", got)
	}
}

func TestATargetReadsOnlyTheStagesItReaches(t *testing.T) {
	d := parseDockerfile(`FROM golang AS build
FROM debian AS fonts
FROM weston AS operator
COPY --from=build /x /x
FROM ffmpeg AS capture
COPY --from=build /x /x
COPY --from=fonts /f /f
FROM scratch AS api
COPY --from=build /x /x
`)
	cases := map[string][]string{
		"operator": {"weston", "golang"},
		"capture":  {"ffmpeg", "golang", "debian"},
		"api":      {"scratch", "golang"},
		"":         {"scratch", "golang"},
	}
	for target, want := range cases {
		t.Run(target, func(t *testing.T) {
			if got := d.Reachable(target); !reflect.DeepEqual(got, want) {
				t.Errorf("%q reaches %v, want %v", target, got, want)
			}
		})
	}
}

func TestAnInstructionContinuesOverLines(t *testing.T) {
	d := parseDockerfile("FROM \\\n  debian@sha256:aaa \\\n  AS build\n" +
		"RUN --mount=type=bind,from=busybox:1,target=/b \\\n# a comment inside the instruction\n  true\n" +
		"COPY \\\n  --from=vulkan / /\n")
	if !reflect.DeepEqual(d.From, []string{"debian@sha256:aaa"}) || !reflect.DeepEqual(d.Sources, []string{"busybox:1", "vulkan"}) ||
		d.Stages[0].Name != "build" {
		t.Errorf("from %v, sources %v, stages %+v", d.From, d.Sources, d.Stages)
	}
}

func TestANumberNamesAStage(t *testing.T) {
	d := parseDockerfile("FROM golang AS build\nFROM weston\nCOPY --from=0 /x /x\nCOPY --from=7 /y /y\n")
	if !reflect.DeepEqual(d.Sources, []string{"7"}) {
		t.Errorf("sources %v", d.Sources)
	}
	if got := d.Reachable(""); !reflect.DeepEqual(got, []string{"weston", "golang", "7"}) {
		t.Errorf("reachable %v", got)
	}
}

func TestAnAddFromTheNetworkIsARemoteSource(t *testing.T) {
	d := parseDockerfile("FROM scratch\nADD --checksum=sha256:abc https://example.com/x.tar.gz /\n" +
		"ADD git@github.com:x/y.git /src\nADD --chown=1:1 local.tar other.tar /\nADD [\"https://example.com/z\", \"/z\"]\n")
	want := []Remote{{"https://example.com/x.tar.gz", "sha256:abc"}, {"git@github.com:x/y.git", ""}, {"https://example.com/z", ""}}
	if !reflect.DeepEqual(d.Remotes, want) {
		t.Errorf("remotes %+v", d.Remotes)
	}
}
