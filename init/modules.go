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
//      ordered so that loading right-to-left satisfies everyone.
//
//   2. Ask the kernel to load each file. That's the finit_module
//      syscall: "here's an open file descriptor, it's a kernel module,
//      trust it." Our modules are zstd-compressed (.ko.zst) exactly as
//      Ubuntu shipped them; the MODULE_INIT_COMPRESSED_FILE flag tells
//      the kernel to decompress for itself (our vendored config has
//      CONFIG_MODULE_DECOMPRESS=y), so init never touches the bytes.
//
// Which modules to load comes from /etc/liken/modules.conf, a plain
// list baked into the image, the same list the image build used to
// decide which module files to ship. liken loads everything k3s will
// need up front, at boot: the alternative (on-demand autoloading) works
// by the kernel exec'ing /sbin/modprobe itself, and we'd rather have a
// fixed, reviewable list than a hidden runtime dependency.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const modulesConf = "/etc/liken/modules.conf"

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
		if err := loadModule(base, name, deps, loaded, &count); err != nil {
			fmt.Fprintf(os.Stderr, "liken: modules: %s: %v\n", name, err)
		}
	}
	fmt.Printf("liken: loaded %d kernel modules for %s\n", count, strings.Join(names, ", "))
}

// readModuleList reads the requested module names: one per line, with
// blank lines and # comments allowed, since the file doubles as
// documentation.
func readModuleList(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
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

func moduleName(path string) string {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, ".zst")
	name = strings.TrimSuffix(name, ".ko")
	return strings.ReplaceAll(name, "-", "_")
}

// loadModule feeds one module and its dependencies to the kernel,
// dependencies first (modules.dep lists them ready to load
// right-to-left). Already-loaded modules, whether by us or by an
// earlier dependency chain, return EEXIST, which counts as success.
func loadModule(base, name string, deps map[string][]string, loaded map[string]bool, count *int) error {
	entry, ok := deps[strings.ReplaceAll(name, "-", "_")]
	if !ok {
		return fmt.Errorf("not in modules.dep (is it built into the kernel?)")
	}
	for i := len(entry) - 1; i >= 0; i-- {
		file := entry[i]
		if loaded[file] {
			continue
		}
		f, err := os.Open(filepath.Join(base, file))
		if err != nil {
			return err
		}
		err = unix.FinitModule(int(f.Fd()), "", unix.MODULE_INIT_COMPRESSED_FILE)
		f.Close()
		if err != nil && err != unix.EEXIST {
			return fmt.Errorf("finit_module %s: %w", file, err)
		}
		if err == nil {
			*count++
		}
		loaded[file] = true
	}
	return nil
}

func kernelRelease() string {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return "unknown"
	}
	return unix.ByteSliceToString(u.Release[:])
}
