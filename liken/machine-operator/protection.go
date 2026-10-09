package main

// Storage protection: the disks a workload must never claim.
//
// A disk belongs either to the machine, as a storage role, or to the
// workloads, through DRA, never both. A claim on the system disk would
// hand an unprivileged pod the machine's own root filesystem. The
// facts that init publishes are the only record of which partitions
// back a role, because init claimed the disks before this cluster
// existed. So when the facts do not read, the operator cannot tell a
// system disk from a spare one, and it treats every disk as the
// machine's own. A device with no block node holds nothing a role can
// use, so a GPU or a radio stays on offer while the facts are away.
//
// Three places check the same protection: the inventory that offers a
// device, the prepare call that delivers an allocation, and the
// refresh that rewrites a prepared claim. Each of them checks, because
// an allocation can outlive the offer it came from: a slice write can
// fail, and the scheduler can allocate from a slice the pass has not
// yet withdrawn.

import (
	"errors"
	"slices"
	"sync/atomic"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/machine"
)

// protection is the set of block devices that back a storage role. Its
// zero value is the protection of a machine whose facts did not read,
// which protects every block device.
type protection struct {
	known  bool
	blocks map[string]bool
}

// protectionOf reads the protection from the facts. facts.Read returns
// a whole record or none, so a non-nil record names every role, and a
// role in memory names no device. A record whose roles name no disk is
// a complete answer, and it protects nothing.
func protectionOf(facts *machine.MachineStatus) protection {
	if facts == nil {
		return protection{}
	}
	p := protection{known: true, blocks: map[string]bool{}}
	for _, name := range machine.StorageRoleNames {
		if role := facts.Storage.Role(name); role != nil && role.Device != "" {
			p.blocks[role.Device] = true
		}
	}
	return p
}

// protects answers whether a block device must stay with the machine.
func (p protection) protects(block string) bool {
	return !p.known || p.blocks[block]
}

// withholds answers whether a delivery carries a block device that
// must stay with the machine.
func (p protection) withholds(d hardware.Delivery) bool {
	return slices.ContainsFunc(d.Blocks(), p.protects)
}

// platformProtection carries the protection to the DRA plugin, which
// prepares claims on the kubelet's schedule, apart from the reconcile
// pass. main seeds it from the boot's facts before the plugin serves,
// and every pass sets it again from the facts it read. Before the
// seed, it protects every disk.
var platformProtection atomic.Pointer[protection]

func setPlatformProtection(p protection) {
	platformProtection.Store(&p)
}

func currentProtection() protection {
	if p := platformProtection.Load(); p != nil {
		return *p
	}
	return protection{}
}

// errWithheld is an allocated device that is present but delivers a
// block device the machine uses, or may use.
var errWithheld = errors.New("it delivers a disk that backs a storage role, or the facts do not say which disks do")

// withheldNode is the node a prepared claim's spec names while its
// device is withheld. No such node exists, so the runtime fails to
// create the next container that holds the claim, and the claim's
// pod shows the failure. A pod that started before the device was
// withheld keeps the nodes it received.
const withheldNode = "/dev/liken.sh/device-withheld"
