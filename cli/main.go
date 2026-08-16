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
	"bufio"
	"flag"
	"fmt"
	"io"
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

  liken kubeconfig [-server URL] <deployment-dir>
      Write an admin kubeconfig: the credential kubectl uses to
      administer the cluster. The endpoint comes from the
      deployment's cluster.yaml; -server overrides it, for when
      that address is not reachable from this machine.

  liken approve-reboot [-server URL] <deployment-dir> <machine>
      Report what a machine waits for, and grant it the one
      disruption its rebootPolicy withholds. The grant is an
      annotation naming the staged change's hash, so it spends
      itself when the change applies, and running this twice is
      the same grant. When nothing waits, this writes nothing.

  liken request-reboot [-server URL] <deployment-dir> <machine>
      Ask a machine to reboot when no change asks it to: a driver
      bound to the wrong device, or a machine you are experimenting
      on. The machine still takes its turn from the cluster,
      cordons, and drains. On rebootPolicy: Auto that is the whole
      procedure. On Manual the request waits for approve-reboot.
      The request names the running boot, so it spends itself when
      the machine comes back.

  liken kubectl [-server URL] <deployment-dir> [args...]
  liken stern   [-server URL] <deployment-dir> [args...]
  liken flux    [-server URL] <deployment-dir> [args...]
      Run the named tool from your PATH against the deployment's
      cluster: resolve the credential, set KUBECONFIG, and hand the
      terminal to the tool. Give liken's -server before the
      directory; everything after the directory goes to the tool.

  liken layer <manifests-dir> <identity-dir> <output.cpio>
      Pack your cluster's half of the operating system into one small
      archive: your cluster and machine manifests, and your identity.
      Kernel modules are not part of it: the system image carries the
      kernel's whole module tree, and spec.modules loads from that
      tree at boot.

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
      Boot it, pick the machine you're standing at, and follow the
      console: the install holds until you remove the stick and press
      Enter, then powers the machine off. -console (repeatable) adds a
      console= argument to every entry; the machines keep it
      permanently.

  liken bundle [-slot-size 1Gi] <vmlinuz> <liken.sqfs> <boot.cpio> <microcode.cpio> <liken-cli> <systemd-boot.efi> <grub-boot.img> <grub-core.img> <licenses.md> <channel-dir> <version> [component=version ...]
      Lay out a release: copy the nine files into the channel and
      write the release.yaml that names each one by its digest. The
      version is a calendar date and serial (2026.07.11-001); the
      component=version pairs record which upstreams shipped inside,
      since the date deliberately doesn't say.

  liken serve <channel-dir> [address]
      Share a release channel over plain HTTP so machines can
      download from it. The address defaults to :8017.

  liken index -source <url> <output-dir> < keys
      Render a channel's index: a front page listing every release, a
      page per release giving its catalog entry and its artifacts, a
      page for the source mirror, and the versions.yaml document that
      lists every release for a script. Read the channel's object keys
      from standard input, one per line, and read each release's
      document from the channel itself. The index is derived, so
      rendering it again over the same channel repairs whatever is
      stale.

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
		fs := flag.NewFlagSet("kubeconfig", flag.ContinueOnError)
		server := fs.String("server", "", "the API server address, when cluster.yaml's endpoint is not reachable from here")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return fmt.Errorf("usage: liken kubeconfig [-server URL] <deployment-dir>")
		}
		_, err := writeKubeconfig(fs.Arg(0), *server, os.Stdout)
		return err
	case "kubectl", "stern", "flux":
		return passthrough(args[0], args[1:])
	case "approve-reboot":
		return approveReboot(args[1:], os.Stdout)
	case "request-reboot":
		return requestReboot(args[1:], os.Stdout)
	case "layer":
		if len(args) != 4 {
			return fmt.Errorf("usage: liken layer <manifests-dir> <identity-dir> <output.cpio>")
		}
		return image.Layer(args[1], args[2], args[3])
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
		fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
		slotSize := fs.String("slot-size", "1Gi", "the boot slot size that every artifact, plus headroom, must fit")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() < 11 {
			return fmt.Errorf("usage: liken bundle [-slot-size 1Gi] <vmlinuz> <liken.sqfs> <boot.cpio> <microcode.cpio> <liken-cli> <systemd-boot.efi> <grub-boot.img> <grub-core.img> <licenses.md> <channel-dir> <version> [component=version ...]")
		}
		var components []machine.ReleaseComponent
		for _, arg := range fs.Args()[11:] {
			name, version, ok := strings.Cut(arg, "=")
			if !ok || name == "" || version == "" {
				return fmt.Errorf("component %q must be name=version", arg)
			}
			components = append(components, machine.ReleaseComponent{Name: name, Version: version})
		}
		return releases.Bundle(fs.Arg(0), fs.Arg(1), fs.Arg(2), fs.Arg(3), fs.Arg(4), fs.Arg(5), fs.Arg(6), fs.Arg(7), fs.Arg(8), fs.Arg(9), fs.Arg(10), *slotSize, components, os.Stdout)
	case "index":
		fs := flag.NewFlagSet("index", flag.ContinueOnError)
		source := fs.String("source", "", "the channel's base URL, where the pages will be served")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 || *source == "" {
			return fmt.Errorf("usage: liken index -source <url> <output-dir> < keys")
		}
		keys, err := readLines(os.Stdin)
		if err != nil {
			return err
		}
		return releases.Index(*source, keys, fs.Arg(0), os.Stdout)
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

// readLines collects a list given on standard input, one entry per
// line, and drops blank lines. The index command takes a channel's
// object keys this way, because the command that lists a bucket holds
// a credential and this program holds none.
func readLines(r io.Reader) ([]string, error) {
	var lines []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
}
