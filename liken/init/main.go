// liken is the first and only program that the kernel starts. The
// kernel unpacks the initramfs into an in-memory root filesystem and
// runs this program as process ID 1, because the kernel command line
// has rdinit=/liken. The program starts with a bare environment, any
// boot parameters the kernel did not recognize as arguments, no other
// processes, and almost no filesystem, so it sets up everything else
// itself.
//
// The kernel treats PID 1 differently from every other process, and
// that shapes this program:
//
//   - If PID 1 exits, the kernel panics.
//
//   - The kernel re-parents every orphaned process to PID 1, which
//     must reap its exit status. Otherwise the process stays in the
//     process table as a zombie.
//
//   - The kernel delivers a signal to PID 1 only when PID 1 has
//     installed a handler for it. SIGKILL cannot have a handler, so a
//     stray kill -9 cannot kill init and panic the machine.
//
// liken is not a service manager. It sets up the minimum environment
// that k3s needs, starts k3s, and keeps it running, and Kubernetes
// manages every other service. The few loops that k3s depends on run
// in init as goroutines, which the code calls the machine plane.
// components.go gives the rule for what runs in the machine plane and
// what runs in the cluster. When the image has no k3s, a boot is a
// self-test: mount the essential filesystems, read the Machine
// manifest, join the network, check the connection with a DNS lookup,
// and power off.
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

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

