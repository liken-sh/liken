package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trustManifest is the manifest of a pin whose generic purl repeats its
// url and its sha256, with the type that an image's record needs.
var trustManifest = "[cacert]\n" +
	"version = \"2026-10-20\"\n" +
	"url = \"https://curl.se/ca/cacert-2026-10-20.pem\"\n" +
	"sha256 = \"" + strings.Repeat("e", 64) + "\"\n" +
	"purl = \"pkg:generic/cacert@2026-10-20?download_url=https://curl.se/ca/cacert-2026-10-20.pem&checksum=sha256:" + strings.Repeat("e", 64) + "\"\n" +
	"license = \"MPL-2.0\"\n" +
	"type = \"library\"\n"

// manifestsTree is a repository in which every pin that ships has a
// manifest that agrees with its fetch: a domain with one file, a
// domain with a second pin nested in it, a domain whose image fetches
// the same file by its checksum, and a domain that another SBOM
// covers, which has no manifest.
func manifestsTree(t *testing.T, changes map[string]string) string {
	t.Helper()
	tree := map[string]string{
		"liken/k3s/VERSION": "v1.37.1+k3s1\n",
		"liken/k3s/fetch.sh": "#!/usr/bin/env bash\n" +
			"source \"$here/../../toolchains/pin.sh\"\n" +
			"version=\"$(cat \"$here/VERSION\")\"\n" +
			"fetch_pinned --resume k3s \"$version\" \"$cache/k3s\"\n",
		"liken/k3s/pin.toml": "# What k3s/fetch.sh downloads.\n" +
			"[k3s]\n" +
			"version = \"v1.37.1+k3s1\"\n" +
			"url = \"https://github.com/k3s-io/k3s/releases/download/v1.37.1%2Bk3s1/k3s\"\n" +
			"sha256 = \"" + strings.Repeat("a", 64) + "\"\n" +
			"purl = \"pkg:github/k3s-io/k3s@v1.37.1%2Bk3s1\"\n" +
			"license = \"Apache-2.0\"\n",
		"liken/linux-firmware/VERSION": "20261019\n",
		"liken/linux-firmware/fetch.sh": "#!/usr/bin/env bash\n" +
			"source \"$here/../../toolchains/pin.sh\"\n" +
			"fetch_pinned --resume linux-firmware \"$version\" \"$cache/$tarball\"\n" +
			"fetch_pinned wireless-regdb \"$(pin_value wireless-regdb version)\" \"$cache/$regdb\"\n",
		"liken/linux-firmware/pin.toml": "[linux-firmware]\n" +
			"version = \"20261019\"\n" +
			"url = \"https://cdn.kernel.org/linux-firmware-20261019.tar.xz\"\n" +
			"sha256 = \"" + strings.Repeat("b", 64) + "\"\n" +
			"purl = \"pkg:generic/linux-firmware@20261019?download_url=https://cdn.kernel.org/linux-firmware-20261019.tar.xz&checksum=sha256:" + strings.Repeat("b", 64) + "\"\n" +
			"license = \"LicenseRef-linux-firmware\"\n" +
			"\n" +
			"[wireless-regdb]\n" +
			"version = \"2026.09.03\"\n" +
			"url = \"https://mirrors.edge.kernel.org/wireless-regdb-2026.09.03.tar.xz\"\n" +
			"sha256 = \"" + strings.Repeat("c", 64) + "\"\n" +
			"purl = \"pkg:generic/wireless-regdb@2026.09.03?download_url=https://mirrors.edge.kernel.org/wireless-regdb-2026.09.03.tar.xz&checksum=sha256:" + strings.Repeat("c", 64) + "\"\n" +
			"license = \"ISC\"\n",
		"liken/grub/VERSION": "2.14-2ubuntu3\n",
		"liken/grub/fetch.sh": "#!/usr/bin/env bash\n" +
			"source \"$here/../../toolchains/pin.sh\"\n" +
			"fetch_pinned --resume grub-pc-bin \"$version\" \"$cache/$pc_bin\"\n",
		"liken/grub/pin.toml": "[grub-pc-bin]\n" +
			"version = \"2.14-2ubuntu3\"\n" +
			"url = \"http://archive.ubuntu.com/grub-pc-bin_2.14-2ubuntu3_amd64.deb\"\n" +
			"sha256 = \"" + strings.Repeat("d", 64) + "\"\n" +
			"purl = \"pkg:deb/ubuntu/grub-pc-bin@2.14-2ubuntu3?arch=amd64\"\n" +
			"license = \"GPL-3.0-or-later\"\n" +
			"scope = \"excluded\"\n",
		"trust/VERSION": "2026-10-20\n",
		"trust/fetch.sh": "#!/usr/bin/env bash\n" +
			"source \"$here/../toolchains/pin.sh\"\n" +
			"fetch_pinned cacert \"$version\" \"$cache/cacert.pem\"\n",
		"trust/pin.toml": trustManifest,
		"trust/Dockerfile": "FROM scratch AS fetch\n" +
			"ADD --checksum=sha256:" + strings.Repeat("e", 64) + " \\\n" +
			"    https://curl.se/ca/cacert-2026-10-20.pem /cacert.pem\n",
		"liken/microcode/VERSION": "20260925\n",
		"liken/microcode/fetch.sh": "#!/usr/bin/env bash\n" +
			"fetch_pinned --resume intel-microcode \"$version\" \"$cache/$tarball\"\n",
		"liken/microcode/pin.toml": "[intel-microcode]\n" +
			"version = \"20260925\"\n" +
			"url = \"https://github.com/intel/x/archive/refs/tags/microcode-20260925.tar.gz\"\n" +
			"sha256 = \"" + strings.Repeat("a", 64) + "\"\n" +
			"purl = \"pkg:github/intel/x@microcode-20260925\"\n" +
			"license = \"LicenseRef-Intel-Microcode\"\n",
		"liken/licensing/sources.sh": "#!/usr/bin/env bash\n" +
			"mirror \"kernel/$kernel_version\" \"$here/../kernel/pin.toml\" linux-source \"$kernel_version\"\n",
		"liken/userland/VERSION": "abc\n",
		"liken/userland/fetch.sh": "#!/usr/bin/env bash\n" +
			"source \"$here/../../toolchains/pin.sh\"\n" +
			"fetch_verified \"$url\" \"" + strings.Repeat("f", 64) + "\" \"$cache/buildroot.tar.gz\"\n",
	}
	for name, text := range changes {
		tree[name] = text
	}
	return writeTree(t, tree)
}

