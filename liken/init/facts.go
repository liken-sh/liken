package main

// Init writes the facts it discovers during boot, such as the DHCP
// lease and the hardware the kernel found, under /run/liken/facts as a
// tree of small files with the same shape as the Machine's status.
// Init never talks to Kubernetes. machine-operator runs in the
// cluster, reads the tree from the host, and copies it into the
// Machine's status. It also reads the manifests that
// publishBootManifest and publishBootClusterManifest write in
// /run/liken.
//
// Each fact has its own file, so the init components that update facts
// after boot each rewrite their own subtree with no shared lock.
// machine/factstree.go lists which component owns each subtree.

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// These are variables so that tests can write into a temporary
// directory and run an iptables that does not exist.
var (
	factsTree        = machine.FactsTree{Dir: machine.FactsDir, Report: reportFactsError}
	bootManifestPath = machine.BootManifestPath
	xtablesProbe     = "iptables"
)

// reportFactsError prints a failed facts write and lets the boot go on,
// so callers of the tree's writers ignore their errors. The operator
// reads a missing fact as its zero value, which loses one report. That
// is no reason to stop PID 1.
func reportFactsError(err error) {
	fmt.Fprintf(os.Stderr, "liken: writing facts: %v\n", err)
}

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
	lastFailStop *machine.FailStop
}

// publishBootFacts writes the first value of every fact in the tree.
// Later components rewrite their own subtrees. The tree repeats what
// the boot printed to the console, because a person who manages the
// machine remotely cannot see its serial console. publishBootFacts
// reads the version, firmware, CPU count, memory, and boot time itself.
// Everything else comes in through in.
func publishBootFacts(tree machine.FactsTree, in bootFacts) {
	now := time.Now()
	memoryBytes, bootedAt := machineUptimeFacts(now)

	tree.WriteRole(in.role)
	// The caller runs settleCrashRecords early in boot, because it
	// moves or deletes records in pstore, and that must happen once.
	tree.WriteLastCrash(in.lastCrash)
	tree.WriteLastFailStop(in.lastFailStop)
	tree.WriteVersion(versionFacts())
	tree.WriteNetwork(networkFacts(in.clusterDoc, in.conns))
	tree.WriteTime(in.time)
	tree.WriteHardwareBasics(runtime.NumCPU(), memoryBytes)
	tree.WriteBlockDevices(in.blockDevices)
	tree.WriteUnclaimed(in.unclaimed)
	tree.WriteFirmware(firmwareFacts(efiVarsDir))
	tree.WriteStorage(in.storage)
	tree.WriteModules(in.modules)
	tree.WriteFeatures(in.features)
	tree.WriteRegistries(in.registries)
	tree.WriteRuntime(runtimeFacts(in.clusterDoc, in.role, memoryBytes))
	// Init reads its own limits, because every process on the machine
	// inherits them from PID 1. in.boot.Rlimits is the spec's set.
	tree.WriteRlimits(readRlimits(machine.OSRlimits, in.boot.Rlimits))

	tree.WriteBootTime(bootedAt)
	tree.WriteBootSlot(in.boot.Slot)
	tree.WriteBootCommandLine(cmdlineRaw())
	tree.WriteBootStorage(in.boot.Storage)
	tree.WriteBootNetwork(in.boot.Network)
	tree.WriteBootModules(in.boot.Modules)
	tree.WriteBootModuleParameters(in.boot.ModuleParameters)
	tree.WriteBootSerio(in.boot.Serio)
	tree.WriteBootRlimits(in.boot.Rlimits)
	tree.WriteBootManifest(in.boot.ManifestSource, in.boot.ManifestHash)
	tree.WriteBootClusterManifest(in.boot.ClusterManifestSource, in.boot.ClusterManifestHash)
	tree.WriteBootCredentials(in.boot.CredentialsSource, in.boot.CredentialsHash)
	tree.WriteBootImports(in.boot.ImportsSource, in.boot.ImportsHash, in.boot.ImportsDiscarded)
	tree.WriteRejection(machine.RejectMachine, in.boot.Rejection)
	tree.WriteRejection(machine.RejectCluster, in.boot.ClusterRejection)
	tree.WriteRejection(machine.RejectSystem, in.boot.SystemRejection)
	tree.WriteRejection(machine.RejectCredentials, in.boot.CredentialsRejection)
}

// runtimeFacts returns the settings that init applies to k3s, the
// kubelet, and containerd. The k3s memory limit is the value in MiB
// that k3sRuntimeEnv computes for GOMEMLIMIT from the same spec and
// memory size, written with Kubernetes's Mi suffix where GOMEMLIMIT
// takes MiB. A limit of 0 reports nothing. The other values are copied
// from the spec, and an unset field reports nothing. The etcd value is
// the exception: it is true when init wrote k3s's etcd-expose-metrics
// key, which only a leader's configuration has.
func runtimeFacts(clusterDoc *cluster.Cluster, role api.Role, memoryBytes uint64) machine.RuntimeStatus {
	spec := clusterDoc.RuntimeSpec()
	st := machine.RuntimeStatus{}
	if gc, ok := spec.GoGCPercent(); ok {
		st.K3s.GoGC = gc
	}
	limit, off, err := spec.GoMemoryLimitBytes(memoryBytes)
	if err == nil && !off && limit > 0 {
		st.K3s.GoMemoryLimit = fmt.Sprintf("%dMi", limit/(1<<20))
	}
	st.K3s.Debug = spec.Debug
	st.Containerd.LogLevel = clusterDoc.ContainerdSpec().LogLevel
	st.Etcd.ExposeMetrics = role == api.RoleLeader && clusterDoc.EtcdSpec().MetricsExposed()
	imageGC := clusterDoc.KubeletSpec().ImageGC
	if imageGC.HighThresholdPercent != nil {
		st.Kubelet.ImageGC.HighThresholdPercent = *imageGC.HighThresholdPercent
	}
	if imageGC.LowThresholdPercent != nil {
		st.Kubelet.ImageGC.LowThresholdPercent = *imageGC.LowThresholdPercent
	}
	st.Kubelet.ImageGC.MaximumAge = imageGC.MaximumAge
	st.Kubelet.ImageGC.MinimumAge = imageGC.MinimumAge
	return st
}

