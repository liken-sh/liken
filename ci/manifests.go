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

// pinManifest is the file name of a pin's manifest, which has one entry
// for each file that the pin's fetch.sh downloads. latest.sh --bump
// writes the entries, and fetch.sh reads the url and sha256 from them
// through fetch_pinned in toolchains/pin.sh, so each digest is in the
// repository once.
const pinManifest = "pin.toml"

// manifestFields are the fields that every entry of a manifest must
// have. Findings report a missing field in this order.
var manifestFields = []string{"version", "url", "sha256", "purl", "license"}

// manifestScopes are the allowed values of an entry's optional scope,
// which the SBOM copies into the component's CycloneDX scope. An entry
// that a build reads and no release ships, such as the deb that
// provides grub-mkimage, has the scope excluded.
var manifestScopes = []string{"required", "optional", "excluded"}

// optionalFields are the fields that an entry may have besides the
// required ones.
//
//   - type is the entry's CycloneDX component type. It defaults to
//     file. An entry that an image ships needs a type that syft keeps,
//     because toolchains/manifest/image-sbom refuses a type that syft
//     drops.
//   - cpe is the CPE 2.3 name that NVD lists the upstream under. An
//     entry that an image ships needs one when NVD has one, because
//     otherwise syft guesses several.
//   - backports lists the GitHub URLs of the upstream commits that the
//     build applies to the release. The SBOM records them as the
//     component's pedigree, so it does not present the component as
//     the stock release.
var optionalFields = []string{"scope", "type", "cpe", "backports"}

// githubCommit matches a backport: the URL of one GitHub commit, with
// its full 40-digit id. toolchains/manifest uses the last path element
// as the commit id and appends .patch to get the diff URL.
var githubCommit = regexp.MustCompile(`^https://github\.com/[^/]+/[^/]+/commit/[0-9a-f]{40}$`)

// componentTypes are the component types of CycloneDX 1.6.
var componentTypes = []string{
	"application", "framework", "library", "container", "platform",
	"operating-system", "device", "device-driver", "firmware", "file",
	"machine-learning-model", "data", "cryptographic-asset",
}

// imageRecorder is the command that writes an image's record of the
// files that its pins list. A Dockerfile that fetches a file of a
// manifest must run it, or the image's SBOM does not list the file.
const imageRecorder = "image-sbom"

// unmanifestedDownloads maps each Dockerfile whose --checksum downloads
// need no manifest, because only its build reads them, to the reason.
var unmanifestedDownloads = map[string]string{
	"bluetooth-operator/bluetoothd/Dockerfile": "buildroot and the SPDX license list are the build system of the daemons, and buildroot's own SBOM lists each package that the image ships",
}

// unmanifested maps each directory whose fetch.sh needs no manifest to
// the reason: a release does not ship what it downloads, or another
// SBOM already lists it.
var unmanifested = map[string]string{
	"liken/userland":             "buildroot writes the CycloneDX SBOM of the programs it builds",
	"liken/dev-cluster/firmware": "the lab's firmware ships in no release",
	"liken/dev-cluster/storage":  "the lab's storage guest ships in no release",
	"toolchains/cargo-auditable": "cargo-auditable is a build tool, and no image ships it",
}

// nestedPins lists, for each directory, the manifest entries whose
// version differs from the directory's VERSION. linux-firmware/fetch.sh
// also downloads wireless-regdb, which has its own releases. The
// k3s-root release in xtables/VERSION builds iptables with a release of
// buildroot, and the source mirror needs the source of both at the
// versions that k3s-root used.
var nestedPins = map[string][]string{
	"liken/linux-firmware": {"wireless-regdb"},
	"liken/xtables":        {"iptables-source", "buildroot-source"},
}

// sourceMirror is the script that copies the source of what an OS
// release ships onto the release channel. The url and sha256 of each
// source file are an entry in the pin.toml of the binary that was built
// from it, so the same latest.sh --bump that moves the binary moves its
// source. A digest written in the script would be a second pin that no
// bump updates.
const sourceMirror = "liken/licensing/sources.sh"

// digestLiteral matches a sha256 or sha512 in hex. Neither a fetch.sh
// in a directory with a manifest nor the source mirror may contain
// one.
var digestLiteral = regexp.MustCompile(`\b([0-9a-f]{128}|[0-9a-f]{64})\b`)

