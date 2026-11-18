package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// publishTools are the images that a publish runs, pinned by digest in
// toolchains/images.toml. Cosign signs each image that the publish
// pushes. Scanner is the syft image that buildx runs to write each
// SBOM. buildx's default scanner is a tag that moves, so without the
// pin the SBOM generator could change while the tree stays the same.
type publishTools struct{ Cosign, Scanner string }

func readPublishTools(root string) (publishTools, error) {
	tools, err := readToolchains(root)
	if err != nil {
		return publishTools{}, err
	}
	t := publishTools{Cosign: tools.Build["cosign"], Scanner: tools.Build["buildkit-syft-scanner"]}
	switch {
	case t.Cosign == "":
		return t, fmt.Errorf("%s has no cosign entry in [build], so the publish cannot sign an image", imagesFile)
	case t.Scanner == "":
		return t, fmt.Errorf("%s has no buildkit-syft-scanner entry in [build], so the publish cannot write an SBOM", imagesFile)
	}
	return t, nil
}

// attestArgs returns the bake arguments that attach an SBOM from the
// pinned syft scanner and max-mode provenance to the image that a
// publish pushes. Max mode records the build arguments, the named
// contexts, the Dockerfile, and the digest of each base image and
// download. buildx stores both attestations in the image's index, so
// the index's digest covers them.
//
// The arguments set the attestations on one target, because bake's
// --sbom and --provenance flags apply to every target of the build,
// including a base that the build uses only as a context and never
// pushes. The bake file sets none, because the images job and
// workstation builds load the image into Docker and push nothing.
func (t publishTools) attestArgs(target string) []string {
	return []string{
		"--set", target + ".attest=type=sbom,generator=" + t.Scanner,
		"--set", target + ".attest=type=provenance,mode=max",
	}
}

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// pushedDigest returns the digest of the image that bake pushed for
// the target, read from the file that --metadata-file wrote. When the
// build has attestations, the digest is the one of the index that
// lists the image and its attestations.
func pushedDigest(file, target string) (string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	var metadata map[string]map[string]any
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", fmt.Errorf("parsing the metadata of %s: %w", target, err)
	}
	digest, _ := metadata[target]["containerimage.digest"].(string)
	if !digestPattern.MatchString(digest) {
		return "", fmt.Errorf("the metadata of %s has no containerimage.digest of the form sha256:<64 hex digits>", target)
	}
	return digest, nil
}

// sign signs each reference with cosign, keyless. In a GitHub Actions
// job with id-token: write, cosign requests the job's OIDC token with
// ACTIONS_ID_TOKEN_REQUEST_URL and ACTIONS_ID_TOKEN_REQUEST_TOKEN, Fulcio issues a certificate
// that names the job's workflow as the signer, and Rekor records the
// signature. cosign pushes the signature to the image's repository,
// using the credential that docker/login-action wrote to the Docker
// configuration. The container runs as the calling user so that it can
// read that configuration, and HOME is /tmp so that cosign has a
// writable place to store the Sigstore trust root it downloads.
//
// cosign signs the digest that the reference names, or that its tag
// resolves to. A publish pushes each image as an index whose digest
// covers the image and both attestations, so one signature covers all
// three.
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

// dockerConfig returns the directory that holds docker's configuration
// and the credential that docker/login-action wrote: DOCKER_CONFIG, or
// ~/.docker when it is not set.
func dockerConfig(getenv func(string) string) string {
	if dir := getenv("DOCKER_CONFIG"); dir != "" {
		return dir
	}
	return filepath.Join(getenv("HOME"), ".docker")
}
