package main

// The operator protects some block devices, and no claim may receive a
// protected device.
//
// A disk either backs one of the machine's storage roles or is
// available to workloads through DRA, never both, because a claim on
// the system disk would give an unprivileged pod the machine's root
// filesystem. init assigned the disks to roles before the cluster
// existed, and the facts it publishes are the only record of which
// partitions back a role. When the facts cannot be read, the operator
// cannot tell a system disk from a spare one, so it protects every
// block device. A device with no block node, such as a GPU or a radio,
// is still offered, because no storage role can use it.
//
// The inventory, the DRA prepare call, and the refresh of a prepared
// claim's CDI spec each check the protection. The later checks are
// needed because an allocation can outlive the slice it came from: a
// slice write can fail, and the scheduler can allocate a device from
// a slice that the pass has not yet removed it from.

import (
	"errors"
	"slices"
	"sync/atomic"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/machine"
)

// protection is the set of block devices that back a storage role. Its
// zero value is the protection for a machine whose facts could not be
// read, and it protects every block device.
type protection struct {
	known  bool
	blocks map[string]bool
}

// protectionOf returns the zero protection when facts is nil. facts.Read
// returns a whole record or none, so a non-nil record covers every
// role. A role backed by memory has no device and protects nothing.
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

// protects reports whether no claim may receive the block device.
func (p protection) protects(block string) bool {
	return !p.known || p.blocks[block]
}

// withholds reports whether a delivery includes a protected block
// device.
func (p protection) withholds(d hardware.Delivery) bool {
	return slices.ContainsFunc(d.Blocks(), p.protects)
}

// platformProtection is the protection that the DRA plugin and the CDI
// refresh read. The kubelet calls the plugin outside the reconcile
// pass, so the plugin cannot take the protection as an argument. main
// sets it from the facts before the plugin starts serving, and each
// pass sets it again from the facts it read. Until main sets it, every
// block device is protected.
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

// errWithheld is the error for an allocated device whose delivery
// includes a protected block device.
var errWithheld = errors.New("it delivers a disk that backs a storage role, or the facts do not say which disks do")

// withheldNode is the device node that a prepared claim's CDI spec
// lists while its device is withheld. The path does not exist, so the
// container runtime fails to create the next container that uses the
// claim, and the pod shows the failure. A container that started
// before the device was withheld keeps the nodes it already has.
const withheldNode = "/dev/liken.sh/device-withheld"
