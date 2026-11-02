package main

// This file holds the verify and sbom commands. Each one reads its
// arguments and prints what the verify package found. The checks
// themselves, the Sigstore bundles and the trust root, live in
// liken/verify, so a machine's operator can run the same checks later.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"regexp"
	"text/tabwriter"

	"github.com/liken-sh/liken/liken/verify"
)

// The verify group's subcommands.
var verifySubcommands = []string{"image", "release"}

// releaseVersion matches a release's CalVer version. `liken sbom` reads
// an argument of this form as a release and any other as an image
// reference. An image tag of this form still needs its repository, so
// the two never collide.
var releaseVersion = regexp.MustCompile(`^[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3}$`)

func verifyCommand(ctx context.Context, args []string, out io.Writer) error {
	usage := fmt.Errorf("usage: liken verify release [-source URL] [-digest sha256:<hex>] <version>, or liken verify image <reference>")
	if len(args) == 0 {
		return usage
	}
	switch args[0] {
	case "release":
		fs := flag.NewFlagSet("verify release", flag.ContinueOnError)
		source := fs.String("source", verify.DefaultSource, "the release channel's base URL")
		digest := fs.String("digest", "", "pin release.yaml to a catalog entry's sha256:<hex> digest")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return usage
		}
		summary, err := verify.Channel{Source: *source}.Release(ctx, fs.Arg(0), *digest, out)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: %s\n", verify.SBOMName, summary)
		return nil
	case "image":
		if len(args) != 2 {
			return usage
		}
		report, err := verify.Image(ctx, args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "ok  %s  signed by %s\n", report.Reference, report.Signer.Workflow)
		for _, a := range report.Attestations {
			fmt.Fprintf(out, "ok  %s  SBOM of %d packages, provenance %s\n", a.Platform, a.Packages, a.Provenance)
		}
		return nil
	default:
		return usage
	}
}

func sbomCommand(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sbom", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the SBOM document itself")
	source := fs.String("source", verify.DefaultSource, "the release channel's base URL, for a release")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: liken sbom [-json] [-source URL] <version | image-reference>")
	}
	target := fs.Arg(0)
	if releaseVersion.MatchString(target) {
		document, err := verify.Channel{Source: *source}.ReleaseSBOM(ctx, target, "")
		if err != nil {
			return err
		}
		if *asJSON {
			_, err := out.Write(document)
			return err
		}
		packages, err := verify.CycloneDXPackages(document)
		if err != nil {
			return err
		}
		return packageTable(out, packages)
	}
	report, err := verify.Image(ctx, target)
	if err != nil {
		return err
	}
	// A multi-platform image carries an SBOM for each platform. -json
	// prints each document in turn, a stream that jq reads one by one.
	for _, a := range report.Attestations {
		if *asJSON {
			if _, err := fmt.Fprintf(out, "%s\n", a.SBOM); err != nil {
				return err
			}
			continue
		}
		if len(report.Attestations) > 1 {
			fmt.Fprintf(out, "# %s\n", a.Platform)
		}
		packages, err := verify.SPDXPackages(a.SBOM)
		if err != nil {
			return err
		}
		if err := packageTable(out, packages); err != nil {
			return err
		}
	}
	return nil
}

// packageTable prints the packages as aligned columns: name, version,
// and package URL.
func packageTable(out io.Writer, packages []verify.Package) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVERSION\tPURL")
	for _, p := range packages {
		fmt.Fprintf(w, "%s\t%s\t%s\n", p.Name, p.Version, p.PURL)
	}
	return w.Flush()
}
