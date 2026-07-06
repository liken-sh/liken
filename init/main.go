// liken: the first and only program the kernel starts.
//
// When the kernel finishes its own boot, it unpacks the initramfs into
// an in-memory root filesystem and executes one program as process ID
// 1. We name ours liken and point the kernel at it with rdinit=/liken
// on the kernel command line. That exec is the entire handoff from
// kernelspace: a bare environment, any boot parameters the kernel
// itself didn't recognize passed as arguments, no other processes, and
// almost no filesystem. Everything else, init has to set up itself.
//
// PID 1 is special to the kernel in three ways, and each one shapes
// this program:
//
//   - It cannot exit. If PID 1 exits for any reason, the kernel
//     panics. There is no fallback.
//
//   - It inherits every orphan. When a process dies, its children are
//     re-parented to PID 1, which must collect ("reap") their exit
//     statuses or they linger forever as zombies in the process table.
//
//   - It is unsignalable by default. The kernel delivers a signal to
//     PID 1 only if init explicitly installed a handler for it, a
//     safety measure so a stray kill -9 can't panic the machine.
//
// A traditional init grows from here into a service manager. liken's
// does not: its whole job is to set up the minimum environment k3s
// needs, start it, and keep it running. Kubernetes is the service
// manager. The few loops init runs for itself — the machine plane —
// are goroutines registered in components.go, which states the rule
// for what is allowed to live there and what must run in the cluster
// instead. When the image carries no k3s, a boot is a self-test:
// mount the essentials, read the Machine manifest, join the network,
// prove the connection with a DNS lookup, power off.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/chrisguidry/liken/machine"
)

func main() {
	// The kernel opened /dev/console for us before the exec, so file
	// descriptors 0, 1, and 2 already point somewhere real; with
	// console=ttyS0 on the kernel command line, that's the serial
	// port. Ordinary prints reach it directly.
	fmt.Println("liken: hello from userspace")

	// Refuse to run as an ordinary process. Everything below assumes
	// the authority (and duties) of PID 1, and exercising it from a
	// shell on a development machine would try to mount filesystems at
	// real system paths.
	if os.Getpid() != 1 {
		fmt.Fprintln(os.Stderr, "liken is an init and must run as PID 1; refusing")
		os.Exit(1)
	}

	// Before any mount or any child process: trade the kernel's rootfs
	// for a real root filesystem. On success this re-execs the program
	// from the new root and main starts over, which is why the console
	// says hello twice. switchroot.go explains why and how.
	maybeSwitchRoot()

	// Reaping starts before anything else: the moment we spawn a child
	// (or inherit an orphan), collecting its exit status is our job and
	// no one else's.
	plane.start("the reaper", reap)

	mountEssentials()

	// Storage settles first, and with it the question of which
	// manifest this boot runs under: the staged one awaiting its
	// proving boot, the proven last-known-good, or (first boot only)
	// the seed baked into the image — selected by liken.machine= when
	// the image carries manifests for many machines. manifests.go
	// tells that story. Everything after this line configures the
	// machine from the manifest that *won*, never from one that was
	// rejected along the way. This is also one of the two actuators
	// allowed to stop a boot; failBoot's rationales explain both.
	choice, storage, boot, err := settleStorage()
	if err != nil {
		failBoot(err)
	}
	m := choice.m

	// The cluster manifest rides in every machine's image: it says
	// which machines are servers, and from it this machine derives
	// what it is. Reading it can also stop the boot (a cluster
	// manifest that won't parse leaves the machine unable to know its
	// role), which is why it's read here, before anything acts on it.
	cluster, err := loadCluster()
	if err != nil {
		failBoot(err)
	}

	if name := m.Metadata.Name; name != "" {
		// Sethostname is one syscall: no hostnamectl, no daemon. The
		// kernel simply keeps a string.
		if err := unix.Sethostname([]byte(name)); err != nil {
			fmt.Fprintf(os.Stderr, "liken: sethostname %q: %v\n", name, err)
		} else {
			fmt.Printf("liken: hostname is %s\n", name)
		}
	}

	// Sysctls are applied before k3s starts so every value holds by
	// the time it reads them. The operator re-asserts the same spec
	// once the cluster is up, which is what makes a live kubectl edit
	// take effect without a reboot.
	applySysctls(m.Spec.Sysctls)

	worldReport()

	conns, err := bringUpNetwork(m.Spec.Network)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: network: %v\n", err)
	}
	for _, conn := range conns {
		conn.report()
	}

	// If the image carries k3s, liken has its real job: set up the
	// rest of the environment Kubernetes expects, then supervise it
	// forever. A machine without k3s (the image's minimal form) just
	// proves it can boot and powers off.
	if _, err := os.Stat(k3sBinary); err == nil {
		// The machine's role and k3s's boot-derived configuration
		// come from the cluster manifest (k3s.go). A failure here is
		// an identity problem too: an agent that can't say where its
		// cluster is must not come up pretending otherwise.
		role, err := writeK3sBootConfig(cluster, m.Metadata.Name, conns)
		if err != nil {
			failBoot(fmt.Errorf("%w: %v", errIdentity, err))
		}
		// The node password k3s mints on first join has to outlive
		// this boot, or the machine can never rejoin its own cluster
		// (k3s.go tells the story).
		persistNodePassword(storage)
		prepareForK3s()
		// Facts wait until here because they live under /run, and
		// prepareForK3s just mounted a fresh tmpfs there; anything
		// written earlier would be shadowed by the mount.
		publishFacts(cluster, role, choice, conns, storage, boot)
		// The reboot channel: init creates the directory (owning its
		// existence and permissions), the operator writes into it,
		// and the watcher carries at most one request into the
		// supervisor (reboot.go).
		if err := os.MkdirAll(machine.OperatorRunDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "liken: creating %s: %v\n", machine.OperatorRunDir, err)
		}
		rebootRequests := make(chan machine.RebootIntent, 1)
		plane.start("the reboot watch", func(ctx context.Context) error {
			return watchForRebootIntent(ctx, machine.OperatorRunDir, 2*time.Second, rebootRequests)
		})
		loadModules()
		// Only a server can narrate cluster state: the admin
		// kubeconfig is a control-plane artifact, and agents hold no
		// credentials of their own. An agent's join shows up on the
		// server's console (and in the agent's own k3s log lines).
		if role == machine.RoleServer {
			plane.start("the node report", reportWhenReady)
		}
		superviseK3s(role, rebootRequests) // never returns
	}

	// With no k3s to supervise, a boot is complete once the report is
	// out. Powering off (never exiting! see above) hands QEMU a clean
	// shutdown, which is what lets `make run` double as a test harness.
	fmt.Println("liken: boot complete, powering off")
	powerOff()
}

