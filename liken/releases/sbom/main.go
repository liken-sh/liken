// Command sbom writes the CycloneDX SBOM of one liken release.
//
// A release carries files from three kinds of source, and each source
// knows a different part of the release:
//
//   - each pin's manifest, pin.toml, names the files that the pin
//     downloads: the kernel, k3s, the firmware, and the rest, with the
//     URL, the digest, the purl, and the license of each one;
//   - buildroot's SBOM names the static programs that liken/userland
//     builds, and every package that they link;
//   - syft, run over the release's root filesystem, its image
//     archives, its toolkit, and the programs that k3s carries inside
//     its binary, names what neither of the others does: liken's own
//     Go programs and the modules they link, and the Go programs of
//     k3s.
//
// The command merges the three into one document whose subject is the
// release at its CalVer version, checks it against the CycloneDX
// schema, and writes it. releases/Makefile runs it before the bundle,
// so release.yaml names the SBOM by its digest like every other file.
//
//	go run ./releases/sbom -version <version> -out <file>
//	    -buildroot <buildroot.cdx.json>
//	    -syft <parent>=<syft.cdx.json>...
//	    -claim <path>=<component>...
//	    <pin.toml>...
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/liken-sh/liken/liken/releases/sbom/schema"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sbom:", err)
		os.Exit(1)
	}
}

// pairs is a flag that each use adds one key=value pair to.
type pairs [][2]string

func (p *pairs) String() string { return fmt.Sprint(*p) }

func (p *pairs) Set(text string) error {
	key, value, ok := strings.Cut(text, "=")
	if !ok || key == "" || value == "" {
		return fmt.Errorf("%q is not <name>=<value>", text)
	}
	*p = append(*p, [2]string{key, value})
	return nil
}

func run(args []string) error {
	fs := flag.NewFlagSet("sbom", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	version := fs.String("version", "", "the release's version")
	out := fs.String("out", "", "the file to write")
	buildroot := fs.String("buildroot", "", "buildroot's CycloneDX SBOM")
	var scans, claims pairs
	fs.Var(&scans, "syft", "<parent>=<file>: a syft SBOM of what the parent component holds, liken for the release")
	fs.Var(&claims, "claim", "<path>=<component>: the component that names the file at the path in a scan")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *version == "" || *out == "" || *buildroot == "":
		return errors.New("-version, -out, and -buildroot are required")
	case fs.NArg() == 0:
		return errors.New("name the pin.toml of each pin that the release ships")
	}
	in := inputs{version: *version, manifests: fs.Args(), buildroot: *buildroot, claims: map[string]string{}}
	for _, s := range scans {
		in.scans = append(in.scans, scan{parent: s[0], file: s[1]})
	}
	for _, c := range claims {
		in.claims[c[0]] = c[1]
	}
	b, err := merge(in)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := schema.Validate(data); err != nil {
		return err
	}
	return os.WriteFile(*out, append(data, '\n'), 0o644)
}
