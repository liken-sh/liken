// liken is the first and only program that the kernel starts.
//
// The kernel finishes its own boot, then unpacks the initramfs into an
// in-memory root filesystem. The kernel executes one program as
// process ID 1. We name our program liken. We point the kernel at it
// with rdinit=/liken on the kernel command line. That exec is the
// entire handoff from kernel space: a bare environment, any boot
// parameters that the kernel did not recognize (passed as arguments),
// no other processes, and almost no filesystem. Init must set up
// everything else itself.
//
// PID 1 is special to the kernel in three ways. Each way shapes this
// program:
//
//   - PID 1 cannot exit. If PID 1 exits for any reason, the kernel
//     panics. There is no fallback.
//
//   - PID 1 inherits every orphan process. When a process dies, the
//     kernel re-parents its children to PID 1. PID 1 must collect
//     ("reap") their exit statuses, or they stay in the process table
//     forever as zombies.
//
//   - PID 1 does not receive signals by default. The kernel delivers a
//     signal to PID 1 only when init has installed a handler for that
//     signal. This is a safety measure: it stops a stray kill -9 from
//     panicking the machine.
//
// A traditional init grows from this point into a service manager.
// liken's init does not grow this way. Its whole job is to set up the
// minimum environment that k3s needs, start k3s, and keep k3s running.
// Kubernetes is the service manager. Init runs a few loops for itself.
// These loops are called the machine plane. They are goroutines
// registered in components.go, which states the rule for what may run
// in the machine plane and what must run in the cluster instead. When
// the image carries no k3s, a boot is a self-test: mount the essential
// filesystems, read the Machine manifest, join the network, prove the
// connection with a DNS lookup, and power off.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

