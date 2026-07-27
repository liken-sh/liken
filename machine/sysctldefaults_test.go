package machine

// Tests for the table of kernel settings every liken machine holds.
// Applying the table needs a real kernel, so these tests check the
// things that go wrong at the table's own level: a name that no
// parameter answers to, a name that would escape the sysctl tree, an
// entry that reaches nothing because it names only one of the three
// scopes, and an edit that adds or drops a parameter without anyone
// noticing.

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestOSSysctlNamesResolveToParameterPaths(t *testing.T) {
	for name := range OSSysctls {
		if _, err := sysctlPath("/proc/sys", name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if strings.TrimSpace(name) != name {
			t.Errorf("%s: surrounding space in the name", name)
		}
		if !strings.Contains(name, ".") {
			t.Errorf("%s: a parameter name has at least one dot", name)
		}
		if strings.ToLower(name) != name {
			t.Errorf("%s: parameter names are lower case", name)
		}
	}
}

func TestOSSysctlValuesAreNotEmpty(t *testing.T) {
	for name, value := range OSSysctls {
		if strings.TrimSpace(value) != value || value == "" {
			t.Errorf("%s: %q is not a value the kernel would take", name, value)
		}
	}
}

// The declared set, pinned. A parameter that every liken machine holds
// is a decision about the whole fleet, so adding or removing one has
// to be a deliberate edit here as well as in the table. That makes
// this list the place a reviewer looks to see what changed.
func TestOSSysctlsHoldsExactlyTheDeclaredSet(t *testing.T) {
	want := []string{
		"fs.inotify.max_user_instances",
		"fs.inotify.max_user_watches",
		"fs.protected_fifos",
		"fs.protected_hardlinks",
		"fs.protected_regular",
		"fs.protected_symlinks",
		"kernel.kptr_restrict",
		"kernel.panic_on_oops",
		"kernel.pid_max",
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
		t.Errorf("the declared set moved:\n got %v\nwant %v", got, want)
	}
}

// The kernel copies net.ipv4.conf.default into an interface when that
// interface registers, and network drivers load before this table is
// applied. So a default entry reaches only the interfaces a CNI
// creates later, never the machine's own network card. Reaching that
// card takes the matching all entry. An entry with no twin is the
// mistake this test exists to catch.
func TestEveryInterfaceScopedDefaultHasItsAllTwin(t *testing.T) {
	for name := range OSSysctls {
		scoped, ok := strings.CutPrefix(name, "net.ipv4.conf.default.")
		if !ok {
			continue
		}
		twin := "net.ipv4.conf.all." + scoped
		if _, ok := OSSysctls[twin]; !ok {
			t.Errorf("%s has no %s, so it never reaches this machine's own interface", name, twin)
		}
	}
}

// Forwarding is the one net.ipv4.conf parameter liken sets through its
// bare name rather than a scope, because the kernel treats a write to
// net.ipv4.ip_forward as a write to the all scope and pushes it to
// every interface already registered. Losing it would leave a machine
// unable to route pod traffic, which is a failure that looks like a
// broken CNI rather than a missing parameter.
func TestOSSysctlsForwardsPackets(t *testing.T) {
	if OSSysctls["net.ipv4.ip_forward"] != "1" {
		t.Error("a node that cannot forward packets cannot route pod traffic")
	}
}

// The queueing discipline names a kernel module. Nothing can name a
// scheduler the kernel has not registered, and liken ships no modprobe
// for the kernel to call, so the name here and the name in the image's
// module list have to agree.
func TestDefaultQdiscNamesTheModuleTheImageLoads(t *testing.T) {
	if OSSysctls["net.core.default_qdisc"] != "fq_codel" {
		t.Error("image/etc/liken/modules.conf loads sch_fq_codel for this value")
	}
}
