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

// optionalFields are the fields that an entry may hold beside the
// required ones. The type is the entry's CycloneDX component type, file
// when it names none, and the cpe is the CPE 2.3 name that NVD lists
// the upstream under. An entry that an image ships names a type that
// syft keeps, because toolchains/manifest/image-sbom refuses one that
// syft drops, and a CPE where NVD has one, because syft otherwise
// guesses several. The backports are the GitHub URLs of the upstream
// commits that a build applies to the release, which the SBOM states
// as the component's pedigree, so it does not name the stock release.
var optionalFields = []string{"scope", "type", "cpe", "backports"}

// githubCommit is the form of a backport: the URL of one commit of a
// GitHub repository, by its full id. toolchains/manifest takes the
// last element as the commit's id and adds .patch for its diff.
var githubCommit = regexp.MustCompile(`^https://github\.com/[^/]+/[^/]+/commit/[0-9a-f]{40}$`)

// componentTypes are the component types of CycloneDX 1.6.
var componentTypes = []string{
	"application", "framework", "library", "container", "platform",
	"operating-system", "device", "device-driver", "firmware", "file",
	"machine-learning-model", "data", "cryptographic-asset",
}

// imageRecorder is the command that writes an image's record of the
// files that its pins name. A Dockerfile that fetches a file of a
// manifest runs it, or the image's SBOM leaves the file out.
const imageRecorder = "image-sbom"

// unmanifestedDownloads names each Dockerfile whose downloads by
// checksum need no manifest, with the reason, because the image ships
// none of the files.
var unmanifestedDownloads = map[string]string{
	"bluetooth-operator/bluetoothd/Dockerfile": "buildroot and the SPDX license list are the build system of the daemons, and buildroot's own SBOM names each package that the image ships",
}

// unmanifested names the directories whose fetch.sh needs no manifest,
// with the reason, because a release does not ship what they download,
// or because another SBOM already lists it.
var unmanifested = map[string]string{
	"liken/userland":             "buildroot writes the CycloneDX SBOM of the programs it builds",
	"liken/dev-cluster/firmware": "the lab's firmware ships in no release",
	"liken/dev-cluster/storage":  "the lab's storage guest ships in no release",
	"toolchains/cargo-auditable": "cargo-auditable is a build tool, and no image ships it",
}

// nestedPins names the entries of a manifest that no VERSION file pins.
// linux-firmware/fetch.sh downloads wireless-regdb beside the firmware,
// and the regulatory database has its own releases, so its manifest
// entry alone holds its version. The k3s-root release in
// xtables/VERSION builds iptables with a release of buildroot, and the
// source mirror offers the source of both at the versions that the
// release names, which xtables/latest.sh --bump writes.
var nestedPins = map[string][]string{
	"liken/linux-firmware": {"wireless-regdb"},
	"liken/xtables":        {"iptables-source", "buildroot-source"},
}

// sourceMirror is the script that mirrors the corresponding source of
// what an OS release ships onto the release channel. Each file that it
// mirrors belongs to the pin of the binary that was built from it, so
// the file's url and sha256 are an entry of that pin's manifest, which
// the pin's latest.sh --bump writes in the same bump that moves the
// binary. A digest in the script itself would be a second pin that no
// bump moves.
const sourceMirror = "liken/licensing/sources.sh"

// digestLiteral matches a sha256 or a sha512 in hex, which a fetch.sh
// in a manifested directory and the source mirror must not hold.
var digestLiteral = regexp.MustCompile(`\b([0-9a-f]{128}|[0-9a-f]{64})\b`)

// sha256Hex matches a whole sha256 in hex, the form of an entry's
// sha256.
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// addChecksum matches a Dockerfile's ADD of a URL by its checksum.
var addChecksum = regexp.MustCompile(`^ADD\b.*--checksum=sha256:([0-9a-f]*)\s+(\S+://\S+)`)

// argDefault matches an ARG with a default value.
var argDefault = regexp.MustCompile(`^ARG\s+([A-Za-z_][A-Za-z0-9_]*)=(\S+)`)

