package main

// How a module gets its parameters on a machine with no modprobe and
// no modprobe.d: init builds the parameter string from the spec and
// hands it to finit_module in the one call that loads the module.
// The driver reads it at probe and never again, which is why a
// parameter change on a loaded module needs a boot. The only place
// the result can be read afterward is /sys/module, and the readback
// here is what status.modules[].parameters reports.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/machine"
)

// sysModuleDir is the kernel's own registry of what is loaded,
// builtins included. It is a variable so a test can fabricate one
// out of directories.
var sysModuleDir = "/sys/module"

// The CRD caps a readback value at 1024 bytes, and apiextensions
// refuses the whole status write when one value runs over, so the
// clamp below is what keeps one chatty parameter from freezing a
// machine's entire status.
const moduleParameterValueMax = 1024

// The longest key admission accepts: a 64-byte module name, the dot,
// and a 64-byte parameter name. A longer key would fail as a file
// name in the boot record, partway through the write.
const moduleParameterKeyMax = 129

// usableModuleParameters drops the declarations the machine cannot
// act on: an empty value, which the facts tree would treat as a
// removed file, and an over-long key, which would abort the boot
// record with ENAMETOOLONG. Admission refuses both, but a manifest
// carried in on a stick never met admission, so this is the last
// guard before the kernel. Each drop prints its reason.
func usableModuleParameters(parameters map[string]string) map[string]string {
	usable := map[string]string{}
	for key, value := range parameters {
		switch {
		case value == "":
			fmt.Fprintf(os.Stderr, "liken: modules: %s declares no value; a parameter needs one\n", key)
		case len(key) > moduleParameterKeyMax:
			fmt.Fprintf(os.Stderr, "liken: modules: %.64s... is longer than %d bytes; skipping it\n",
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

// sysModuleName is the spelling /sys/module uses: underscores,
// whatever the module's file name used. The kernel treats - and _ in
// a module name interchangeably and normalizes to _ when it
// registers the module.
func sysModuleName(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

// moduleIsResident answers whether the kernel already holds a
// module. The declared pass asks before it loads, because asking
// after answers too late: finit_module returns EEXIST for a resident
// module and drops the parameter string without a trace, and the
// status must be able to say that happened.
func moduleIsResident(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, sysModuleName(name)))
	return err == nil && info.IsDir()
}

// readModuleParameters reads back the declared parameter names for
// one module, and only those; the kernel's directory also lists
// every parameter the declaration never mentioned. A declared name
// whose file cannot be read is left absent. Absence usually means
// the name is wrong, because the kernel accepts an unknown parameter
// name and warns only in its log; it can also mean the parameter is
// real but offers nothing to read, since a driver may register a
// parameter with no sysfs file at all (libata.force does) or with a
// write-only mode. The value kept is the kernel's own rendering, not
// the string that was passed: a bool comes back as Y or N, an array
// with its own separators.
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
		// Some parameters print a whole legend on read;
		// acpi.debug_level answers 1313 bytes. The schema takes
		// 1024, and one oversize value would fail the whole status
		// write, so the value is cut and the cut is silent.
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
