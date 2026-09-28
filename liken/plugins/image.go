package plugins

// This file derives a CLI image from its operator image, pulls it
// from the registry for the workstation's architecture, and extracts
// the single binary the image runs as its entrypoint. The pull uses
// go-containerregistry compiled into the base binary, so the
// workstation needs no docker, oras, or crane.

import (
	"archive/tar"
	"fmt"
	"io"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// cliImageRef derives the CLI image reference from an operator image
// reference: the repository gains a -cli suffix and the tag stays
// the same, so the CLI comes from the same version as the running
// operator and the two never drift. A reference by digest carries no
// version tag to reuse, so it has no derived CLI.
func cliImageRef(operatorImage string) (string, error) {
	if strings.Contains(operatorImage, "@") {
		return "", fmt.Errorf("image %q is pinned by digest and names no version tag", operatorImage)
	}
	tag, err := name.NewTag(operatorImage)
	if err != nil {
		return "", fmt.Errorf("reading image reference %q: %w", operatorImage, err)
	}
	return tag.Context().Name() + "-cli:" + tag.TagStr(), nil
}

// ImageVersion reports the version tag an operator image carries, the
// universal source every CLI compares against.
func ImageVersion(operatorImage string) (string, error) {
	if strings.Contains(operatorImage, "@") {
		return "", fmt.Errorf("image %q is pinned by digest and names no version tag", operatorImage)
	}
	tag, err := name.NewTag(operatorImage)
	if err != nil {
		return "", fmt.Errorf("reading image reference %q: %w", operatorImage, err)
	}
	return tag.TagStr(), nil
}

// entrypoint reports the path of the binary an image runs. The CLI
// image runs its binary as its entrypoint, so the first entrypoint
// element names the file to extract.
func entrypoint(cfg *v1.ConfigFile) (string, error) {
	if cfg == nil || len(cfg.Config.Entrypoint) == 0 {
		return "", fmt.Errorf("image declares no entrypoint")
	}
	return cfg.Config.Entrypoint[0], nil
}

// binaryFromTar reads one file out of a flattened image filesystem.
// Tar entries carry paths as "./x" or "x" where the entrypoint names
// "/x", so both sides drop a leading slash and "./" before they
// compare.
func binaryFromTar(r io.Reader, path string) ([]byte, error) {
	want := strings.TrimPrefix(path, "/")
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(header.Name, "./")
		name = strings.TrimPrefix(name, "/")
		if name == want {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("no file %q in the image", path)
}

// pullBinary is the seam the tests replace. It pulls one CLI image
// for one architecture and returns its binary.
var pullBinary = remotePull

// remotePull pulls the CLI image for the workstation's architecture,
// reads the binary its entrypoint names, and returns it.
func remotePull(ref, arch string) ([]byte, error) {
	tag, err := name.NewTag(ref)
	if err != nil {
		return nil, fmt.Errorf("reading image reference %q: %w", ref, err)
	}
	image, err := remote.Image(tag,
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithPlatform(v1.Platform{OS: "linux", Architecture: arch}))
	if err != nil {
		return nil, fmt.Errorf("pulling %q: %w", ref, err)
	}
	cfg, err := image.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("reading the config of %q: %w", ref, err)
	}
	path, err := entrypoint(cfg)
	if err != nil {
		return nil, fmt.Errorf("%q: %w", ref, err)
	}
	fs := mutate.Extract(image)
	defer fs.Close()
	return binaryFromTar(fs, path)
}