// powerOff shuts the machine down cleanly: sync flushes dirty pages
// to disk (a no-op on a machine with no writable disk, but essential
// the moment one exists), then the reboot syscall powers off. PID 1
// must never simply exit; this is the only correct way for init to
// stop.
func powerOff() {
	unix.Sync()
	if err := unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF); err != nil {
		fmt.Fprintf(os.Stderr, "liken: power off failed: %v\n", err)
	}
}

// failBoot is the fail-stop: print the problem and why it warrants a
// power-off, then power off. It lives here rather than in storage.go
// or manifests.go because it is boot policy, not domain logic: the
// domains report what they couldn't do, and main decides which
// failures a machine must not run through. There are two:
//
//   - identity: the machine can't tell which manifest, or which role,
//     is its own. Guessing could join the wrong cluster, start a
//     rival control plane, or claim another machine's disks.
//   - storage: a declared role can't be satisfied, and a machine
//     declared to have persistent state must not come up ephemeral.
//
// Both are cases of the same rule: down is recoverable, wrong is not.
func failBoot(err error) {
	rationale := "storage: a declared role can't be satisfied, and a machine declared to have persistent state must not come up ephemeral; powering off"
	if errors.Is(err, errIdentity) {
		rationale = "identity: one image boots many machines, and a machine that can't tell which configuration is its own must not guess; powering off"
	}
	fmt.Fprintf(os.Stderr, "liken: %v\n", err)
	fmt.Fprintf(os.Stderr, "liken: %s\n", rationale)
	powerOff()
	// powerOff only returns if the reboot syscall failed; PID 1 still
	// must never exit, so hold the machine here for a person to
	// investigate.
	for {
		time.Sleep(time.Hour)
	}
}

// bootParamValue returns the value of a name=value parameter on the
// kernel command line ("" when absent): the channel for facts a
// machine must know before it has read any file, like which machine
// it is. The bootloader owns the command line, which is exactly why
// it can carry identity: it's configured per machine even when the
// image is shared by a fleet.
func bootParamValue(name string) string {
	raw, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return ""
	}
	for _, field := range strings.Fields(string(raw)) {
		if value, ok := strings.CutPrefix(field, name+"="); ok {
			return value
		}
	}
	return ""
}

// bootParam reports whether a word appears on the kernel command
// line, liken's channel for per-boot behavior that isn't machine
// configuration (that belongs in the Machine manifest). Parameters
// are namespaced liken.* to stay clear of the kernel's own.
func bootParam(name string) bool {
	raw, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return false
	}
	return slices.Contains(strings.Fields(string(raw)), name)
}

