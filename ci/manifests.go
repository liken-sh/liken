package main

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// pinManifest is the file name of a pin's manifest. Each pin whose files
// a release ships holds one in its directory, beside its fetch.sh. The
// manifest names each file that the fetch downloads, with the facts
// that the SBOM of a release needs about it: its version, its URL, its
// sha256, its package URL, and the SPDX expression of its license.
// latest.sh --bump writes it, and fetch.sh reads the URL and the digest
// from it through fetch_pinned in toolchains/pin.sh, so the repository
// holds each digest once.
const pinManifest = "pin.toml"

// manifestFields are the fields that each entry of a manifest must
// hold, in the order that a finding names them.
var manifestFields = []string{"version", "url", "sha256", "purl", "license"}

// manifestScopes are the values of an entry's optional scope, which the
// SBOM copies into the component's CycloneDX scope. An entry that a
// build reads and no release ships, such as the deb that holds
// grub-mkimage, is excluded.
var manifestScopes = []string{"required", "optional", "excluded"}

// unmanifested names the directories whose fetch.sh needs no manifest,
// with the reason, because a release does not ship what they download,
// or because another SBOM already lists it.
var unmanifested = map[string]string{
	"liken/userland":             "buildroot writes the CycloneDX SBOM of the programs it builds",
	"liken/open-iscsi":           "iscsid and iscsiadm link libraries that apk installs in the alpine image, which no download names, so a manifest of the downloads would not list them",
	"liken/dev-cluster/firmware": "the lab's firmware ships in no release",
	"liken/dev-cluster/storage":  "the lab's storage guest ships in no release",
	"toolchains/cargo-auditable": "cargo-auditable is a build tool, and no image ships it",
}

// nestedPins names the entries of a manifest that no VERSION file pins.
// linux-firmware/fetch.sh downloads wireless-regdb beside the firmware,
// and the regulatory database has its own releases, so its manifest
// entry alone holds its version.
var nestedPins = map[string][]string{
	"liken/linux-firmware": {"wireless-regdb"},
}

// digestLiteral matches a sha256 or a sha512 in hex, which a fetch.sh
// in a manifested directory must not hold.
var digestLiteral = regexp.MustCompile(`\b([0-9a-f]{128}|[0-9a-f]{64})\b`)

// sha256Hex matches a whole sha256 in hex, the form of an entry's
// sha256.
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// addChecksum matches a Dockerfile's ADD of a URL by its checksum.
var addChecksum = regexp.MustCompile(`^ADD\b.*--checksum=sha256:([0-9a-f]*)\s+(\S+://\S+)`)

// checkManifests lists each pin whose manifest is missing, incomplete,
// or different from what its fetch downloads. Root AGENTS.md states the
// rule under "Pins and downloads":
//
//   - each directory with a fetch.sh holds a pin.toml, unless
//     unmanifested names it;
//   - each entry of a pin.toml holds a version, a url, a sha256, a purl,
//     and a license, and nothing else but an optional scope;
//   - an entry's version is the one that the directory's VERSION pins,
//     and its purl names the same version, alone or after the prefix of
//     a tag, as in microcode-20260925; a pkg:generic purl repeats
//     the url as its download_url and the sha256 as its checksum;
//   - the fetch.sh holds no digest of its own and downloads through
//     fetch_pinned, which reads the url and the sha256 from pin.toml;
//   - a Dockerfile beside a pin.toml that ADDs a URL by its checksum
//     names a URL and a digest of the manifest.
func checkManifests(root string) ([]string, error) {
	files, err := sourceFiles(root)
	if err != nil {
		return nil, err
	}
	var findings []string
	for _, file := range files {
		if path.Base(file) != "fetch.sh" {
			continue
		}
		dir := path.Dir(file)
		if _, exempt := unmanifested[dir]; exempt {
			continue
		}
		found, err := checkManifest(root, dir)
		if err != nil {
			return nil, err
		}
		findings = append(findings, found...)
	}
	return findings, nil
}

// checkManifest checks the manifest of one directory against its
// VERSION, its fetch.sh, and its Dockerfile.
func checkManifest(root, dir string) ([]string, error) {
	manifest := path.Join(dir, pinManifest)
	data, err := os.ReadFile(filepath.Join(root, manifest))
	if errors.Is(err, fs.ErrNotExist) {
		return []string{fmt.Sprintf("%s/fetch.sh downloads a file that a release ships, and %s has no %s", dir, dir, pinManifest)}, nil
	}
	if err != nil {
		return nil, err
	}
	var entries map[string]map[string]string
	if err := toml.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("%s: %w", manifest, err)
	}
	pinned := ""
	if version, err := os.ReadFile(filepath.Join(root, dir, "VERSION")); err == nil {
		pinned = strings.TrimSpace(string(version))
	}
	var findings []string
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		entry := entries[name]
		findings = append(findings, entryFindings(manifest, name, entry)...)
		if pinned != "" && entry["version"] != pinned && !slices.Contains(nestedPins[dir], name) {
			findings = append(findings, fmt.Sprintf("%s: [%s] is at %s, and %s/VERSION pins %s, the version that fetch.sh downloads", manifest, name, entry["version"], dir, pinned))
		}
	}
	fetch, err := os.ReadFile(filepath.Join(root, dir, "fetch.sh"))
	if err != nil {
		return nil, err
	}
	findings = append(findings, fetchFindings(path.Join(dir, "fetch.sh"), string(fetch))...)
	dockerfile, err := os.ReadFile(filepath.Join(root, dir, "Dockerfile"))
	if err == nil {
		findings = append(findings, imageFindings(path.Join(dir, "Dockerfile"), manifest, string(dockerfile), entries)...)
	}
	return findings, nil
}

