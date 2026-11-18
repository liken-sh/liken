package main

// liken ships no modprobe, so init loads kernel modules itself, in
// the two steps that modprobe takes:
//
//  1. Find the dependencies. At image build time, depmod writes
//     modules.dep, which lists each module with every module it
//     needs, in an order that loads correctly from right to left.
//
//  2. Call finit_module on each file. The files are zstd-compressed
//     (.ko.zst), as Ubuntu ships them. The MODULE_INIT_COMPRESSED_FILE
//     flag makes the kernel decompress the file, which works because
//     liken's kernel config sets CONFIG_MODULE_DECOMPRESS=y.
//
// init loads two lists, in two passes. The first is
// /etc/liken/modules.conf, the OS's own list, which the image build
// also reads to choose which module files to ship. init loads that
// list at boot because on-demand autoloading makes the kernel exec
// /sbin/modprobe, and a fixed list is easier to review. The second
// is spec.modules of the Machine manifest, the drivers for this
// machine's hardware. init loads it after settleStorage has chosen
// the manifest for this boot.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// modulesConf is the path of the OS's module list. It is a variable so
// that a test can point the first pass at its own list.
var modulesConf = "/etc/liken/modules.conf"

// finitModule is the finit_module syscall. It is a variable so that a
// test can record the file and the parameter string of each load
// without loading a module.
var finitModule = unix.FinitModule

func loadModules() {
	release := kernelRelease()
	base := filepath.Join("/lib/modules", release)

	names, err := readModuleList(modulesConf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: modules: reading the module list: %v\n", err)
		return
	}
	if len(names) == 0 {
		return
	}

	deps, err := readModulesDep(filepath.Join(base, "modules.dep"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: modules: reading modules.dep: %v\n", err)
		return
	}

	loaded := map[string]bool{}
	count := 0
	for _, name := range names {
		n, err := loadModule(base, name, "", deps, loaded)
		count += n
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: modules: %s: %v\n", name, err)
		}
	}
	fmt.Printf("liken: loaded %d kernel modules for %s\n", count, strings.Join(names, ", "))
}

// loadDeclaredModules loads spec.modules of this boot's Machine
// manifest and returns the outcome for each name, which init
// publishes to status.modules through the facts tree. loadModules
// only prints its failures, because the image build ships every
// module on its list, but a declared name can be wrong. A failure
// does not stop the boot, because a machine without a workload's
// driver still runs.
func loadDeclaredModules(names []string, parameters map[string]string) []machine.ModuleStatus {
	return loadDeclaredModulesFrom(filepath.Join("/lib/modules", kernelRelease()), names, parameters)
}

