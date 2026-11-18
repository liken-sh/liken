package main

import (
	"strings"
	"testing"
)

func TestEveryCargoBuildIsAudited(t *testing.T) {
	findings, err := checkPins(pinsTree(t, map[string]string{
		"a/Dockerfile.rust": "FROM rust AS build\n" +
			"RUN cargo auditable build --release --locked\n" +
			"RUN --mount=type=cache,target=/usr/local/cargo/registry \\\n" +
			"    touch src/main.rs \\\n" +
			"    && cargo auditable build --release --locked -p idle-screen \\\n" +
			"    && sh /cargo.sh /notices -p idle-screen\n" +
			"# A comment that says cargo build is not a build.\n" +
			"RUN cargo test --release && cargo metadata --format-version 1\n",
	}))
	if err != nil {
		t.Fatalf("checking the pins of a Dockerfile: %v", err)
	}
	if len(findings) > 0 {
		t.Errorf("the pins check finds a problem in a Dockerfile that runs cargo auditable build:\n%s", strings.Join(findings, "\n"))
	}
}

func TestACargoBuildWithoutTheAuditIsFound(t *testing.T) {
	cases := map[string]struct {
		text, wants string
	}{
		"a plain build": {"FROM rust AS build\nRUN cargo build --release --locked\n",
			"a/Dockerfile.rust:2 runs cargo build. Run cargo auditable build"},
		"a build after another command": {"FROM rust AS build\nRUN touch src/main.rs \\\n    && cargo build --release -p media-display\n",
			"a/Dockerfile.rust:2 runs cargo build. Run cargo auditable build"},
		"one of two builds": {"FROM rust AS build\nRUN cargo auditable build -p a && cargo build -p b\n",
			"a/Dockerfile.rust:2 runs cargo build. Run cargo auditable build"},
		"a build with a toolchain": {"FROM rust AS build\nRUN cargo +nightly build\n",
			"a/Dockerfile.rust:2 runs cargo build. Run cargo auditable build"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			findings, err := checkPins(pinsTree(t, map[string]string{"a/Dockerfile.rust": c.text}))
			if err != nil {
				t.Fatalf("checking the pins of a Dockerfile: %v", err)
			}
			if !strings.Contains(strings.Join(findings, "\n"), c.wants) {
				t.Errorf("the findings are:\n%s\nwant one that contains %q", strings.Join(findings, "\n"), c.wants)
			}
		})
	}
}
