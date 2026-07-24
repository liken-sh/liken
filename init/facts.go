package main

// Facts: init's half of the Machine status.
//
// Init is the only program that observes the boot: the DHCP exchange,
// and the hardware as the kernel first presents it. So it is the only
// program that can report those facts. It writes them under
// /run/liken/facts as a tree of small files (machine/factstree.go),
// shaped exactly like the Machine's status block. The liken operator,
// which runs in the cluster and cannot see any of this directly,
// reads the tree and publishes it to the API. Init never talks to
// Kubernetes; this tree is the entire interface between the two.
//
// No single owner holds the facts. Each fact has its own file, so each
// init component writes its own subtree with no shared lock. A boot
// step writes its subtree once, at the point where it discovered the
// fact. The long-lived components own the subtrees that change after
// boot: the clock owns time/, the hardware watch owns
// hardware/blockDevices/ and hardware/unclaimed/, the module loader
// owns modules/ and boot/manifest, and the restart path owns
// features/, registries/, and the boot/ manifest records it rewrites.
// factstree.go documents the whole ownership map.
//
// The boot's write-once facts land together, in publishBootFacts,
// rather than each at its own discovery line. The facts tree lives
// under /run, and prepareForK3s mounts a fresh tmpfs there partway
// through the boot. A write before that mount would be hidden. So each
// boot step holds its discovered facts locally and hands them here,
// once /run is the tmpfs that lasts the machine's life.

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// Where the facts tree and the boot manifest land. These are package
// variables rather than constants, so tests can publish into a
// tempdir. A real boot never points them anywhere but /run.
var (
	factsTree        = machine.FactsTree{Dir: machine.FactsDir, Report: reportFactsError}
	bootManifestPath = machine.BootManifestPath

	// xtablesProbe is the command that reports the netfilter
	// userspace's version. It is a variable so tests can aim it at
	// nothing.
	xtablesProbe = "iptables"
)

// reportFactsError reports a failed facts write to stderr. The facts
// tree calls it for each failed write, so a boot step calls the writers
// bare. A write failure must never stop the boot: the machine keeps
// running and the operator falls back to a partial tree, which reads a
// missing fact as its zero value. Losing a fact is a reporting gap, not
// a reason to halt PID 1.
func reportFactsError(err error) {
	fmt.Fprintf(os.Stderr, "liken: writing facts: %v\n", err)
}

// bootFacts gathers the write-once facts that the boot discovered. It
// is a struct rather than a parameter list because nearly a dozen
// positional arguments invite mixed-up order, and named fields read
// correctly at the call site. It carries values the boot already
// holds; nothing routes through a shared owner.
type bootFacts struct {
	clusterDoc   *cluster.Cluster
	role         api.Role
	conns        []*connection
	storage      machine.StorageStatus
	boot         machine.BootStatus
	modules      []machine.ModuleStatus
	features     []machine.FeatureStatus
	registries   machine.RegistriesStatus
	time         machine.TimeStatus
	blockDevices []machine.BlockDevice
	unclaimed    []machine.UnclaimedDevice
	lastCrash    *machine.CrashStatus
}

// publishBootFacts writes every fact the boot discovered once and never
// revisits. It calls one per-subtree writer for each, so each fact
// lands in its own file through the same atomic rename as every later
// write. The tree carries the same facts the boot printed to the
// console, the console-parity principle: anything reported only to the
// serial port is invisible to anyone operating the machine remotely, so
// the status must repeat what the console reports. The hardware,
// version, and firmware blocks are re-derived here rather than
// remembered from earlier boot steps.
func publishBootFacts(tree machine.FactsTree, in bootFacts) {
	now := time.Now()
	memoryBytes, bootedAt := machineUptimeFacts(now)

	tree.WriteRole(in.role)
	// The crash stub arrives settled rather than being re-read here,
	// because settling it has side effects (preserving and clearing the
	// platform store) that belong to one moment early in boot.
	tree.WriteLastCrash(in.lastCrash)
	tree.WriteVersion(versionFacts())
	// Network facts exist only for interfaces that came up; a machine
	// that failed DHCP still publishes the facts it has.
	tree.WriteNetwork(networkFacts(in.clusterDoc, in.conns, now))
	// The clock's state so far: the boot-time measurement, if one
	// succeeded, or an accurate unsynchronized or free-running report.
	// The clock loop owns time/ after this seed.
	tree.WriteTime(in.time)
	tree.WriteHardwareBasics(runtime.NumCPU(), memoryBytes)
	tree.WriteBlockDevices(in.blockDevices)
	tree.WriteUnclaimed(in.unclaimed)
	tree.WriteFirmware(firmwareFacts(efiVarsDir))
	tree.WriteStorage(in.storage)
	tree.WriteModules(in.modules)
	tree.WriteFeatures(in.features)
	tree.WriteRegistries(in.registries)

	// The boot record: what this boot ran under. The four manifest
	// records seed here at boot; the module loader and the restart path
	// own the ones they rewrite afterward.
	tree.WriteBootTime(bootedAt)
	tree.WriteBootSlot(in.boot.Slot)
	tree.WriteBootStorage(in.boot.Storage)
	tree.WriteBootModules(in.boot.Modules)
	tree.WriteBootManifest(in.boot.ManifestSource, in.boot.ManifestHash)
	tree.WriteBootClusterManifest(in.boot.ClusterManifestSource, in.boot.ClusterManifestHash)
	tree.WriteBootCredentials(in.boot.CredentialsSource, in.boot.CredentialsHash)
	tree.WriteBootImports(in.boot.ImportsSource, in.boot.ImportsHash, in.boot.ImportsDiscarded)
	tree.WriteRejection(machine.RejectMachine, in.boot.Rejection)
	tree.WriteRejection(machine.RejectCluster, in.boot.ClusterRejection)
	tree.WriteRejection(machine.RejectSystem, in.boot.SystemRejection)
	tree.WriteRejection(machine.RejectCredentials, in.boot.CredentialsRejection)
}