// A mount table rather than a sequence of calls: the essential
// filesystems are data, and the world report below prints what
// actually got mounted, so the two are easy to compare.
type mount struct {
	source string
	target string
	fstype string
	flags  uintptr
}

var essentials = []mount{
	// /proc is two things at once: a directory per running process, and
	// the kernel's control panel (/proc/sys, /proc/cmdline, ...). Almost
	// any tool that inspects the system reads it (including our own
	// world report), and k3s will refuse to start without it.
	{"proc", "/proc", "proc", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV},

	// /sys exposes the kernel's object model: every device, driver, and
	// bus, as a filesystem. cgroup2, which Kubernetes uses to account
	// and limit every container, mounts beneath it later.
	{"sysfs", "/sys", "sysfs", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV},

	// devtmpfs is the kernel's own device catalog: mount it and a node
	// appears for every device the kernel knows, maintained by the
	// kernel itself. On a machine with known hardware this replaces the
	// entire udev apparatus.
	{"devtmpfs", "/dev", "devtmpfs", unix.MS_NOSUID},
}

func mountEssentials() {
	for _, m := range essentials {
		// The initramfs root is plain RAM and freely writable, so init
		// creates its own mountpoints; the image doesn't need to ship
		// empty directories.
		if err := os.MkdirAll(m.target, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mkdir %s: %v\n", m.target, err)
			continue
		}
		// Failures are reported but not fatal: a partial environment
		// that can still print its report is worth far more than a
		// kernel panic, because the console is where we debug.
		if err := unix.Mount(m.source, m.target, m.fstype, m.flags, ""); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mount %s on %s: %v\n", m.fstype, m.target, err)
		}
	}
}

// The world report is liken's substitute for an interactive shell:
// every question we would have answered by poking around at a prompt,
// init answers on the console, every boot. When something goes wrong,
// the first step is usually extending the report to answer a new
// question.
func worldReport() {
	// Uname fills fixed-size byte arrays rather than returning strings:
	// it's a thin wrapper over the raw syscall, and the kernel ABI deals
	// in fixed-size buffers. ByteSliceToString finds the NUL terminator.
	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		fmt.Printf("liken: kernel %s (%s)\n",
			unix.ByteSliceToString(u.Release[:]),
			unix.ByteSliceToString(u.Machine[:]))
	}

	// The command line is how the outside world parameterizes a boot:
	// it's where rdinit= points at us, and the channel for any fact a
	// machine must know before it has a filesystem.
	if cmdline, err := os.ReadFile("/proc/cmdline"); err == nil {
		fmt.Printf("liken: cmdline: %s\n", strings.TrimSpace(string(cmdline)))
	}

	// /proc/self/mounts is the kernel's authoritative mount table; if
	// mountEssentials succeeded, its work shows up here.
	if mounts, err := os.ReadFile("/proc/self/mounts"); err == nil {
		fmt.Print("liken: mounts:\n")
		for line := range strings.SplitSeq(strings.TrimSpace(string(mounts)), "\n") {
			fmt.Printf("liken:   %s\n", line)
		}
	}

	// A populated /dev shows that devtmpfs did its job without any
	// udev in the picture.
	if entries, err := os.ReadDir("/dev"); err == nil {
		fmt.Printf("liken: /dev has %d entries\n", len(entries))
	}

	reportBlockDevices()
}

// reap collects the exit status of any child process, for as long as
// the machine plane runs — which is to say until shutdown, after
// every process it might collect has already been stopped. SIGCHLD
// arrives whenever a child dies; because signal coalescing can fold
// many deaths into one delivery, each wakeup collects every exited
// child, not just one. This loop is the only place in liken that
// calls wait; every exit status it collects is posted to the registry
// in supervisor.go, where whoever started the process can claim it.
// (Go note: signal.Notify registers a handler with the runtime and
// forwards deliveries onto a channel, turning an async interrupt into
// an ordinary receive loop, and satisfying the "PID 1 must install
// handlers" rule from the header comment.)
func reap(ctx context.Context) error {
	sigchld := make(chan os.Signal, 1)
	signal.Notify(sigchld, unix.SIGCHLD)
	defer signal.Stop(sigchld)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sigchld:
		}
		for {
			// -1 means "any child"; WNOHANG means "don't block if none
			// have exited", in which case Wait4 returns pid 0.
			var status unix.WaitStatus
			pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
			if pid <= 0 || err != nil {
				break
			}
			recordDeath(pid, status)
		}
	}
}
