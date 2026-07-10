// The liken CLI: the toolkit for producing and operating a deployment
// of liken.
//
// Everything an operator does to a deployment that isn't running a
// machine lives here: minting or adopting a cluster identity,
// computing credentials from it, and (as this tool grows) assembling
// install media from a public release. The other Go programs in this
// repo run *inside* the machine — init as PID 1, the operators as
// pods; this one runs on the operator's workstation, and it ships
// with public releases so that producing a cluster never requires
// this repo or a build.
//
// The command is a thin dispatcher. The logic lives with the domain
// that owns it (the identity package, and later the image and
// releases packages), so the CLI stays a table of names while each
// capability keeps its own file, its own tests, and its own
// documentation.
package main

import (
	"fmt"
	"os"

	"github.com/liken-sh/liken/identity"
	"github.com/liken-sh/liken/image"
	"github.com/liken-sh/liken/machine"
)

const usage = `liken — the toolkit for deployments of liken

usage:
  liken mint <identity-dir>                mint a new cluster identity
  liken adopt <harvest-dir> <identity-dir> adopt an existing cluster's identity
  liken kubeconfig <identity-dir>          compute an admin kubeconfig
  liken layer <manifests-dir> <identity-dir> <kernel-dist> <output.cpio>
                                           pack a deployment layer
  liken version                            print the toolkit's version

An identity directory belongs to a deployment and holds its
certificate authorities and join token; the files are private keys
and never belong in version control.

A deployment layer is the small archive that turns the generic liken
image into one deployment's image: concatenate the two cpio files and
the kernel unpacks them as one system (image/layer.go explains).
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "liken: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("a command is required")
	}
	switch args[0] {
	case "mint":
		if len(args) != 2 {
			return fmt.Errorf("usage: liken mint <identity-dir>")
		}
		return identity.Mint(args[1], os.Stdout)
	case "adopt":
		if len(args) != 3 {
			return fmt.Errorf("usage: liken adopt <harvest-dir> <identity-dir>")
		}
		return identity.Adopt(args[1], args[2], os.Stdout)
	case "kubeconfig":
		if len(args) != 2 {
			return fmt.Errorf("usage: liken kubeconfig <identity-dir>")
		}
		return identity.Kubeconfig(args[1], os.Stdout)
	case "layer":
		if len(args) != 5 {
			return fmt.Errorf("usage: liken layer <manifests-dir> <identity-dir> <kernel-dist> <output.cpio>")
		}
		return image.Layer(args[1], args[2], args[3], args[4], os.Stdout)
	case "version":
		fmt.Println(machine.Version)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}
