package main

import (
	"reflect"
	"strings"
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

func TestAnEmptyLineInsideAnInstructionDoesNotEndIt(t *testing.T) {
	d := parseDockerfile("FROM scratch\nCOPY \\\n\n  --from=debian:trixie-slim / /\n")
	if !reflect.DeepEqual(d.Sources, []string{"debian:trixie-slim"}) {
		t.Errorf("sources %v", d.Sources)
	}
}

func TestAnEscapeDirectiveIsRead(t *testing.T) {
	cases := map[string]bool{
		"# escape=`\nFROM scratch\n":                                true,
		"# syntax=docker/dockerfile:1\n#Escape = `\nFROM scratch\n": true,
		"# syntax=docker/dockerfile:1\nFROM scratch\n":              false,
		"# a comment\n# escape=`\nFROM scratch\n":                   false,
	}
	for text, escape := range cases {
		t.Run(text, func(t *testing.T) {
			if got := parseDockerfile(text).Escape; got != escape {
				t.Errorf("escape %v", got)
			}
		})
	}
}

func TestEachStageThatRunsAptPointsItAtTheSnapshotFirst(t *testing.T) {
	cases := map[string]struct{ text, wants string }{
		"the same RUN, snapshot first": {"FROM d AS a\nRUN sh /snapshot.sh \"$V\" && apt-get install -y x\n", ""},
		"an earlier RUN":               {"FROM d AS a\nRUN sh /snapshot.sh 1\nRUN apt install x\n", ""},
		"a stage that inherits it":     {"FROM d AS a\nRUN sh /snapshot.sh 1\nFROM a AS b\nRUN apt-get install x\n", ""},
		"a path with apt in it":        {"FROM d AS a\nRUN rm -rf /var/lib/apt/lists\n", ""},
		"no snapshot":                  {"FROM d AS a\nRUN apt-get update\n", "the stage a runs apt"},
		"apt before the snapshot":      {"FROM d AS a\nRUN apt-get update && sh /snapshot.sh 1\n", "the stage a runs apt"},
		"an unnamed stage":             {"FROM d\nRUN true\nRUN apt-get update\n", "the stage 0 runs apt"},
		"a stage from another image":   {"FROM d AS a\nRUN sh /snapshot.sh 1\nFROM d AS b\nRUN apt-get install x\n", "the stage b runs apt"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := parseDockerfile(c.text).SnapshotFirst()
			if c.wants == "" && err != nil || c.wants != "" && (err == nil || !strings.Contains(err.Error(), c.wants)) {
				t.Errorf("got %v, want %q", err, c.wants)
			}
		})
	}
}
