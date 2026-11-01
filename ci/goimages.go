package main

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// goImageFindings lists each place where a Dockerfile builds or ships
// a Go program in a way other than the one form that root AGENTS.md
// states under "Go programs":
//
//   - each `go build` sets CGO_ENABLED=0 and -trimpath, so the binary
//     is static and holds no path of the build machine;
//   - each `go build` passes -ldflags with "-s -w", which drops the
//     symbol table and the DWARF data and keeps the build info, the
//     module list that syft and Trivy read from the binary;
//   - each `go build` writes its program to /<name>, each COPY of the
//     program puts it at that same path, and an ENTRYPOINT that runs
//     it names that path. A person, a manifest, and an operator that
//     writes a pod's command find every Go program in every image at
//     the same place;
//   - no upx, strip, or objcopy runs on the program. upx and objcopy
//     can hide or remove the build info, and -ldflags "-s -w" already
//     strips what strip would.
//
// The check reads the RUN, COPY, and ENTRYPOINT instructions with
// patterns, so a build that a script runs is outside it.
func goImageFindings(file, text string) []string {
	var findings []string
	outputs := map[string]bool{}
	byName := map[string]string{}
	for _, l := range logicalLines(text) {
		instruction, rest, _ := strings.Cut(strings.TrimSpace(l.text), " ")
		switch strings.ToUpper(instruction) {
		case "RUN":
			for _, command := range shellCommands(rest) {
				if !isGoBuild(command) {
					continue
				}
				found, output := goBuildFindings(file, l.n, command)
				findings = append(findings, found...)
				if output != "" {
					outputs[output] = true
					byName[path.Base(output)] = output
				}
			}
			for _, command := range shellCommands(rest) {
				findings = append(findings, strippers(file, l.n, command, outputs)...)
			}
		case "COPY":
			findings = append(findings, goCopies(file, l.n, rest, outputs)...)
		case "ENTRYPOINT":
			var argv []string
			if json.Unmarshal([]byte(strings.TrimSpace(rest)), &argv) != nil || len(argv) == 0 {
				continue
			}
			if want, ok := byName[path.Base(argv[0])]; ok && argv[0] != want {
				findings = append(findings, fmt.Sprintf("%s:%d runs %s; the entrypoint of a Go program is %s", file, l.n, argv[0], want))
			}
		}
	}
	return findings
}

// shellSeparator matches the operators that join the commands of a RUN
// line.
var shellSeparator = regexp.MustCompile(`&&|\|\||;`)

// shellCommands splits a RUN line into its commands.
func shellCommands(run string) []string {
	return shellSeparator.Split(run, -1)
}

// isGoBuild is true for a command that runs `go build`.
func isGoBuild(command string) bool {
	words := strings.Fields(command)
	for i := 0; i+1 < len(words); i++ {
		if words[i] == "go" && words[i+1] == "build" {
			return true
		}
	}
	return false
}

// stripLdflags matches -ldflags whose value starts with -s -w, quoted
// or not, with = or a space.
var stripLdflags = regexp.MustCompile(`-ldflags[= ]["']?-s -w\b`)

// goBuildFindings checks one `go build` command and returns its
// findings and the path it writes, or no path when it names none.
func goBuildFindings(file string, n int, command string) ([]string, string) {
	var findings []string
	words := strings.Fields(command)
	if !slices.Contains(words, "CGO_ENABLED=0") {
		findings = append(findings, fmt.Sprintf("%s:%d runs go build without CGO_ENABLED=0", file, n))
	}
	if !slices.Contains(words, "-trimpath") {
		findings = append(findings, fmt.Sprintf("%s:%d runs go build without -trimpath", file, n))
	}
	if !stripLdflags.MatchString(command) {
		findings = append(findings, fmt.Sprintf("%s:%d runs go build without -ldflags \"-s -w\", which strips the binary and keeps its build info", file, n))
	}
	output := ""
	for i, word := range words {
		if word == "-o" && i+1 < len(words) {
			output = words[i+1]
		} else if value, ok := strings.CutPrefix(word, "-o="); ok {
			output = value
		}
	}
	switch {
	case output == "":
		findings = append(findings, fmt.Sprintf("%s:%d runs go build with no -o /<name>", file, n))
	case path.Dir(output) != "/":
		findings = append(findings, fmt.Sprintf("%s:%d builds %s; build the program at /<name>", file, n, output))
	}
	return findings, output
}

// strippers lists each upx, strip, or objcopy that a command runs on a
// Go program the file built.
func strippers(file string, n int, command string, outputs map[string]bool) []string {
	words := strings.Fields(command)
	for len(words) > 0 && strings.Contains(words[0], "=") {
		words = words[1:]
	}
	if len(words) == 0 {
		return nil
	}
	tool := path.Base(words[0])
	if tool != "upx" && tool != "strip" && tool != "objcopy" {
		return nil
	}
	var findings []string
	for _, word := range words[1:] {
		if outputs[word] {
			findings = append(findings, fmt.Sprintf("%s:%d runs %s on %s; strip a Go program with -ldflags \"-s -w\", which keeps the build info that syft and Trivy read", file, n, tool, word))
		}
	}
	return findings
}

// goCopies lists each COPY that puts a Go program the file built at a
// path other than its own.
func goCopies(file string, n int, rest string, outputs map[string]bool) []string {
	var args []string
	from := false
	for _, word := range strings.Fields(rest) {
		if strings.HasPrefix(word, "--") {
			from = from || strings.HasPrefix(word, "--from=")
			continue
		}
		args = append(args, word)
	}
	if !from || len(args) < 2 {
		return nil
	}
	dest := args[len(args)-1]
	var findings []string
	for _, source := range args[:len(args)-1] {
		if outputs[source] && dest != source && dest != "/" {
			findings = append(findings, fmt.Sprintf("%s:%d copies %s to %s; copy it to %s", file, n, source, dest, source))
		}
	}
	return findings
}