// sha256Hex matches a complete sha256 in hex, the form of an entry's
// sha256 field.
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// addChecksum matches a Dockerfile's ADD of a URL by its checksum.
var addChecksum = regexp.MustCompile(`^ADD\b.*--checksum=sha256:([0-9a-f]*)\s+(\S+://\S+)`)

// argDefault matches an ARG with a default value.
var argDefault = regexp.MustCompile(`^ARG\s+([A-Za-z_][A-Za-z0-9_]*)=(\S+)`)

// checkManifests lists each pin whose manifest is missing, incomplete,
// or different from what its fetch downloads. Root AGENTS.md states the
// rules under "Pins and downloads":
//
//   - Each directory with a fetch.sh has a pin.toml, unless
//     unmanifested lists it.
//   - Each entry of a pin.toml has a version, url, sha256, purl, and
//     license, and may have a scope, type, cpe, and backports. It has
//     no other field.
//   - An entry's version equals the directory's VERSION, except for the
//     entries of nestedPins. The purl's version ends with the entry's
//     version, which allows a tag prefix as in microcode-20260925. The
//     CPE's version is the entry's version or its upstream part, as
//     cpeFindings explains. A pkg:generic purl repeats the url as its
//     download_url and the sha256 as its checksum.
//   - The fetch.sh contains no digest and downloads through
//     fetch_pinned.
//   - Each Dockerfile that ADDs a URL with --checksum has a pin.toml
//     in its directory with an entry for that URL at that digest.
//     The exceptions are Debian packages and the Dockerfiles in
//     unmanifestedDownloads. A Dockerfile that ADDs a file that a
//     manifest lists, with a scope other than excluded, also runs
//     image-sbom.
//   - A sums file next to a pin.toml, such as models.sha256, has the
//     same digest as the manifest for each file that both list.
//   - The source mirror contains no digest.
func checkManifests(root string) ([]string, error) {
	files, err := sourceFiles(root)
	if err != nil {
		return nil, err
	}
	var findings []string
	mirror, err := os.ReadFile(filepath.Join(root, sourceMirror))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	findings = append(findings, mirrorFindings(string(mirror))...)
	var dirs []string
	for _, file := range files {
		dir := path.Dir(file)
		switch path.Base(file) {
		case "fetch.sh":
			if _, exempt := unmanifested[dir]; !exempt && !slices.Contains(dirs, dir) {
				dirs = append(dirs, dir)
			}
		case pinManifest:
			if !slices.Contains(dirs, dir) {
				dirs = append(dirs, dir)
			}
		}
	}
	manifests := map[string]map[string]map[string]string{}
	for _, dir := range dirs {
		entries, found, err := checkManifest(root, dir)
		if err != nil {
			return nil, err
		}
		findings = append(findings, found...)
		if entries != nil {
			manifests[dir] = entries
		}
	}
	for _, file := range files {
		name, dir := path.Base(file), path.Dir(file)
		if !isDockerfile(name) && !(strings.HasSuffix(name, ".sha256") && manifests[dir] != nil) {
			continue
		}
		text, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			return nil, err
		}
		if isDockerfile(name) {
			findings = append(findings, imageFindings(file, dir, string(text), manifests[dir])...)
		} else {
			findings = append(findings, sumsFindings(file, path.Join(dir, pinManifest), string(text), manifests[dir])...)
		}
	}
	return findings, nil
}

// checkManifest checks one directory's manifest against its VERSION and
// its fetch.sh. It returns the manifest's entries, or none when the
// directory has no manifest.
func checkManifest(root, dir string) (map[string]map[string]string, []string, error) {
	manifest := path.Join(dir, pinManifest)
	data, err := os.ReadFile(filepath.Join(root, manifest))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, []string{fmt.Sprintf("%s/fetch.sh downloads a file that a release ships, and %s has no %s", dir, dir, pinManifest)}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var entries map[string]map[string]string
	if err := toml.Unmarshal(data, &entries); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", manifest, err)
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
	switch {
	case err == nil:
		findings = append(findings, fetchFindings(path.Join(dir, "fetch.sh"), string(fetch))...)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, nil, err
	}
	return entries, findings, nil
}

