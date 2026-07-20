package main

// Loading kernel modules, without modprobe.
//
// The kernel cannot use a driver that was built as a module until
// something feeds that module back to it, and the kernel itself does
// not know where modules live; that is userspace's job. The usual
// program for this is modprobe, from the kmod project. liken ships
// no modprobe, so init does the two things that modprobe would have
// done:
//
//  1. Resolve dependencies. Modules depend on other modules (overlay
//     needs nothing; iptable_nat pulls in a chain of netfilter
//     pieces). Nothing scans the module tree at runtime to work this
//     out. At image build time, depmod wrote an index, modules.dep,
//     that maps every module to the full list of modules it needs,
//     already ordered so that loading right-to-left satisfies every
//     dependency.
//
//  2. Ask the kernel to load each file. This is the finit_module
//     syscall: "here is an open file descriptor, it is a kernel
//     module, trust it." liken's modules are zstd-compressed
//     (.ko.zst) exactly as Ubuntu shipped them. The
//     MODULE_INIT_COMPRESSED_FILE flag tells the kernel to decompress
//     the module itself (liken's vendored config sets
//     CONFIG_MODULE_DECOMPRESS=y), so init never touches the bytes.
//
// Which modules to load comes from two lists, loaded in two passes.
// The first list is /etc/liken/modules.conf, a plain list baked into
// the image, the same list that the image build used to decide which
// module files to ship: the OS's own needs, loaded up front because
// the alternative (on-demand autoloading) works by the kernel
// exec'ing /sbin/modprobe itself, and a fixed, reviewable list is
// better than a hidden runtime dependency. The second list is the
// Machine spec's declared extras (spec.modules), the drivers for
// whatever hardware this machine's workloads use, which cannot load
// until the boot knows which manifest won. loadDeclaredModules below
// explains what each outcome means.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// modulesConf is the image's fixed module list. A package variable
// rather than a constant so tests can point the first pass at a list
// of their own making.
var modulesConf = "/etc/liken/modules.conf"

func loadModules() {
	release := kernelRelease()
	base := filepath.Join("/lib/modules", release)

	names, err := readModuleList(modulesConf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: modules: %v\n", err)
		return
	}
	if len(names) == 0 {
		return
	}

	deps, err := readModulesDep(filepath.Join(base, "modules.dep"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: modules: %v\n", err)
		return
	}

	loaded := map[string]bool{}
	count := 0
	for _, name := range names {
		n, err := loadModule(base, name, deps, loaded)
		count += n
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: modules: %s: %v\n", name, err)
		}
	}
	fmt.Printf("liken: loaded %d kernel modules for %s\n", count, strings.Join(names, ", "))
}

// loadDeclaredModules loads the extra modules that the winning
// Machine manifest declared, and reports each name's outcome. Unlike
// the fixed list, whose failures are printed and forgotten (the OS
// knows its own list is shippable), a declared module is a
// deployment's request, and the answer must reach the cluster.
// These outcomes travel through the facts file into status.modules.
// Nothing here can stop the boot. A machine missing a workload's
// driver is degraded, not down.
func loadDeclaredModules(names []string) []machine.ModuleStatus {
	return loadDeclaredModulesFrom(filepath.Join("/lib/modules", kernelRelease()), names)
}

