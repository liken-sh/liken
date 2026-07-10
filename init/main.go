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
// manager. The few loops init runs for itself, called the machine
// plane, are goroutines registered in components.go, which states the
// rule for what is allowed to live there and what must run in the
// cluster instead. When the image carries no k3s, a boot is a self-test:
// mount the essentials, read the Machine manifest, join the network,
// prove the connection with a DNS lookup, power off.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
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

	// The panic fault fires immediately, before anything is mounted
	// or supervised: PID 1 dying panics the kernel, panic=10 reboots
	// it, and the firmware's consumed BootNext lands the machine back
	// on its proven slot with no liken code involved in the recovery
	// (fault.go).
	if fault == "panic" {
		panic("liken: fault injection: this release panics at first breath")
	}

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

	mountEssentials()

	// With /proc and /dev mounted, init's own output moves to
	// /dev/kmsg (console.go). This happens before the first
	// machine-plane component starts, so the os.Stdout and os.Stderr
	// variables are reassigned while main is still the only goroutine
	// reading them.
	redirectToKmsg()

	// Reaping starts before any child process exists: the moment we
	// spawn one (or inherit an orphan), collecting its exit status is
	// our job and no one else's. Nothing above this line forks.
	plane.start("the reaper", reap)

	// The firmware's variable store, when there is one: boot entries
	// and boot order live there, and both the world report and the
	// facts file read them (efi.go).
	mountEFIVars()

	// The OS's own kernel modules load before storage settles because
	// some roles' filesystems arrive as modules: the system slots are
	// FAT32, and mounting vfat pulls in its default character-encoding
	// table (nls_iso8859-1), which Ubuntu's config builds as a module.
	// Everything else on the fixed list is for k3s, which starts much
	// later. Modules load in exactly two passes: this one, and the
	// declared extras below, which must wait until storage has settled
	// which manifest this boot runs under.
	loadModules()

	// Storage settles first, and with it the question of which
	// manifest this boot runs under: the staged one awaiting its
	// proving boot, the proven last-known-good, or (first boot only)
	// the seed baked into the image. When the image carries manifests
	// for many machines, liken.machine= selects among them.
	// manifests.go explains the whole selection. Everything after
	// this line configures the machine from the manifest that *won*,
	// never from one that was rejected along the way. This is also
	// one of the two actuators allowed to stop a boot; failBoot's
	// rationales explain both.
	choice, storage, boot, err := settleStorage()
	if err != nil {
		failBoot(err)
	}
	m := choice.m

	// The installer bakes liken.slot= into each boot entry's command
	// line, so a from-disk boot always knows which half of blue-green
	// it is running from. The boot record carries the fact to the
	// cluster, where the operator uses it to aim downloads at the
	// other slot.
	if slot := bootParamValue("liken.slot"); slot != "" {
		boot.Slot = slot
		fmt.Printf("liken: firmware: running from system slot %s\n", slot)
	}

	// An install boot does exactly one job and stops: put this
	// running version on the machine's own disk (install.go). It
	// runs this early because nothing after it (network, time, k3s)
	// is its business. It must power off rather than reboot: the
	// install medium is still first in line, and a reboot would just
	// run the installer again.
	if bootParam(installParam) {
		if err := installToDisk(m.Metadata.Name); err != nil {
			fmt.Fprintf(os.Stderr, "liken: %v\n", err)
			fmt.Fprintln(os.Stderr, "liken: install failed; powering off (installs are idempotent: fix the cause and boot the installer again)")
		} else {
			fmt.Println("liken: install complete; powering off; the next boot comes from the disk")
		}
		powerOff()
		for {
			time.Sleep(time.Hour) // PID 1 must never exit, even here
		}
	}

	// Settle where this boot sits in the system release lifecycle: it
	// may be the trial of a staged release, the fallback from one, or
	// an ordinary boot whose only job is to keep the firmware's
	// BootOrder agreeing with the store (proving.go).
	proving := settleSystemRelease(machine.MachineStateDir, boot.Slot,
		storage.MachineState.Backing == machine.BackingPartition, &boot)

	// The cluster document says which machines are leaders, and from
	// it this machine derives what it is. It goes through the same
	// staged/proven/seed lifecycle as the Machine manifest
	// (cluster.go), and reading it can stop the boot: a machine whose
	// only cluster document won't parse cannot know its role, and a
	// machine that can't tell its role must not guess.
	cluster, clusterRaw, err := chooseCluster(machine.MachineStateDir, machine.ClusterManifestPath,
		storage.MachineState.Backing == machine.BackingPartition, &boot)
	if err != nil {
		failBoot(fmt.Errorf("%w: %v", errIdentity, err))
	}

	// The registry credentials ride their own document lifecycle
	// (registries.go): staged by the operator from the
	// registry-credentials Secret, chosen here, rendered into k3s's
	// registries.yaml below. Never fatal — a machine without
	// credentials pulls anonymously.
	creds := chooseRegistryCredentials(machine.MachineStateDir,
		storage.MachineState.Backing == machine.BackingPartition, &boot)

	// The declared modules: the second of the two module passes,
	// possible only now that the winning manifest is known. The boot
	// record keeps the ask (the drift reference: rebooting with the
	// same image would ask the same) and the statuses keep the
	// answers, bound for status.modules through the facts file.
	boot.Modules = slices.Sorted(slices.Values(m.Spec.Modules))
	moduleStatuses := loadDeclaredModules(m.Spec.Modules)

	// The cluster's opt-in features, actuated and reported per
	// machine the way declared modules are (features.go): bundled
	// components take effect in the k3s drop-in rendered below, and
	// vendored payloads load their modules, write their boot files,
	// and seed their workloads here. No boot record entry, because
	// features drift by the cluster document's whole-document hash,
	// not field by field.
	featureStatuses := actuateFeatures(cluster, m.Metadata.Name)

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
		// Before k3s can touch its container store: decide whether
		// this boot can trust it. A store whose last imports were
		// never proven is discarded rather than believed (imports.go),
		// and the tarballs this boot carries are staged as a trial the
		// operator will prove.
		settleImageImports(machine.MachineStateDir,
			storage.MachineState.Backing == machine.BackingPartition,
			storage.ClusterState.Backing == machine.BackingPartition, &boot)
		// The machine's role and k3s's boot-derived configuration
		// come from the cluster manifest (k3s.go). A failure here is
		// an identity problem too: a follower that can't say where
		// its cluster is must not come up pretending otherwise.
		role, err := writeK3sBootConfig(cluster, m, conns)
		if err != nil {
			failBoot(fmt.Errorf("%w: %v", errIdentity, err))
		}
		// How this machine pulls container images: mirrors and the
		// embedded registry from the cluster document, credentials
		// from their own document, rendered into the registries.yaml
		// k3s reads at start (registries.go). Writing it promotes
		// staged credentials — the write is their whole actuation.
		registries := writeRegistriesConfig(cluster, creds,
			machine.RegistryCredentialsStore(machine.MachineStateDir), boot.CredentialsSource)
		// The node password k3s mints on first join has to outlive
		// this boot, or the machine can never rejoin its own cluster
		// (k3s.go explains).
		persistNodePassword(storage)
		// A proven demotion also removes the datastore left over from
		// when this machine was a leader: etcd won't let a removed
		// member rejoin over its old data, so the datastore must be
		// deleted before k3s starts (k3s.go).
		purgeLeaderLeftovers(role, boot.ClusterManifestSource, k3sServerDB)
		// The clock is corrected before k3s starts, because a wrong
		// clock fails TLS: every certificate the CA minted looks
		// like it's from the future. This is the only moment liken
		// ever steps the clock; from here on it only slews (time.go
		// explains both corrections).
		clk := newClock(timeSources(cluster, role, machine.MachineManifestDir))
		firstSync := stepClockAtBoot(clk.sources)
		clk.record(firstSync)
		// A successful boot measurement is worth persisting at once:
		// a machine that later loses power without a clean shutdown
		// still boots with roughly the right time (time.go describes
		// the two moments the RTC is written).
		if firstSync != nil {
			writeRTC()
		}
		prepareForK3s()
		// The previous boot's k3s and containerd logs step aside
		// before k3s starts writing this boot's. This is a plain call
		// rather than a component because it must finish before k3s
		// opens the files, and boot is the only safe moment to rename
		// them: nothing holds them open (logrotate.go).
		rotateBootLogs()
		// Facts wait until here because they live under /run, and
		// prepareForK3s just mounted a fresh tmpfs there; anything
		// written earlier would be shadowed by the mount.
		facts := publishFacts(cluster, role, choice, conns, storage, boot, moduleStatuses, featureStatuses, registries, firstSync, clk.sources)
		publishBootClusterManifest(clusterRaw)
		// A machine with time sources keeps disciplining its clock
		// for as long as it runs; a free-running machine has nothing
		// to follow, and its status already says so.
		if len(clk.sources) > 0 {
			plane.start("the clock", disciplineClock(clk, facts))
		}
		// Only leaders serve time, because only leaders are asked:
		// followers sync from the leaders themselves. Serving works
		// even when free-running: a fleet with no upstreams still
		// keeps one consistent time across its machines (responder.go).
		if role == machine.RoleLeader {
			plane.start("the time responder", serveTime(clk))
		}
		// The intent channel: init creates the directory (owning its
		// existence and permissions), the operator writes into it,
		// and the watcher carries requests into the supervisor
		// (reboot.go) — at most one reboot, and any number of k3s
		// restarts over the boot's life.
		if err := os.MkdirAll(machine.OperatorRunDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "liken: creating %s: %v\n", machine.OperatorRunDir, err)
		}
		rebootRequests := make(chan machine.RebootIntent, 1)
		restartRequests := make(chan machine.RestartIntent, 1)
		plane.start("the intent watch", func(ctx context.Context) error {
			return watchForOperatorIntents(ctx, machine.OperatorRunDir, 2*time.Second, rebootRequests, restartRequests)
		})
		// The restart path: everything a k3s bounce may re-render,
		// gathered while it's at hand (restart.go).
		restarter := newRestartState(machine.MachineStateDir, m, conns, facts,
			cluster, clusterRaw, creds, boot.CredentialsSource)
		// A proving boot watches for its own promotion: when the
		// operator's first reconcile proves the staged release, init
		// rewrites the firmware's BootOrder to put the newly proven
		// slot first (proving.go).
		if proving {
			plane.start("the proving watch", provingWatch)
		}
		// Only a leader can report cluster state: the admin
		// kubeconfig is a control-plane artifact, and followers hold
		// no credentials of their own. A follower's join shows up on
		// the leader's console (and in its own k3s log lines).
		if role == machine.RoleLeader {
			plane.start("the node report", reportWhenReady)
		}
		// The wedge fault boots everything except k3s: the node never
		// joins, the operator never runs, and no promotion ever
		// lands. That is exactly the failure the proving watchdog
		// exists to catch (fault.go). The machine plane keeps
		// running, so the watchdog's reboot still works.
		if fault == "wedge-k3s" {
			fmt.Println("liken: fault injection: wedging instead of starting k3s")
			select {}
		}
		superviseK3s(role, rebootRequests, restartRequests, restarter.apply) // never returns
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
	syncLogs()
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
// The reasoning is the same in both cases: a machine that is down can
// be fixed and booted again, but a machine running with the wrong
// configuration can do damage that a reboot won't undo.
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
