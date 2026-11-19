package machine

// A module parameter is a setting that a driver reads once, when the
// kernel loads the module. spec.moduleParameters keys each one as
// "<module>.<parameter>", the form of the kernel command line and of
// the path /sys/module/<module>/parameters/<parameter>.

import (
	"maps"
	"slices"
	"strings"
)

// splitModuleParameterKey splits a key at its first dot into the
// module and the parameter. Admission allows exactly one dot, but a
// manifest on an install stick does not pass through admission, so a
// key can have more dots, or none.
func splitModuleParameterKey(key string) (module, parameter string, ok bool) {
	module, parameter, ok = strings.Cut(key, ".")
	if !ok || module == "" || parameter == "" {
		return "", "", false
	}
	return module, parameter, true
}

// ModuleParameterString returns the parameter string that init passes
// to finit_module for one module: its parameter=value pairs, sorted by
// key and joined with spaces. The sort makes the string the same on
// every call for the same map, because Go randomizes the order of a
// map.
func ModuleParameterString(module string, parameters map[string]string) string {
	var pairs []string
	for _, key := range slices.Sorted(maps.Keys(parameters)) {
		name, parameter, ok := splitModuleParameterKey(key)
		if !ok || name != module {
			continue
		}
		pairs = append(pairs, parameter+"="+parameters[key])
	}
	return strings.Join(pairs, " ")
}

// ModuleParameterNames returns the parameter names declared for one
// module, sorted. After the load, init reads each one back from
// /sys/module/<module>/parameters.
func ModuleParameterNames(module string, parameters map[string]string) []string {
	var names []string
	for _, key := range slices.Sorted(maps.Keys(parameters)) {
		name, parameter, ok := splitModuleParameterKey(key)
		if !ok || name != module {
			continue
		}
		names = append(names, parameter)
	}
	return names
}

// ModuleParameterModules returns the modules of the keys, sorted and
// spelled as in the keys. The kernel treats - and _ in a module name
// as the same character, but the functions in this file and the CEL
// rule on the spec match names exactly, so a key must spell its module
// as spec.modules does.
func ModuleParameterModules(parameters map[string]string) []string {
	names := map[string]bool{}
	for key := range parameters {
		if module, _, ok := splitModuleParameterKey(key); ok {
			names[module] = true
		}
	}
	return slices.Sorted(maps.Keys(names))
}
