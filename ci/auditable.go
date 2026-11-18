package main

import (
	"fmt"
	"strings"
)

// unauditedBuilds returns a finding for each instruction of a
// Dockerfile that runs `cargo build`. A Rust binary does not record the
// crates it links, so syft and Trivy list none of them and match no
// advisory against them. `cargo auditable build` writes the crate graph
// into the binary's .dep-v0 section, which both scanners read.
//
// The check also covers the stub build of the dependency layer, because
// a pattern cannot tell it from the real build. When the stub builds
// through the same wrapper, the dependencies compile once.
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
				findings = append(findings, fmt.Sprintf("%s:%d runs cargo build. Run cargo auditable build, so the binary records its crates", file, l.n))
				break
			}
		}
	}
	return findings
}