// machineMemoryBytes returns the machine's total memory in bytes, or 0
// when sysinfo fails.
func machineMemoryBytes() uint64 {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0
	}
	return uint64(si.Totalram) * uint64(si.Unit)
}

// machineUptimeFacts returns the machine's total memory and its boot
// time, or zero and nil when sysinfo fails. The boot time is now minus
// the uptime, so it is only as accurate as the clock. At this point in
// boot the clock can still be the RTC's time, before NTP corrects it.
func machineUptimeFacts(now time.Time) (memoryBytes uint64, bootedAt *time.Time) {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0, nil
	}
	booted := now.Add(-time.Duration(si.Uptime) * time.Second)
	return uint64(si.Totalram) * uint64(si.Unit), &booted
}

// versionFacts returns the versions of the software on the machine.
// The kernel release, the iptables version, and the microcode revision
// come from the running machine. The other versions come from the
// record that the image build wrote (versions.go).
func versionFacts() machine.VersionStatus {
	v := machine.VersionStatus{Liken: machine.Version}

	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		v.Kernel = unix.ByteSliceToString(u.Release[:])
	}

	// "iptables -V" prints a line such as "iptables v1.8.10 (legacy)".
	// The status keeps the version and the backend in parentheses.
	if out, ok := run(xtablesProbe, "-V"); ok {
		v.Xtables = strings.TrimPrefix(out, "iptables ")
	}

	// This is the revision that the CPU runs. When it matches the
	// release's microcode pin, the early microcode archive was applied.
	// On a virtual machine it is the revision that the hypervisor
	// reports, so only real hardware proves the match.
	v.MicrocodeRevision = microcodeRevision(cpuinfoPath)

	applyComponentFacts(&v)
	return v
}

// publishBootManifest writes the exact bytes of the Machine manifest
// that this boot ran under. The operator reads it to find which Machine
// it manages, and on a first boot it creates the Machine from its spec.
func publishBootManifest(choice *manifestChoice) {
	if len(choice.raw) == 0 {
		return
	}
	if err := os.WriteFile(bootManifestPath, choice.raw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing the boot manifest: %v\n", err)
	}
}

// publishBootClusterManifest writes the exact bytes of the cluster
// document that this boot took its role from. The operator parses it
// and renders it again before it compares it with the Cluster, so a
// difference in formatting alone is not drift. A hash of the bytes
// could not tell the two apart.
func publishBootClusterManifest(raw []byte) {
	if len(raw) == 0 {
		return
	}
	if err := os.WriteFile(cluster.BootClusterManifestPath, raw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing the boot cluster manifest: %v\n", err)
	}
}

// networkFacts builds the NetworkStatus from the connections. It reads
// no clock and no kernel state, so when the radio watch calls it again
// after boot, every interface that did not change reports the same
// facts.
func networkFacts(clusterDoc *cluster.Cluster, conns []*connection) machine.NetworkStatus {
	status := machine.NetworkStatus{}
	if len(conns) == 0 {
		return status
	}

	for _, conn := range conns {
		status.Interfaces = append(status.Interfaces, interfaceFacts(conn))
	}

	// The top-level fields describe the primary interface: the one with
	// the node address, else the first with an address, else the first.
	// A radio that did not join a network has no address, so it is
	// listed in Interfaces but is not primary while another interface
	// has an address.
	primary := conns[0]
	for _, conn := range conns {
		if conn.addr != nil {
			primary = conn
			break
		}
	}
	if _, ifname := nodeAddress(clusterDoc, conns); ifname != "" {
		for _, conn := range conns {
			if conn.ifname == ifname {
				primary = conn
			}
		}
	}
	summary := interfaceFacts(primary)
	status.Interface = summary.Name
	status.MAC = summary.MAC
	status.Addresses = []string{summary.Address}
	status.Gateway = summary.Gateway
	status.Nameservers = summary.Nameservers
	status.LeaseExpires = summary.LeaseExpires
	return status
}

func interfaceFacts(conn *connection) machine.InterfaceStatus {
	status := machine.InterfaceStatus{
		Name:        conn.ifname,
		MAC:         conn.mac.String(),
		Method:      conn.method,
		Nameservers: make([]string, 0, len(conn.nameservers)),
	}
	if conn.addr != nil {
		status.Address = conn.addr.String()
	}
	if conn.radio != nil {
		status.Wireless = conn.radio.wirelessStatus()
	}
	// applyLease set leaseExpires when the DHCP ACK arrived, so every
	// call reports the same expiry.
	if conn.method == machine.MethodDHCP && !conn.leaseExpires.IsZero() {
		expires := conn.leaseExpires
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
