package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Keep every Trivy setting in this file or in ci/templates/trivy.tmpl.
// trivyConfig lists both files, so a push to main that changes either
// one scans every published image again. See Planner.Rescan.

// trivyVersion is the Trivy release that every workflow step installs.
// ci/latest.sh compares it with Trivy's newest release.
const trivyVersion = "0.76.2"

// trivySHA256 is the SHA-256 of Trivy's Linux-64bit archive at
// trivyVersion. The install step checks the archive against it before
// it unpacks the binary. CI does not use the setup-trivy action,
// because that action checks the archive only against the release's
// checksums file, which it downloads at run time, so a release that
// changed upstream would change what CI runs. `ci/latest.sh --bump
// trivy` writes this digest and trivyVersion, after it verifies the
// archive against the Sigstore signature of Trivy's release workflow.
const trivySHA256 = "bc0fc726e017b8927a62b2d3531d7a84ee01769daf5c99ed866dced54089fdbf"

// scanImage runs Trivy on one published image and writes the findings
// as SARIF to <dir>/<image>.sarif, which the job uploads to code
// scanning under the category trivy/<image>.
//
// The scan pulls the image by its version tag. A publish never pushes a
// version tag twice, so the scan reads the same bytes that a cluster
// pulls. It looks for vulnerabilities only, because GitHub's secret
// scanning already reads the repository that every image is built from,
// and it reports only vulnerabilities with a fixed version, because a
// bump can fix only those. Trivy matches no Debian sid package against
// an advisory, so the scan of a base image reports only what the image
// has besides those packages.
// plans/open-problems/trivy-matches-nothing-on-debian-sid.md has the
// measurement.
//
// A finding does not fail the run, and a failed scan prints a warning
// and returns, so a failure of Trivy or of its database download never
// blocks a publish.
func scanImage(run Runner, root, dir, image, version string) {
	err := os.MkdirAll(dir, 0o755)
	if err == nil {
		err = run(root, "trivy", "image", "--scanners", "vuln", "--ignore-unfixed", "--format", "sarif",
			"--output", filepath.Join(dir, image+".sarif"), ref(image, version))
	}
	if err != nil {
		fmt.Printf("::warning::the Trivy scan of the image %s stopped, and the run continues: %v\n", ref(image, version), err)
	}
}

// trivyUpload is the data for the trivy-upload template. Image is the
// image whose scan the step uploads, as text or as a workflow
// expression. Present is the pattern that hashFiles uses to check that
// the scan's file exists. Dry is true in a publish job, which uploads
// nothing in a dry run.
type trivyUpload struct {
	Image, Present string
	Dry            bool
}

// trivyCache is the Go cache of the trivy job, in the root workflow and
// in the scheduled scan. The job restores the ci module's downloaded
// modules, which the ci component's go job saves. It saves no build
// cache, because every job of the matrix would save the same entry.
var trivyCache = goCache{Mod: "ci/go.mod", Keys: []string{"ci/go.sum"}, Family: "ci"}
