package main

// Loading kernel modules, without modprobe.
//
// The kernel can't use a driver that was built as a module until
// someone feeds that module back to it, and the kernel itself doesn't
// know where modules live; that's userspace's job. The usual someone
// is modprobe, from the kmod project; liken ships no modprobe, so init
// does the two things modprobe would have done:
//
//   1. Resolve dependencies. Modules depend on other modules (overlay
//      needs nothing; iptable_nat pulls a chain of netfilter pieces).
//      Nobody scans the module tree at runtime to figure this out: at
//      image build time, depmod wrote an index, modules.dep, mapping
//      every module to the full list of modules it needs, already
//      ordered so that loading right-to-left satisfies every
//      dependency.
//
//   2. Ask the kernel to load each file. That's the finit_module
//      syscall: "here's an open file descriptor, it's a kernel module,
//      trust it." Our modules are zstd-compressed (.ko.zst) exactly as
//      Ubuntu shipped them; the MODULE_INIT_COMPRESSED_FILE flag tells
//      the kernel to decompress for itself (our vendored config has
//      CONFIG_MODULE_DECOMPRESS=y), so init never touches the bytes.
//
// Which modules to load comes from two lists, loaded in two passes.
// The first is /etc/liken/modules.conf, a plain list baked into the
// image, the same list the image build used to decide which module
// files to ship: the OS's own needs, loaded up front because the
// alternative (on-demand autoloading) works by the kernel exec'ing
// /sbin/modprobe itself, and we'd rather have a fixed, reviewable
// list than a hidden runtime dependency. The second is the Machine
// spec's declared extras (spec.modules), the drivers for whatever
// hardware this machine's workloads use, which cannot load until the
// boot knows which manifest won; loadDeclaredModules below explains
// what each outcome means.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/chrisguidry/liken/machine"
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

// loadDeclaredModules loads the extra modules the winning Machine
// manifest declared and reports each name's outcome. Unlike the fixed
// list, whose failures are printed and forgotten (the OS knows its
// own list is shippable), a declared module is a deployment's ask,
// and the answer has to reach the cluster: these outcomes ride the
// facts file into status.modules. Nothing here can stop the boot; a
// machine missing a workload's driver is degraded, not down.
func loadDeclaredModules(names []string) []machine.ModuleStatus {
	return loadDeclaredModulesFrom(filepath.Join("/lib/modules", kernelRelease()), names)
}

// loadDeclaredModulesFrom is the same pass with the module tree as a
// parameter, so tests can point it at a fabricated one; only the
// kernel's own tree ever has real modules to load.
func loadDeclaredModulesFrom(base string, names []string) []machine.ModuleStatus {
	if len(names) == 0 {
		return nil
	}

	// Both indexes are depmod's work, shipped beside the modules
	// themselves. modules.dep maps every shipped module to its
	// dependency chain; modules.builtin names what is compiled into
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

// declaredModuleOutcomes classifies each declared name before asking
// the kernel for anything, so the verdicts don't depend on the order
// failures happen to occur in. The vocabulary is machine.ModuleState's:
// Loaded and Builtin are healthy; Missing means the booted image never
// shipped the module (an edit outran its image, and only a new image
// fixes it); Failed means the kernel refused a module we do ship.
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

// readModulesDep parses depmod's index. Each line is
//
//	kernel/fs/overlayfs/overlay.ko.zst: kernel/a.ko.zst kernel/b.ko.zst
//
// that is, a module's path, then every module it transitively needs. We key
// the map by module name (the filename minus extensions), remembering
// that module names use "_" and "-" interchangeably.
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
// vmlinuz itself: one module path per line, e.g.
//
//	kernel/fs/binfmt_misc.ko
//
// A name found here needs no loading, ever; the kernel already
// contains it. The set is keyed like modules.dep's, names normalized
// to "_".
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
// loaded. Already-loaded modules, whether by us or by an earlier
// dependency chain, return EEXIST, which counts as success but not
// toward the count.
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
