package main

// The virtualization devices: the misc nodes that a virtual machine
// monitor opens, published so that a pod can claim them.
//
// A program that boots a virtual machine opens /dev/kvm, and QEMU
// also opens /dev/vhost-net to move a tap device's packets in the
// kernel and /dev/vhost-vsock to give a guest a vsock address.
// containerd's default device rules refuse all three to a container,
// so without a claim only a privileged pod reaches them. The kernel
// registers each one as a misc device, under /sys/class/misc and
// outside every bus, so the walk of the buses never meets them.
//
// The misc class publishes from a table, never by default. A bus
// device is one piece of equipment, and the policy publishes one it
// has not examined whole and exclusive. A misc device is a service of
// the whole kernel, and several of them hand over the machine itself:
// loop-control and device-mapper make block devices on the host, and
// snapshot reads the hibernation image. So a misc device that this
// table does not name never reaches the slice. /dev/net/tun is not in
// the table, because containerd's default device rules already admit
// it to every container, so a claim on it would grant nothing.
//
// Each open of one of these nodes is an instance of its own in the
// kernel: a VM, or a vhost worker. So every device in the table is
// shareable, and has no capacity. The pod's memory and CPU requests
// bound how many guests a node holds, because a guest's memory is the
// memory of its monitor's process.
//
// Milestone 93 (liken/plans/completed/93-a-pod-claims-kvm-to-run-virtual-machines.md)
// holds the measurements and the reasons.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// virtualizationDevice is one row of the table: the kernel's name for
// the misc device, the attribute that states what it is, the minor
// number the kernel fixes for it in include/linux/miscdevice.h, and
// the module that registers the node, which the device publishes as
// its driver. kvm has no module here, because /dev/kvm comes from one
// of two vendor modules, and kvmAttributes finds which.
type virtualizationDevice struct {
	misc      string
	attribute string
	minor     int
	module    string
	// offered answers whether the machine offers the device, beyond
	// its node existing. Most rows need nothing more.
	offered func(sysctlRoot string) bool
}

var virtualizationDevices = []virtualizationDevice{
	{misc: "kvm", attribute: "kvm", minor: 232},
	{misc: "vhost-net", attribute: "vhostNet", minor: 238, module: "vhost_net"},
	{misc: "vhost-vsock", attribute: "vhostVsock", minor: 241, module: "vhost_vsock", offered: vsockNamespacePerPod},
}

// The misc major number, which every misc device shares.
const miscMajor = 10

// virtualizationFileMode is the mode a claim gives the node inside the
// container. It is the mode systemd's default rules give these three
// nodes (rules.d/50-udev-default.rules.in, with the dev-kvm-mode build
// option), because the kernel designs the KVM and vhost interfaces for
// processes with no privilege. A monitor often runs as a user other
// than root, and the host's node is 0600 root:root, because liken runs
// no udev. The device cgroup still refuses the node to every container
// that holds no claim on it, and the host's node keeps its own mode.
const virtualizationFileMode = 0o666

// vsockNamespacePerPod answers whether each pod's network namespace
// gets vsock addresses of its own. A guest's vsock address is unique in
// its namespace. In global mode every pod shares the host's addresses,
// so two pods that each start a guest at the same address collide, and
// any pod can open a socket to any pod's guest with no claim at all,
// because a socket needs no device node. A new namespace takes its mode
// from its parent's child_ns_mode, and machine.ModuleSysctls sets the
// host's to local. The operator runs in the host's network namespace,
// so this reads the host's value. A machine whose spec keeps global
// offers no vhost-vsock, because a claim on it would grant a guest that
// every pod can reach.
func vsockNamespacePerPod(sysctlRoot string) bool {
	mode, err := machine.ReadSysctl(sysctlRoot, "net.vsock.child_ns_mode")
	return err == nil && mode == "local"
}

func (v virtualizationDevice) name() string { return "misc-" + v.misc }

func (v virtualizationDevice) node() string { return "/dev/" + v.misc }

