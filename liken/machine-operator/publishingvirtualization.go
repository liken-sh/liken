package main

// The virtualization devices are the misc nodes that a virtual machine
// monitor opens: /dev/kvm, and for QEMU also /dev/vhost-net (tap
// packets handled in the kernel) and /dev/vhost-vsock (vsock addresses
// for guests). containerd's default device rules deny all three to a
// container, so without a claim only a privileged pod can use them.
// The kernel registers each as a misc device under /sys/class/misc,
// outside every bus, so the bus walk does not find them.
//
// Only the devices in virtualizationDevices are published. A bus device is
// one piece of equipment, but a misc device is a service of the whole
// kernel, and some give control of the machine: loop-control and
// device-mapper create block devices on the host, and snapshot reads
// the hibernation image. /dev/net/tun is not in the table because
// containerd's default rules already allow it in every container, so a
// claim on it would grant nothing.
//
// The kernel gives each process that opens one of these nodes its own
// VM or vhost worker, so every device is shareable and has no
// capacity. The pods' memory and CPU requests limit the guests on a
// node, because a guest's memory belongs to its monitor process.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// virtualizationDevice is one row of the table. attribute is the
// device attribute that marks what it is, and minor is the number that
// include/linux/miscdevice.h fixes for it. module is the kernel module
// that registers the node, published as the device's driver. kvm has
// none, because /dev/kvm comes from kvm_intel or kvm_amd, and
// kvmAttributes finds which.
type virtualizationDevice struct {
	misc      string
	attribute string
	minor     int
	module    string
	// offered reports whether the machine offers the device when its
	// node exists. A nil offered means the node is enough.
	offered func(sysctlRoot string) bool
}

var virtualizationDevices = []virtualizationDevice{
	{misc: "kvm", attribute: "kvm", minor: 232},
	{misc: "vhost-net", attribute: "vhostNet", minor: 238, module: "vhost_net"},
	{misc: "vhost-vsock", attribute: "vhostVsock", minor: 241, module: "vhost_vsock", offered: vsockNamespacePerPod},
}

// miscMajor is the major number shared by every misc device.
const miscMajor = 10

// virtualizationFileMode is the mode of the node that a claim creates
// in the container. A monitor often runs as a non-root user, and the
// host's node is 0600 root:root because liken runs no udev, so the
// claim uses the mode of systemd's default udev rules
// (rules.d/50-udev-default.rules.in, with the dev-kvm-mode build
// option). The host's node keeps its own mode, and the device cgroup
// still denies the node to a container without a claim.
const virtualizationFileMode = 0o666

// vsockNamespacePerPod reports whether each pod's network namespace
// gets its own vsock addresses. A new namespace takes its mode from
// its parent's child_ns_mode, and the operator runs in the host's
// namespace, so it reads the host's value. machine.ModuleSysctls sets
// it to local. In global mode every pod shares the host's addresses,
// so two pods' guests can collide on an address, and any pod can
// connect to any guest without a claim, because a vsock socket needs
// no device node. So a machine in global mode does not offer
// vhost-vsock.
func vsockNamespacePerPod(sysctlRoot string) bool {
	mode, err := machine.ReadSysctl(sysctlRoot, "net.vsock.child_ns_mode")
	return err == nil && mode == "local"
}

func (v virtualizationDevice) name() string { return "misc-" + v.misc }

func (v virtualizationDevice) node() string { return "/dev/" + v.misc }

// present reports whether the machine offers the device: the kernel
// registered its node and the offered check, if any, passes.
func (v virtualizationDevice) present(sysRoot, sysctlRoot string) bool {
	if hardware.MiscNode(sysRoot, v.misc) != v.node() {
		return false
	}
	return v.offered == nil || v.offered(sysctlRoot)
}

// virtualizationNamed returns the row whose published name is name.
func virtualizationNamed(name string) (virtualizationDevice, bool) {
	for _, v := range virtualizationDevices {
		if v.name() == name {
			return v, true
		}
	}
	return virtualizationDevice{}, false
}

// virtualizationMinor returns the fixed minor number of the node at
// path, so a claim can state the node's major and minor numbers.
func virtualizationMinor(path string) (int, bool) {
	for _, v := range virtualizationDevices {
		if v.node() == path {
			return v.minor, true
		}
	}
	return 0, false
}

// virtualizationSliceDevices returns the table's devices that this
// machine offers, in table order, which is sorted by name.
func virtualizationSliceDevices(sysRoot, sysctlRoot string) []kubernetes.SliceDevice {
	var out []kubernetes.SliceDevice
	for _, v := range virtualizationDevices {
		if !v.present(sysRoot, sysctlRoot) {
			continue
		}
		attrs := map[string]kubernetes.DeviceAttribute{
			// class has a small fixed set of values so that the device
			// metric's label stays bounded (metrics.go).
			"class":     kubernetes.AttrString("virtualization"),
			"subsystem": kubernetes.AttrString("misc"),
			v.attribute: kubernetes.AttrBool(true),
		}
		if v.misc == "kvm" {
			kvmAttributes(sysRoot, attrs)
		}
		// A misc device has no bound driver, so driver is the module
		// that registered the node. A module built into the kernel with
		// no parameters has no /sys/module entry, so the device then has
		// no driver attribute, and selectors must guard with has().
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

// kvmAttributes adds the driver and nested attributes from the vendor
// module, kvm_intel or kvm_amd, that registered /dev/kvm. nested means
// a guest can run KVM itself. kvm_intel writes the parameter as Y or N,
// and kvm_amd as 1 or 0. The parameter changes only when the module
// reloads, which removes and adds the node, so the uevents of the
// reload start the pass that reads it again.
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

// resolveVirtualization returns what a claim on an allocated device
// receives. It runs the same present check as the slice, so prepare
// grants only a device that the slice would publish now. ok is false
// when the table has no such name. errNotPublished means the table has
// the name but the machine no longer offers the device.
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