func main() {
	// The kernel opened /dev/console as file descriptors 0, 1, and 2
	// before it started init. With console=ttyS0 on the kernel command
	// line, that is the serial port.
	fmt.Println("liken: hello from userspace")

	// The panic fault runs before init mounts anything, so the recovery
	// that fault.go describes involves only the kernel and the firmware.
	if fault == "panic" {
		panic("liken: fault injection: this release panics at startup")
	}

	// Run as any other process, for example from a shell on a
	// development machine, the code below would mount filesystems over
	// the host's system paths.
	if os.Getpid() != 1 {
		fmt.Fprintln(os.Stderr, "liken must run as PID 1 and this process is not PID 1, so it did not start")
		os.Exit(1)
	}

	// maybeSwitchRoot replaces the initramfs root with a real root
	// filesystem before any mount or child process exists. When it
	// switches, it executes init again from the new root and main starts
	// over, so the console shows the hello line twice (switchroot.go).
	maybeSwitchRoot()

	mountEssentials()

	// Once /proc and /dev are mounted, init writes its output to
	// /dev/kmsg (console.go). main replaces os.Stdout and os.Stderr
	// here, while no other goroutine uses them.
	redirectToKmsg()

	// The reaper starts before any child process exists, so no exit
	// status is missed. Nothing above this line forks.
	plane.start("the reaper", reap)

	// efivarfs holds the firmware's boot entries and boot order, on a
	// machine with UEFI firmware. The world report and the facts tree
	// both read it (efi.go).
	mountEFIVars()

	// loadModules loads the OS's own list of kernel modules. It runs
	// before settleStorage because the system slots are FAT32, and
	// mounting vfat needs the nls_iso8859-1 character table, which
	// Ubuntu's kernel config builds as a module. The rest of the list is
	// for k3s. The modules that the manifest declares load later, in a
	// second pass, because settleStorage chooses the manifest.
	loadModules()

	// A report boot changes nothing on the machine. It loads the drivers
	// for this hardware, records which disks and interfaces appear,
	// writes a proposed manifest to the install stick, and reboots. It
	// runs before settleStorage so that it never claims, formats, or
	// writes the machine's own disks (report.go).
	if reporting() {
		runHardwareReport() // reboots and never returns
	}

	// An install boot parses its cluster document before it touches a
	// disk. The install copies the document into both slots, and every
	// boot from the disk derives the machine's role from it, so a
	// document that does not parse would fail every later boot.
	//
	// The check runs before reclaimManifestDisks blanks the disks and
	// settleStorage formats them, so a typo in the document costs no
	// data. failBoot holds the console on an install boot, so the person
	// who chose the install entry sees the reason.
	if installing() {
		if err := checkSeedCluster(cluster.ClusterManifestPath); err != nil {
			failBoot(err)
		}
	}

	// A reinstall boot blanks the disks that its manifest declares, so
	// you can install over a machine that already runs liken with no
	// other tool. It runs after loadModules, which creates the device
	// nodes, and before settleStorage, which then finds the disks blank
	// and claims them. The boot then goes on to install (reinstall.go).
	if bootParam(reinstallParam) {
		reclaimManifestDisks()
	}

	// settleStorage also chooses the manifest that this boot runs under:
	// the staged manifest that waits for its trial boot, the proven
	// manifest, or, on the first boot only, the seed manifest in the
	// image. When the image has manifests for many machines,
	// liken.machine= selects one (manifests.go). It returns an error
	// when it cannot select this machine's manifest, or when no manifest
	// can satisfy its storage roles.
	choice, storage, boot, err := settleStorage()
	if err != nil {
		failBoot(err)
	}
	m := choice.m

	// The installer writes liken.slot= into the command line of each
	// boot entry from disk. The operator reads it from the boot record
	// and downloads the next release into the other system slot.
	if slot := bootParamValue("liken.slot"); slot != "" {
		boot.Slot = slot
		fmt.Printf("liken: running from system slot %s\n", slot)
	}

	// An install boot copies the running release onto the machine's own
	// disk (install.go), before the network, the clock, and k3s, which
	// it does not need. It powers off instead of rebooting, because the
	// install stick is still first in the boot order and a reboot would
	// run the installer again. On an install that a person chose from
	// the menu, holdInstallerConsole waits for that person (attended.go).
	if installing() {
		slot, err := installToDisk(m.Metadata.Name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: %v\n", err)
			fmt.Fprintln(os.Stderr, "liken: the install did not finish, so the machine is powering off. Running the installer again is safe: fix the cause and boot the installer again")
			holdInstallerConsole("liken: press Enter to power off", true)
		} else {
			holdInstallerConsole(fmt.Sprintf("liken: installed to slot %s. Remove the stick, then press Enter to power off. The next power-on boots from the disk.", slot), false)
		}
		powerOff()
		for {
			time.Sleep(time.Hour) // PID 1 must not exit, even here
		}
	}

	// A kernel crash in an earlier boot leaves a record in pstore.
	// settleCrashRecords copies it to machineState and returns the
	// summary for status.lastCrash (crash.go). It runs after
	// settleStorage because it copies only when machineState is on a
	// partition. An install boot never reaches this line, so the crash
	// records stay for the installed system to read.
	mountPstore()
	lastCrash := settleCrashRecords(machine.MachineStateDir,
		storage.MachineState.Backing == machine.BackingPartition)

	// When failBoot powered off an earlier boot, it saved its reason on
	// machineState, because nothing recorded the console. reportFailStop
	// reads it back for status.lastFailStop (failstop.go).
	lastFailStop := reportFailStop(machine.MachineStateDir)

	// settleSystemRelease finds where this boot is in a release upgrade
	// (proving.go). On the trial boot of a staged release, it returns
	// the staged record, so the proving watch below can promote it. On
	// the fallback boot after a failed release, and on an ordinary boot,
	// it makes the firmware's boot entries agree with the stored state.
	// The actuator edits this machine's firmware boot entries
	// (actuator.go).
	//
	// observeBootOrder runs first because it reads the BootOrder that
	// the firmware kept through the reset, and settleSystemRelease can
	// write BootOrder (handover.go).
	actuator := chooseBootActuator()
	bootOrderKept = observeBootOrder(actuator, machine.MachineStateDir)
	trial := settleSystemRelease(actuator, machine.MachineStateDir, boot.Slot,
		storage.MachineState.Backing == machine.BackingPartition, &boot)

	// The cluster document lists the leaders, and this machine derives
	// its role from it. Like the Machine manifest, it has a staged, a
	// proven, and a seed copy (cluster.go). A machine that cannot read it
	// cannot tell its role, so the boot stops.
	clusterDoc, clusterRaw, err := chooseCluster(machine.MachineStateDir, cluster.ClusterManifestPath,
		storage.MachineState.Backing == machine.BackingPartition, &boot)
	if err != nil {
		failBoot(fmt.Errorf("%w: %v", errIdentity, err))
	}

	// The registry credentials also have a staged and a proven copy
	// (registries.go). The operator stages them from the
	// registry-credentials Secret. A failure here does not stop the
	// boot, because a machine without credentials pulls images
	// anonymously.
	creds := chooseRegistryCredentials(machine.MachineStateDir,
		storage.MachineState.Backing == machine.BackingPartition, &boot)

	// This is the second module pass, for the modules that spec.modules
	// declares. The boot record keeps the requested modules, in the
	// manifest's order, and the operator compares them with the spec to
	// tell whether the running kernel matches it
	// (machine-operator/converge.go). The order is the load order, and
	// it decides which driver binds a device, so the record must not be
	// sorted.
	boot.Modules = slices.Clone(m.Spec.Modules)
	boot.ModuleParameters = m.Spec.ModuleParameters
	moduleStatuses := loadDeclaredModules(m.Spec.Modules, m.Spec.ModuleParameters)
	// A serio attachment needs its driver loaded, so the attachments are
	// declared after the modules. The serio watch attaches them later
	// (serio.go).
	boot.Serio = slices.Clone(m.Spec.Serio)
	serioAttachments.declare(m.Spec.Serio)

	// actuateFeatures applies the cluster's optional features on this
	// machine and returns a status for each (features.go). A feature
	// bundled in k3s takes effect through the k3s configuration that
	// clusterLife writes. A vendored feature loads its modules, writes
	// its boot files, and creates its workloads here. The boot record has
	// no entry for features, because a change to the features shows
	// in the hash of the whole cluster document.
	featureStatuses := actuateFeatures(clusterDoc, m.Metadata.Name)

	if name := m.Metadata.Name; name != "" {
		if err := unix.Sethostname([]byte(name)); err != nil {
			fmt.Fprintf(os.Stderr, "liken: setting the hostname to %q: %v\n", name, err)
		} else {
			fmt.Printf("liken: hostname is %s\n", name)
		}
	}

	// The default sysctls apply first and the Machine spec's sysctls
	// second, so a spec value overrides a default. Both apply before k3s
	// starts, so k3s reads the final values, and before the network
	// comes up, so the default queueing discipline applies to the
	// machine's interfaces. The operator applies both sets again when
	// the cluster is up, so an edit takes effect without a reboot.
	//
	// The defaults include the sysctls of module parameters
	// (machine.ModuleSysctls). Their files exist only after the module
	// loads, and the declared modules loaded above.
	defaults, _ := machine.DefaultSysctls(sysctlDir, m.Spec.Sysctls)
	applySysctls(defaults)
	applySysctls(m.Spec.Sysctls)

	// The request sizes of the disks apply before k3s starts, so etcd
	// never writes to a clusterState disk with the kernel's larger
	// request size (disksettings.go). The operator applies them again
	// when the cluster is up, so an edit takes effect without a reboot.
	applyRequestSizes(m.Spec.Disks, storage)

	// Resource limits apply in the same order. A process inherits its
	// limits when it forks, so init sets them on itself before it starts
	// k3s, and k3s and every process it starts inherit them. The
	// operator cannot apply a limit live, so an edit to spec.rlimits
	// takes effect at the next reboot.
	applyRlimits(machine.OSRlimits)
	applyRlimits(m.Spec.Rlimits)
	// The boot record keeps the requested limits. status.rlimits reports
	// the limits that the kernel applied, which publishBootFacts reads.
	boot.Rlimits = m.Spec.Rlimits

	worldReport()

	conns, radios, err := bringUpNetwork(m.Spec.Network, clusterDoc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: bringing up the network: %v\n", err)
	}
	for _, conn := range conns {
		conn.report()
	}

	if _, err := os.Stat(k3sBinary); err == nil {
		clusterLife(choice, storage, boot, clusterDoc, clusterRaw, creds,
			conns, radios, moduleStatuses, featureStatuses, actuator, trial, lastCrash, lastFailStop) // never returns
	}

	// An image without k3s ends the self-test here. The power-off makes
	// QEMU exit cleanly, so `make run` works as a test.
	fmt.Println("liken: boot complete, powering off")
	powerOff()
}

