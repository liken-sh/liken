package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// This file and ci/templates/trivy.tmpl are the Trivy configuration:
// the Trivy version, the arguments of each scan, the steps that install
// Trivy, restore its database, and upload each scan, and the root
// workflow's trivy-db and trivy jobs. A change to either file can
// change what a scan reports, so the plan of a push to main that
// changes one of them scans every published image again. See
// Planner.Rescan.

// trivyVersion is the Trivy that each publish job, the root workflow's
// trivy-db job, and each entry of its trivy job's matrix install.
// ci/latest.sh reports it against Trivy's newest release.
const trivyVersion = "0.76.1"

// scanImage runs Trivy on one published image, and writes its findings
// as SARIF to <dir>/<image>.sarif, which the job that runs the scan
// uploads to code scanning under the category trivy/<image>.
//
// The scan reads the image from the registry by its version tag. A
// publish never pushes a version tag twice, so the tag names the bytes
// that a cluster pulls. It looks for known vulnerabilities only:
// GitHub's secret scanning reads the repository that every image is
// built from. It reports only a vulnerability that has a fixed
// version. The bases install from Debian sid, which carries many
// vulnerabilities that no package fixes yet, and those would bury the
// findings that a bump can act on.
//
// A scan reports, and it does not gate. A finding has no effect on the
// run, and a scan that fails prints a warning, so a publish is never
// held back by Trivy or by its database download.
func scanImage(run Runner, root, dir, image, version string) {
	err := os.MkdirAll(dir, 0o755)
	if err == nil {
		err = run(root, "trivy", "image", "--scanners", "vuln", "--ignore-unfixed", "--format", "sarif",
			"--output", filepath.Join(dir, image+".sarif"), ref(image, version))
	}
	if err != nil {
		fmt.Printf("::warning::the Trivy scan of %s failed, and the run goes on: %v\n", ref(image, version), err)
	}
}

// trivyUpload is the data of the trivy-upload template: the image
// whose scan the step uploads, as text or as an expression, and the
// pattern that hashFiles checks for the scan's file. Dry is true in a
// publish job, which uploads nothing in a dry run.
type trivyUpload struct {
	Image, Present string
	Dry            bool
}
