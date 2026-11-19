package machine

// Applying OSSysctls needs a real kernel, so these tests check only the
// table itself.

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestOSSysctlNamesResolveToParameterPaths(t *testing.T) {
	for name := range OSSysctls {
		if _, err := sysctlPath("/proc/sys", name); err != nil {
			t.Errorf("resolving %s to a path: %v", name, err)
		}
		if strings.TrimSpace(name) != name {
			t.Errorf("%q has surrounding space", name)
		}
		if !strings.Contains(name, ".") {
			t.Errorf("%q has no dot, want at least one", name)
		}
		if strings.ToLower(name) != name {
			t.Errorf("%q has upper case letters, want lower case", name)
		}
	}
}

func TestOSSysctlValuesAreNotEmpty(t *testing.T) {
	for name, value := range OSSysctls {
		if strings.TrimSpace(value) != value || value == "" {
			t.Errorf("the value of %s is %q, want a non-empty value with no surrounding space", name, value)
		}
	}
}

// Adding or removing an entry changes every machine of a fleet, so the
// change must also be made in this list, where a reviewer sees it.
func TestOSSysctlsHoldsExactlyTheDeclaredSet(t *testing.T) {
	want := []string{
		"fs.inotify.max_user_instances",
		"fs.inotify.max_user_watches",
		"fs.protected_fifos",
		"fs.protected_hardlinks",
		"fs.protected_regular",
		"fs.protected_symlinks",
		"kernel.hardlockup_panic",
		"kernel.kptr_restrict",
		"kernel.panic_on_oops",
		"kernel.pid_max",
		"kernel.softlockup_panic",
		"net.core.default_qdisc",
		"net.ipv4.conf.all.accept_source_route",
		"net.ipv4.conf.all.promote_secondaries",
		"net.ipv4.conf.all.rp_filter",
		"net.ipv4.conf.default.accept_source_route",
		"net.ipv4.conf.default.promote_secondaries",
		"net.ipv4.conf.default.rp_filter",
		"net.ipv4.ip_forward",
		"net.ipv4.tcp_mtu_probing",
		"net.ipv4.tcp_slow_start_after_idle",
		"net.unix.max_dgram_qlen",
		"vm.max_map_count",
		"vm.watermark_scale_factor",
	}
	got := slices.Sorted(maps.Keys(OSSysctls))
	if !slices.Equal(got, want) {
		t.Errorf("the entries are %v, want %v", got, want)
	}
}

// The OSSysctls doc comment gives the reason.
func TestEveryDefaultEntryHasAnAllEntry(t *testing.T) {
	for name := range OSSysctls {
		scoped, ok := strings.CutPrefix(name, "net.ipv4.conf.default.")
		if !ok {
			continue
		}
		all := "net.ipv4.conf.all." + scoped
		if _, ok := OSSysctls[all]; !ok {
			t.Errorf("%s has no %s, so it never reaches the interfaces that exist when init applies the table", name, all)
		}
	}
}

// The kernel applies a write of net.ipv4.ip_forward to every
// interface. Without it, pod traffic fails in a way that looks like a
// broken CNI.
func TestOSSysctlsForwardsPackets(t *testing.T) {
	if OSSysctls["net.ipv4.ip_forward"] != "1" {
		t.Errorf("net.ipv4.ip_forward is %q, want 1", OSSysctls["net.ipv4.ip_forward"])
	}
}

// The kernel accepts only a registered scheduler, and a liken machine
// has no modprobe to load one on demand, so this value must match a
// module in image/etc/liken/modules.conf.
func TestDefaultQdiscNamesTheModuleTheImageLoads(t *testing.T) {
	if OSSysctls["net.core.default_qdisc"] != "fq_codel" {
		t.Errorf("net.core.default_qdisc is %q, want fq_codel, the scheduler that image/etc/liken/modules.conf loads", OSSysctls["net.core.default_qdisc"])
	}
}