// clusterLife runs the part of a boot that needs k3s. It writes k3s's
// configuration, sets the clock, publishes the facts, starts the
// machine plane's components, and then supervises k3s. It never
// returns. The machine leaves it only by a reboot or a power-off.
func clusterLife(choice *manifestChoice, storage machine.StorageStatus, boot machine.BootStatus,
	clusterDoc *cluster.Cluster, clusterRaw []byte, creds *machine.RegistryCredentials,
	conns []*connection, radios *radioPass, moduleStatuses []machine.ModuleStatus,
	featureStatuses []machine.FeatureStatus, actuator bootActuator, trial *machine.SystemRelease,
	lastCrash *machine.CrashStatus, lastFailStop *machine.FailStop) {
	m := choice.m

	// When the container images that an earlier boot imported were
	// never proven, settleImageImports discards the container store
	// before k3s opens it (imports.go). It stages this boot's image
	// tarballs as a trial for the operator to prove.
	settleImageImports(machine.MachineStateDir,
		storage.MachineState.Backing == machine.BackingPartition,
		storage.ClusterState.Backing == machine.BackingPartition, &boot)
	// A failure here is an identity failure too, because a follower
	// with no address for its cluster must not start as if it had one
	// (k3s.go).
	role, err := writeK3sBootConfig(clusterDoc, m, conns)
	if err != nil {
		failBoot(fmt.Errorf("%w: %v", errIdentity, err))
	}
	// k3s reads registries.yaml when it starts. Writing the file also
	// promotes staged credentials, so no other step applies them
	// (registries.go).
	registries := writeRegistriesConfig(clusterDoc, creds,
		machine.RegistryCredentialsStore(machine.MachineStateDir), boot.CredentialsSource)
	// k3s creates a node password when the machine first joins. Without
	// it after a reboot, the machine cannot rejoin its own cluster, so a
	// password that cannot be kept on machineState is a boot fault
	// (k3s.go).
	if err := persistNodePassword(storage); err != nil {
		failBoot(err)
	}
	// After a proven demotion from leader, purgeLeaderLeftovers deletes
	// the old etcd datastore before k3s starts, because etcd refuses a
	// removed member that rejoins over its old data (k3s.go).
	purgeLeaderLeftovers(role, boot.ClusterManifestSource, k3sServerDB)
	// The clock is set before k3s starts, because TLS fails with a clock
	// that runs behind: every certificate looks like it was issued in
	// the future. init steps the clock only here, and slews it after
	// that (time.go).
	clk := newClock(timeSources(clusterDoc, role, machine.MachineManifestDir))
	firstSync := stepClockAtBoot(clk.sources)
	clk.record(firstSync)
	// The hardware clock gets the measured time at once, so a machine
	// that loses power before a clean shutdown still boots with close to
	// the right time (time.go).
	if firstSync != nil {
		writeRTC()
	}
	hostname, _ := os.Hostname()
	configureNameResolution(hostname, m.Spec.Network.HostEntries)
	prepareForK3s()
	// bindPodLogs mounts podEphemeral at /var/log/pods, where kubelet
	// writes pod logs. It runs after prepareForK3s, which creates
	// /var/log and sets the mount propagation that the bind mount
	// inherits, and before kubelet opens its first log file (podlogs.go).
	bindPodLogs(storage)
	// rotateBootLogs moves the previous boot's k3s and containerd logs
	// aside. It runs in the boot sequence and not in the machine plane,
	// because the rename is safe only before k3s opens the files
	// (logrotate.go).
	rotateBootLogs()
	// The hardware walk runs here because every declared module has
	// loaded and bound its devices. So a device without a driver at
	// this point lacks one, and is not waiting for a load to run.
	catalog := loadHardwareCatalog()
	unclaimed := discoverUnclaimed(catalog)
	blockDevices := discoverBlockDevices()
	initialTime := timeStatus(firstSync, clk.sources)
	// The boot publishes all its facts in one call, so the operator's
	// first read of the facts tree finds a complete tree.
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
		lastFailStop: lastFailStop,
	})
	publishBootManifest(choice)
	publishBootClusterManifest(clusterRaw)
	// The radio watch writes the result of a background wireless join,
	// and then keeps each radio's access point current (radiowatch.go).
	// It starts after publishBootFacts, because that call writes the
	// whole network subtree and would overwrite an earlier result with
	// the pending state.
	if radios != nil || declaresRadio(conns) {
		plane.start("the radio watch", watchRadios(conns, radios, m.Spec.Network.Interfaces, factsTree, clusterDoc))
	}
	// The hardware watch reads uevents and updates the published
	// hardware report when a device is plugged in or removed
	// (hardware.go). Without the catalog, init cannot tell which devices
	// lack a driver, so it starts no watch.
	if catalog != nil {
		for _, line := range hardwareTransitions(nil, unclaimed, nil) {
			fmt.Println(line)
		}
		plane.start("the hardware watch", watchHardware(catalog, factsTree, unclaimed, blockDevices))
	}
	// The disk links watch writes the /dev/disk/by-path, by-id, and
	// by-uuid links that CSI drivers, mount tools, and people use to find
	// a disk or a filesystem (disklinks.go). It runs on every machine,
	// with or without the catalog, because the links need no device
	// database. A machine can also get its first iSCSI disk long after
	// boot, when a workload requests a volume.
	plane.start("the disk links", watchDiskLinks)
	// The serio watch attaches the serial lines that spec.serio declares
	// (serio.go). It starts before k3s, so an adapter plugged in at boot
	// already has its devices when machine-operator publishes its first
	// ResourceSlice. It runs on every machine, because a live module
	// load can declare the first entry at any time.
	plane.start("the serio watch", watchSerio(serioAttachments, factsTree))
	// A machine with no time sources has no clock to follow, and its
	// time status already says so.
	if len(clk.sources) > 0 {
		plane.start("the clock", disciplineClock(clk, factsTree, machine.ClockDir, initialTime))
	}
	// Followers sync their clocks from the leaders, so only a leader
	// serves time. A leader serves time even with no upstream source, so
	// the machines of a fleet with no source still agree with each other
	// (responder.go).
	if role == api.RoleLeader {
		plane.start("the time responder", serveTime(clk))
	}
	// machine-operator writes intent files into machine.OperatorRunDir,
	// and the intent watch sends each one to the channel for it
	// (reboot.go). The watch stops after its first reboot intent, so a
	// boot sends at most one reboot request.
	if err := os.MkdirAll(machine.OperatorRunDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "liken: creating %s: %v\n", machine.OperatorRunDir, err)
	}
	rebootRequests := make(chan machine.RebootIntent, 1)
	restartRequests := make(chan machine.RestartIntent, 1)
	loadRequests := make(chan machine.ModulesIntent, 1)
	plane.start("the intent watch", func(ctx context.Context) error {
		return watchForOperatorIntents(ctx, machine.OperatorRunDir, rebootRequests, restartRequests, loadRequests)
	})
	// The module loader loads the modules that an edit adds to
	// spec.modules into the running kernel, with no reboot and no k3s
	// restart (liveload.go). It runs apart from the k3s supervisor,
	// because the load does not touch k3s.
	loader := &moduleLoader{
		tree:           factsTree,
		bootStorage:    boot.Storage,
		bootNetwork:    m.Spec.Network,
		bootModules:    boot.Modules,
		bootParameters: boot.ModuleParameters,
		bootSerio:      boot.Serio,
		serio:          serioAttachments,
		statuses:       moduleStatuses,
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
	// The restart state keeps what a k3s restart needs to write k3s's
	// configuration again (restart.go).
	restarter := newRestartState(machine.MachineStateDir, m, conns, factsTree,
		clusterDoc, clusterRaw, creds, boot.CredentialsSource)
	// On the trial boot of a staged release, the proving watch waits for
	// the operator to prove the release, and then makes the firmware
	// boot the new slot by default (proving.go).
	if trial != nil {
		plane.start("the proving watch", provingWatch(actuator, *trial, machine.MachineStateDir, factsTree))
	}
	// Only a leader prints the node report, because k3s writes the admin
	// kubeconfig only on a server. A follower's join shows on the
	// leader's console and in the follower's own k3s log.
	if role == api.RoleLeader {
		plane.start("the node report", reportWhenReady)
	}
	// The wedge fault never starts k3s, so the release is never proven
	// (fault.go). The machine plane keeps running, so the proving
	// watchdog can still reboot the machine.
	if fault == "wedge-k3s" {
		fmt.Println("liken: fault injection: not starting k3s, and staying up")
		select {}
	}
	superviseK3s(role, rebootRequests, restartRequests, restarter.apply,
		restarter.removeQueuedRetractions) // returns only when a one-shot boot fails to power off
}