func main() {
	// The kernel opened /dev/console before the exec. File descriptors
	// 0, 1, and 2 already point to a real device. With console=ttyS0
	// on the kernel command line, that device is the serial port.
	// Ordinary prints reach it directly.
	fmt.Println("liken: hello from userspace")

	// The panic fault activates immediately, before the program mounts
	// or supervises anything. When PID 1 dies, the kernel panics.
	// panic=10 reboots the kernel. The firmware consumes BootNext and
	// starts the machine from its proven slot. No liken code takes
	// part in this recovery (fault.go).
	if fault == "panic" {
		panic("liken: fault injection: this release panics at startup")
	}

	// Refuse to run as an ordinary process. Everything below this point
	// assumes the authority and duties of PID 1. Running it from a
	// shell on a development machine would try to mount filesystems at
	// real system paths.
	if os.Getpid() != 1 {
		fmt.Fprintln(os.Stderr, "liken is an init and must run as PID 1; refusing")
		os.Exit(1)
	}

	// Before any mount or any child process, replace the kernel's root
	// filesystem with a real root filesystem. On success, this re-execs
	// the program from the new root, and main starts over. This is why
	// the console shows the hello message twice. switchroot.go explains
	// why and how.
	maybeSwitchRoot()

	mountEssentials()

	// With /proc and /dev mounted, init sends its own output to
	// /dev/kmsg instead (console.go). This happens before the first
	// machine-plane component starts. At this point, main reassigns
	// the os.Stdout and os.Stderr variables while it is still the only
	// goroutine that reads them.
	redirectToKmsg()

	// Reaping starts before any child process exists. From the moment
	// init spawns a process, or inherits an orphan, only init collects
	// its exit status. Nothing above this line forks.
	plane.start("the reaper", reap)

	// The firmware's variable store, when the machine has one, holds
	// the boot entries and the boot order. Both the world report and
	// the facts tree read this store (efi.go).
	mountEFIVars()

	// The OS's own kernel modules load before storage settles, because
	// some roles need their filesystems to arrive as modules. The
	// system slots use FAT32, and mounting vfat pulls in its default
	// character-encoding table (nls_iso8859-1), which Ubuntu's config
	// builds as a module. Everything else on the fixed list is for
	// k3s, which starts much later. Modules load in exactly two
	// passes: this pass, and the declared extras below. The declared
	// extras must wait until storage has settled which manifest this
	// boot runs under.
	loadModules()

	// Storage settles first. This also settles which manifest this
	// boot runs under: the staged manifest awaiting its proving boot,
	// the proven last-known-good manifest, or, on the first boot only,
	// the seed manifest baked into the image. When the image carries
	// manifests for many machines, liken.machine= selects among them.
	// manifests.go explains the full selection. Everything after this
	// line configures the machine from the manifest that this
	// selection chose, never from a manifest that this selection
	// rejected. This call is also one of the two places that can stop
	// a boot. failBoot's rationale explains both places.
	choice, storage, boot, err := settleStorage()
	if err != nil {
		failBoot(err)
	}
	m := choice.m

	// The installer sets liken.slot= into each boot entry's command
	// line. A from-disk boot always knows which half of the blue-green
	// pair it runs from because of this parameter. The boot record
	// carries this fact to the cluster. The operator uses the fact to
	// direct downloads to the other slot.
	if slot := bootParamValue("liken.slot"); slot != "" {
		boot.Slot = slot
		fmt.Printf("liken: firmware: running from system slot %s\n", slot)
	}

	// An install boot does exactly one job and stops: it puts this
	// running version on the machine's own disk (install.go). This
	// check runs early because nothing after it, such as network,
	// time, or k3s, is relevant to an install boot. An install boot
	// must power off rather than reboot. The install medium is still
	// first in the boot order, so a reboot would run the installer
	// again.
	if bootParam(installParam) {
		if err := installToDisk(m.Metadata.Name); err != nil {
			fmt.Fprintf(os.Stderr, "liken: %v\n", err)
			fmt.Fprintln(os.Stderr, "liken: install failed; powering off (installs are idempotent: fix the cause and boot the installer again)")
		} else {
			fmt.Println("liken: install complete; powering off; the next boot comes from the disk")
		}
		powerOff()
		for {
			time.Sleep(time.Hour) // PID 1 must not exit, even here
		}
	}

	// A crash in an earlier boot left its evidence in the platform
	// store. This step reads it, preserves it under machineState, and
	// derives the one-line summary that becomes status.lastCrash
	// (crash.go). It sits here because it needs settled storage: the
	// machineState mount to preserve into, and the backing verdict to
	// know whether preserving is possible at all. An install boot
	// never reaches this line, which is correct: the store belongs to
	// the installed machine's history, not to the install medium's.
	mountPstore()
	lastCrash := settleCrashRecords(machine.MachineStateDir,
		storage.MachineState.Backing == machine.BackingPartition)

	// This block settles where this boot sits in the system release
	// lifecycle. This boot may be the trial of a staged release, in
	// which case the staged record comes back to arm the proving
	// watch. This boot may be the fallback from a staged release. Or
	// this boot may be an ordinary boot, whose only job here is to
	// keep the firmware's boot preference in agreement with the store
	// (proving.go). The actuator is the firmware dialect that these
	// conversations use. main chooses the actuator once, for the
	// whole boot (actuator.go).
	actuator := chooseBootActuator()
	trial := settleSystemRelease(actuator, machine.MachineStateDir, boot.Slot,
		storage.MachineState.Backing == machine.BackingPartition, &boot)

	// The cluster document says which machines are leaders. This
	// machine derives its own role from that document. The cluster
	// document goes through the same staged, proven, and seed
	// lifecycle as the Machine manifest (cluster.go). Reading the
	// cluster document can stop the boot: if a machine's only cluster
	// document does not parse, the machine cannot know its role, and a
	// machine that cannot tell its role must not guess.
	clusterDoc, clusterRaw, err := chooseCluster(machine.MachineStateDir, cluster.ClusterManifestPath,
		storage.MachineState.Backing == machine.BackingPartition, &boot)
	if err != nil {
		failBoot(fmt.Errorf("%w: %v", errIdentity, err))
	}

	// The registry credentials follow their own document lifecycle
	// (registries.go). The operator stages credentials from the
	// registry-credentials Secret. main chooses credentials here, and
	// renders them into k3s's registries.yaml below. This step is
	// never fatal. A machine without credentials pulls images
	// anonymously.
	creds := chooseRegistryCredentials(machine.MachineStateDir,
		storage.MachineState.Backing == machine.BackingPartition, &boot)

	// The declared modules load in the second of the two module
	// passes. This pass is possible only now that main knows the
	// chosen manifest. The boot record keeps the request (the drift
	// reference: rebooting with the same image would request the same
	// modules). The statuses keep the results, bound for
	// status.modules through the facts tree.
	boot.Modules = slices.Sorted(slices.Values(m.Spec.Modules))
	moduleStatuses := loadDeclaredModules(m.Spec.Modules)

	// The cluster's opt-in features are actuated and reported per
	// machine, the same way as declared modules (features.go). Bundled
	// components take effect in the k3s drop-in rendered below.
	// Vendored payloads load their modules, write their boot files,
	// and seed their workloads at this point. There is no boot record
	// entry for features, because features drift by the cluster
	// document's whole-document hash, not field by field.
	featureStatuses := actuateFeatures(clusterDoc, m.Metadata.Name)

	if name := m.Metadata.Name; name != "" {
		// Sethostname is one syscall. It needs no hostnamectl command
		// and no daemon. The kernel only keeps a string.
		if err := unix.Sethostname([]byte(name)); err != nil {
			fmt.Fprintf(os.Stderr, "liken: sethostname %q: %v\n", name, err)
		} else {
			fmt.Printf("liken: hostname is %s\n", name)
		}
	}

	// The OS's own default sysctl values apply first. The Machine
	// spec's sysctls apply after them, so a deployment that disagrees
	// with a default overwrites that default. Both sets apply before
	// k3s starts, so every value is set by the time k3s reads it. The
	// operator re-applies the spec's values once the cluster is up.
	// This is what makes a live kubectl edit take effect without a
	// reboot.
	applySysctls(osSysctls)
	applySysctls(m.Spec.Sysctls)

	worldReport()

	conns, err := bringUpNetwork(m.Spec.Network)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: network: %v\n", err)
	}
	for _, conn := range conns {
		conn.report()
	}

	// If the image carries k3s, liken does its main job: it sets up
	// the rest of the environment that Kubernetes expects, then
	// supervises k3s for as long as the machine runs. A machine
	// without k3s (the image's minimal form) only proves that it can
	// boot, then powers off.
	if _, err := os.Stat(k3sBinary); err == nil {
		clusterLife(choice, storage, boot, clusterDoc, clusterRaw, creds,
			conns, moduleStatuses, featureStatuses, actuator, trial, lastCrash) // never returns
	}

	// With no k3s to supervise, a boot is complete once the report is
	// out. Powering off, rather than exiting (see above), gives QEMU
	// a clean shutdown signal. This is what lets `make run` also work
	// as a test harness.
	fmt.Println("liken: boot complete, powering off")
	powerOff()
}

