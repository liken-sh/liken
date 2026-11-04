package machine

// The settings for parameters that an optional module registers: init
// and the operator apply each one only where its file exists, and
// never over a spec that names it.

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// sysctlTree writes a /proc/sys with one file for each name.
func sysctlTree(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		path, err := sysctlPath(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("global\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestModuleSysctlsHoldsExactlyTheDeclaredSet(t *testing.T) {
	want := []string{"net.vsock.child_ns_mode"}
	if got := slices.Sorted(maps.Keys(ModuleSysctls)); !slices.Equal(got, want) {
		t.Errorf("the declared set moved:\n got %v\nwant %v", got, want)
	}
}

func TestDefaultSysctlsTakeAModuleSettingOnlyWhereItsFileExists(t *testing.T) {
	// A machine that never loads vsock has no net.vsock tree, and
	// that is not a fault. The name still comes back as absent, so the
	// operator's check applies it when the module loads later.
	for _, tc := range []struct {
		name       string
		files      []string
		spec       map[string]string
		wantModule map[string]string
		wantAbsent []string
	}{
		{"no vsock module", nil, nil, map[string]string{}, []string{"net.vsock.child_ns_mode"}},
		{"vsock loaded", []string{"net.vsock.child_ns_mode"}, nil,
			map[string]string{"net.vsock.child_ns_mode": "local"}, nil},
		// The kernel takes one write of child_ns_mode and refuses a
		// second value, so a spec that names it gets the only write.
		{"the spec names it", []string{"net.vsock.child_ns_mode"}, map[string]string{"net/vsock/child_ns_mode": "global"},
			map[string]string{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := sysctlTree(t, tc.files...)

			defaults, absent := DefaultSysctls(dir, tc.spec)

			module := maps.Clone(defaults)
			maps.DeleteFunc(module, func(name, _ string) bool { _, inOS := OSSysctls[name]; return inOS })
			if !reflect.DeepEqual(module, tc.wantModule) {
				t.Errorf("module settings = %v, want %v", module, tc.wantModule)
			}
			if !slices.Equal(absent, tc.wantAbsent) {
				t.Errorf("absent = %v, want %v", absent, tc.wantAbsent)
			}
		})
	}
}

func TestDefaultSysctlsKeepEveryOSSetting(t *testing.T) {
	defaults, _ := DefaultSysctls(t.TempDir(), nil)
	for name, value := range OSSysctls {
		if defaults[name] != value {
			t.Errorf("%s = %q, want %q", name, defaults[name], value)
		}
	}
}