// machineUptimeFacts answers two questions from one syscall: how much
// memory the machine has, and the moment it booted. Sysinfo reports
// total memory and uptime; subtracting uptime from the clock gives the
// boot instant. The wall clock at this point comes from the
// hypervisor's RTC, because no NTP synchronization has happened yet.
func machineUptimeFacts(now time.Time) (memoryBytes uint64, bootedAt *time.Time) {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0, nil
	}
	booted := now.Add(-time.Duration(si.Uptime) * time.Second)
	return uint64(si.Totalram) * uint64(si.Unit), &booted
}

// versionFacts assembles the machine's version inventory. Two kinds of
// value live here. The kernel release and the netfilter userspace
// version are observed from the running machine, so the code asks the
// machine itself rather than copying a build pin. The rest, the boot
// artifacts and bundled payloads, have no version command of their own,
// so applyComponentFacts reports them from the record the image build
// staged beside the bytes (versions.go).
func versionFacts() machine.VersionStatus {
	v := machine.VersionStatus{Liken: machine.Version}

	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		v.Kernel = unix.ByteSliceToString(u.Release[:])
	}

	// The netfilter userspace reports itself as "iptables vX.Y.Z
	// (legacy)"; the version and variant are the interesting part.
	if out, ok := run(xtablesProbe, "-V"); ok {
		v.Xtables = strings.TrimPrefix(out, "iptables ")
	}

	// The CPU's running microcode revision is observed the same way.
	// The microcode pin says which early cpio the release carries; this
	// says which revision the CPU actually runs. The two agreeing is the
	// proof that the early cpio applied, and only real hardware can give
	// it: on a virtual machine the hypervisor owns the microcode, and
	// this reports the hypervisor's value.
	v.MicrocodeRevision = microcodeRevision(cpuinfoPath)

	applyComponentFacts(&v)
	return v
}

// publishBootManifest writes the Machine manifest this boot ran under,
// byte for byte. This is how the operator identifies which Machine it
// manages, and, on a first boot, the spec to seed the in-cluster
// Machine from. It stays one whole file, beside the facts tree, because
// it shares the tree's lifetime and the operator needs its exact bytes.
func publishBootManifest(choice *manifestChoice) {
	if len(choice.raw) == 0 {
		return
	}
	if err := os.WriteFile(bootManifestPath, choice.raw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing the boot manifest: %v\n", err)
	}
}

// publishBootClusterManifest is the cluster document's version of the
// boot manifest publication: the exact bytes this boot derived its
// role from. The operator's drift detection needs these bytes, since
// it compares documents by meaning and needs bytes to parse, not
// just a hash.
func publishBootClusterManifest(raw []byte) {
	if len(raw) == 0 {
		return
	}
	if err := os.WriteFile(cluster.BootClusterManifestPath, raw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing the boot cluster manifest: %v\n", err)
	}
}

// networkFacts folds every connection into a NetworkStatus.
func networkFacts(clusterDoc *cluster.Cluster, conns []*connection, now time.Time) machine.NetworkStatus {
	status := machine.NetworkStatus{}
	if len(conns) == 0 {
		return status
	}

	for _, conn := range conns {
		status.Interfaces = append(status.Interfaces, interfaceFacts(conn, now))
	}

	primary := conns[0]
	if _, ifname := nodeAddress(clusterDoc, conns); ifname != "" {
		for _, conn := range conns {
			if conn.ifname == ifname {
				primary = conn
			}
		}
	}
	summary := interfaceFacts(primary, now)
	status.Interface = summary.Name
	status.MAC = summary.MAC
	status.Addresses = []string{summary.Address}
	status.Gateway = summary.Gateway
	status.Nameservers = summary.Nameservers
	status.LeaseExpires = summary.LeaseExpires
	return status
}

// interfaceFacts turns one connection into status: the same facts
// the console report prints, in a form other code can query.
func interfaceFacts(conn *connection, now time.Time) machine.InterfaceStatus {
	status := machine.InterfaceStatus{
		Name:        conn.ifname,
		MAC:         conn.mac.String(),
		Address:     conn.addr.String(),
		Method:      conn.method,
		Nameservers: make([]string, 0, len(conn.nameservers)),
	}
	if conn.method == machine.MethodDHCP {
		expires := now.Add(conn.leaseTime)
		status.LeaseExpires = &expires
	}
	if conn.gateway != nil {
		status.Gateway = conn.gateway.String()
	}
	for _, ns := range conn.nameservers {
		status.Nameservers = append(status.Nameservers, ns.String())
	}
	return status
}