// present answers whether the machine offers the device now: the
// kernel registered its node, and its condition holds.
func (v virtualizationDevice) present(sysRoot, sysctlRoot string) bool {
	if hardware.MiscNode(sysRoot, v.misc) != v.node() {
		return false
	}
	return v.offered == nil || v.offered(sysctlRoot)
}

// virtualizationNamed answers the row that a published name denotes.
func virtualizationNamed(name string) (virtualizationDevice, bool) {
	for _, v := range virtualizationDevices {
		if v.name() == name {
			return v, true
		}
	}
	return virtualizationDevice{}, false
}

// virtualizationMinor answers the fixed minor of a node that the table
// names, so a claim can state the node's numbers.
func virtualizationMinor(path string) (int, bool) {
	for _, v := range virtualizationDevices {
		if v.node() == path {
			return v.minor, true
		}
	}
	return 0, false
}

// virtualizationSliceDevices answers the table's devices that this
// machine offers, in the table's order, which is sorted by name.
func virtualizationSliceDevices(sysRoot, sysctlRoot string) []kubernetes.SliceDevice {
	var out []kubernetes.SliceDevice
	for _, v := range virtualizationDevices {
		if !v.present(sysRoot, sysctlRoot) {
			continue
		}
		attrs := map[string]kubernetes.DeviceAttribute{
			// The class word keeps the device metric's label bounded
			// (metrics.go), and states what the device is for.
			"class":     kubernetes.AttrString("virtualization"),
			"subsystem": kubernetes.AttrString("misc"),
			v.attribute: kubernetes.AttrBool(true),
		}
		if v.misc == "kvm" {
			kvmAttributes(sysRoot, attrs)
		}
		// A misc device has no bound driver, so driver names the
		// module that registered the node. A class that reads driver
		// with no has() then matches nothing here instead of failing
		// on a CEL error, though every selector should still guard the
		// read: a module built into the kernel with no parameters has
		// no /sys/module entry, and the device then has no driver.
		if _, err := os.Stat(filepath.Join(sysRoot, "module", v.module)); v.module != "" && err == nil {
			attrs["driver"] = kubernetes.AttrString(v.module)
		}
		shared := true
		out = append(out, kubernetes.SliceDevice{
			Name:                     v.name(),
			Attributes:               attrs,
			AllowMultipleAllocations: &shared,
		})
	}
	return out
}

// kvmAttributes adds the facts of the vendor module that registered
// /dev/kvm. The kernel registers the node only after kvm_intel or
// kvm_amd loads, so one of them is in /sys/module. nested states that
// a guest can run KVM itself, which a guest that hosts guests needs.
// kvm_intel reports the parameter as Y or N, and kvm_amd as 1 or 0.
// Both modules turn it on by default, and spec.moduleParameters can
// turn it off. A parameter changes only when the module loads again,
// and the reload removes and adds the node, so its uevents wake the
// pass that reads the parameter again.
func kvmAttributes(sysRoot string, attrs map[string]kubernetes.DeviceAttribute) {
	for _, module := range []string{"kvm_intel", "kvm_amd"} {
		dir := filepath.Join(sysRoot, "module", module)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		attrs["driver"] = kubernetes.AttrString(module)
		raw, err := os.ReadFile(filepath.Join(dir, "parameters", "nested"))
		if value := strings.TrimSpace(string(raw)); err == nil && (value == "Y" || value == "1") {
			attrs["nested"] = kubernetes.AttrBool(true)
		}
		return
	}
}

// resolveVirtualization maps an allocated name to what a claim on it
// receives, from the same table and the same reads that publish the
// slice, so the prepare call and the slice cannot disagree. ok is false
// for a name the table does not name.
func resolveVirtualization(name, sysRoot, sysctlRoot string) (published publishedDevice, ok bool, err error) {
	v, ok := virtualizationNamed(name)
	if !ok {
		return publishedDevice{}, false, nil
	}
	if !v.present(sysRoot, sysctlRoot) {
		return publishedDevice{}, true, errNotPublished
	}
	return publishedDevice{Subsystem: "misc", Nodes: []string{v.node()}, Shareable: true}, true, nil
}