func TestEveryPinThatShipsHasAManifestThatAgreesWithItsFetch(t *testing.T) {
	findings, err := checkManifests(manifestsTree(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) > 0 {
		t.Errorf("the tree breaks a rule:\n%s", strings.Join(findings, "\n"))
	}
}

func TestAManifestThatBreaksARuleIsFound(t *testing.T) {
	k3s := func(old, new string) string {
		return strings.Replace(
			"[k3s]\n"+
				"version = \"v1.37.1+k3s1\"\n"+
				"url = \"https://github.com/k3s-io/k3s/releases/download/v1.37.1%2Bk3s1/k3s\"\n"+
				"sha256 = \""+strings.Repeat("a", 64)+"\"\n"+
				"purl = \"pkg:github/k3s-io/k3s@v1.37.1%2Bk3s1\"\n"+
				"license = \"Apache-2.0\"\n", old, new, 1)
	}
	cases := map[string]struct {
		file, text, wants string
	}{
		"a pin that ships with no manifest": {"liken/hwdata/fetch.sh",
			"#!/usr/bin/env bash\nfetch_pinned hwdata \"$version\" \"$cache/pci.ids\"\n",
			"liken/hwdata/fetch.sh downloads a file that a release ships, and liken/hwdata has no pin.toml"},
		"an entry with no license": {"liken/k3s/pin.toml",
			k3s("license = \"Apache-2.0\"\n", ""),
			"liken/k3s/pin.toml: [k3s] has no license"},
		"an entry with an empty purl": {"liken/k3s/pin.toml",
			k3s("pkg:github/k3s-io/k3s@v1.37.1%2Bk3s1", ""),
			"liken/k3s/pin.toml: [k3s] has no purl"},
		"an entry with a key no manifest holds": {"liken/k3s/pin.toml",
			k3s("license = ", "licence = \"x\"\nlicense = "),
			"liken/k3s/pin.toml: [k3s] holds licence, which is not a field of a manifest"},
		"an entry with a scope CycloneDX does not name": {"liken/k3s/pin.toml",
			k3s("license = ", "scope = \"build\"\nlicense = "),
			"liken/k3s/pin.toml: [k3s] has the scope \"build\", which is not required, optional, or excluded"},
		"a digest that is not a sha256": {"liken/k3s/pin.toml",
			k3s(strings.Repeat("a", 64), strings.Repeat("a", 40)),
			"liken/k3s/pin.toml: [k3s] has a sha256 that is not 64 hex digits"},
		"a purl that is not a package URL": {"liken/k3s/pin.toml",
			k3s("pkg:github/", "github/"),
			"liken/k3s/pin.toml: [k3s] has the purl \"github/k3s-io/k3s@v1.37.1%2Bk3s1\", which does not start with pkg:"},
		"a purl with no version": {"liken/k3s/pin.toml",
			k3s("k3s@v1.37.1%2Bk3s1", "k3s"),
			"liken/k3s/pin.toml: [k3s] has a purl at , and its version is v1.37.1+k3s1"},
		"a purl at another version": {"liken/k3s/pin.toml",
			k3s("k3s@v1.37.1%2Bk3s1", "k3s@v1.36.0%2Bk3s1"),
			"liken/k3s/pin.toml: [k3s] has a purl at v1.36.0+k3s1, and its version is v1.37.1+k3s1"},
		"a manifest at another version than VERSION": {"liken/k3s/VERSION", "v1.38.0+k3s1\n",
			"liken/k3s/pin.toml: [k3s] is at v1.37.1+k3s1, and liken/k3s/VERSION pins v1.38.0+k3s1, the version that fetch.sh downloads"},
		"a generic purl with another download URL": {"trust/pin.toml",
			strings.Replace(trustManifest, "download_url=https://curl.se/ca/", "download_url=https://example.org/", 1),
			"trust/pin.toml: [cacert] has a purl whose download_url is https://example.org/cacert-2026-10-20.pem, and its url is https://curl.se/ca/cacert-2026-10-20.pem"},
		"a generic purl with another checksum": {"trust/pin.toml",
			strings.Replace(trustManifest, "checksum=sha256:e", "checksum=sha256:0", 1),
			"trust/pin.toml: [cacert] has a purl whose checksum is sha256:0" + strings.Repeat("e", 63) + ", and its sha256 is " + strings.Repeat("e", 64)},
		"a generic purl with no checksum": {"trust/pin.toml",
			strings.Replace(trustManifest, "&checksum=sha256:"+strings.Repeat("e", 64), "", 1),
			"trust/pin.toml: [cacert] has a purl whose checksum is , and its sha256 is"},
		"a fetch that holds its own digest": {"liken/k3s/fetch.sh",
			"#!/usr/bin/env bash\ndeclare -A k3s_sha256=(\n    [\"v1\"]=\"" + strings.Repeat("a", 64) + "\"\n)\nfetch_pinned k3s \"$version\" \"$cache/k3s\"\n",
			"liken/k3s/fetch.sh:3 holds a digest; the digest of each file that a fetch.sh downloads is in pin.toml"},
		"a fetch that does not read its manifest": {"liken/k3s/fetch.sh",
			"#!/usr/bin/env bash\nfetch_verified \"$url\" \"$digest\" \"$cache/k3s\"\n",
			"liken/k3s/fetch.sh does not download through fetch_pinned, so it does not read pin.toml"},
		"an image that fetches by another checksum": {"trust/Dockerfile",
			"FROM scratch\nADD --checksum=sha256:" + strings.Repeat("0", 64) + " https://curl.se/ca/cacert-2026-10-20.pem /cacert.pem\n",
			"trust/Dockerfile:2 fetches https://curl.se/ca/cacert-2026-10-20.pem by sha256:" + strings.Repeat("0", 64) + ", and trust/pin.toml names " + strings.Repeat("e", 64)},
		"a source mirror that holds a digest": {"liken/licensing/sources.sh",
			"#!/usr/bin/env bash\n# Mirror the sources.\nmirror \"xtables/$v\" \"iptables-1.8.11.tar.xz\" \\\n    \"" + strings.Repeat("a", 64) + "\" \\\n    \"https://www.netfilter.org/pub/iptables/iptables-1.8.11.tar.xz\"\n",
			"liken/licensing/sources.sh:3 holds a digest; the digest of each source that it mirrors is in the pin.toml of the pin that the source belongs to"},
		"an entry with a type CycloneDX does not name": {"trust/pin.toml",
			strings.Replace(trustManifest, "\"library\"", "\"tarball\"", 1),
			"trust/pin.toml: [cacert] has the type \"tarball\", which is not a CycloneDX component type"},
		"a CPE at another version": {"trust/pin.toml",
			trustManifest + "cpe = \"cpe:2.3:a:mozilla:cacert:2026-10-19:*:*:*:*:*:*:*\"\n",
			"trust/pin.toml: [cacert] has a CPE at 2026-10-19, and its version is 2026-10-20"},
		"a CPE that is not a CPE 2.3 name": {"trust/pin.toml",
			trustManifest + "cpe = \"cpe:/a:mozilla:cacert:2026-10-20\"\n",
			"trust/pin.toml: [cacert] has the CPE \"cpe:/a:mozilla:cacert:2026-10-20\", which is not a CPE 2.3 name of 13 parts"},
		"an image that fetches a file no entry names": {"trust/Dockerfile",
			"FROM scratch\nADD --checksum=sha256:" + strings.Repeat("e", 64) + " https://curl.se/ca/cacert-2026-10-21.pem /cacert.pem\n",
			"trust/Dockerfile:2 fetches https://curl.se/ca/cacert-2026-10-21.pem, and trust/pin.toml names no file at that url"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			findings, err := checkManifests(manifestsTree(t, map[string]string{c.file: c.text}))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(findings, "\n"), c.wants) {
				t.Errorf("got:\n%s\nwant a finding with %q", strings.Join(findings, "\n"), c.wants)
			}
		})
	}
}

