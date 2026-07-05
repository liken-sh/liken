package main

// Facts: init's half of the Machine status.
//
// Init is the only program that witnesses the boot — the DHCP exchange,
// the hardware as the kernel first presented it — so it's the only
// program that can report those facts. It writes them to /run/liken,
// shaped exactly like the Machine's status block, and the liken
// operator (which lives in the cluster and can't see any of this
// firsthand) publishes them to the API. Init never talks to Kubernetes;
// this file is the entire interface between the two.

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/chrisguidry/liken/machine"
)

func publishFacts(conn *connection, storage machine.StorageStatus) {
	now := time.Now()
	facts := &machine.MachineStatus{
		Version: machine.VersionStatus{Liken: machine.Version},
		Hardware: machine.HardwareStatus{
			CPUs: runtime.NumCPU(),
			// The same inventory the world report prints, re-derived
			// rather than remembered — the statelessness that makes
			// status trustworthy.
			BlockDevices: discoverBlockDevices(),
		},
		// Where every storage role landed — the one block that can't
		// be re-derived here, because only reconcileStorage witnessed
		// the claiming.
		Storage: storage,
	}

	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		facts.Version.Kernel = unix.ByteSliceToString(u.Release[:])
	}

	// The netfilter userspace introduces itself as "iptables vX.Y.Z
	// (legacy)"; the version and variant are the interesting part. Like
	// the kernel release, this is asked of the running machine, not
	// copied from a build pin.
	if out, ok := run("iptables", "-V"); ok {
		facts.Version.Xtables = strings.TrimPrefix(out, "iptables ")
	}

	// Sysinfo is one syscall answering two questions: how much memory
	// the machine has, and how long it's been up — which, subtracted
	// from the clock, is the moment it booted. (The wall clock itself
	// comes from the hypervisor's RTC; there's no NTP yet.)
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err == nil {
		facts.Hardware.MemoryBytes = uint64(si.Totalram) * uint64(si.Unit)
		booted := now.Add(-time.Duration(si.Uptime) * time.Second)
		facts.BootedAt = &booted
	}

	// Network facts only exist if the network came up; a machine that
	// failed DHCP still publishes what it knows.
	if conn != nil {
		expires := now.Add(conn.leaseTime)
		facts.Network = machine.NetworkStatus{
			Interface:    conn.ifname,
			MAC:          conn.mac.String(),
			Addresses:    []string{conn.addr.String()},
			Nameservers:  make([]string, 0, len(conn.nameservers)),
			LeaseExpires: &expires,
		}
		if conn.gateway != nil {
			facts.Network.Gateway = conn.gateway.String()
		}
		for _, ns := range conn.nameservers {
			facts.Network.Nameservers = append(facts.Network.Nameservers, ns.String())
		}
	}

	if err := machine.WriteFacts(machine.FactsPath, facts); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing facts: %v\n", err)
		return
	}
	fmt.Printf("liken: facts published to %s\n", machine.FactsPath)
}