// loadDeclaredModulesFrom is loadDeclaredModules with the module tree
// as a parameter, so that a test can build its own tree.
//
// parameters is spec.moduleParameters, and only declared modules take
// parameters. The OS's own modules and a feature's modules ship with
// the release, so a machine that needs other parameters for them has
// found a bug in the release.
func loadDeclaredModulesFrom(base string, names []string, parameters map[string]string) []machine.ModuleStatus {
	if len(names) == 0 {
		return nil
	}
	// A manifest on an install stick never passes admission, so
	// drop the parameters that the CRD would refuse.
	parameters = usableModuleParameters(parameters)

	// modules.builtin lists the modules compiled into the kernel, so
	// that a declared name with no module file reports as builtin and
	// not as missing.
	deps, err := readModulesDep(filepath.Join(base, "modules.dep"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: modules: reading modules.dep: %v\n", err)
		deps = map[string][]string{}
	}
	builtin, err := readModulesBuiltin(filepath.Join(base, "modules.builtin"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: modules: reading modules.builtin: %v\n", err)
	}

	loaded := map[string]bool{}
	statuses := declaredModuleOutcomes(names, deps, builtin, parameters, declaredPass{
		resident: func(name string) bool { return moduleIsResident(sysModuleDir, name) },
		load: func(name, params string) error {
			_, err := loadModule(base, name, params, deps, loaded)
			return err
		},
		readParameters: func(name string) map[string]string {
			return readModuleParameters(sysModuleDir, name, parameters)
		},
	})
	for _, s := range statuses {
		fmt.Println(declaredModuleLine(s, parameters))
	}
	return statuses
}

// declaredModuleLine returns the console line for one declared
// module. When the load passed parameters, the line shows the exact
// string that the kernel received.
func declaredModuleLine(s machine.ModuleStatus, parameters map[string]string) string {
	line := fmt.Sprintf("liken: modules: %s: %s", s.Name, strings.ToLower(string(s.State)))
	if params := passedParameters(s, parameters); params != "" {
		line += " (" + params + ")"
	}
	if s.Message != "" {
		line += ": " + s.Message
	}
	// A module that was already loaded or is builtin did not receive
	// its parameters. Say so on the console, because the console is
	// the only live output on a machine with no shell.
	if declared := machine.ModuleParameterString(s.Name, parameters); declared != "" {
		switch {
		case s.AlreadyResident:
			line += ": already in the kernel, so " + declared + " did not reach it"
		case s.State == machine.ModuleBuiltin:
			line += ": the kernel builds it in, so " + declared + " did not reach it"
		}
	}
	return line
}

// passedParameters returns the parameter string that a load passed to
// the kernel. It returns "" for a module that was already loaded,
// because the kernel ignored the string, and for a failed load,
// because its message already includes the string.
func passedParameters(s machine.ModuleStatus, parameters map[string]string) string {
	if s.AlreadyResident || s.State != machine.ModuleLoaded {
		return ""
	}
	return machine.ModuleParameterString(s.Name, parameters)
}

// declaredPass holds the kernel operations that declaredModuleOutcomes
// calls: whether a module is loaded, loading a module with a parameter
// string, and reading a module's parameters from /sys. A test replaces
// them so that it needs no kernel.
type declaredPass struct {
	resident       func(name string) bool
	load           func(name, parameters string) error
	readParameters func(name string) map[string]string
}

// declaredModuleOutcomes returns a machine.ModuleStatus for each
// declared name. Missing means that neither modules.dep nor
// modules.builtin has the name. The image ships the kernel's whole
// module tree, so the name is misspelled or belongs to another kernel
// build. Failed means that modules.dep has the name and the load
// failed.
func declaredModuleOutcomes(names []string, deps map[string][]string,
	builtin map[string]bool, parameters map[string]string, pass declaredPass) []machine.ModuleStatus {
	statuses := make([]machine.ModuleStatus, 0, len(names))
	for _, name := range names {
		status := machine.ModuleStatus{Name: name}
		key := strings.ReplaceAll(name, "-", "_")
		params := machine.ModuleParameterString(name, parameters)
		switch {
		case deps[key] != nil:
			// For a module that is already loaded, finit_module
			// returns EEXIST and ignores the parameter string, and
			// loadModule treats EEXIST as success. So check first,
			// and pass no string that the kernel would ignore.
			status.AlreadyResident = pass.resident(name)
			if status.AlreadyResident {
				params = ""
			}
			if err := pass.load(name, params); err != nil {
				status.State = machine.ModuleFailed
				status.Message = err.Error()
				if params != "" {
					status.Message += fmt.Sprintf(" (parameters %s)", params)
				}
			} else {
				status.State = machine.ModuleLoaded
				status.Parameters = pass.readParameters(name)
			}
		case builtin[key]:
			status.State = machine.ModuleBuiltin
			status.Parameters = pass.readParameters(name)
		default:
			status.State = machine.ModuleMissing
			status.Message = "this image's kernel has no module and no built-in code with this name. For a module in spec.modules, check the spelling against the drivers in status.hardware.unclaimed"
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// readModuleList reads one module name per line, and skips blank
// lines and lines that start with #. A missing file is an empty list.
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

// readModulesDep parses modules.dep. Each line is a module's path,
// then every module that it needs, directly or indirectly:
//
//	kernel/fs/overlayfs/overlay.ko.zst: kernel/a.ko.zst kernel/b.ko.zst
//
// The map is keyed by module name, which is the file name without its
// extensions and with "-" replaced by "_", because the kernel treats
// the two characters as the same.
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

// readModulesBuiltin parses modules.builtin, which lists the modules
// compiled into vmlinuz, one path per line:
//
//	kernel/fs/binfmt_misc.ko
//
// The set uses the same keys as readModulesDep.
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

// loadModule loads a module and its dependencies, dependencies first,
// and returns how many files the kernel loaded. loaded records the
// files that earlier calls loaded, so that they are not opened again.
// A file that the kernel already has makes finit_module return
// EEXIST, which is not an error and is not counted.
//
// parameters goes only to the module's own file, the first entry of
// its modules.dep line. Dependencies load with no parameters, as
// modprobe loads them. To set a dependency's parameters, declare that
// module before the module that needs it.
func loadModule(base, name, parameters string, deps map[string][]string, loaded map[string]bool) (int, error) {
	entry, ok := deps[strings.ReplaceAll(name, "-", "_")]
	if !ok {
		return 0, errors.New("modules.dep has no entry for the module, which can be built into the kernel")
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
		params := ""
		if i == 0 {
			params = parameters
		}
		err = finitModule(int(f.Fd()), params, unix.MODULE_INIT_COMPRESSED_FILE)
		f.Close()
		if err != nil && !errors.Is(err, unix.EEXIST) {
			return count, fmt.Errorf("loading %s with finit_module: %w", file, err)
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
