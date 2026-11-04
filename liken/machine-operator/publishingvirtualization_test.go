package main

// The virtualization devices: which misc nodes the slice offers, with
// which facts, and what a claim on one delivers.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/liken-sh/liken/liken/kubernetes"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

// vendorModule writes the /sys/module entry of a loaded KVM vendor
// module, with the value its nested parameter reads.
func vendorModule(t *testing.T, sysRoot, module, nested string) {
	t.Helper()
	dir := filepath.Join(sysRoot, "module", module, "parameters")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested"), []byte(nested+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// vsockNamespaceMode writes the host namespace's child_ns_mode, the
// file the vsock module registers when it loads.
func vsockNamespaceMode(t *testing.T, sysctlRoot, mode string) {
	t.Helper()
	dir := filepath.Join(sysctlRoot, "net", "vsock")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child_ns_mode"), []byte(mode+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func boolAttr() kubernetes.DeviceAttribute { return kubernetes.AttrBool(true) }

func TestTheKVMDeviceNamesItsVendorModuleAndNesting(t *testing.T) {
	// kvm_intel reports nesting as Y or N, and kvm_amd as 1 or 0. A
	// guest that runs KVM itself needs nesting, so the attribute is
	// present only when the module turned it on.
	shared := true
	for _, tc := range []struct {
		name, module, nested string
		want                 kubernetes.SliceDevice
	}{
		{"intel, nested", "kvm_intel", "Y", kubernetes.SliceDevice{
			Name: "misc-kvm", AllowMultipleAllocations: &shared,
			Attributes: map[string]kubernetes.DeviceAttribute{
				"class": kubernetes.AttrString("virtualization"), "subsystem": kubernetes.AttrString("misc"),
				"kvm": boolAttr(), "driver": kubernetes.AttrString("kvm_intel"), "nested": boolAttr(),
			},
		}},
		{"amd, nested", "kvm_amd", "1", kubernetes.SliceDevice{
			Name: "misc-kvm", AllowMultipleAllocations: &shared,
			Attributes: map[string]kubernetes.DeviceAttribute{
				"class": kubernetes.AttrString("virtualization"), "subsystem": kubernetes.AttrString("misc"),
				"kvm": boolAttr(), "driver": kubernetes.AttrString("kvm_amd"), "nested": boolAttr(),
			},
		}},
		{"intel, not nested", "kvm_intel", "N", kubernetes.SliceDevice{
			Name: "misc-kvm", AllowMultipleAllocations: &shared,
			Attributes: map[string]kubernetes.DeviceAttribute{
				"class": kubernetes.AttrString("virtualization"), "subsystem": kubernetes.AttrString("misc"),
				"kvm": boolAttr(), "driver": kubernetes.AttrString("kvm_intel"),
			},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sysRoot := t.TempDir()
			miscDevice(t, sysRoot, "kvm", 232)
			vendorModule(t, sysRoot, tc.module, tc.nested)

			got := virtualizationSliceDevices(sysRoot, t.TempDir())

			if !reflect.DeepEqual(got, []kubernetes.SliceDevice{tc.want}) {
				t.Errorf("devices = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestOnlyTheVirtualizationNodesPublish(t *testing.T) {
	// A misc device is a service of the whole kernel, and some of them
	// hand over the machine: loop-control and device-mapper make block
	// devices on the host. Only the nodes the table names publish.
	for _, tc := range []struct {
		name      string
		misc      map[string]int
		vsockMode string
		want      []string
	}{
		{"no virtualization modules", map[string]int{"loop-control": 237, "fuse": 229}, "", nil},
		{"vhost-net", map[string]int{"vhost-net": 238}, "", []string{"misc-vhost-net"}},
		{"vhost-vsock with a namespace for each pod", map[string]int{"vhost-vsock": 241}, "local", []string{"misc-vhost-vsock"}},
		{"vhost-vsock with one namespace for every pod", map[string]int{"vhost-vsock": 241}, "global", nil},
		{"vhost-vsock with no vsock parameter", map[string]int{"vhost-vsock": 241}, "", nil},
		{"all three", map[string]int{"kvm": 232, "vhost-net": 238, "vhost-vsock": 241}, "local",
			[]string{"misc-kvm", "misc-vhost-net", "misc-vhost-vsock"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sysRoot, sysctlRoot := t.TempDir(), t.TempDir()
			for name, minor := range tc.misc {
				miscDevice(t, sysRoot, name, minor)
			}
			if tc.vsockMode != "" {
				vsockNamespaceMode(t, sysctlRoot, tc.vsockMode)
			}

			var names []string
			for _, d := range virtualizationSliceDevices(sysRoot, sysctlRoot) {
				names = append(names, d.Name)
			}

			if !reflect.DeepEqual(names, tc.want) {
				t.Errorf("devices = %v, want %v", names, tc.want)
			}
		})
	}
}

func TestTheVhostDevicesCarryTheirOwnAttribute(t *testing.T) {
	sysRoot, sysctlRoot := t.TempDir(), t.TempDir()
	miscDevice(t, sysRoot, "vhost-net", 238)
	miscDevice(t, sysRoot, "vhost-vsock", 241)
	vsockNamespaceMode(t, sysctlRoot, "local")

	got := map[string]map[string]kubernetes.DeviceAttribute{}
	for _, d := range virtualizationSliceDevices(sysRoot, sysctlRoot) {
		got[d.Name] = d.Attributes
	}

	want := map[string]map[string]kubernetes.DeviceAttribute{
		"misc-vhost-net": {
			"class": kubernetes.AttrString("virtualization"), "subsystem": kubernetes.AttrString("misc"), "vhostNet": boolAttr(),
		},
		"misc-vhost-vsock": {
			"class": kubernetes.AttrString("virtualization"), "subsystem": kubernetes.AttrString("misc"), "vhostVsock": boolAttr(),
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("attributes = %+v, want %+v", got, want)
	}
}

// preparedVirtualization prepares the fixture's claim, allocated to one
// virtualization device, and answers the kubelet's answer for it.
func preparedVirtualization(t *testing.T, fixture *draFixture, device string) *drav1.NodePrepareResourceResponse {
	t.Helper()
	fixture.allocated = device
	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Claims["claim-1"]
}

// withSysctlRoot points the operator's /proc/sys at a test directory.
func withSysctlRoot(t *testing.T, dir string) {
	t.Helper()
	saved := sysctlRoot
	t.Cleanup(func() { sysctlRoot = saved })
	sysctlRoot = dir
}

func TestPrepareDeliversAVirtualizationNodeThatEveryUserOpens(t *testing.T) {
	// A virtual machine monitor often runs as a user other than root.
	// The host's node is 0600 root:root, so the claim states the node's
	// numbers and the mode that systemd's default rules give it, and
	// the runtime makes the node in the container with that mode.
	mode := 0o666
	for _, tc := range []struct {
		device string
		misc   string
		minor  int
	}{
		{"misc-kvm", "kvm", 232},
		{"misc-vhost-net", "vhost-net", 238},
		{"misc-vhost-vsock", "vhost-vsock", 241},
	} {
		t.Run(tc.device, func(t *testing.T) {
			fixture := newDRAFixture(t)
			miscDevice(t, draSysfsRoot, tc.misc, tc.minor)
			sysctls := t.TempDir()
			vsockNamespaceMode(t, sysctls, "local")
			withSysctlRoot(t, sysctls)

			answer := preparedVirtualization(t, fixture, tc.device)

			if answer == nil || answer.Error != "" {
				t.Fatalf("answer = %+v", answer)
			}
			want := []cdiDeviceNode{{Path: "/dev/" + tc.misc, Type: "c", Major: 10, Minor: tc.minor, FileMode: &mode}}
			if got := specNodes(t, fixture, "claim-1"); !sameDeviceNodes(got, want) {
				t.Errorf("nodes = %+v, want %+v", got, want)
			}
		})
	}
}

func TestPrepareRefusesAVirtualizationDeviceTheMachineDoesNotOffer(t *testing.T) {
	// A claim allocated from an older slice can name a device that is
	// gone, or vhost-vsock on a machine whose pods share one vsock
	// namespace. Prepare must not grant either.
	for _, tc := range []struct {
		name, device, misc string
		minor              int
		vsockMode          string
	}{
		{"kvm with no node", "misc-kvm", "", 0, ""},
		{"vhost-vsock with one namespace for every pod", "misc-vhost-vsock", "vhost-vsock", 241, "global"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newDRAFixture(t)
			if tc.misc != "" {
				miscDevice(t, draSysfsRoot, tc.misc, tc.minor)
			}
			sysctls := t.TempDir()
			if tc.vsockMode != "" {
				vsockNamespaceMode(t, sysctls, tc.vsockMode)
			}
			withSysctlRoot(t, sysctls)

			answer := preparedVirtualization(t, fixture, tc.device)

			if answer == nil || answer.Error == "" {
				t.Errorf("answer = %+v, want a refusal", answer)
			}
		})
	}
}

func TestTheSliceListsTheVirtualizationDevicesInNameOrder(t *testing.T) {
	// The slice writer compares the devices it sends with its last
	// write, so the same machine must always publish the same order.
	hardware := []kubernetes.SliceDevice{{Name: "pci-0000-00-02-0"}, {Name: "usb-1-8-1-0"}}
	virtual := []kubernetes.SliceDevice{{Name: "misc-kvm"}, {Name: "misc-vhost-net"}}

	var names []string
	for _, d := range withVirtualization(hardware, virtual) {
		names = append(names, d.Name)
	}

	want := []string{"misc-kvm", "misc-vhost-net", "pci-0000-00-02-0", "usb-1-8-1-0"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}
