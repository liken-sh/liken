package main

// Facts: init's half of the Machine status.
//
// Init is the only program that observes the boot (the DHCP
// exchange, the hardware as the kernel first presented it), so it's
// the only program that can report those facts. It writes them to
// /run/liken, shaped exactly like the Machine's status block, and the
// liken operator (which runs in the cluster and can't see any of this
// firsthand) publishes them to the API. Init never talks to
// Kubernetes; this file is the entire interface between the two.

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// factsFile is the facts' owner after boot: two writers share it,
// the clock (folding in each new time measurement) and the restart
// path (recording what a k3s restart just actuated). Each mutates
// and rewrites only under the lock, because the file write
// serializes the whole struct, so even writers of disjoint fields
// race without one.
type factsFile struct {
	mu     sync.Mutex
	status *machine.MachineStatus
}

// mutate edits the facts in memory without rewriting the file: for
// updates that aren't news on their own, but should ride along with
// whatever write comes next.
func (f *factsFile) mutate(edit func(*machine.MachineStatus)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	edit(f.status)
}

// publish edits the facts and rewrites the file, atomically as every
// facts write is: the operator sees old facts or new, never torn
// ones.
func (f *factsFile) publish(edit func(*machine.MachineStatus)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	edit(f.status)
	if err := machine.WriteFacts(factsPath, f.status); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing facts: %v\n", err)
	}
}

// Where the facts and the boot manifest land. Package variables
// rather than constants so tests can publish into a tempdir; a real
// boot never points them anywhere but /run.
var (
	factsPath        = machine.FactsPath
	bootManifestPath = machine.BootManifestPath

	// xtablesProbe is the command that reports the netfilter
	// userspace's version, a variable so tests can aim it at nothing.
	xtablesProbe = "iptables"
)

// factsInputs gathers everything the boot decided that the facts
// report: publishFacts assembles it into the Machine status. A struct
// rather than a parameter list for the same reason as k3sBootInputs:
// nearly a dozen positional arguments invite transposition, and named
// fields read correctly at the call site.
type factsInputs struct {
	clusterDoc  *cluster.Cluster
	role        machine.Role
	choice      *manifestChoice
	conns       []*connection
	storage     machine.StorageStatus
	boot        machine.BootStatus
	modules     []machine.ModuleStatus
	features    []machine.FeatureStatus
	registries  machine.RegistriesStatus
	firstSync   *timeSync
	timeSources []string
}

// publishFacts returns the facts it wrote, wrapped in the guarded
// owner the boot's long-lived writers share.
func publishFacts(in factsInputs) *factsFile {
	now := time.Now()
	// Every block here carries the same facts the boot printed to the
	// console — the console-parity principle: anything reported only
	// to the serial port is invisible to anyone operating the machine
	// remotely, so what the console narrates, the status must repeat.
	// The hardware and firmware blocks are re-derived rather than
	// remembered; storage and boot arrive as arguments because they
	// were only observable while storage was settling.
	facts := &machine.MachineStatus{
		Role:       in.role,
		Version:    machine.VersionStatus{Liken: machine.Version},
		Modules:    in.modules,
		Features:   in.features,
		Registries: in.registries,
		Hardware: machine.HardwareStatus{
			CPUs:         runtime.NumCPU(),
			BlockDevices: discoverBlockDevices(),
		},
		Firmware: firmwareFacts(efiVarsDir),
		Storage:  in.storage,
		Boot:     in.boot,
	}

	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		facts.Version.Kernel = unix.ByteSliceToString(u.Release[:])
	}

	// The netfilter userspace reports itself as "iptables vX.Y.Z
	// (legacy)"; the version and variant are the interesting part. Like
	// the kernel release, this is asked of the running machine, not
	// copied from a build pin.
	if out, ok := run(xtablesProbe, "-V"); ok {
		facts.Version.Xtables = strings.TrimPrefix(out, "iptables ")
	}

	// Sysinfo is one syscall answering two questions: how much memory
	// the machine has, and how long it's been up, which, subtracted
	// from the clock, is the moment it booted. (The wall clock itself
	// comes from the hypervisor's RTC; there's no NTP yet.)
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err == nil {
		facts.Hardware.MemoryBytes = uint64(si.Totalram) * uint64(si.Unit)
		booted := now.Add(-time.Duration(si.Uptime) * time.Second)
		facts.BootedAt = &booted
	}

	// Network facts only exist for interfaces that came up; a machine
	// that failed DHCP still publishes what it knows. The top-level
	// summary describes the primary interface: the cluster-facing one
	// when the Cluster's nodeCIDR identifies it, otherwise the first
	// that came up.
	facts.Network = networkFacts(in.clusterDoc, in.conns, now)

	// The clock's state so far: the boot-time measurement if one
	// succeeded, or an accurate unsynchronized/free-running report.
	facts.Time = timeStatus(in.firstSync, in.timeSources)

	// The founding write happens directly rather than through
	// publish: main is still the only goroutine at this point, and
	// the success line should only print for a write that landed.
	owner := &factsFile{status: facts}
	if err := machine.WriteFacts(factsPath, facts); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing facts: %v\n", err)
		return owner
	}
	fmt.Printf("liken: facts published to %s\n", factsPath)

	// The manifest this boot ran under, byte for byte: the operator's
	// way to know which Machine it manages (and, on a first boot, the
	// spec to seed the in-cluster Machine from). Published beside the
	// facts because it shares their lifetime: it describes this boot.
	if len(in.choice.raw) > 0 {
		if err := os.WriteFile(bootManifestPath, in.choice.raw, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "liken: writing the boot manifest: %v\n", err)
		}
	}
	return owner
}

// publishBootClusterManifest is the cluster document's version of the
// boot manifest publication: the exact bytes this boot derived its
// role from, for the operator's drift detection (which compares
// documents by meaning, and needs bytes to parse, not just a hash).
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

// interfaceFacts is one connection as status: the same facts the
// console report prints, made queryable.
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
