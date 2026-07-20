// The liken CLI is the toolkit for producing and operating a
// deployment of liken.
//
// This program does everything an operator needs to do to a
// deployment that is not running a machine: minting or adopting a
// cluster identity, computing credentials from that identity,
// packing a deployment layer, and assembling install media from a
// public release. The other Go programs in this repo run inside the
// machine: init as PID 1, and the operators as pods. This program
// runs on the operator's workstation, and it ships with public
// releases, so producing a cluster never requires this repo or a
// build.
//
// The command is a thin dispatcher. The logic lives with the domain
// that owns it (the identity package, and later the image and
// releases packages). Because of this, the CLI stays a table of
// names, while each capability keeps its own file, its own tests,
// and its own documentation.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

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

  liken fetch [-digest sha256:<hex>] <source-url> <version|latest> <channel-dir>
      Download a published release from a channel into a local
      channel directory, verifying every artifact against the
      release's document. Pass "latest" to take whatever the channel
      currently names newest. -digest pins the document itself to a
      catalog entry's digest, closing the trust chain end to end.

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

  liken bundle <vmlinuz> <liken.sqfs> <boot.cpio> <liken-cli> <systemd-boot.efi> <grub-boot.img> <grub-core.img> <licenses.md> <channel-dir> <version> [component=version ...]
      Lay out a release: copy the eight files into the channel and
      write the release.yaml that names each one by its digest. The
      version is a calendar date and serial (2026.07.11-001); the
      component=version pairs record which upstreams shipped inside,
      since the date deliberately doesn't say.

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
	case "fetch":
		fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
		digest := fs.String("digest", "", "pin the release document to a catalog entry's sha256:<hex> digest")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 3 {
			return fmt.Errorf("usage: liken fetch [-digest sha256:<hex>] <source-url> <version|latest> <channel-dir>")
		}
		return releases.Fetch(fs.Arg(0), fs.Arg(1), *digest, fs.Arg(2), os.Stdout)
	case "media":
		if len(args) != 4 {
			return fmt.Errorf("usage: liken media <release-dir> <deployment.cpio> <output.cpio>")
		}
		return image.Media(args[1], args[2], args[3], os.Stdout)
	case "stick":
		// The CLI's first flags. This uses the standard library's
		// flag package over a subcommand's own FlagSet, so the
		// positional arguments stay positional.
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
		if len(args) < 11 {
			return fmt.Errorf("usage: liken bundle <vmlinuz> <liken.sqfs> <boot.cpio> <liken-cli> <systemd-boot.efi> <grub-boot.img> <grub-core.img> <licenses.md> <channel-dir> <version> [component=version ...]")
		}
		var components []machine.ReleaseComponent
		for _, arg := range args[11:] {
			name, version, ok := strings.Cut(arg, "=")
			if !ok || name == "" || version == "" {
				return fmt.Errorf("component %q must be name=version", arg)
			}
			components = append(components, machine.ReleaseComponent{Name: name, Version: version})
		}
		return releases.Bundle(args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9], args[10], components, os.Stdout)
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