// clusterLife runs the k3s half of a boot, everything after the point
// where the machine itself is settled. It covers the container
// store's trust decision, the role and its configuration, the clock,
// the facts, the machine plane's long-running components, and finally
// the supervisor, which runs for as long as the machine runs.
// clusterLife never returns. Every path out of it is a reboot or a
// power-off.
func clusterLife(choice *manifestChoice, storage machine.StorageStatus, boot machine.BootStatus,
	clusterDoc *cluster.Cluster, clusterRaw []byte, creds *machine.RegistryCredentials,
	conns []*connection, moduleStatuses []machine.ModuleStatus,
	featureStatuses []machine.FeatureStatus, actuator bootActuator, trial *machine.SystemRelease,
	lastCrash *machine.CrashStatus) {
	m := choice.m

	// Before k3s can touch its container store, this call decides
	// whether this boot can trust that store. If a store's last
	// imports were never proven, main discards the store rather
	// than trust it (imports.go). The tarballs that this boot
	// carries are staged as a trial for the operator to prove.
	settleImageImports(machine.MachineStateDir,
		storage.MachineState.Backing == machine.BackingPartition,
		storage.ClusterState.Backing == machine.BackingPartition, &boot)
	// The machine's role and k3s's boot-derived configuration
	// come from the cluster manifest (k3s.go). A failure here is
	// also an identity problem. A follower that cannot say where
	// its cluster is must not start up as if it can.
	role, err := writeK3sBootConfig(clusterDoc, m, conns)
	if err != nil {
		failBoot(fmt.Errorf("%w: %v", errIdentity, err))
	}
	// This call sets how this machine pulls container images:
	// mirrors and the embedded registry from the cluster document,
	// and credentials from their own document. It renders these
	// into the registries.yaml file that k3s reads at start
	// (registries.go). Writing this file promotes staged
	// credentials. The write is the credentials' whole actuation.
	registries := writeRegistriesConfig(clusterDoc, creds,
		machine.RegistryCredentialsStore(machine.MachineStateDir), boot.CredentialsSource)
	// The node password that k3s creates on first join must
	// outlive this boot. Otherwise, the machine could never rejoin
	// its own cluster (k3s.go explains).
	persistNodePassword(storage)
	// A proven demotion also removes the datastore left over from
	// when this machine was a leader. etcd does not let a removed
	// member rejoin over its old data, so this call must delete
	// the datastore before k3s starts (k3s.go).
	purgeLeaderLeftovers(role, boot.ClusterManifestSource, k3sServerDB)
	// The clock is corrected before k3s starts, because a wrong
	// clock makes TLS fail: every certificate that the CA issued
	// looks like it comes from the future. This is the only
	// moment when liken steps the clock. After this moment, liken
	// only slews the clock (time.go explains both kinds of
	// correction).
	clk := newClock(timeSources(clusterDoc, role, machine.MachineManifestDir))
	firstSync := stepClockAtBoot(clk.sources)
	clk.record(firstSync)
	// This call saves a successful boot measurement at once. If
	// the machine later loses power without a clean shutdown, it
	// still boots with roughly the right time (time.go describes
	// the two moments when the code writes the RTC).
	if firstSync != nil {
		writeRTC()
	}
	prepareForK3s()
	// The previous boot's k3s and containerd logs move aside
	// before k3s starts to write this boot's logs. This is a
	// plain function call rather than a machine-plane component,
	// because it must finish before k3s opens the log files. Boot
	// is the only safe moment to rename these files, because
	// nothing holds them open at boot (logrotate.go).
	rotateBootLogs()
	// The hardware walk also waits until this point. Every
	// declared module has loaded and bound whatever it drives, so
	// any device that is still undriven now is a real gap, not a
	// result of a race with the rest of boot.
	catalog := loadHardwareCatalog()
	unclaimed := discoverUnclaimed(catalog)
	blockDevices := discoverBlockDevices()
	initialTime := timeStatus(firstSync, clk.sources)
	// The facts step waits until this point because the facts tree
	// lives under /run, and prepareForK3s just mounted a fresh tmpfs
	// there. The mount would hide anything written to /run earlier.
	// Each boot step held its discovered facts locally, and hands them
	// here, once /run is the tmpfs that lasts the machine's life.
	publishBootFacts(factsTree, bootFacts{
		clusterDoc:   clusterDoc,
		role:         role,
		conns:        conns,
		storage:      storage,
		boot:         boot,
		modules:      moduleStatuses,
		features:     featureStatuses,
		registries:   registries,
		time:         initialTime,
		blockDevices: blockDevices,
		unclaimed:    unclaimed,
		lastCrash:    lastCrash,
	})
	publishBootManifest(choice)
	publishBootClusterManifest(clusterRaw)
	// The hardware watch keeps that walk correct for the whole
	// life of the machine. Hot-plugged devices arrive as uevents,
	// and the report follows these events (hardware.go). An image
	// without the catalog cannot judge devices, so it does not run
	// this watch either.
	if catalog != nil {
		for _, line := range hardwareTransitions(nil, unclaimed, nil) {
			fmt.Println(line)
		}
		plane.start("the hardware watch", watchHardware(catalog, factsTree, unclaimed, blockDevices))
	}
	// A machine with time sources keeps disciplining its clock for
	// as long as it runs. A free-running machine has no source to
	// follow, and its status already states this.
	if len(clk.sources) > 0 {
		plane.start("the clock", disciplineClock(clk, factsTree, initialTime))
	}
	// Only leaders serve time, because only leaders receive time
	// requests. Followers sync from the leaders themselves.
	// Serving still works when free-running: a fleet with no
	// upstream time source still keeps one consistent time across
	// its machines (responder.go).
	if role == api.RoleLeader {
		plane.start("the time responder", serveTime(clk))
	}
	// The intent channel works like this: init creates the
	// directory and sets its permissions, the operator writes
	// into the directory, and the watcher carries requests to the
	// supervisor (reboot.go). Over the life of a boot, this
	// channel carries at most one reboot request and any number
	// of k3s restart requests.
	if err := os.MkdirAll(machine.OperatorRunDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "liken: creating %s: %v\n", machine.OperatorRunDir, err)
	}
	rebootRequests := make(chan machine.RebootIntent, 1)
	restartRequests := make(chan machine.RestartIntent, 1)
	loadRequests := make(chan machine.ModulesIntent, 1)
	plane.start("the intent watch", func(ctx context.Context) error {
		return watchForOperatorIntents(ctx, machine.OperatorRunDir, 2*time.Second, rebootRequests, restartRequests, loadRequests)
	})
	// The module loader handles the lightest intent: an additive
	// spec.modules edit that applies to the running kernel, with
	// no reboot and no k3s restart involved (liveload.go). It runs
	// beside the supervisor rather than inside it, because, unlike
	// the other two intents, this intent does not involve k3s.
	loader := &moduleLoader{
		tree:        factsTree,
		bootStorage: boot.Storage,
		bootModules: boot.Modules,
		statuses:    moduleStatuses,
	}
	plane.start("the module loader", func(ctx context.Context) error {
		store := machine.MachineManifests(machine.MachineStateDir)
		moduleBase := filepath.Join("/lib/modules", kernelRelease())
		for {
			select {
			case <-ctx.Done():
				return nil
			case intent := <-loadRequests:
				loader.apply(intent, store, moduleBase)
			}
		}
	})
	// The restart path gathers everything that a k3s restart may
	// re-render, while that data is available (restart.go).
	restarter := newRestartState(machine.MachineStateDir, m, conns, factsTree,
		clusterDoc, clusterRaw, creds, boot.CredentialsSource)
	// A proving boot watches for its own promotion. When the
	// operator's first reconcile proves the staged release, init
	// sets the firmware's boot preference to the newly proven slot
	// (proving.go).
	if trial != nil {
		plane.start("the proving watch", provingWatch(actuator, *trial))
	}
	// Only a leader can report cluster state, because the admin
	// kubeconfig is a control-plane artifact, and followers hold
	// no credentials of their own. A follower's join appears on
	// the leader's console, and in the follower's own k3s log
	// lines.
	if role == api.RoleLeader {
		plane.start("the node report", reportWhenReady)
	}
	// The wedge fault boots everything except k3s. The node never
	// joins, the operator never runs, and no promotion ever
	// happens. This is exactly the failure that the proving
	// watchdog exists to catch (fault.go). The machine plane keeps
	// running, so the watchdog's reboot still works.
	if fault == "wedge-k3s" {
		fmt.Println("liken: fault injection: wedging instead of starting k3s")
		select {}
	}
	superviseK3s(role, rebootRequests, restartRequests, restarter.apply,
		restarter.removeOfflineRetractions) // never returns
}

