package main

import (
	"os"
	"slices"
	"strings"
)

// A Dockerfile is what one Dockerfile builds from: the images its FROM
// lines name, and the sources its COPY --from and RUN --mount from=
// flags name. A name that an earlier stage of the same file declares
// with AS is the stage, not an image, so neither list holds it.
type Dockerfile struct {
	From    []string
	Sources []string
	Stages  []Stage
}

// A Stage is one FROM line and the instructions under it.
type Stage struct {
	Name string
	// From is the image or the earlier stage that the stage starts
	// from, and Sources are the images or stages it copies from.
	From    string
	Sources []string
}

func readDockerfile(path string) (Dockerfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Dockerfile{}, err
	}
	return parseDockerfile(string(data)), nil
}

func parseDockerfile(text string) Dockerfile {
	var d Dockerfile
	stages := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "FROM":
			args := withoutFlags(fields[1:])
			if len(args) == 0 {
				continue
			}
			stage := Stage{From: args[0]}
			if !stages[args[0]] {
				d.From = appendOnce(d.From, args[0])
			}
			if len(args) == 3 && strings.EqualFold(args[1], "AS") {
				stage.Name = args[2]
				stages[args[2]] = true
			}
			d.Stages = append(d.Stages, stage)
		case "COPY", "ADD", "RUN":
			for _, field := range fields[1:] {
				if !strings.HasPrefix(field, "--") {
					break
				}
				source := fromFlag(field)
				if source == "" {
					continue
				}
				if !stages[source] {
					d.Sources = appendOnce(d.Sources, source)
				}
				if n := len(d.Stages); n > 0 {
					d.Stages[n-1].Sources = appendOnce(d.Stages[n-1].Sources, source)
				}
			}
		}
	}
	return d
}

// Reachable lists the images and sources that the build of the target
// stage reads: the target's own, and those of every stage it builds on
// or copies from, through the whole file. An empty target is the last
// stage, the one a build with no target builds.
func (d Dockerfile) Reachable(target string) []string {
	byName := map[string]int{}
	for i, stage := range d.Stages {
		if stage.Name != "" {
			byName[stage.Name] = i
		}
	}
	start := len(d.Stages) - 1
	if i, ok := byName[target]; ok {
		start = i
	}
	var names []string
	seen := map[int]bool{}
	var visit func(i int)
	visit = func(i int) {
		if i < 0 || seen[i] {
			return
		}
		seen[i] = true
		for _, name := range append([]string{d.Stages[i].From}, d.Stages[i].Sources...) {
			if j, ok := byName[name]; ok && j < i {
				visit(j)
			} else {
				names = appendOnce(names, name)
			}
		}
	}
	visit(start)
	return names
}

// withoutFlags drops the leading --flag=value fields of an
// instruction, such as FROM --platform=$BUILDPLATFORM.
func withoutFlags(fields []string) []string {
	for len(fields) > 0 && strings.HasPrefix(fields[0], "--") {
		fields = fields[1:]
	}
	return fields
}

// fromFlag reads the source of COPY --from=x or of RUN
// --mount=type=cache,from=x.
func fromFlag(field string) string {
	if source, ok := strings.CutPrefix(field, "--from="); ok {
		return source
	}
	if mount, ok := strings.CutPrefix(field, "--mount="); ok {
		for _, option := range strings.Split(mount, ",") {
			if source, ok := strings.CutPrefix(option, "from="); ok {
				return source
			}
		}
	}
	return ""
}

func appendOnce(list []string, value string) []string {
	if slices.Contains(list, value) {
		return list
	}
	return append(list, value)
}

// ImageProducers maps each image of the repository to the component
// that builds it. The bake file builds each image as a target of that
// name, so a Dockerfile that says FROM mpv starts from the mpv target.
func ImageProducers(components map[string]*Component) map[string]string {
	producer := map[string]string{}
	for _, c := range components {
		for _, image := range c.Outputs.Images {
			producer[image.Name] = c.Name()
		}
	}
	return producer
}

// Bases lists the images of the repository that the image's build
// reads, from the stages its target reaches. A source that the image
// declares as a named directory context is that directory, not an
// image, even when an image has the same name.
func (d Dockerfile) Bases(image Image, producer map[string]string) []string {
	var bases []string
	for _, name := range d.Reachable(image.Target) {
		if _, dir := image.Contexts[name]; dir {
			continue
		}
		if _, ok := producer[name]; ok {
			bases = appendOnce(bases, name)
		}
	}
	return bases
}
