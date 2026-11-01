package main

import (
	"fmt"
	"strings"
)

// unauditedBuilds lists each instruction of a Dockerfile that runs
// `cargo build`. A Rust binary holds no record of the crates it links,
// so syft and Trivy list none of them from an image, and a scan matches
// no advisory against them. `cargo auditable build` records the crate
// graph in the binary's .dep-v0 section, which both scanners read.
// Root AGENTS.md states the rule under "Rust binaries".
//
// The check holds every build in a Dockerfile to the rule, the stub
// build of the dependency layer too. A pattern cannot tell the build
// that makes the shipped binary from the stub build, and a stub build
// through the same wrapper keeps the dependency layer usable for the
// real build.
func unauditedBuilds(file, text string) []string {
	var findings []string
	for _, l := range logicalLines(text) {
		words := strings.Fields(l.text)
		for i, word := range words {
			if word != "cargo" {
				continue
			}
			rest := words[i+1:]
			for len(rest) > 0 && strings.HasPrefix(rest[0], "+") {
				rest = rest[1:]
			}
			if len(rest) > 0 && rest[0] == "build" {
				findings = append(findings, fmt.Sprintf("%s:%d runs cargo build; run cargo auditable build, so the binary records its crates", file, l.n))
				break
			}
		}
	}
	return findings
}
