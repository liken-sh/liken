package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fetchesTree is a repository in which every pin follows the fetch
// rules: a domain of the OS with its VERSION, fetch.sh, and latest.sh,
// an image whose Dockerfile downloads under a latest.sh of its
// component, the library that each fetch.sh and latest.sh sources, and
// the scripts that read a checksum file at bump time or in a test,
// which the rules allow.
func fetchesTree(t *testing.T, changes map[string]string) string {
	t.Helper()
	tree := map[string]string{
		"liken/k3s/VERSION": "v1.36.0+k3s1\n",
		"liken/k3s/latest.sh": "#!/usr/bin/env bash\n" +
			"# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh\n" +
			"source \"$here/../../toolchains/pin.sh\"\n" +
			"sums=\"$archive/sha256sums.asc\"\n" +
			"digest=\"$(\"$here/../../trust/keys/kernel-org-digest.sh\" \"$sums\" k3s)\"\n",
		"liken/k3s/fetch.sh": "#!/usr/bin/env bash\n" +
			". \"$here/../../toolchains/pin.sh\"\n" +
			"# latest.sh --bump copies the digest from sha256sums.asc.\n" +
			"digest=\"aaaa\"\n" +
			"fetch_verified --resume \"$base/k3s\" \"$digest\" \"$cache/k3s\"\n" +
			"sha1=\"$(fetch_text \"https://snapshot.debian.org/mr/binary/k3s/binfiles\" | jq -r .hash)\"\n" +
			"url_exists \"$base/k3s\"\n" +
			"row k3s \"$pinned\" \"$latest\" - - - \"the digest that curl publishes\"\n" +
			"file k3s | grep -qE \"statically linked|static-pie linked\" || echo \"k3s is not statically linked\"\n",
		"toolchains/pin.sh": "http() { curl -sS --retry 3 \"$@\"; }\n" +
			"fetch_file() {\n" +
			"    curl -fL -o \"$2.partial\" \"$1\"\n" +
			"    mv \"$2.partial\" \"$2\"\n" +
			"}\n" +
			"url_exists() { curl -fsL -o /dev/null \"$1\"; }\n",
		"a/latest.sh": "#!/bin/sh\nsource \"$here/../toolchains/pin.sh\"\nfetch_text \"$base/dbus-$v.tar.xz.sha256sum\"\n",
		"a/image/Dockerfile": "FROM alpine AS build\n" +
			"ADD --checksum=sha256:aaaa \\\n    https://example.org/a.tar.xz /a.tar.xz\n" +
			"RUN wget -q \"https://example.org/b-${B}.tar.xz\" \\\n" +
			"    && echo \"${B_SHA256}  b-${B}.tar.xz\" | sha256sum -c -\n" +
			"COPY --from=builder snapshot.sh snapshot.sha256 /\n",
		"b/Dockerfile":                    "FROM scratch\nCOPY x /x\n",
		"trust/keys/kernel-org-digest.sh": "curl -fsSL -o \"$work/sha256sums.asc\" \"$sums\"\n",
		"trust/smoke/trust.sh":            "published=$(curl -fsS \"https://curl.se/ca/cacert-$date.pem.sha256\")\n",
		"a/testdata/VERSION":              "1\n",
		"plans/1-history.md":              "fetch.sh read SHA256SUMS then.\n",
	}
	for name, text := range changes {
		tree[name] = text
	}
	root := writeTree(t, tree)
	for name := range tree {
		if filepath.Base(name) == "latest.sh" {
			if err := os.Chmod(filepath.Join(root, name), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func TestEveryFetchFollowsTheRules(t *testing.T) {
	findings, err := checkFetches(fetchesTree(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) > 0 {
		t.Errorf("the tree breaks a rule:\n%s", strings.Join(findings, "\n"))
	}
}

func TestAFetchThatBreaksARuleIsFound(t *testing.T) {
	cases := map[string]struct {
		file, text, wants string
	}{
		"a fetch.sh that downloads a sums file": {"liken/k3s/fetch.sh",
			"#!/bin/sh\ndigest=\"$(curl -fsSL \"$base/SHA256SUMS\" | awk '$2 == \"k3s\" { print $1 }')\"\n",
			"liken/k3s/fetch.sh:2 reads a checksum file from upstream at build time"},
		"a fetch.sh that names a sums file in a variable": {"liken/k3s/fetch.sh",
			"#!/bin/sh\nsums=\"$archive/v$version/sha256sums.asc\"\n",
			"liken/k3s/fetch.sh:2 reads a checksum file from upstream at build time"},
		"a Dockerfile that downloads a sums file": {"a/image/Dockerfile",
			"FROM alpine\nRUN wget -q \\\n    \"https://example.org/b.tar.xz.sha256\"\n",
			"a/image/Dockerfile:2 reads a checksum file from upstream at build time"},
		"a Dockerfile that adds a sums file": {"a/image/Dockerfile",
			"FROM alpine\nADD https://example.org/SHA512SUMS /sums\n",
			"a/image/Dockerfile:2 reads a checksum file from upstream at build time"},
		"a curl into the cache": {"liken/k3s/fetch.sh",
			"#!/bin/sh\ncurl -fL --retry 5 \\\n  -o \"$cache/k3s\" \"$base/k3s\"\n",
			"liken/k3s/fetch.sh:2 downloads into \"$cache/k3s\", not into a .partial file"},
		"a curl with an attached output": {"liken/k3s/fetch.sh",
			"#!/bin/sh\ncurl -fL --output=$cache/k3s \"$base/k3s\"\n",
			"liken/k3s/fetch.sh:2 downloads into \"$cache/k3s\", not into a .partial file"},
		"a curl redirected into the cache": {"liken/k3s/fetch.sh",
			"#!/bin/sh\ncurl -fsSL \"$base/k3s\" >\"$cache/k3s\"\n",
			"liken/k3s/fetch.sh:2 downloads into \"$cache/k3s\", not into a .partial file"},
		"a wget into the cache": {"liken/k3s/fetch.sh",
			"#!/bin/sh\nwget -q -O \"$cache/k3s\" \"$base/k3s\"\n",
			"liken/k3s/fetch.sh:2 downloads into \"$cache/k3s\", not into a .partial file"},
		"a wget to the remote name": {"liken/k3s/fetch.sh",
			"#!/bin/sh\nwget -q \"$base/k3s\"\n",
			"liken/k3s/fetch.sh:2 downloads into the remote file name, not into a .partial file"},
		"a VERSION with no latest.sh": {"liken/hwdata/VERSION", "v0.400\n",
			"liken/hwdata/VERSION pins a version, and liken/hwdata has no latest.sh"},
		"a fetch.sh with no latest.sh": {"liken/hwdata/fetch.sh", "#!/bin/sh\n",
			"liken/hwdata/fetch.sh downloads a pin, and liken/hwdata has no latest.sh"},
		"a Dockerfile that downloads with no latest.sh": {"b/Dockerfile",
			"FROM scratch\nADD --checksum=sha256:aaaa https://example.org/b /b\n",
			"b/Dockerfile downloads a pin, and neither b nor a directory above it has a latest.sh"},
		"a latest.sh that make versions skips": {"liken/dist/latest.sh", "#!/bin/sh\n",
			"liken/dist/latest.sh is under a directory that versions.sh skips"},
		"a latest.sh that reads kernel.org's sums unsigned": {"a/latest.sh",
			"#!/bin/sh\ncurl -fsSL \"$archive/sha256sums.asc\" | awk '{ print $1 }'\n",
			"a/latest.sh reads a sha256sums.asc file and does not verify it with trust/keys/kernel-org-digest.sh"},
		"a static check that refuses a static PIE": {"liken/k3s/fetch.sh",
			"#!/bin/sh\nfile k3s | grep -q \"statically linked\"\n",
			"liken/k3s/fetch.sh:2 accepts \"statically linked\" and refuses \"static-pie linked\""},
		"a latest.sh that does not source the library": {"liken/k3s/latest.sh",
			"#!/bin/sh\nrow k3s v1 v1 - - - note\n",
			"liken/k3s/latest.sh does not source toolchains/pin.sh"},
		"a fetch.sh that does not source the library": {"liken/k3s/fetch.sh",
			"#!/bin/sh\nfetch_verified \"$url\" \"$digest\" \"$cache/k3s\"\n",
			"liken/k3s/fetch.sh does not source toolchains/pin.sh"},
		"a source line one directory off": {"liken/k3s/latest.sh",
			"#!/bin/sh\nsource \"$here/../toolchains/pin.sh\"\n",
			"liken/k3s/latest.sh does not source toolchains/pin.sh"},
		"a source line in a comment": {"liken/k3s/latest.sh",
			"#!/bin/sh\n# source \"$here/../../toolchains/pin.sh\"\n",
			"liken/k3s/latest.sh does not source toolchains/pin.sh"},
		"a latest.sh that runs curl": {"liken/k3s/latest.sh",
			"#!/bin/sh\nsource \"$here/../../toolchains/pin.sh\"\nlatest=\"$(curl -fsS \"$channels\" | jq -r .latest)\"\n",
			"liken/k3s/latest.sh:3 runs curl, not a function of toolchains/pin.sh"},
		"a fetch.sh that runs wget": {"liken/k3s/fetch.sh",
			"#!/bin/sh\nsource \"$here/../../toolchains/pin.sh\"\nwget -qO- \"$base/k3s\" | tar -x\n",
			"liken/k3s/fetch.sh:3 runs wget, not a function of toolchains/pin.sh"},
		"a library download outside a .partial file": {"toolchains/pin.sh",
			"fetch_file() { curl -fL -o \"$2\" \"$1\"; }\n",
			"toolchains/pin.sh:1 downloads into \"$2\", not into a .partial file"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			findings, err := checkFetches(fetchesTree(t, map[string]string{c.file: c.text}))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(findings, "\n"), c.wants) {
				t.Errorf("got:\n%s\nwant a finding with %q", strings.Join(findings, "\n"), c.wants)
			}
		})
	}
}

func TestALatestShThatCannotRunIsFound(t *testing.T) {
	root := fetchesTree(t, nil)
	if err := os.Chmod(filepath.Join(root, "liken/k3s/latest.sh"), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := checkFetches(root)
	if err != nil {
		t.Fatal(err)
	}
	wants := "liken/k3s/latest.sh is not executable, so versions.sh cannot run it"
	if !strings.Contains(strings.Join(findings, "\n"), wants) {
		t.Errorf("got:\n%s\nwant a finding with %q", strings.Join(findings, "\n"), wants)
	}
}

func TestAnAllowCommentExcusesTheLineBelowIt(t *testing.T) {
	findings, err := checkFetches(fetchesTree(t, map[string]string{
		"liken/k3s/fetch.sh": "#!/bin/sh\n" +
			"source \"$here/../../toolchains/pin.sh\"\n" +
			"# fetch-check: allow, the lookup returns a file name, not a digest\n" +
			"curl -fsSL \"$base/SHA256SUMS\" -o \"$cache/names\"\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) > 0 {
		t.Errorf("the allowed line is a finding:\n%s", strings.Join(findings, "\n"))
	}
}

func TestThePinsCommandFailsOnAFetchThatBreaksARule(t *testing.T) {
	root := pinsTree(t, map[string]string{
		"liken/k3s/VERSION":   "v1\n",
		"liken/k3s/fetch.sh":  "#!/bin/sh\ncurl -o \"$cache/k3s\" \"$base/k3s\"\n",
		"liken/k3s/latest.sh": "#!/bin/sh\n",
	})
	if err := os.Chmod(filepath.Join(root, "liken/k3s/latest.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, &fakeRegistry{})
	if err := w.run([]string{"pins", "-root", root}); err == nil {
		t.Errorf("the pins command passes a curl into the cache")
	}
}