// entryFindings lists each field of one entry that is missing or
// inconsistent with the entry's other fields.
func entryFindings(manifest, name string, entry map[string]string) []string {
	var findings []string
	for _, field := range manifestFields {
		if entry[field] == "" {
			findings = append(findings, fmt.Sprintf("%s: [%s] has no %s", manifest, name, field))
		}
	}
	for _, field := range slices.Sorted(maps.Keys(entry)) {
		if !slices.Contains(manifestFields, field) && !slices.Contains(optionalFields, field) {
			findings = append(findings, fmt.Sprintf("%s: [%s] has %s, which is not a field of a manifest", manifest, name, field))
		}
	}
	if scope, ok := entry["scope"]; ok && !slices.Contains(manifestScopes, scope) {
		findings = append(findings, fmt.Sprintf("%s: [%s] has the scope %q, which is not required, optional, or excluded", manifest, name, scope))
	}
	if kind, ok := entry["type"]; ok && !slices.Contains(componentTypes, kind) {
		findings = append(findings, fmt.Sprintf("%s: [%s] has the type %q, which is not a CycloneDX component type", manifest, name, kind))
	}
	if cpe, ok := entry["cpe"]; ok {
		findings = append(findings, cpeFindings(manifest, name, cpe, entry["version"])...)
	}
	for _, backport := range strings.Fields(entry["backports"]) {
		if !githubCommit.MatchString(backport) {
			findings = append(findings, fmt.Sprintf("%s: [%s] has the backport %q, which is not the URL of a commit on GitHub", manifest, name, backport))
		}
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

// purl is the parts of a package URL that entryFindings compares. kind
// is the purl's type, such as generic or github.
type purl struct {
	kind, version string
	qualifiers    map[string]string
}

// parsePurl parses a package URL of the form
// pkg:<type>/<namespace>/<name>@<version>?<qualifiers>#<subpath>. The
// version is percent-decoded, so the k3s purl's v1.37.1%2Bk3s1 compares
// as v1.37.1+k3s1. Qualifiers are not decoded, because the bump copies
// the url into the qualifier as it is.
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

// fetchFindings lists each digest in a fetch.sh, and reports a fetch.sh
// that does not download through fetch_pinned. It reads only commands,
// because a comment may quote a digest.
func fetchFindings(file, text string) []string {
	var findings []string
	reads := false
	for _, l := range logicalLines(text) {
		if digestLiteral.MatchString(l.text) {
			findings = append(findings, fmt.Sprintf("%s:%d contains a digest, and the digest of each file that fetch.sh downloads belongs in %s", file, l.n, pinManifest))
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

// mirrorFindings lists each digest in the source mirror. It reads only
// commands, because a comment may quote a digest.
func mirrorFindings(text string) []string {
	var findings []string
	for _, l := range logicalLines(text) {
		if digestLiteral.MatchString(l.text) {
			findings = append(findings, fmt.Sprintf("%s:%d contains a digest, and the digest of each source that it mirrors belongs in the %s of the pin that the source belongs to", sourceMirror, l.n, pinManifest))
		}
	}
	return findings
}

// cpeFindings reports a CPE that is not a CPE 2.3 formatted string of
// 13 parts, or whose version is neither the entry's version nor
// upstreamVersion of it. Backslash escapes are removed before the
// comparison.
//
// A scanner matches the CPE's version against the version ranges in
// NVD, which lists each upstream under its own version. A version such
// as v1.37.1+k3s1 falls in no range, so the CPE may use the upstream
// part: k3s 1.37.1 for the tag v1.37.1+k3s1, and GRUB 2.14 for
// Ubuntu's package 2.14-2ubuntu3.
func cpeFindings(manifest, name, cpe, version string) []string {
	parts := strings.Split(cpe, ":")
	if len(parts) != 13 || parts[0] != "cpe" || parts[1] != "2.3" {
		return []string{fmt.Sprintf("%s: [%s] has the CPE %q, which is not a CPE 2.3 name of 13 parts", manifest, name, cpe)}
	}
	if got := strings.ReplaceAll(parts[5], `\`, ""); got != version && got != upstreamVersion(version) {
		return []string{fmt.Sprintf("%s: [%s] has a CPE at %s, and its version is %s", manifest, name, got, version)}
	}
	return nil
}

// upstreamVersion returns the upstream's own version from a release tag
// or a package version: the text after a leading v and before the first
// + or -. cpe_version in toolchains/pin.sh applies the same rule when a
// bump writes the CPE.
func upstreamVersion(version string) string {
	version = strings.TrimPrefix(version, "v")
	if i := strings.IndexAny(version, "+-"); i >= 0 {
		version = version[:i]
	}
	return version
}

// imageFindings lists each ADD with --checksum in a Dockerfile whose
// URL and digest are not in the manifest in the same directory, and
// reports a Dockerfile that fetches a shipped file of the manifest and
// does not run image-sbom. BuildKit reads the checksum from the
// Dockerfile text, so the Dockerfile keeps its own copy of the digest.
// syft finds no software in a file that a Dockerfile downloaded, so
// image-sbom writes the image's record of the file from the manifest.
//
// Debian packages are skipped, because the closure that unpacks one
// writes the package's control file into /var/lib/dpkg/status.d, where
// syft reads it.
func imageFindings(file, dir, text string, entries map[string]map[string]string) []string {
	if _, exempt := unmanifestedDownloads[file]; exempt {
		return nil
	}
	manifest := path.Join(dir, pinManifest)
	var findings []string
	shipsEntry, records := false, false
	args := map[string]string{}
	for _, l := range logicalLines(text) {
		joined := strings.Join(strings.Fields(l.text), " ")
		if m := argDefault.FindStringSubmatch(joined); m != nil {
			args[m[1]] = strings.Trim(m[2], `"'`)
		}
		if strings.Contains(joined, imageRecorder) {
			records = true
		}
		m := addChecksum.FindStringSubmatch(expandArgs(joined, args))
		if m == nil {
			continue
		}
		digest, address := m[1], m[2]
		if strings.HasSuffix(address, ".deb") {
			continue
		}
		if entries == nil {
			findings = append(findings, fmt.Sprintf("%s:%d fetches %s by its checksum, and %s has no %s, so the image's SBOM cannot name the file", file, l.n, address, dir, pinManifest))
			continue
		}
		found := false
		for _, entry := range entries {
			if entry["url"] != address {
				continue
			}
			found = true
			shipsEntry = shipsEntry || entry["scope"] != "excluded"
			if entry["sha256"] != digest {
				findings = append(findings, fmt.Sprintf("%s:%d fetches %s by sha256:%s, and %s has sha256 %s", file, l.n, address, digest, manifest, entry["sha256"]))
			}
		}
		if !found {
			findings = append(findings, fmt.Sprintf("%s:%d fetches %s, and %s has no file at that url", file, l.n, address, manifest))
		}
	}
	if shipsEntry && !records {
		findings = append(findings, fmt.Sprintf("%s fetches a file that %s lists, and runs no %s, so the image's SBOM does not list the file", file, manifest, imageRecorder))
	}
	return findings
}

// expandArgs replaces each ${NAME} and $NAME in a line with the default
// value of the ARG of that name, so a checksum that comes from an ARG
// is compared as BuildKit reads it. A name with no default is left as
// ${NAME}.
func expandArgs(text string, args map[string]string) string {
	return os.Expand(text, func(name string) string {
		if value, ok := args[name]; ok {
			return value
		}
		return "${" + name + "}"
	})
}

// sumsFindings lists each line of a sums file, such as models.sha256,
// whose digest for a manifest entry's file differs from the entry's
// sha256. A build that checks files against the sums file and an SBOM
// that reads the manifest must describe the same bytes.
func sumsFindings(file, manifest, text string, entries map[string]map[string]string) []string {
	var findings []string
	for i, raw := range strings.Split(text, "\n") {
		fields := strings.Fields(raw)
		if len(fields) != 2 {
			continue
		}
		digest, name := fields[0], strings.TrimPrefix(fields[1], "*")
		for _, entry := range entries {
			if path.Base(entry["url"]) == name && entry["sha256"] != digest {
				findings = append(findings, fmt.Sprintf("%s:%d lists %s with sha256:%s, and %s has sha256 %s", file, i+1, name, digest, manifest, entry["sha256"]))
			}
		}
	}
	return findings
}