// powerOff makes every disk read-only, which records that the machine
// stopped cleanly (quiesce.go), flushes the logs and the disks, and
// powers the machine off. The installer calls it with the slots that
// it wrote still mounted. PID 1 must not exit, so this is the only
// way for init to stop.
func powerOff() {
	stopSupplicants()
	quiesceDisks()
	syncLogs()
	unix.Sync()
	if err := unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF); err != nil {
		fmt.Fprintf(os.Stderr, "liken: powering off: %v\n", err)
	}
}

// failBoot prints the error and the reason for the power-off, and
// powers the machine off. Each step of the boot returns what it could
// not do, and main decides which failures stop the boot:
//
//   - identity: the machine cannot tell which manifest, or which
//     role, is its own. A guess could join the wrong cluster, start a
//     second control plane, or claim another machine's disks.
//   - storage: a declared storage role cannot be satisfied, and a
//     machine declared to have persistent state must not start with
//     its state in memory.
//
// A machine that is off can be fixed and booted again. A machine that
// runs with the wrong configuration can do damage that a reboot does
// not undo.
func failBoot(err error) {
	rationale := "storage: a declared storage role cannot be satisfied, and a machine declared to have persistent state must not start with its state in memory. Powering off"
	if errors.Is(err, errIdentity) {
		rationale = "identity: one image boots many machines, and a machine that cannot tell which configuration is its own must not guess. Powering off"
	}
	fmt.Fprintf(os.Stderr, "liken: %v\n", err)
	fmt.Fprintf(os.Stderr, "liken: %s\n", rationale)
	// Nothing records the console of a boot that never started k3s, so
	// the reason is also written to machineState for the next boot to
	// report (failstop.go).
	recordFailStop(machine.MachineStateDir, err.Error(), time.Now().UTC())
	// On an install boot, holdInstallerConsole shows the disks and, when
	// a person chose this boot from the menu, waits for them
	// (attended.go). An install that nobody chose powers off without a
	// prompt.
	if installing() {
		holdInstallerConsole("liken: press Enter to power off", true)
	}
	powerOff()
	// powerOff returns only when the power-off failed. PID 1 must not
	// exit, so the machine stays up for a person to investigate.
	for {
		time.Sleep(time.Hour)
	}
}
