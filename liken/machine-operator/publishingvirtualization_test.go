package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/liken-sh/liken/liken/kubernetes"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

// vendorModule writes the /sys/module entry of a loaded KVM vendor
// module, with nested as the value of its nested parameter.
func vendorModule(t *testing.T, sysRoot, module, nested string) {
	t.Helper()
	dir := filepath.Join(sysRoot, "module", module, "parameters")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the parameters directory of %s: %v", module, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested"), []byte(nested+"\n"), 0o644); err != nil {
		t.Fatalf("writing the nested parameter of %s: %v", module, err)
	}
}

// vsockNamespaceMode writes the host's net.vsock.child_ns_mode sysctl,
// which the vsock module registers when it loads.
func vsockNamespaceMode(t *testing.T, sysctlRoot, mode string) {
	t.Helper()
	dir := filepath.Join(sysctlRoot, "net", "vsock")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the net.vsock sysctl directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child_ns_mode"), []byte(mode+"\n"), 0o644); err != nil {
		t.Fatalf("writing net.vsock.child_ns_mode: %v", err)
	}
}

func boolAttr() kubernetes.DeviceAttribute { return kubernetes.AttrBool(true) }

func TestTheKVMDeviceHasItsVendorModuleAndNesting(t *testing.T) {
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

// A vhost device has a driver attribute only when its module has a
// /sys/module entry.
func TestTheVhostDevicesHaveTheirOwnAttributeAndModule(t *testing.T) {
	cases := []struct {
		name    string
		modules []string
		want    map[string]map[string]kubernetes.DeviceAttribute
	}{
		{"modules loaded", []string{"vhost_net", "vhost_vsock"}, map[string]map[string]kubernetes.DeviceAttribute{
			"misc-vhost-net": {
				"class": kubernetes.AttrString("virtualization"), "subsystem": kubernetes.AttrString("misc"), "vhostNet": boolAttr(),
				"driver": kubernetes.AttrString("vhost_net"),
			},
			"misc-vhost-vsock": {
				"class": kubernetes.AttrString("virtualization"), "subsystem": kubernetes.AttrString("misc"), "vhostVsock": boolAttr(),
				"driver": kubernetes.AttrString("vhost_vsock"),
			},
		}},
		{"no module entries", nil, map[string]map[string]kubernetes.DeviceAttribute{
			"misc-vhost-net": {
				"class": kubernetes.AttrString("virtualization"), "subsystem": kubernetes.AttrString("misc"), "vhostNet": boolAttr(),
			},
			"misc-vhost-vsock": {
				"class": kubernetes.AttrString("virtualization"), "subsystem": kubernetes.AttrString("misc"), "vhostVsock": boolAttr(),
			},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sysRoot, sysctlRoot := t.TempDir(), t.TempDir()
			miscDevice(t, sysRoot, "vhost-net", 238)
			miscDevice(t, sysRoot, "vhost-vsock", 241)
			vsockNamespaceMode(t, sysctlRoot, "local")
			for _, module := range c.modules {
				if err := os.MkdirAll(filepath.Join(sysRoot, "module", module), 0o755); err != nil {
					t.Fatalf("creating the /sys/module entry of %s: %v", module, err)
				}
			}
			got := map[string]map[string]kubernetes.DeviceAttribute{}
			for _, d := range virtualizationSliceDevices(sysRoot, sysctlRoot) {
				got[d.Name] = d.Attributes
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("attributes = %+v, want %+v", got, c.want)
			}
		})
	}
}

// preparedVirtualization prepares the fixture's claim, allocated to one
// virtualization device, and returns the response for it.
func preparedVirtualization(t *testing.T, fixture *draFixture, device string) *drav1.NodePrepareResourceResponse {
	t.Helper()
	fixture.allocated = device
	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim for %s: %v", device, err)
	}
	return resp.Claims["claim-1"]
}

// withSysctlRoot makes the operator read sysctls from a test directory
// in place of /proc/sys.
func withSysctlRoot(t *testing.T, dir string) {
	t.Helper()
	saved := sysctlRoot
	t.Cleanup(func() { sysctlRoot = saved })
	sysctlRoot = dir
}

func TestPrepareReturnsAVirtualizationNodeThatEveryUserCanOpen(t *testing.T) {
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

			resp := preparedVirtualization(t, fixture, tc.device)

			if resp == nil || resp.Error != "" {
				t.Fatalf("the response is %+v, want a prepared claim with no error", resp)
			}
			want := []cdiDeviceNode{{Path: "/dev/" + tc.misc, Type: "c", Major: 10, Minor: tc.minor, FileMode: &mode}}
			if got := specNodes(t, fixture, "claim-1"); !sameDeviceNodes(got, want) {
				t.Errorf("nodes = %+v, want %+v", got, want)
			}
		})
	}
}

func TestPrepareRefusesAVirtualizationDeviceTheMachineDoesNotOffer(t *testing.T) {
	// A claim allocated from an older slice can name a device that no
	// longer exists, or vhost-vsock on a machine that has since set
	// vsock to global mode.
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

			resp := preparedVirtualization(t, fixture, tc.device)

			if resp == nil || resp.Error == "" {
				t.Errorf("response = %+v, want a refusal", resp)
			}
		})
	}
}

func TestTheSliceListsTheVirtualizationDevicesInNameOrder(t *testing.T) {
	// The slice writer compares the devices to its last write, so the
	// same machine must publish them in the same order every time.
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