// loadDeclaredModulesFrom is the same pass with the module tree as a
// parameter, so tests can point it at a fabricated tree. Only the
// kernel's own tree ever has real modules to load.
func loadDeclaredModulesFrom(base string, names []string) []machine.ModuleStatus {
	if len(names) == 0 {
		return nil
	}

	// Both indexes are depmod's work, shipped beside the modules
	// themselves. modules.dep maps every shipped module to its
	// dependency chain. modules.builtin names what is compiled into
	// vmlinuz, which is what lets a declared name that matches no
	// file mean "already there" instead of "missing".
	deps, err := readModulesDep(filepath.Join(base, "modules.dep"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: modules: %v\n", err)
		deps = map[string][]string{}
	}
	builtin, err := readModulesBuiltin(filepath.Join(base, "modules.builtin"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: modules: %v\n", err)
	}

	loaded := map[string]bool{}
	statuses := declaredModuleOutcomes(names, deps, builtin, func(name string) error {
		_, err := loadModule(base, name, deps, loaded)
		return err
	})
	for _, s := range statuses {
		if s.Message != "" {
			fmt.Printf("liken: modules: %s: %s: %s\n", s.Name, strings.ToLower(string(s.State)), s.Message)
		} else {
			fmt.Printf("liken: modules: %s: %s\n", s.Name, strings.ToLower(string(s.State)))
		}
	}
	return statuses
}

// declaredModuleOutcomes classifies each declared name before it
// asks the kernel for anything, so the verdicts do not depend on the
// order that failures happen to occur in. The vocabulary belongs to
// machine.ModuleState: Loaded and Builtin are healthy. Missing means
// the booted image never shipped the module (an edit outran its
// image, and only a new image fixes it). Failed means the kernel
// refused a module that the image does ship.
func declaredModuleOutcomes(names []string, deps map[string][]string,
	builtin map[string]bool, load func(name string) error) []machine.ModuleStatus {
	statuses := make([]machine.ModuleStatus, 0, len(names))
	for _, name := range names {
		status := machine.ModuleStatus{Name: name}
		key := strings.ReplaceAll(name, "-", "_")
		switch {
		case deps[key] != nil:
			if err := load(name); err != nil {
				status.State = machine.ModuleFailed
				status.Message = err.Error()
			} else {
				status.State = machine.ModuleLoaded
			}
		case builtin[key]:
			status.State = machine.ModuleBuiltin
		default:
			status.State = machine.ModuleMissing
			status.Message = "not in this image; rebuild the deployment's image, or upgrade to a release built from manifests that declare it"
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// readModuleList reads the requested module names: one per line, with
// blank lines and # comments allowed, since the file doubles as
// documentation.
func readModuleList(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		names = append(names, line)
	}
	return names, nil
}

// readModulesDep parses depmod's index. Each line looks like this:
//
//	kernel/fs/overlayfs/overlay.ko.zst: kernel/a.ko.zst kernel/b.ko.zst
//
// That is, a module's path, then every module it transitively needs.
// This function keys the map by module name (the filename minus
// extensions), because module names use "_" and "-" interchangeably.
func readModulesDep(path string) (map[string][]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	deps := map[string][]string{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		path, needs, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		entry := append([]string{path}, strings.Fields(needs)...)
		deps[moduleName(path)] = entry
	}
	return deps, nil
}

// readModulesBuiltin parses depmod's record of what is compiled into
// vmlinuz itself: one module path per line, for example:
//
//	kernel/fs/binfmt_misc.ko
//
// A name found here needs no loading, because the kernel already
// contains it. This set is keyed the same way as modules.dep, with
// names normalized to "_".
func readModulesBuiltin(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	builtin := map[string]bool{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		builtin[moduleName(line)] = true
	}
	return builtin, nil
}

func moduleName(path string) string {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, ".zst")
	name = strings.TrimSuffix(name, ".ko")
	return strings.ReplaceAll(name, "-", "_")
}

// loadModule feeds one module and its dependencies to the kernel,
// dependencies first (modules.dep lists them ready to load
// right-to-left), and returns how many files the kernel actually
// loaded. An already-loaded module, whether loaded by this call or
// by an earlier dependency chain, returns EEXIST, which counts as
// success but not toward the count.
func loadModule(base, name string, deps map[string][]string, loaded map[string]bool) (int, error) {
	entry, ok := deps[strings.ReplaceAll(name, "-", "_")]
	if !ok {
		return 0, fmt.Errorf("not in modules.dep (is it built into the kernel?)")
	}
	count := 0
	for i := len(entry) - 1; i >= 0; i-- {
		file := entry[i]
		if loaded[file] {
			continue
		}
		f, err := os.Open(filepath.Join(base, file))
		if err != nil {
			return count, err
		}
		err = unix.FinitModule(int(f.Fd()), "", unix.MODULE_INIT_COMPRESSED_FILE)
		f.Close()
		if err != nil && !errors.Is(err, unix.EEXIST) {
			return count, fmt.Errorf("finit_module %s: %w", file, err)
		}
		if err == nil {
			count++
		}
		loaded[file] = true
	}
	return count, nil
}

func kernelRelease() string {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return "unknown"
	}
	return unix.ByteSliceToString(u.Release[:])
}
