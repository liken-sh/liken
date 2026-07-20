package main

// Facts: init's half of the Machine status.
//
// Init is the only program that observes the boot: the DHCP
// exchange, and the hardware as the kernel first presents it. So it
// is the only program that can report those facts. It writes them to
// /run/liken, shaped exactly like the Machine's status block. The
// liken operator, which runs in the cluster and cannot see any of
// this directly, publishes them to the API. Init never talks to
// Kubernetes; this file is the entire interface between the two.

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// factsFile is the facts' owner after boot. Two writers share it: the
// clock, which folds in each new time measurement, and the restart
// path, which records what a k3s restart just did. Each writer
// mutates and rewrites the facts only under the lock, because the
// file write serializes the whole struct. Even writers of separate
// fields would race without the lock.
type factsFile struct {
	mu     sync.Mutex
	status *machine.MachineStatus
}

// mutate edits the facts in memory without rewriting the file. It
// serves updates that are not news on their own, but should travel
// with whatever write comes next.
func (f *factsFile) mutate(edit func(*machine.MachineStatus)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	edit(f.status)
}

// publish edits the facts and rewrites the file. Every facts write is
// atomic, so the operator sees old facts or new facts, never a torn
// mix of both.
func (f *factsFile) publish(edit func(*machine.MachineStatus)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	edit(f.status)
	if err := machine.WriteFacts(factsPath, f.status); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing facts: %v\n", err)
	}
}

// Where the facts and the boot manifest land. These are package
// variables rather than constants, so tests can publish into a
// tempdir. A real boot never points them anywhere but /run.
var (
	factsPath        = machine.FactsPath
	bootManifestPath = machine.BootManifestPath

	// xtablesProbe is the command that reports the netfilter
	// userspace's version. It is a variable so tests can aim it at
	// nothing.
	xtablesProbe = "iptables"
)

// factsInputs gathers everything the boot decided that the facts
// report. publishFacts assembles it into the Machine status. This is
// a struct rather than a parameter list for the same reason as
// k3sBootInputs: nearly a dozen positional arguments invite mixed-up
// order, and named fields read correctly at the call site.
type factsInputs struct {
	clusterDoc  *cluster.Cluster
	role        api.Role
	choice      *manifestChoice
	conns       []*connection
	storage     machine.StorageStatus
	boot        machine.BootStatus
	modules     []machine.ModuleStatus
	features    []machine.FeatureStatus
	registries  machine.RegistriesStatus
	firstSync   *timeSync
	timeSources []string
	unclaimed   []machine.UnclaimedDevice
}

// publishFacts returns the facts it wrote, wrapped in the guarded
// owner that the boot's long-lived writers share.
func publishFacts(in factsInputs) *factsFile {
	now := time.Now()
	// Every block here carries the same facts the boot printed to the
	// console, the console-parity principle: anything reported only
	// to the serial port is invisible to anyone operating the machine
	// remotely, so the status must repeat what the console reports.
	// The hardware and firmware blocks are re-derived rather than
	// remembered. Storage and boot arrive as arguments, because they
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
			Unclaimed:    in.unclaimed,
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
	// (legacy)"; the version and variant are the interesting part.
	// Like the kernel release, the code asks the running machine for
	// this, rather than copying it from a build pin.
	if out, ok := run(xtablesProbe, "-V"); ok {
		facts.Version.Xtables = strings.TrimPrefix(out, "iptables ")
	}

	// Boot artifacts, bundled images, and data files have no version
	// command of their own to ask. For these, applyComponentFacts
	// reports from the record that the image build staged beside the
	// bytes (versions.go).
	applyComponentFacts(&facts.Version)

	// Sysinfo is one syscall that answers two questions: how much
	// memory the machine has, and how long it has been up. Subtracted
	// from the clock, uptime gives the moment the machine booted. (The
	// wall clock itself comes from the hypervisor's RTC; there is no
	// NTP synchronization yet at this point.)
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err == nil {
		facts.Hardware.MemoryBytes = uint64(si.Totalram) * uint64(si.Unit)
		booted := now.Add(-time.Duration(si.Uptime) * time.Second)
		facts.BootedAt = &booted
	}

	// Network facts exist only for interfaces that came up; a machine
	// that failed DHCP still publishes the facts it has. The
	// top-level summary describes the primary interface: the
	// cluster-facing one when the Cluster's nodeCIDR identifies it,
	// otherwise the first interface that came up.
	facts.Network = networkFacts(in.clusterDoc, in.conns, now)

	// The clock's state so far: the boot-time measurement, if one
	// succeeded, or an accurate unsynchronized or free-running report.
	facts.Time = timeStatus(in.firstSync, in.timeSources)

	// The founding write happens directly, rather than through
	// publish, because main is still the only goroutine at this
	// point, and the success line should print only for a write that
	// lands.
	owner := &factsFile{status: facts}
	if err := machine.WriteFacts(factsPath, facts); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing facts: %v\n", err)
		return owner
	}
	fmt.Printf("liken: facts published to %s\n", factsPath)

	// The manifest this boot ran under, byte for byte. This is how
	// the operator identifies which Machine it manages, and, on a
	// first boot, the spec to seed the in-cluster Machine from. The
	// code publishes it beside the facts because it shares their
	// lifetime: it describes this boot.
	if len(in.choice.raw) > 0 {
		if err := os.WriteFile(bootManifestPath, in.choice.raw, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "liken: writing the boot manifest: %v\n", err)
		}
	}
	return owner
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