// powerOff shuts the machine down cleanly. sync flushes dirty pages to
// disk. This is a no-op on a machine with no writable disk, but it is
// essential the moment the machine has one. Then the reboot syscall
// powers the machine off. PID 1 must not simply exit. This is the only
// correct way for init to stop.
func powerOff() {
	syncLogs()
	unix.Sync()
	if err := unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF); err != nil {
		fmt.Fprintf(os.Stderr, "liken: power off failed: %v\n", err)
	}
}

// failBoot is the fail-stop function. It prints the problem and why
// the problem warrants a power-off, then it powers the machine off.
// failBoot lives here rather than in storage.go or manifests.go
// because it is boot policy, not domain logic. Each domain reports
// what it could not do, and main decides which failures a machine
// must not run through. There are two such failures:
//
//   - identity: the machine cannot tell which manifest, or which
//     role, is its own. A guess could join the wrong cluster, start a
//     rival control plane, or claim another machine's disks.
//   - storage: a declared role cannot be satisfied, and a machine
//     declared to have persistent state must not start up with no
//     persistent state.
//
// The reasoning is the same in both cases. A machine that is down can
// be fixed and booted again. A machine running with the wrong
// configuration can do damage that a reboot will not undo.
func failBoot(err error) {
	rationale := "storage: a declared role can't be satisfied, and a machine declared to have persistent state must not come up ephemeral; powering off"
	if errors.Is(err, errIdentity) {
		rationale = "identity: one image boots many machines, and a machine that can't tell which configuration is its own must not guess; powering off"
	}
	fmt.Fprintf(os.Stderr, "liken: %v\n", err)
	fmt.Fprintf(os.Stderr, "liken: %s\n", rationale)
	powerOff()
	// powerOff returns only if the reboot syscall failed. PID 1 still
	// must not exit, so this loop keeps the machine here for a person
	// to investigate.
	for {
		time.Sleep(time.Hour)
	}
}