// checkManifests lists each pin whose manifest is missing, incomplete,
// or different from what its fetch downloads. Root AGENTS.md states the
// rule under "Pins and downloads":
//
//   - each directory with a fetch.sh holds a pin.toml, unless
//     unmanifested names it;
//   - each entry of a pin.toml holds a version, a url, a sha256, a purl,
//     and a license, and nothing else but an optional scope, type, and
//     cpe;
//   - an entry's version is the one that the directory's VERSION pins,
//     and its purl and its CPE name the same version, the purl alone or
//     after the prefix of a tag, as in microcode-20260925; a
//     pkg:generic purl repeats the url as its download_url and the
//     sha256 as its checksum;
//   - the fetch.sh holds no digest of its own and downloads through
//     fetch_pinned, which reads the url and the sha256 from pin.toml;
//   - each Dockerfile that ADDs a URL by its checksum has a pin.toml
//     beside it that names the URL at that digest, unless the file is a
//     Debian package or unmanifestedDownloads names the Dockerfile, and
//     a Dockerfile that ADDs a file that a manifest names runs
//     image-sbom, which writes the image's record of the file;
//   - a sums file beside a pin.toml, such as models.sha256, names the
//     file of each entry by the entry's digest;
//   - the source mirror holds no digest of its own.
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

// checkManifest checks the manifest of one directory against its
// VERSION and its fetch.sh, and returns its entries, or none when the
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
		if !slices.Contains(manifestFields, field) && !slices.Contains(optionalFields, field) {
			findings = append(findings, fmt.Sprintf("%s: [%s] holds %s, which is not a field of a manifest", manifest, name, field))
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

// mirrorFindings lists each digest that the source mirror holds. A
// comment can quote a digest, so the check reads the commands alone.
func mirrorFindings(text string) []string {
	var findings []string
	for _, l := range logicalLines(text) {
		if digestLiteral.MatchString(l.text) {
			findings = append(findings, fmt.Sprintf("%s:%d holds a digest; the digest of each source that it mirrors is in the %s of the pin that the source belongs to", sourceMirror, l.n, pinManifest))
		}
	}
	return findings
}

// cpeFindings lists what is wrong with an entry's CPE: a name that is
// not a CPE 2.3 formatted string of 13 parts, or a version other than
// the entry's. A CPE quotes some characters with a backslash, which
// the comparison removes.
func cpeFindings(manifest, name, cpe, version string) []string {
	parts := strings.Split(cpe, ":")
	if len(parts) != 13 || parts[0] != "cpe" || parts[1] != "2.3" {
		return []string{fmt.Sprintf("%s: [%s] has the CPE %q, which is not a CPE 2.3 name of 13 parts", manifest, name, cpe)}
	}
	if got := strings.ReplaceAll(parts[5], `\`, ""); got != version {
		return []string{fmt.Sprintf("%s: [%s] has a CPE at %s, and its version is %s", manifest, name, got, version)}
	}
	return nil
}

// imageFindings lists each ADD of a Dockerfile that fetches a URL by
// its checksum and that no manifest beside the Dockerfile names at that
// checksum, and a Dockerfile that fetches a file of a manifest and runs
// no image-sbom. BuildKit reads the checksum from the Dockerfile's own
// text, so the Dockerfile keeps a copy of the digest, and this check
// holds the copy to the manifest. The manifest is what image-sbom reads
// to write the image's record of the file, because syft finds nothing
// in a file that a Dockerfile fetches.
//
// A Debian package is the exception: the closure that unpacks it writes
// the package's control file into /var/lib/dpkg/status.d, and syft
// reads the package from there.
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
				findings = append(findings, fmt.Sprintf("%s:%d fetches %s by sha256:%s, and %s names %s", file, l.n, address, digest, manifest, entry["sha256"]))
			}
		}
		if !found {
			findings = append(findings, fmt.Sprintf("%s:%d fetches %s, and %s names no file at that url", file, l.n, address, manifest))
		}
	}
	if shipsEntry && !records {
		findings = append(findings, fmt.Sprintf("%s fetches a file that %s names, and runs no %s, so the image's SBOM does not list the file", file, manifest, imageRecorder))
	}
	return findings
}

// expandArgs replaces each ${NAME} and $NAME in a line with the default
// of the ARG of that name, so a checksum that an argument holds is
// compared as BuildKit reads it. A name with no default stays as it is.
func expandArgs(text string, args map[string]string) string {
	return os.Expand(text, func(name string) string {
		if value, ok := args[name]; ok {
			return value
		}
		return "${" + name + "}"
	})
}

// sumsFindings lists each line of a sums file, such as models.sha256,
// that names the file of a manifest entry by another digest. A build
// that checks the files against the sums file and an SBOM that reads
// the manifest must name the same bytes.
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
				findings = append(findings, fmt.Sprintf("%s:%d names %s by sha256:%s, and %s names %s", file, i+1, name, digest, manifest, entry["sha256"]))
			}
		}
	}
	return findings
}
