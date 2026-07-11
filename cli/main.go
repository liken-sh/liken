// The liken CLI: the toolkit for producing and operating a deployment
// of liken.
//
// Everything an operator does to a deployment that isn't running a
// machine lives here: minting or adopting a cluster identity,
// computing credentials from it, packing a deployment layer, and
// assembling install media from a public release. The other Go
// programs in this repo run *inside* the machine — init as PID 1,
// the operators as pods; this one runs on the operator's
// workstation, and it ships with public releases so that producing a
// cluster never requires this repo or a build.
//
// The command is a thin dispatcher. The logic lives with the domain
// that owns it (the identity package, and later the image and
// releases packages), so the CLI stays a table of names while each
// capability keeps its own file, its own tests, and its own
// documentation.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/liken-sh/liken/identity"
	"github.com/liken-sh/liken/image"
	"github.com/liken-sh/liken/machine"
	"github.com/liken-sh/liken/releases"
	"github.com/liken-sh/liken/scaffold"
)

// A consoleList collects repeated -console flags in order.
type consoleList []string

func (c *consoleList) String() string { return fmt.Sprint([]string(*c)) }

func (c *consoleList) Set(v string) error {
	*c = append(*c, v)
	return nil
}

const usage = `liken — the toolkit for setting up and running a liken cluster

usage:

  liken new <directory>
      Start a deployment: answer a few questions and get a directory
      of manifests — cluster.yaml and one file per machine — with
      comments that teach every field. The other commands build on
      this directory.

  liken mint <identity-dir>
      Create a new cluster identity: the certificates and join token
      that every machine in one cluster shares.

  liken adopt <harvest-dir> <identity-dir>
      Take identity files copied off an existing cluster's disk and
      arrange them as an identity directory. The cluster does not
      have to be a liken cluster: any k3s cluster's identity can be
      adopted, so liken machines can join a cluster you already run.

  liken kubeconfig <identity-dir>
      Write an admin kubeconfig: the credential kubectl uses to
      administer the cluster.

  liken layer <manifests-dir> <identity-dir> <kernel-dist> <output.cpio>
      Pack your cluster's half of the operating system into one small
      archive: your cluster and machine manifests, your identity, and
      any kernel modules your machines ask for. The kernel directory
      is only consulted when a machine declares modules; pass - when
      none do.

  liken media <release-dir> <deployment.cpio> <output.cpio>
      Build a bootable install image from a downloaded release and
      your deployment layer. Machines install themselves from it.

  liken stick [-console ttyS0] <release-dir> <deployment.cpio> <output.img>
      Build the USB install stick's disk image from a downloaded
      release and your deployment layer: one stick for the whole
      deployment, with a boot menu listing every machine by name.
      Boot it, pick the machine you're standing at, and it installs
      itself and powers off. -console (repeatable) adds a console=
      argument to every entry; the machines keep it permanently.

  liken bundle <vmlinuz> <liken.cpio> <liken-cli> <systemd-boot.efi> <channel-dir> <version>
      Lay out a release: copy the four files into the channel and
      write the release.yaml that names each one by its digest.

  liken serve <channel-dir> [address]
      Share a release channel over plain HTTP so machines can
      download from it. The address defaults to :8017.

  liken version
      Print this toolkit's version.

An identity directory holds the certificates and join token that
make a cluster one cluster. Some of the files are private keys, so
keep the directory out of version control.

A deployment layer is a small archive holding everything about the
operating system that is yours and not liken's. A machine boots the
generic liken image and your layer together, and the kernel joins
them into one system.

A release channel is a directory any web server can share: one
subdirectory per version, each holding the release's files and a
release.yaml that names every file by its sha256 digest, so a
machine can check that what it downloaded is what was published.
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
	case "new":
		if len(args) != 2 {
			return fmt.Errorf("usage: liken new <directory>")
		}
		return scaffold.New(args[1], os.Stdin, os.Stdout)
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
	case "media":
		if len(args) != 4 {
			return fmt.Errorf("usage: liken media <release-dir> <deployment.cpio> <output.cpio>")
		}
		return image.Media(args[1], args[2], args[3], os.Stdout)
	case "stick":
		// The CLI's first flags; stdlib flag over a subcommand's own
		// FlagSet, so the positional arguments stay positional.
		fs := flag.NewFlagSet("stick", flag.ContinueOnError)
		var consoles consoleList
		fs.Var(&consoles, "console", "add console=<value> to every menu entry (repeatable)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 3 {
			return fmt.Errorf("usage: liken stick [-console ttyS0] <release-dir> <deployment.cpio> <output.img>")
		}
		return image.Stick(fs.Arg(0), fs.Arg(1), fs.Arg(2), consoles, os.Stdout)
	case "bundle":
		if len(args) != 7 {
			return fmt.Errorf("usage: liken bundle <vmlinuz> <liken.cpio> <liken-cli> <systemd-boot.efi> <channel-dir> <version>")
		}
		return releases.Bundle(args[1], args[2], args[3], args[4], args[5], args[6], os.Stdout)
	case "serve":
		addr := ":8017"
		switch len(args) {
		case 2:
		case 3:
			addr = args[2]
		default:
			return fmt.Errorf("usage: liken serve <channel-dir> [address]")
		}
		return releases.Serve(args[1], addr)
	case "version":
		fmt.Println(machine.Version)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}