// entryFindings lists each field of one entry that is missing or does
// not agree with the others.
func entryFindings(manifest, name string, entry map[string]string) []string {
	var findings []string
	for _, field := range manifestFields {
		if entry[field] == "" {
			findings = append(findings, fmt.Sprintf("%s: [%s] has no %s", manifest, name, field))
		}
	}
	for _, field := range slices.Sorted(maps.Keys(entry)) {
		if !slices.Contains(manifestFields, field) && field != "scope" {
			findings = append(findings, fmt.Sprintf("%s: [%s] holds %s, which is not a field of a manifest", manifest, name, field))
		}
	}
	if scope, ok := entry["scope"]; ok && !slices.Contains(manifestScopes, scope) {
		findings = append(findings, fmt.Sprintf("%s: [%s] has the scope %q, which is not required, optional, or excluded", manifest, name, scope))
	}
	if digest := entry["sha256"]; digest != "" && !sha256Hex.MatchString(digest) {
		findings = append(findings, fmt.Sprintf("%s: [%s] has a sha256 that is not 64 hex digits", manifest, name))
	}
	if entry["purl"] == "" {
		return findings
	}
	purl, ok := parsePurl(entry["purl"])
	if !ok {
		return append(findings, fmt.Sprintf("%s: [%s] has the purl %q, which does not start with pkg:", manifest, name, entry["purl"]))
	}
	if purl.version == "" || !strings.HasSuffix(purl.version, entry["version"]) {
		findings = append(findings, fmt.Sprintf("%s: [%s] has a purl at %s, and its version is %s", manifest, name, purl.version, entry["version"]))
	}
	if purl.kind == "generic" {
		if got := purl.qualifiers["download_url"]; got != entry["url"] {
			findings = append(findings, fmt.Sprintf("%s: [%s] has a purl whose download_url is %s, and its url is %s", manifest, name, got, entry["url"]))
		}
		if got := purl.qualifiers["checksum"]; got != "sha256:"+entry["sha256"] {
			findings = append(findings, fmt.Sprintf("%s: [%s] has a purl whose checksum is %s, and its sha256 is %s", manifest, name, got, entry["sha256"]))
		}
	}
	return findings
}

// purl is the part of a package URL that the check compares: its type,
// its version, and its qualifiers.
type purl struct {
	kind, version string
	qualifiers    map[string]string
}

// parsePurl reads a package URL, pkg:<type>/<namespace>/<name>@<version>
// ?<qualifiers>#<subpath>. The version is percent-decoded, so the purl
// of k3s names v1.37.1%2Bk3s1 and compares as v1.37.1+k3s1. A qualifier
// is compared as written, because the bump writes the url into it
// unchanged.
func parsePurl(text string) (purl, bool) {
	rest, ok := strings.CutPrefix(text, "pkg:")
	if !ok {
		return purl{}, false
	}
	rest, _, _ = strings.Cut(rest, "#")
	rest, query, _ := strings.Cut(rest, "?")
	p := purl{qualifiers: map[string]string{}}
	p.kind, _, _ = strings.Cut(rest, "/")
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		version, err := url.PathUnescape(rest[i+1:])
		if err != nil {
			version = rest[i+1:]
		}
		p.version = version
	}
	for _, pair := range strings.Split(query, "&") {
		if key, value, ok := strings.Cut(pair, "="); ok {
			p.qualifiers[key] = value
		}
	}
	return p, true
}

// fetchFindings lists each digest that a fetch.sh holds, and a fetch.sh
// that does not download through fetch_pinned. A comment can quote a
// digest, so the check reads the commands alone.
func fetchFindings(file, text string) []string {
	var findings []string
	reads := false
	for _, l := range logicalLines(text) {
		if digestLiteral.MatchString(l.text) {
			findings = append(findings, fmt.Sprintf("%s:%d holds a digest; the digest of each file that a fetch.sh downloads is in %s", file, l.n, pinManifest))
		}
		if slices.Contains(commandWords(l.text), "fetch_pinned") {
			reads = true
		}
	}
	if !reads {
		findings = append(findings, fmt.Sprintf("%s does not download through fetch_pinned, so it does not read %s", file, pinManifest))
	}
	return findings
}

// imageFindings lists each ADD of a Dockerfile that fetches a URL by a
// checksum other than the one that the manifest beside it names. The
// image and the OS build fetch the same file, and BuildKit reads the
// checksum from the Dockerfile's own text, so the Dockerfile keeps a
// copy and this check holds the copy to the manifest.
func imageFindings(file, manifest, text string, entries map[string]map[string]string) []string {
	var findings []string
	for _, l := range logicalLines(text) {
		m := addChecksum.FindStringSubmatch(strings.Join(strings.Fields(l.text), " "))
		if m == nil {
			continue
		}
		digest, address := m[1], m[2]
		found := false
		for _, entry := range entries {
			if entry["url"] != address {
				continue
			}
			found = true
			if entry["sha256"] != digest {
				findings = append(findings, fmt.Sprintf("%s:%d fetches %s by sha256:%s, and %s names %s", file, l.n, address, digest, manifest, entry["sha256"]))
			}
		}
		if !found {
			findings = append(findings, fmt.Sprintf("%s:%d fetches %s, and %s names no file at that url", file, l.n, address, manifest))
		}
	}
	return findings
}
