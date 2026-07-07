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
	"time"

	"golang.org/x/sys/unix"

	"github.com/chrisguidry/liken/machine"
)

// publishFacts returns the facts it wrote: the time component takes
// over as their sole owner afterward, folding in each new clock
// measurement and rewriting the file.
func publishFacts(cluster *machine.Cluster, role machine.Role, choice *manifestChoice,
	conns []*connection, storage machine.StorageStatus, boot machine.BootStatus,
	firstSync *timeSync, timeSources []string) *machine.MachineStatus {
	now := time.Now()
	facts := &machine.MachineStatus{
		Role:    role,
		Version: machine.VersionStatus{Liken: machine.Version},
		Hardware: machine.HardwareStatus{
			CPUs: runtime.NumCPU(),
			// The same inventory the world report prints, re-derived
			// rather than remembered, like everything else in status.
			BlockDevices: discoverBlockDevices(),
		},
		// The firmware's boot facts, the same ones reportFirmware
		// printed at boot: console parity, as always.
		Firmware: firmwareFacts(efiVarsDir),
		// Where every storage role landed, and under which manifest.
		// These blocks can't be re-derived here, because they were
		// only observable while storage was settling.
		Storage: storage,
		Boot:    boot,
	}

	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		facts.Version.Kernel = unix.ByteSliceToString(u.Release[:])
	}

	// The netfilter userspace reports itself as "iptables vX.Y.Z
	// (legacy)"; the version and variant are the interesting part. Like
	// the kernel release, this is asked of the running machine, not
	// copied from a build pin.
	if out, ok := run("iptables", "-V"); ok {
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
	facts.Network = networkFacts(cluster, conns, now)

	// The clock's state so far: the boot-time measurement if one
	// succeeded, or an accurate unsynchronized/free-running report.
	facts.Time = timeStatus(firstSync, timeSources)

	if err := machine.WriteFacts(machine.FactsPath, facts); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing facts: %v\n", err)
		return facts
	}
	fmt.Printf("liken: facts published to %s\n", machine.FactsPath)

	// The manifest this boot ran under, byte for byte: the operator's
	// way to know which Machine it manages (and, on a first boot, the
	// spec to seed the in-cluster Machine from). Published beside the
	// facts because it shares their lifetime: it describes this boot.
	if len(choice.raw) > 0 {
		if err := os.WriteFile(machine.BootManifestPath, choice.raw, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "liken: writing the boot manifest: %v\n", err)
		}
	}
	return facts
}

// publishBootClusterManifest is the cluster document's version of the
// boot manifest publication: the exact bytes this boot derived its
// role from, for the operator's drift detection (which compares
// documents by meaning, and needs bytes to parse, not just a hash).
func publishBootClusterManifest(raw []byte) {
	if len(raw) == 0 {
		return
	}
	if err := os.WriteFile(machine.BootClusterManifestPath, raw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing the boot cluster manifest: %v\n", err)
	}
}

// networkFacts folds every connection into a NetworkStatus.
func networkFacts(cluster *machine.Cluster, conns []*connection, now time.Time) machine.NetworkStatus {
	status := machine.NetworkStatus{}
	if len(conns) == 0 {
		return status
	}

	for _, conn := range conns {
		status.Interfaces = append(status.Interfaces, interfaceFacts(conn, now))
	}

	primary := conns[0]
	if _, ifname := nodeAddress(cluster, conns); ifname != "" {
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
