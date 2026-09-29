package main

import (
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
)

// A Dockerfile is what one Dockerfile builds from: the images its FROM
// lines name, the sources its COPY --from and RUN --mount from= flags
// name, and the files its ADD lines fetch from the network. A name that
// an earlier stage of the same file declares with AS, or a number that
// counts an earlier stage, is that stage, not an image, so neither list
// holds it.
type Dockerfile struct {
	From    []string
	Sources []string
	Remotes []Remote
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

// A Remote is one source of an ADD line that the build fetches from
// the network, with the checksum that its --checksum flag states.
type Remote struct {
	URL, Checksum string
}

func readDockerfile(path string) (Dockerfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Dockerfile{}, err
	}
	return parseDockerfile(string(data)), nil
}

// instructions joins each instruction that a backslash continues onto
// the next lines into one line. Docker skips a comment line inside an
// instruction, so the join does too.
func instructions(text string) []string {
	var lines []string
	current := ""
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if current != "" && strings.HasPrefix(trimmed, "#") {
			continue
		}
		if rest, ok := strings.CutSuffix(strings.TrimRight(line, " \t"), "\\"); ok {
			current += rest + " "
			continue
		}
		lines = append(lines, current+line)
		current = ""
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func parseDockerfile(text string) Dockerfile {
	var d Dockerfile
	stages := map[string]bool{}
	for _, line := range instructions(text) {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		switch instruction := strings.ToUpper(fields[0]); instruction {
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
			checksum := ""
			for _, field := range fields[1:] {
				if !strings.HasPrefix(field, "--") {
					break
				}
				if value, ok := strings.CutPrefix(field, "--checksum="); ok {
					checksum = value
				}
				source := fromFlag(field)
				if source == "" {
					continue
				}
				if !stages[source] && !d.isStageNumber(source) {
					d.Sources = appendOnce(d.Sources, source)
				}
				if n := len(d.Stages); n > 0 {
					d.Stages[n-1].Sources = appendOnce(d.Stages[n-1].Sources, source)
				}
			}
			if instruction == "ADD" {
				for _, source := range addSources(fields[1:]) {
					if strings.Contains(source, "://") || strings.HasPrefix(source, "git@") {
						d.Remotes = append(d.Remotes, Remote{source, checksum})
					}
				}
			}
		}
	}
	return d
}

// isStageNumber is true for a number that counts one of the stages
// before the current one, the way COPY --from=0 names the first stage.
func (d Dockerfile) isStageNumber(name string) bool {
	n, err := strconv.Atoi(name)
	return err == nil && n >= 0 && n < len(d.Stages)-1
}

// addSources lists the sources of an ADD line: every argument after the
// flags but the last, which is the destination, in either the plain
// form or the JSON form.
func addSources(fields []string) []string {
	args := withoutFlags(fields)
	joined := strings.Join(args, " ")
	if strings.HasPrefix(joined, "[") {
		var list []string
		if json.Unmarshal([]byte(joined), &list) == nil {
			args = list
		}
	}
	if len(args) < 2 {
		return nil
	}
	return args[:len(args)-1]
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
			} else if j, err := strconv.Atoi(name); err == nil && j >= 0 && j < i {
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