func TestAManifestThatDoesNotParseIsAnError(t *testing.T) {
	_, err := checkManifests(manifestsTree(t, map[string]string{"liken/k3s/pin.toml": "[k3s\n"}))
	if err == nil {
		t.Errorf("a manifest that is not TOML passes")
	}
}

func TestThePinsCommandFailsOnAManifestThatDisagrees(t *testing.T) {
	root := pinsTree(t, map[string]string{
		"liken/k3s/VERSION": "v2\n",
		"liken/k3s/fetch.sh": "#!/bin/sh\nsource \"$here/../../toolchains/pin.sh\"\n" +
			"fetch_pinned k3s \"$version\" \"$cache/k3s\"\n",
		"liken/k3s/latest.sh": "#!/bin/sh\nsource \"$here/../../toolchains/pin.sh\"\n",
		"liken/k3s/pin.toml": "[k3s]\nversion = \"v1\"\nurl = \"https://x/k3s\"\n" +
			"sha256 = \"" + strings.Repeat("a", 64) + "\"\npurl = \"pkg:github/k3s-io/k3s@v1\"\nlicense = \"Apache-2.0\"\n",
	})
	if err := os.Chmod(filepath.Join(root, "liken/k3s/latest.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, &fakeRegistry{})
	err := w.run([]string{"pins", "-root", root})
	if err == nil || !strings.Contains(err.Error(), "1 manifests disagree") {
		t.Errorf("the pins command passes a manifest at v1 for a fetch of v2: %v", err)
	}
}
