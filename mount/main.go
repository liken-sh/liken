// Command mount is liken's mount(8): the program Kubernetes runs
// when a pod needs a volume.
//
// Nearly everything else on a liken machine speaks to the kernel
// directly. Init mounts filesystems with the mount system call, and
// so would any Go program here. This one exists because the kubelet
// does not. When a pod declares a volume, the kubelet builds a
// command line and runs a program named `mount`, found on its PATH.
// So the OS has to supply one, and the OS may as well supply one
// that it can explain.
//
// mount(8) does two small jobs. Neither of them is the mount itself.
//
// The first job is translation, and options.go does it. mount(2)
// takes a flag word and an opaque data string, and the kernel's
// split between them is arbitrary: `ro` is a bit the VFS acts on for
// every filesystem, while `vers=4.1` is text that only the NFS
// client understands. Callers write one comma list mixing the two.
//
// The second job is dispatch, and it is why this program had to be
// written. Some filesystems cannot be mounted by the syscall alone.
// NFS is the one that matters here: before the kernel can mount an
// export, something in userspace has to reach the server, agree on a
// protocol version, and turn the result into the options the kernel
// needs. That work lives in a helper program named
// /sbin/mount.<type>, and running it is mount(8)'s job. The kernel
// never runs a helper, and nothing else will do it either.
//
// This is why a plain `nfs:` volume needs no version in its options.
// The helper negotiates the highest version the server offers, and
// asks the kernel for that one. Without a helper, the raw syscall
// asks for NFSv3, which liken does not carry, and the kernel
// refuses. A version written by hand in the mount options is a
// workaround for a missing mount(8), not a requirement of NFS.
//
// This program ships at /sbin/mount so that it wins. k3s carries a
// busybox with a `mount` applet in its own bin directory, and that
// applet has no helper support: every mount it performs is the raw
// syscall. k3s puts /sbin ahead of that directory on the PATHs it
// builds, which is the same seam that makes liken's static iptables
// win over k3s's bundled one.
//
// There is no umount here, for the same reason in reverse. k3s's
// busybox umount sits earlier on the PATH than /sbin, so a umount
// shipped here would never run, and none is needed: unmounting an
// NFSv4 mount is the plain syscall, with nobody to tell.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// helperDir is where mount helpers live. The name /sbin/mount.<type>
// is a contract older than any of the software here, and liken's
// image puts mount.nfs and its mount.nfs4 alias there. It is a
// variable so the tests can point the lookup at a directory of their
// own.
var helperDir = "/sbin"

// helperExcluded are the operations that never use a helper, because
// none of them introduces a filesystem. A bind mount grafts a tree
// that is already mounted, a move relocates one, a remount edits
// one, and a propagation change only tells the kernel how mount
// events should travel. In every case the filesystem's userspace has
// already done its work, or has nothing to do. Real mount commands
// draw this same line, and it matters here because the kubelet's
// bind mounts carry the volume's own -o list along with `bind`.
const helperExcluded = unix.MS_BIND | unix.MS_MOVE | unix.MS_REMOUNT |
	unix.MS_SHARED | unix.MS_PRIVATE | unix.MS_SLAVE | unix.MS_UNBINDABLE

// These are mount(8)'s exit codes, and callers read them. The
// kubelet reports the failure of a volume mount by this number and
// the program's output together.
const (
	exitUsage   = 1
	exitFailure = 32
)

func main() {
	req, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "mount: %v\n", err)
		os.Exit(exitUsage)
	}
	if err := req.run(); err != nil {
		fmt.Fprintf(os.Stderr, "mount: %v\n", err)
		os.Exit(exitFailure)
	}
}

// run performs the mount the command line asked for, by helper when
// the filesystem has one and by syscall otherwise.
func (r *request) run() error {
	flags, data := splitOptions(r.optionString())
	flags |= r.flags

	if helper := r.helper(flags); helper != "" {
		// A helper replaces this process rather than running under
		// it. Its exit code is then the exit code of the mount, and
		// its output is the output of the mount, with nothing here
		// left to translate or lose.
		argv := r.helperArgs(helper)
		if err := unix.Exec(helper, argv, os.Environ()); err != nil {
			return fmt.Errorf("running %s: %w", helper, err)
		}
	}

	if err := unix.Mount(r.source, r.target, r.fstype, flags, data); err != nil {
		return fmt.Errorf("mounting %s on %s: %w", r.describeSource(), r.target, err)
	}
	return nil
}

// helper finds the mount helper for this request's filesystem type,
// and answers with an empty string when the mount must go straight
// to the syscall. A type with no helper installed is the ordinary
// case: ext4, tmpfs, and every other filesystem the kernel mounts
// alone. Falling through to the syscall is therefore not a failure
// path, it is the common one.
func (r *request) helper(flags uintptr) string {
	if r.fstype == "" || flags&helperExcluded != 0 {
		return ""
	}
	path := filepath.Join(helperDir, "mount."+r.fstype)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return ""
	}
	return path
}

// helperArgs builds the command line a mount helper expects:
//
//	mount.<type> <source> <target> [-sfnv] [-o options]
//
// The options go across whole and unsplit. The helper has its own
// idea of which names it acts on, which names it forwards to the
// kernel, and which names it rewrites on the way, and none of that
// is this program's business. There is no -t argument, because the
// helper is always named for the type it was chosen for, and a
// helper reads its own name to get the filesystem type:
// this is how one binary answers as both mount.nfs and mount.nfs4.
func (r *request) helperArgs(helper string) []string {
	argv := []string{helper, r.source, r.target}
	for _, sw := range []struct {
		set  bool
		name string
	}{
		{r.sloppy, "-s"},
		{r.fake, "-f"},
		{r.noMtab, "-n"},
		{r.verbose, "-v"},
	} {
		if sw.set {
			argv = append(argv, sw.name)
		}
	}
	if opts := r.optionString(); opts != "" {
		argv = append(argv, "-o", opts)
	}
	return argv
}

// describeSource names what was being mounted, for a failure
// message. A remount or a propagation change has no source to name,
// so the message says what it did have.
func (r *request) describeSource() string {
	if r.source != "" {
		return r.source
	}
	if r.fstype != "" {
		return r.fstype
	}
	return "the mount"
}
