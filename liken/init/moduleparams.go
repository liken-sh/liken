package main

// A liken machine has no modprobe or modprobe.d, so init builds the
// parameter string from spec.moduleParameters and passes it to
// finit_module in the call that loads the module. The kernel applies
// parameters only at that load, so a changed parameter of a loaded
// module takes effect at the next reboot. status.modules[].parameters
// reports the values that /sys/module reads back.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/liken/machine"
)

// sysModuleDir lists the loaded modules and the built-in ones.
var sysModuleDir = "/sys/module"

// The CRD limits a readback value to 1024 bytes, and the API server
// refuses the whole status write when one value is longer. Some
// values are longer, such as acpi.debug_level, so readModuleParameters
// truncates them.
const moduleParameterValueMax = 1024

// The longest key that the CRD accepts: a 64-byte module name, a dot,
// and a 64-byte parameter name. Each key becomes a file name in the
// boot record.
const moduleParameterKeyMax = 129

// usableModuleParameters drops each parameter that the CRD would
// refuse, because a manifest on an install stick never passes
// admission. It returns nil when none remain. The boot record writes
// no file for an empty value, so the parameter would be lost from it.
func usableModuleParameters(parameters map[string]string) map[string]string {
	usable := map[string]string{}
	for key, value := range parameters {
		switch {
		case value == "":
			fmt.Fprintf(os.Stderr, "liken: modules: parameter %s has no value and is not passed to the module\n", key)
		case len(key) > moduleParameterKeyMax:
			fmt.Fprintf(os.Stderr, "liken: modules: parameter name %.64s... is longer than %d bytes and is not passed to the module\n",
				key, moduleParameterKeyMax)
		default:
			usable[key] = value
		}
	}
	if len(usable) == 0 {
		return nil
	}
	return usable
}

// sysModuleName returns the module name as /sys/module spells it. The
// kernel treats - and _ in a module name as the same character and
// registers the name with _.
func sysModuleName(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

// moduleIsResident reports whether the module is loaded or built into
// the kernel. init checks it before a load, because finit_module
// returns EEXIST for a loaded module and does not apply the parameter
// string.
func moduleIsResident(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, sysModuleName(name)))
	return err == nil && info.IsDir()
}

// readModuleParameters reads the current value of each parameter that
// the spec declares for one module, from
// <dir>/<module>/parameters/<name>. It leaves out a parameter whose
// file cannot be read. Usually the name is wrong, because the kernel
// loads a module with an unknown parameter and only logs a warning. A
// driver can also register a parameter with no sysfs file, such as
// libata.force, or with a write-only mode. The values are the kernel's
// own rendering, so a bool reads back as Y or N.
func readModuleParameters(dir, name string, declared map[string]string) map[string]string {
	names := machine.ModuleParameterNames(name, declared)
	if len(names) == 0 {
		return nil
	}
	base := filepath.Join(dir, sysModuleName(name), "parameters")
	held := map[string]string{}
	for _, parameter := range names {
		raw, err := os.ReadFile(filepath.Join(base, parameter))
		if err != nil {
			continue
		}
		value := strings.TrimSuffix(string(raw), "\n")
		if len(value) > moduleParameterValueMax {
			value = value[:moduleParameterValueMax]
		}
		held[parameter] = value
	}
	if len(held) == 0 {
		return nil
	}
	return held
}
