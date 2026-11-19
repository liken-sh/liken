package machine

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
			t.Fatalf("resolving the path of %s: %v", name, err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating the directory of %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte("global\n"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	return dir
}

func TestModuleSysctlsHasExactlyTheDeclaredEntries(t *testing.T) {
	want := []string{"net.vsock.child_ns_mode"}
	if got := slices.Sorted(maps.Keys(ModuleSysctls)); !slices.Equal(got, want) {
		t.Errorf("ModuleSysctls has the entries %v, want %v", got, want)
	}
}

func TestDefaultSysctlsIncludeAModuleSettingOnlyWhenItsFileExists(t *testing.T) {
	// absent lists the parameter so that the operator's sysctl check
	// applies it when the module loads.
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

func TestDefaultSysctlsIncludeEveryOSSetting(t *testing.T) {
	defaults, _ := DefaultSysctls(t.TempDir(), nil)
	for name, value := range OSSysctls {
		if defaults[name] != value {
			t.Errorf("%s = %q, want %q", name, defaults[name], value)
		}
	}
}
