package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// publishTools are the two outside images that a publish runs, each
// pinned by its digest in toolchains/images.toml. Cosign signs each
// image that the publish pushes, and it is the same pinned build that
// the bump scripts verify upstream signatures with. Scanner is the
// syft scanner that buildx runs to write each SBOM. Without a pin,
// buildx pulls its default scanner by a moving tag, and the program
// that writes the SBOM would change with no change to the tree.
type publishTools struct{ Cosign, Scanner string }

func readPublishTools(root string) (publishTools, error) {
	tools, err := readToolchains(root)
	if err != nil {
		return publishTools{}, err
	}
	t := publishTools{Cosign: tools.Build["cosign"], Scanner: tools.Build["buildkit-syft-scanner"]}
	switch {
	case t.Cosign == "":
		return t, fmt.Errorf("%s names no cosign image, so the publish cannot sign an image", imagesFile)
	case t.Scanner == "":
		return t, fmt.Errorf("%s names no buildkit-syft-scanner image, so the publish cannot write an SBOM", imagesFile)
	}
	return t, nil
}

// attestArgs makes buildx attach two attestations to the image that a
// publish pushes: an SBOM, which the pinned syft scanner writes, and
// the build's provenance in max mode, which records the build
// arguments, the named contexts, the Dockerfile, and the digest of
// each base image and download. buildx stores both in the image's
// index, beside the image, so the digest of the index covers them.
//
// The setting is on the target that the command names, and not on the
// bake file or on bake's --sbom and --provenance flags. The flags set
// every target of the build, so a base that the build needs as a
// context would carry the setting too, and it is never pushed. The
// images job of each component's workflow and a workstation build load
// the image into Docker, where an attestation has no use, so the bake
// file carries none.
func (t publishTools) attestArgs(target string) []string {
	return []string{
		"--set", target + ".attest=type=sbom,generator=" + t.Scanner,
		"--set", target + ".attest=type=provenance,mode=max",
	}
}

// digestPattern is the form of an image digest in buildx's metadata.
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// pushedDigest reads the digest of the image that a bake pushed for
// the target from the file that --metadata-file wrote. With
// attestations, the digest names the index that holds the image and
// its attestations.
func pushedDigest(file, target string) (string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	var metadata map[string]map[string]any
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", fmt.Errorf("the metadata of %s: %w", target, err)
	}
	digest, _ := metadata[target]["containerimage.digest"].(string)
	if !digestPattern.MatchString(digest) {
		return "", fmt.Errorf("the metadata of %s names no digest", target)
	}
	return digest, nil
}

// sign signs each reference with cosign, keyless: in a GitHub Actions
// job that holds id-token: write, cosign takes the job's OIDC token
// from the two ACTIONS_ID_TOKEN_REQUEST variables, Fulcio certifies
// the job's workflow as the signer, and Rekor records the signature.
// cosign pushes the signature to the image's repository, under the
// credential that docker/login-action wrote to the Docker
// configuration. The container runs as the calling user, so it reads
// that configuration, and HOME points at a writable directory for the
// sigstore trust root that cosign fetches.
//
// A signature covers the digest that it names. The digest of an index
// covers the SBOM and the provenance in it, so one signature covers
// the image and both attestations.
func (p Publisher) sign(cosign string, references ...string) error {
	for _, reference := range references {
		err := p.Run(p.Root, "docker", "run", "--rm",
			"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "-e", "HOME=/tmp",
			"-e", "ACTIONS_ID_TOKEN_REQUEST_URL", "-e", "ACTIONS_ID_TOKEN_REQUEST_TOKEN",
			"-e", "DOCKER_CONFIG=/docker", "-v", p.DockerConfig+":/docker:ro",
			cosign, "sign", "--yes", reference)
		if err != nil {
			return fmt.Errorf("signing %s: %w", reference, err)
		}
	}
	return nil
}

// dockerConfig is the directory that docker reads its configuration
// from, and docker/login-action writes the credential to:
// DOCKER_CONFIG, or ~/.docker.
func dockerConfig(getenv func(string) string) string {
	if dir := getenv("DOCKER_CONFIG"); dir != "" {
		return dir
	}
	return filepath.Join(getenv("HOME"), ".docker")
}
