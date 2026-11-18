package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A Dockerfile lists what one Dockerfile builds from: the images of its
// FROM lines, the sources of its COPY --from and RUN --mount from=
// flags, and the files that its ADD lines download. From and Sources
// leave out a name that an earlier stage declared with AS, because the
// name refers to that stage and not to an image. Sources also leaves out
// the number of an earlier stage, as in COPY --from=0.
type Dockerfile struct {
	From    []string
	Sources []string
	Remotes []Remote
	Stages  []Stage
	// Args are the names of the build arguments that the file's ARG
	// lines declare, in any stage.
	Args []string
	// Escape is true when the file starts with an escape parser
	// directive. The directive changes the line continuation character,
	// and this parser reads only a backslash as the continuation.
	Escape bool
}

// A Stage is one FROM line and the instructions under it.
type Stage struct {
	Name string
	// From is the image or the earlier stage that the stage starts
	// from, and Sources are the images or stages it copies from.
	From    string
	Sources []string
	// Runs are the commands of the stage's RUN lines without their
	// flags, in order.
	Runs []string
}

// BuildKit reads parser directives only at the top of the file. The
// first line that is not a directive, such as a comment, an empty line,
// or an instruction, ends them.
var escapeDirective = regexp.MustCompile(`(?i)^#\s*escape\s*=`)

var directive = regexp.MustCompile(`^#\s*[A-Za-z]+\s*=`)

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
// the next lines into one line. BuildKit skips comment lines and empty
// lines inside a continued instruction, so the join skips them too.
func instructions(text string) []string {
	var lines []string
	current := ""
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if current != "" && (trimmed == "" || strings.HasPrefix(trimmed, "#")) {
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
	for _, line := range strings.Split(text, "\n") {
		if !directive.MatchString(strings.TrimSpace(line)) {
			break
		}
		if escapeDirective.MatchString(strings.TrimSpace(line)) {
			d.Escape = true
		}
	}
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
		case "ARG":
			for _, field := range fields[1:] {
				name, _, _ := strings.Cut(field, "=")
				d.Args = appendOnce(d.Args, name)
			}
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
			if n := len(d.Stages); n > 0 && instruction == "RUN" {
				d.Stages[n-1].Runs = append(d.Stages[n-1].Runs, strings.Join(withoutFlags(fields[1:]), " "))
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
// stage reads: the target's own, and those of every stage that it builds
// on or copies from, directly or through other stages. A target that
// matches no stage name, such as "", is the last stage, which a build with no
// target builds.
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

// FromImage returns the image that the target stage starts from. It
// follows FROM through earlier stages until it reaches a name that is
// not a stage. A target that matches no stage name is the last stage, and a
// file with no stage returns "". The target's config starts as a copy
// of that image's config, so the target inherits each of its labels,
// and a Dockerfile can overwrite an inherited label but not remove it.
func (d Dockerfile) FromImage(target string) string {
	byName := map[string]int{}
	for i, stage := range d.Stages {
		if stage.Name != "" {
			byName[stage.Name] = i
		}
	}
	i := len(d.Stages) - 1
	if j, ok := byName[target]; ok {
		i = j
	}
	for i >= 0 {
		from := d.Stages[i].From
		if j, ok := byName[from]; ok && j < i {
			i = j
		} else if j, err := strconv.Atoi(from); err == nil && j >= 0 && j < i {
			i = j
		} else {
			return from
		}
	}
	return ""
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

// aptCommand matches apt or apt-get run as a command, and not a path
// such as /var/lib/apt/lists.
var aptCommand = regexp.MustCompile(`(^|[\s;&|(])apt(-get)?\s`)

// SnapshotFirst checks that each stage that runs apt runs snapshot.sh
// before its first apt command, in its own RUN lines or in the stage
// that its FROM refers to. Without snapshot.sh, apt installs from Debian's
// current archive and not from the pinned snapshot date.
func (d Dockerfile) SnapshotFirst() error {
	pinned := map[string]bool{}
	for i, stage := range d.Stages {
		name := stage.Name
		if name == "" {
			name = strconv.Itoa(i)
		}
		ready := pinned[stage.From]
		for _, run := range stage.Runs {
			apt := aptCommand.FindStringIndex(run)
			snapshot := strings.Index(run, "snapshot.sh")
			if apt != nil && !ready && (snapshot < 0 || snapshot > apt[0]) {
				return fmt.Errorf("the stage %s runs apt before it runs snapshot.sh, so apt reads Debian's moving archive and not the snapshot", name)
			}
			if snapshot >= 0 {
				ready = true
			}
		}
		pinned[name] = ready
	}
	return nil
}
