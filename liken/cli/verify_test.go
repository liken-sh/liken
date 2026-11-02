package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/verify/verifytest"
)

func TestVerifyAndSBOMCheckTheirArguments(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"verify with no subcommand", []string{"verify"}},
		{"verify with an unknown subcommand", []string{"verify", "launder"}},
		{"verify release with no version", []string{"verify", "release"}},
		{"verify image with no reference", []string{"verify", "image"}},
		{"sbom with no target", []string{"sbom", "-json"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := run(c.args)
			if err == nil || !strings.Contains(err.Error(), "usage:") {
				t.Errorf("bad arguments were not refused: %v", err)
			}
		})
	}
}

// The channel holds the release's documents and its SBOM, and none of
// its larger artifacts, so the verify stops at the first of them.
func TestVerifyReleaseReportsEachFile(t *testing.T) {
	server := verifytest.ServeChannel(t, verifytest.Version, verifytest.Published(t))
	var out bytes.Buffer
	err := verifyCommand(t.Context(), []string{"release", "-source", server.URL, verifytest.Version}, &out)
	if !strings.HasPrefix(out.String(), "ok  release.yaml  signed by https://github.com/liken-sh/liken/") {
		t.Errorf("the output is %q", out.String())
	}
	if err == nil || !strings.HasPrefix(err.Error(), "vmlinuz:") {
		t.Errorf("the verify returned %v, want a failure that names vmlinuz", err)
	}
}

func TestVerifyImageReportsTheSignerAndAttestations(t *testing.T) {
	r := verifytest.NewRegistry(t)
	r.Publish(t)
	r.Sign(t, verifytest.ImageFile(t, "signature.sigstore.json"))
	var out bytes.Buffer
	if err := verifyCommand(t.Context(), []string{"image", r.Repo.Tag(verifytest.Version).String()}, &out); err != nil {
		t.Fatal(err)
	}
	want := "ok  " + r.Repo.Digest(verifytest.IndexDigest).String() +
		"  signed by https://github.com/liken-sh/liken/.github/workflows/component-bluetooth-operator.yaml@refs/tags/2026.11.01-001\n" +
		"ok  linux/amd64  SBOM of 21 packages, provenance https://slsa.dev/provenance/v1\n"
	if out.String() != want {
		t.Errorf("the output is\n%s\nwant\n%s", out.String(), want)
	}
}

func TestVerifyImageRefusesAnUnsignedImage(t *testing.T) {
	r := verifytest.NewRegistry(t)
	r.Publish(t)
	err := verifyCommand(t.Context(), []string{"image", r.Repo.Tag(verifytest.Version).String()}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "carries no Sigstore signature") {
		t.Errorf("the verify returned %v", err)
	}
}

func TestSBOMOfARelease(t *testing.T) {
	server := verifytest.ServeChannel(t, verifytest.Version, verifytest.Published(t))
	cases := []struct {
		name  string
		flags []string
		check func(t *testing.T, out string)
	}{
		{"as a table", nil, func(t *testing.T, out string) {
			lines := strings.Split(out, "\n")
			if len(lines) != 746 || !strings.HasPrefix(lines[0], "NAME") || !strings.HasPrefix(lines[1], "flux ") {
				t.Errorf("the table has %d lines and starts %q", len(lines), lines[:2])
			}
		}},
		{"as the document", []string{"-json"}, func(t *testing.T, out string) {
			if out != string(verifytest.ReleaseFile(t, "sbom.cdx.json")) {
				t.Error("the output is not the published SBOM")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			args := append(append([]string{"-source", server.URL}, c.flags...), verifytest.Version)
			if err := sbomCommand(t.Context(), args, &out); err != nil {
				t.Fatal(err)
			}
			c.check(t, out.String())
		})
	}
}

func TestSBOMOfAnImage(t *testing.T) {
	r := verifytest.NewRegistry(t)
	r.Publish(t)
	r.Sign(t, verifytest.ImageFile(t, "signature.sigstore.json"))
	cases := []struct {
		name  string
		flags []string
		want  string
	}{
		{"as a table", nil, "NAME                                    VERSION       PURL\nbluez-static                            5.87"},
		{"as the document", []string{"-json"}, `{"spdxVersion":"SPDX-2.3",`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := sbomCommand(t.Context(), append(c.flags, r.Repo.Tag(verifytest.Version).String()), &out); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(out.String(), c.want) {
				t.Errorf("the output starts %q, want %q", out.String()[:min(len(out.String()), 120)], c.want)
			}
		})
	}
}

func TestSBOMRefusesARelease(t *testing.T) {
	files := verifytest.Published(t)
	files["sbom.cdx.json"] = verifytest.Tampered(files["sbom.cdx.json"])
	server := verifytest.ServeChannel(t, verifytest.Version, files)
	err := sbomCommand(t.Context(), []string{"-source", server.URL, verifytest.Version}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "sbom.cdx.json digest mismatch") {
		t.Errorf("the sbom returned %v", err)
	}
}
